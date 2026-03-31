package git

import (
	"encoding/json"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opax-sh/opax/internal/types"
)

type worktreeConfigFixture struct {
	mainRepo     string
	worktreeRoot string
	mainCtx      *RepoContext
	worktreeCtx  *RepoContext
	baseCommit   string
}

func TestBackendCompatOpenRepoFromContextWithWorktreeConfig(t *testing.T) {
	fixture := setupWorktreeConfigFixture(t)

	if _, err := openRepoFromContext(fixture.worktreeCtx); err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}
}

func TestBackendCompatMajorOpsWithWorktreeConfig(t *testing.T) {
	fixture := setupWorktreeConfigFixture(t)

	worktreeTip, err := EnsureOpaxBranch(fixture.worktreeCtx)
	if err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}
	mainTip, err := GetOpaxBranchTip(fixture.mainCtx)
	if err != nil {
		t.Fatalf("GetOpaxBranchTip() error = %v", err)
	}
	if mainTip != worktreeTip {
		t.Fatalf("GetOpaxBranchTip() = %s, want %s", mainTip, worktreeTip)
	}
	if err := ValidateOpaxBranch(fixture.worktreeCtx); err != nil {
		t.Fatalf("ValidateOpaxBranch() error = %v", err)
	}

	recordID := types.NewSessionID().String()
	writeResult, err := WriteRecord(fixture.worktreeCtx, WriteRequest{
		Collection: "sessions",
		RecordID:   recordID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"worktree-config"}`)},
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}

	readResult, err := ReadRecord(fixture.worktreeCtx, "sessions", recordID)
	if err != nil {
		t.Fatalf("ReadRecord() error = %v", err)
	}
	if readResult.RecordRoot != writeResult.RecordRoot {
		t.Fatalf("ReadRecord().RecordRoot = %q, want %q", readResult.RecordRoot, writeResult.RecordRoot)
	}
	if string(readResult.Files["metadata.json"]) != `{"source":"worktree-config"}` {
		t.Fatalf("ReadRecord().Files[metadata.json] = %q", readResult.Files["metadata.json"])
	}

	recordPath := pathpkg.Join(writeResult.RecordRoot, "metadata.json")
	content, err := ReadFileAtPath(fixture.worktreeCtx, recordPath)
	if err != nil {
		t.Fatalf("ReadFileAtPath() error = %v", err)
	}
	if string(content) != `{"source":"worktree-config"}` {
		t.Fatalf("ReadFileAtPath() content = %q", content)
	}

	var roots []string
	err = WalkRecords(fixture.worktreeCtx, func(locator RecordLocator) error {
		roots = append(roots, locator.RecordRoot)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkRecords() error = %v", err)
	}
	if !slices.Contains(roots, writeResult.RecordRoot) {
		t.Fatalf("WalkRecords() roots = %#v, want %q", roots, writeResult.RecordRoot)
	}

	noteNamespace := "ext-reviews"
	if err := WriteNote(fixture.worktreeCtx, types.Note{
		CommitHash: fixture.baseCommit,
		Namespace:  noteNamespace,
		Version:    1,
		Content:    mustJSONRawMessageCompat(t, `{"reviewer":"compat"}`),
	}); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}

	note, err := ReadNote(fixture.worktreeCtx, noteNamespace, fixture.baseCommit)
	if err != nil {
		t.Fatalf("ReadNote() error = %v", err)
	}
	if note.Namespace != noteNamespace {
		t.Fatalf("ReadNote().Namespace = %q, want %q", note.Namespace, noteNamespace)
	}
	if string(note.Content) != `{"reviewer":"compat"}` {
		t.Fatalf("ReadNote().Content = %q", note.Content)
	}

	notes, err := ListNotes(fixture.worktreeCtx, noteNamespace)
	if err != nil {
		t.Fatalf("ListNotes() error = %v", err)
	}
	if len(notes) != 1 || notes[0].CommitHash != fixture.baseCommit {
		t.Fatalf("ListNotes() = %#v", notes)
	}

	namespaces, err := ListNoteNamespaces(fixture.worktreeCtx)
	if err != nil {
		t.Fatalf("ListNoteNamespaces() error = %v", err)
	}
	if !slices.Contains(namespaces, noteNamespace) {
		t.Fatalf("ListNoteNamespaces() = %#v, want %q", namespaces, noteNamespace)
	}
}

func TestBackendCompatParseSaveTrailerFromCommitWithWorktreeConfig(t *testing.T) {
	fixture := setupWorktreeConfigFixture(t)

	runGitCompat(t, fixture.worktreeRoot, "config", "--worktree", "commit.cleanup", "verbatim")
	writeFileCompat(t, fixture.worktreeRoot, "feature.txt", "change\n")
	runGitCompat(t, fixture.worktreeRoot, "add", "feature.txt")

	messagePath := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	message := "feat: test\n\nbody\n\nOpax-Save: sav_01ARZ3NDEKTSV4RRFFQ69G5FAV\n; comment one\n"
	if err := os.WriteFile(messagePath, []byte(message), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", messagePath, err)
	}
	runGitCompat(t, fixture.worktreeRoot, "commit", "-F", messagePath)

	commitHash := strings.TrimSpace(runGitCompat(t, fixture.worktreeRoot, "rev-parse", "HEAD"))
	saveID, ok, err := ParseSaveTrailerFromCommit(fixture.worktreeCtx, commitHash)
	if err != nil {
		t.Fatalf("ParseSaveTrailerFromCommit() error = %v", err)
	}
	if !ok {
		t.Fatal("ParseSaveTrailerFromCommit() ok = false, want true")
	}
	if saveID.String() != "sav_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("ParseSaveTrailerFromCommit() = %q, want %q", saveID, "sav_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	}
}

func setupWorktreeConfigFixture(t *testing.T) worktreeConfigFixture {
	t.Helper()
	requireGitBinaryCompat(t)

	repoRoot := initGitRepoCompat(t)
	writeFileCompat(t, repoRoot, "README.md", "hello\n")
	runGitCompat(t, repoRoot, "add", "README.md")
	runGitCompat(t, repoRoot, "commit", "-m", "initial")
	baseCommit := strings.TrimSpace(runGitCompat(t, repoRoot, "rev-parse", "HEAD"))

	runGitCompat(t, repoRoot, "branch", "feature")
	runGitCompat(t, repoRoot, "config", "extensions.worktreeConfig", "true")

	worktreeRoot := filepath.Join(t.TempDir(), "compat-worktree")
	runGitCompat(t, repoRoot, "worktree", "add", worktreeRoot, "feature")
	runGitCompat(t, worktreeRoot, "config", "--worktree", "core.commentChar", ";")

	return worktreeConfigFixture{
		mainRepo:     repoRoot,
		worktreeRoot: worktreeRoot,
		mainCtx:      mustDiscoverRepoCompat(t, repoRoot),
		worktreeCtx:  mustDiscoverRepoCompat(t, worktreeRoot),
		baseCommit:   baseCommit,
	}
}

func requireGitBinaryCompat(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func initGitRepoCompat(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	runGitCompat(t, t.TempDir(), "init", repoRoot)
	return repoRoot
}

func writeFileCompat(t *testing.T, repoRoot, relativePath, contents string) {
	t.Helper()
	path := filepath.Join(repoRoot, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func runGitCompat(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Opax Test",
		"GIT_AUTHOR_EMAIL=opax-test@example.com",
		"GIT_COMMITTER_NAME=Opax Test",
		"GIT_COMMITTER_EMAIL=opax-test@example.com",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (dir=%s) error = %v\n%s", strings.Join(args, " "), dir, err, output)
	}
	return string(output)
}

func mustDiscoverRepoCompat(t *testing.T, repoRoot string) *RepoContext {
	t.Helper()
	ctx, err := DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo(%q) error = %v", repoRoot, err)
	}
	return ctx
}

func mustJSONRawMessageCompat(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	var parsed json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", raw, err)
	}
	return parsed
}
