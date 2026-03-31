package git_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalgit "github.com/opax-sh/opax/internal/git"
)

func TestAllocateSaveID(t *testing.T) {
	saveID := internalgit.AllocateSaveID()
	if err := saveID.Validate(); err != nil {
		t.Fatalf("AllocateSaveID() returned invalid ID %q: %v", saveID, err)
	}
}

func TestUpsertSaveTrailerPlainMessage(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}
	if err := saveID.Validate(); err != nil {
		t.Fatalf("UpsertSaveTrailer() returned invalid save ID %q: %v", saveID, err)
	}

	want := "feat: test\n\nbody\n\nOpax-Save: " + saveID.String() + "\n"
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}
}

func TestUpsertSaveTrailerSubjectOnlyMessage(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := "feat: test\n\nOpax-Save: " + saveID.String() + "\n"
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}

	if parsed := parseTrailersWithGit(t, repoRoot, string(got)); parsed != "Opax-Save: "+saveID.String() {
		t.Fatalf("git interpret-trailers --parse = %q, want %q", parsed, "Opax-Save: "+saveID.String())
	}
}

func TestUpsertSaveTrailerReplacesExistingMixedCase(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\nopax-save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	text := string(got)
	if strings.Contains(strings.ToLower(text), "sav_01arz3ndektsv4rrffq69g5fav") {
		t.Fatalf("UpsertSaveTrailer() kept stale save ID in %q", text)
	}
	want := "feat: test\n\nbody\n\nOpax-Save: " + saveID.String() + "\n"
	if text != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", text, want)
	}
}

func TestUpsertSaveTrailerPreservesOtherTrailers(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\nSigned-off-by: Dev <dev@example.com>\nReviewed-by: QA <qa@example.com>\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"",
		"body",
		"",
		"Signed-off-by: Dev <dev@example.com>",
		"Reviewed-by: QA <qa@example.com>",
		"Opax-Save: " + saveID.String(),
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}
}

func TestUpsertSaveTrailerTreatsUnseparatedTrailerLikeLinesAsBody(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\nContext: still body\nDetails: still body\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"Context: still body",
		"Details: still body",
		"",
		"Opax-Save: " + saveID.String(),
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}
}

func TestUpsertSaveTrailerPreservesCommentBlockDefaultChar(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\n# Please enter the commit message\n# line 2\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"",
		"body",
		"",
		"Opax-Save: " + saveID.String(),
		"",
		"# Please enter the commit message",
		"# line 2",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}
}

func TestUpsertSaveTrailerPreservesCommentBlockCustomChar(t *testing.T) {
	repoRoot := initGitRepo(t)
	runGit(t, repoRoot, "config", "core.commentChar", ";")
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\n; comment one\n; comment two\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"",
		"body",
		"",
		"Opax-Save: " + saveID.String(),
		"",
		"; comment one",
		"; comment two",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}
}

func TestUpsertSaveTrailerPreservesAutoCommentBlock(t *testing.T) {
	repoRoot := initGitRepo(t)
	runGit(t, repoRoot, "config", "core.commentChar", "auto")
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\n# body starts with hash\n\n; ------------------------ >8 ------------------------\n; comment after scissor\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"",
		"# body starts with hash",
		"",
		"Opax-Save: " + saveID.String(),
		"",
		"; ------------------------ >8 ------------------------",
		"; comment after scissor",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}

}

func TestUpsertSaveTrailerAutoKeepsTrailingMarkdownListInBody(t *testing.T) {
	repoRoot := initGitRepo(t)
	runGit(t, repoRoot, "config", "core.commentChar", "auto")
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\n- item one\n- item two\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"",
		"body",
		"",
		"- item one",
		"- item two",
		"",
		"Opax-Save: " + saveID.String(),
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}

	parsedID, ok, err := internalgit.ParseSaveTrailer(got)
	if err != nil {
		t.Fatalf("ParseSaveTrailer() error = %v", err)
	}
	if !ok {
		t.Fatalf("ParseSaveTrailer() ok = false, want true for %q", got)
	}
	if parsedID != saveID {
		t.Fatalf("ParseSaveTrailer() ID = %q, want %q", parsedID, saveID)
	}
}

func TestUpsertSaveTrailerAutoKeepsPunctuationParagraphInBody(t *testing.T) {
	repoRoot := initGitRepo(t)
	runGit(t, repoRoot, "config", "core.commentChar", "auto")
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\n; not template\n; still body text\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"",
		"body",
		"",
		"; not template",
		"; still body text",
		"",
		"Opax-Save: " + saveID.String(),
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}

	parsedID, ok, err := internalgit.ParseSaveTrailer(got)
	if err != nil {
		t.Fatalf("ParseSaveTrailer() error = %v", err)
	}
	if !ok {
		t.Fatalf("ParseSaveTrailer() ok = false, want true for %q", got)
	}
	if parsedID != saveID {
		t.Fatalf("ParseSaveTrailer() ID = %q, want %q", parsedID, saveID)
	}
}

func TestUpsertSaveTrailerPreservesCommentBlockMultiCharPrefix(t *testing.T) {
	repoRoot := initGitRepo(t)
	runGit(t, repoRoot, "config", "core.commentChar", "//")
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\n// comment one\n// comment two\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"",
		"body",
		"",
		"Opax-Save: " + saveID.String(),
		"",
		"// comment one",
		"// comment two",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}
}

func TestUpsertSaveTrailerHandlesScissors(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	message := []byte("feat: test\n\nbody\n\n# ------------------------ >8 ------------------------\n# comment after scissor\n")
	got, saveID, err := internalgit.UpsertSaveTrailer(ctx, message)
	if err != nil {
		t.Fatalf("UpsertSaveTrailer() error = %v", err)
	}

	want := strings.Join([]string{
		"feat: test",
		"",
		"body",
		"",
		"Opax-Save: " + saveID.String(),
		"",
		"# ------------------------ >8 ------------------------",
		"# comment after scissor",
		"",
	}, "\n")
	if string(got) != want {
		t.Fatalf("UpsertSaveTrailer() = %q, want %q", got, want)
	}
}

func TestParseSaveTrailerAbsent(t *testing.T) {
	saveID, ok, err := internalgit.ParseSaveTrailer([]byte("feat: test\n\nbody\n"))
	if err != nil {
		t.Fatalf("ParseSaveTrailer() error = %v", err)
	}
	if ok {
		t.Fatalf("ParseSaveTrailer() ok = true, want false with ID %q", saveID)
	}
	if saveID != "" {
		t.Fatalf("ParseSaveTrailer() ID = %q, want empty", saveID)
	}
}

func TestParseSaveTrailerRequiresBlankSeparator(t *testing.T) {
	saveID, ok, err := internalgit.ParseSaveTrailer([]byte("feat: test\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n"))
	if err != nil {
		t.Fatalf("ParseSaveTrailer() error = %v", err)
	}
	if ok {
		t.Fatalf("ParseSaveTrailer() ok = true, want false with ID %q", saveID)
	}
	if saveID != "" {
		t.Fatalf("ParseSaveTrailer() ID = %q, want empty", saveID)
	}
}

func TestParseSaveTrailerMalformedValue(t *testing.T) {
	_, _, err := internalgit.ParseSaveTrailer([]byte("feat: test\n\nbody\n\nOpax-Save: sav_not-a-ulid\n"))
	if err == nil {
		t.Fatal("ParseSaveTrailer() error = nil, want malformed value error")
	}
	if !strings.Contains(err.Error(), "invalid Opax-Save value") {
		t.Fatalf("ParseSaveTrailer() error = %v, want invalid value message", err)
	}
}

func TestParseSaveTrailerDuplicateMixedCase(t *testing.T) {
	message := []byte("feat: test\n\nbody\n\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\nopax-save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAW\n")
	_, _, err := internalgit.ParseSaveTrailer(message)
	if err == nil {
		t.Fatal("ParseSaveTrailer() error = nil, want duplicate error")
	}
	if !strings.Contains(err.Error(), "multiple Opax-Save trailers") {
		t.Fatalf("ParseSaveTrailer() error = %v, want duplicate trailer message", err)
	}
}

func TestParseSaveTrailerFromCommit(t *testing.T) {
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

func TestUpsertSaveTrailerParseRoundTripSupportedMessages(t *testing.T) {
	cases := []struct {
		name        string
		commentChar string
		message     string
	}{
		{
			name:    "plain message",
			message: "feat: test\n\nbody\n",
		},
		{
			name:    "existing mixed-case save trailer",
			message: "feat: test\n\nbody\n\nopax-save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n",
		},
		{
			name:    "preserve other trailers",
			message: "feat: test\n\nbody\n\nSigned-off-by: Dev <dev@example.com>\n",
		},
		{
			name:        "auto ambiguous punctuation remains body",
			commentChar: "auto",
			message:     "feat: test\n\nbody\n\n; not template\n; still body text\n",
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

			parsedID, ok, err := internalgit.ParseSaveTrailer(updated)
			if err != nil {
				t.Fatalf("ParseSaveTrailer() error = %v", err)
			}
			if !ok {
				t.Fatalf("ParseSaveTrailer() ok = false for updated message %q", updated)
			}
			if parsedID != saveID {
				t.Fatalf("ParseSaveTrailer() ID = %q, want %q", parsedID, saveID)
			}
		})
	}
}

func parseTrailersWithGit(t *testing.T, dir, message string) string {
	t.Helper()

	messagePath := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(messagePath, []byte(message), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", messagePath, err)
	}

	return strings.TrimSpace(runGit(t, dir, "interpret-trailers", "--parse", messagePath))
}
