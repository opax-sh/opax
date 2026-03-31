package git_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	internalgit "github.com/opax-sh/opax/internal/git"
)

var minimumTrailerOracleGitVersion = gitVersion{major: 2, minor: 30, patch: 0}

type gitVersion struct {
	major int
	minor int
	patch int
}

func (v gitVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func TestTrailerPolicyAllocateSaveID(t *testing.T) {
	saveID := internalgit.AllocateSaveID()
	if err := saveID.Validate(); err != nil {
		t.Fatalf("AllocateSaveID() returned invalid ID %q: %v", saveID, err)
	}
}

func TestTrailerParityUpsertSaveTrailer(t *testing.T) {
	requireTrailerOracleGit(t)

	cases := []struct {
		name        string
		commentChar string
		message     string
	}{
		{
			name:    "subject only",
			message: "feat: test\n",
		},
		{
			name:    "subject and body",
			message: "feat: test\n\nbody\n",
		},
		{
			name:    "existing non-opax trailers preserved",
			message: "feat: test\n\nbody\n\nSigned-off-by: Dev <dev@example.com>\nReviewed-by: QA <qa@example.com>\n",
		},
		{
			name:    "existing mixed-case opax-save replaced",
			message: "feat: test\n\nbody\n\nopax-save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n",
		},
		{
			name:    "blank-line requirement for trailer recognition",
			message: "feat: test\nContext: still body\nopax-save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n",
		},
		{
			name:    "commented template block with default comment char",
			message: "feat: test\n\nbody\n\n# Please enter the commit message\n# line 2\n",
		},
		{
			name:        "commented template block with custom comment char",
			commentChar: ";",
			message:     "feat: test\n\nbody\n\n; comment one\n; comment two\n",
		},
		{
			name:    "scissor block placement",
			message: "feat: test\n\nbody\n\n# ------------------------ >8 ------------------------\n# comment after scissor\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := initGitRepo(t)
			if tc.commentChar != "" {
				runGit(t, repoRoot, "config", "core.commentChar", tc.commentChar)
			}
			ctx := mustDiscoverRepo(t, repoRoot)

			updated, saveID, err := internalgit.UpsertSaveTrailer(ctx, []byte(tc.message))
			if err != nil {
				t.Fatalf("UpsertSaveTrailer() error = %v", err)
			}
			if err := saveID.Validate(); err != nil {
				t.Fatalf("UpsertSaveTrailer() returned invalid save ID %q: %v", saveID, err)
			}
			wantTrailer := "Opax-Save: " + saveID.String()

			oracleMessage := rewriteTrailersWithGit(t, repoRoot, tc.message, wantTrailer)
			if !bytes.Equal(updated, []byte(oracleMessage)) {
				t.Fatalf("UpsertSaveTrailer() message mismatch with git oracle:\ngot:\n%q\nwant:\n%q", string(updated), oracleMessage)
			}

			parsed := parseTrailersWithGit(t, repoRoot, string(updated))
			if !containsLine(parsed, wantTrailer) {
				t.Fatalf("git interpret-trailers --parse = %q, want %q present", parsed, wantTrailer)
			}
		})
	}
}

func TestTrailerParityParseSaveTrailer(t *testing.T) {
	requireTrailerOracleGit(t)

	cases := []struct {
		name             string
		message          string
		wantOracleParsed []string
	}{
		{
			name:             "trailer absent",
			message:          "feat: test\n\nbody\n",
			wantOracleParsed: nil,
		},
		{
			name:             "trailer present in proper trailer block",
			message:          "feat: test\n\nbody\n\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n",
			wantOracleParsed: []string{"Opax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		},
		{
			name:             "unseparated opax-save line treated as body",
			message:          "feat: test\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n",
			wantOracleParsed: nil,
		},
		{
			name:             "other trailers preserved in oracle parse output",
			message:          "feat: test\n\nbody\n\nReviewed-by: QA <qa@example.com>\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n",
			wantOracleParsed: []string{"Reviewed-by: QA <qa@example.com>", "Opax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := initGitRepo(t)

			oracleParsed := parseTrailersWithGit(t, repoRoot, tc.message)
			if !sameLines(oracleParsed, tc.wantOracleParsed) {
				t.Fatalf("git interpret-trailers --parse = %q, want %q", oracleParsed, tc.wantOracleParsed)
			}

			saveID, ok, err := internalgit.ParseSaveTrailer([]byte(tc.message))
			if err != nil {
				t.Fatalf("ParseSaveTrailer() error = %v", err)
			}

			oracleSaveTrailers := filterSaveTrailers(oracleParsed)
			switch len(oracleSaveTrailers) {
			case 0:
				if ok {
					t.Fatalf("ParseSaveTrailer() ok = true, want false with ID %q", saveID)
				}
				if saveID != "" {
					t.Fatalf("ParseSaveTrailer() ID = %q, want empty", saveID)
				}
			case 1:
				wantID := parseTrailerValue(t, oracleSaveTrailers[0])
				if !ok {
					t.Fatalf("ParseSaveTrailer() ok = false, want true with ID %q", wantID)
				}
				if saveID.String() != wantID {
					t.Fatalf("ParseSaveTrailer() ID = %q, want %q", saveID, wantID)
				}
			default:
				t.Fatalf("test case produced unsupported oracle output with duplicate save trailers: %q", oracleSaveTrailers)
			}
		})
	}
}

func TestTrailerPolicyParseSaveTrailerValidation(t *testing.T) {
	cases := []struct {
		name          string
		message       string
		wantErrSubstr string
	}{
		{
			name:          "malformed save ID",
			message:       "feat: test\n\nbody\n\nOpax-Save: sav_not-a-ulid\n",
			wantErrSubstr: "invalid Opax-Save value",
		},
		{
			name:          "duplicate mixed-case save trailers",
			message:       "feat: test\n\nbody\n\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\nopax-save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAW\n",
			wantErrSubstr: "multiple Opax-Save trailers",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := internalgit.ParseSaveTrailer([]byte(tc.message))
			if err == nil {
				t.Fatal("ParseSaveTrailer() error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("ParseSaveTrailer() error = %v, want substring %q", err, tc.wantErrSubstr)
			}
		})
	}
}

func TestTrailerPolicyCanonicalTokenSpellingOnInsert(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\nopax-save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n")
	updated, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	text := string(updated)
	want := "Opax-Save: " + saveID.String()
	if !strings.Contains(text, want) {
		t.Fatalf("UpsertSaveTrailer() = %q, want canonical trailer line %q", text, want)
	}
	if strings.Contains(text, "\nopax-save:") {
		t.Fatalf("UpsertSaveTrailer() retained non-canonical token in %q", text)
	}
}

func TestTrailerPolicyParseSaveTrailerFromCommit(t *testing.T) {
	repoRoot := initGitRepo(t)
	writeTrackedFile(t, repoRoot, "README.md", "hello\n")
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "feat: test", "-m", "body\n\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	ctx := mustDiscoverRepo(t, repoRoot)

	commitHash := strings.TrimSpace(runGit(t, repoRoot, "rev-parse", "HEAD"))
	saveID, ok, err := internalgit.ParseSaveTrailerFromCommit(ctx, commitHash)
	if err != nil {
		t.Fatalf("ParseSaveTrailerFromCommit() error = %v", err)
	}
	if !ok {
		t.Fatal("ParseSaveTrailerFromCommit() ok = false, want true")
	}
	if saveID.String() != "sav_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("ParseSaveTrailerFromCommit() ID = %q, want %q", saveID, "sav_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	}
}

func TestTrailerPolicyParseSaveTrailerFromCommitAppliesValidation(t *testing.T) {
	repoRoot := initGitRepo(t)
	writeTrackedFile(t, repoRoot, "README.md", "hello\n")
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "feat: test", "-m", "body\n\nOpax-Save: sav_not-a-ulid")
	ctx := mustDiscoverRepo(t, repoRoot)

	commitHash := strings.TrimSpace(runGit(t, repoRoot, "rev-parse", "HEAD"))
	_, _, err := internalgit.ParseSaveTrailerFromCommit(ctx, commitHash)
	if err == nil {
		t.Fatal("ParseSaveTrailerFromCommit() error = nil, want invalid save ID error")
	}
	if !strings.Contains(err.Error(), "invalid Opax-Save value") {
		t.Fatalf("ParseSaveTrailerFromCommit() error = %v, want invalid value message", err)
	}
}

func TestTrailerPolicyAutoCommentCharFailClosedHeuristics(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    []string
	}{
		{
			name:    "markdown list stays in body",
			message: "feat: test\n\nbody\n\n- item one\n- item two\n",
			want: []string{
				"feat: test",
				"",
				"body",
				"",
				"- item one",
				"- item two",
			},
		},
		{
			name:    "punctuation paragraph stays in body",
			message: "feat: test\n\nbody\n\n; not template\n; still body text\n",
			want: []string{
				"feat: test",
				"",
				"body",
				"",
				"; not template",
				"; still body text",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := initGitRepo(t)
			runGit(t, repoRoot, "config", "core.commentChar", "auto")
			ctx := mustDiscoverRepo(t, repoRoot)

			updated, saveID, err := internalgit.UpsertSaveTrailer(ctx, []byte(tc.message))
			if err != nil {
				t.Fatalf("UpsertSaveTrailer() error = %v", err)
			}

			for _, bodyLine := range tc.want {
				if !strings.Contains(string(updated), bodyLine) {
					t.Fatalf("UpsertSaveTrailer() = %q, want body line %q preserved", string(updated), bodyLine)
				}
			}

			parsedID, ok, err := internalgit.ParseSaveTrailer(updated)
			if err != nil {
				t.Fatalf("ParseSaveTrailer() error = %v", err)
			}
			if !ok {
				t.Fatalf("ParseSaveTrailer() ok = false, want true for %q", string(updated))
			}
			if parsedID != saveID {
				t.Fatalf("ParseSaveTrailer() ID = %q, want %q", parsedID, saveID)
			}
		})
	}
}

func TestTrailerPolicyUpsertSaveTrailerUsesLinkedWorktreeConfig(t *testing.T) {
	requireTrailerOracleGit(t)

	mainRepo := initGitRepo(t)
	writeTrackedFile(t, mainRepo, "README.md", "hello\n")
	runGit(t, mainRepo, "add", "README.md")
	runGit(t, mainRepo, "commit", "-m", "initial")
	runGit(t, mainRepo, "branch", "feature")
	runGit(t, mainRepo, "config", "extensions.worktreeConfig", "true")

	worktreeRoot := filepath.Join(t.TempDir(), "wt")
	runGit(t, mainRepo, "worktree", "add", worktreeRoot, "feature")
	runGit(t, worktreeRoot, "config", "--worktree", "core.commentChar", ";")

	ctx := mustDiscoverRepo(t, worktreeRoot)
	message := []byte("feat: test\n\nbody\n\n; comment one\n; comment two\n")

	updated, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	wantTrailer := "Opax-Save: " + saveID.String()
	oracleMessage := rewriteTrailersWithGit(t, worktreeRoot, string(message), wantTrailer)
	if !bytes.Equal(updated, []byte(oracleMessage)) {
		t.Fatalf("UpsertSaveTrailer() worktree mismatch with git oracle:\ngot:\n%q\nwant:\n%q", string(updated), oracleMessage)
	}

	text := string(updated)
	trailerIndex := strings.Index(text, wantTrailer)
	commentIndex := strings.Index(text, "; comment one")
	if trailerIndex < 0 || commentIndex < 0 || trailerIndex > commentIndex {
		t.Fatalf("expected trailer %q above worktree comment block in message %q", wantTrailer, text)
	}
}

func TestTrailerOracleDriftDetectors(t *testing.T) {
	requireTrailerOracleGit(t)

	cases := []struct {
		name    string
		message string
	}{
		{
			name:    "bodiless message without trailing newline",
			message: "feat: test",
		},
		{
			name:    "bodied message without trailing newline",
			message: "feat: test\n\nbody",
		},
		{
			name:    "replace existing trailer while preserving other trailers",
			message: "feat: test\n\nbody\n\nReviewed-by: QA <qa@example.com>\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n",
		},
		{
			name:    "comment block stays below inserted trailer",
			message: "feat: test\n\nbody\n\n# ------------------------ >8 ------------------------\n# comment after scissor\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoRoot := initGitRepo(t)
			ctx := mustDiscoverRepo(t, repoRoot)

			updated, saveID, err := internalgit.UpsertSaveTrailer(ctx, []byte(tc.message))
			if err != nil {
				t.Fatalf("UpsertSaveTrailer() error = %v", err)
			}

			wantTrailer := "Opax-Save: " + saveID.String()
			oracleMessage := rewriteTrailersWithGit(t, repoRoot, tc.message, wantTrailer)
			if !bytes.Equal(updated, []byte(oracleMessage)) {
				t.Fatalf("drift detector mismatch:\ngot:\n%q\nwant:\n%q", string(updated), oracleMessage)
			}

			if strings.Contains(tc.name, "comment block") {
				text := string(updated)
				trailerIndex := strings.Index(text, wantTrailer)
				commentIndex := strings.Index(text, "# ------------------------ >8 ------------------------")
				if trailerIndex < 0 || commentIndex < 0 || trailerIndex > commentIndex {
					t.Fatalf("expected trailer %q above comment block in message %q", wantTrailer, text)
				}
			}
		})
	}
}

func rewriteTrailersWithGit(t *testing.T, repoRoot, message, trailer string, extraArgs ...string) string {
	t.Helper()

	messagePath := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(messagePath, []byte(message), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", messagePath, err)
	}

	args := []string{
		"interpret-trailers",
		"--if-exists", "replace",
		"--if-missing", "add",
		"--trailer", trailer,
	}
	args = append(args, extraArgs...)
	args = append(args, messagePath)
	return runGit(t, repoRoot, args...)
}

func parseTrailersWithGit(t *testing.T, repoRoot, message string) []string {
	t.Helper()

	messagePath := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(messagePath, []byte(message), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", messagePath, err)
	}

	output := runGit(t, repoRoot, "interpret-trailers", "--parse", messagePath)
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.TrimSuffix(output, "\n")
	if output == "" {
		return nil
	}
	return strings.Split(output, "\n")
}

func requireTrailerOracleGit(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		if runningInCI() {
			t.Fatalf("git is required for trailer oracle tests in CI: %v", err)
		}
		t.Skip("git is not available; skipping trailer oracle parity tests")
	}

	versionOutput, err := exec.Command("git", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("git --version failed: %v\n%s", err, versionOutput)
	}

	version, err := parseGitVersion(string(versionOutput))
	if err != nil {
		t.Fatalf("parse git --version output %q: %v", strings.TrimSpace(string(versionOutput)), err)
	}
	if compareGitVersion(version, minimumTrailerOracleGitVersion) < 0 {
		t.Fatalf("git version %s is below required minimum %s for trailer oracle tests", version.String(), minimumTrailerOracleGitVersion.String())
	}
}

func runningInCI() bool {
	return strings.TrimSpace(os.Getenv("CI")) != "" || strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")) != ""
}

func parseGitVersion(raw string) (gitVersion, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	for _, field := range fields {
		if len(field) == 0 || field[0] < '0' || field[0] > '9' {
			continue
		}
		return parseGitVersionToken(field)
	}
	return gitVersion{}, fmt.Errorf("missing version token")
}

func parseGitVersionToken(token string) (gitVersion, error) {
	parts := strings.Split(token, ".")
	values := [3]int{}
	count := 0

	for _, part := range parts {
		if count == len(values) {
			break
		}

		digits := leadingDigits(part)
		if digits == "" {
			break
		}

		n, err := strconv.Atoi(digits)
		if err != nil {
			return gitVersion{}, fmt.Errorf("parse version component %q: %w", digits, err)
		}
		values[count] = n
		count++
	}

	if count < 2 {
		return gitVersion{}, fmt.Errorf("expected major.minor in %q", token)
	}
	return gitVersion{major: values[0], minor: values[1], patch: values[2]}, nil
}

func leadingDigits(value string) string {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			if i == 0 {
				return ""
			}
			return value[:i]
		}
	}
	return value
}

func compareGitVersion(a, b gitVersion) int {
	switch {
	case a.major != b.major:
		return compareInt(a.major, b.major)
	case a.minor != b.minor:
		return compareInt(a.minor, b.minor)
	default:
		return compareInt(a.patch, b.patch)
	}
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func containsLine(lines []string, needle string) bool {
	for _, line := range lines {
		if line == needle {
			return true
		}
	}
	return false
}

func sameLines(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for idx := range left {
		if left[idx] != right[idx] {
			return false
		}
	}
	return true
}

func filterSaveTrailers(trailers []string) []string {
	saveTrailers := make([]string, 0, len(trailers))
	for _, trailer := range trailers {
		if strings.HasPrefix(strings.ToLower(trailer), "opax-save:") {
			saveTrailers = append(saveTrailers, trailer)
		}
	}
	return saveTrailers
}

func parseTrailerValue(t *testing.T, trailer string) string {
	t.Helper()

	separator := strings.IndexByte(trailer, ':')
	if separator < 0 {
		t.Fatalf("invalid trailer line %q", trailer)
	}
	return strings.TrimSpace(trailer[separator+1:])
}
