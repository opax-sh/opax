package git_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	internalgit "github.com/opax-sh/opax/internal/git"
	"github.com/opax-sh/opax/internal/types"
)

const notesRefPrefix = "refs/notes/opax/"

func TestWriteNoteSessionsNamespace(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	note := types.Note{
		CommitHash: commitHash,
		Namespace:  "sessions",
		Version:    1,
		Content:    mustJSONRawMessage(t, `{"session_id":"ses_123"}`),
	}
	if err := internalgit.WriteNote(ctx, note); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}

	repo := mustOpenRepo(t, repoRoot)
	ref, err := repo.Reference(plumbing.ReferenceName(notesRefPrefix+"sessions"), true)
	if err != nil {
		t.Fatalf("Reference(%q) error = %v", notesRefPrefix+"sessions", err)
	}
	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("CommitObject(%s) error = %v", ref.Hash(), err)
	}

	path := expectedNoteFanoutPath(commitHash)
	if got := readFileAtCommitPath(t, commit, path); got != `{"session_id":"ses_123","version":1}` {
		t.Fatalf("stored note payload = %q, want merged payload", got)
	}
}

func TestWriteNoteExtensionNamespace(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	note := types.Note{
		CommitHash: commitHash,
		Namespace:  "ext-reviews",
		Version:    1,
		Content:    mustJSONRawMessage(t, `{"reviewer":"qa","verdict":"pass"}`),
	}
	if err := internalgit.WriteNote(ctx, note); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}

	repo := mustOpenRepo(t, repoRoot)
	if _, err := repo.Reference(plumbing.ReferenceName(notesRefPrefix+"ext-reviews"), true); err != nil {
		t.Fatalf("Reference(%q) error = %v", notesRefPrefix+"ext-reviews", err)
	}
}

func TestWriteNoteRejectsBadNamespace(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	err := internalgit.WriteNote(ctx, types.Note{
		CommitHash: commitHash,
		Namespace:  "ext/reviews",
		Version:    1,
		Content:    mustJSONRawMessage(t, `{"reviewer":"qa"}`),
	})
	if err == nil {
		t.Fatal("WriteNote() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "must not contain slash") {
		t.Fatalf("WriteNote() error = %v, want slash validation", err)
	}
}

func TestWriteNoteRejectsMissingCommit(t *testing.T) {
	repoRoot, _ := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	err := internalgit.WriteNote(ctx, types.Note{
		CommitHash: strings.Repeat("a", 40),
		Namespace:  "sessions",
		Version:    1,
		Content:    mustJSONRawMessage(t, `{"session_id":"ses_123"}`),
	})
	if err == nil {
		t.Fatal("WriteNote() error = nil, want missing-commit error")
	}
}

func TestWriteNoteRejectsReservedVersionInContent(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	err := internalgit.WriteNote(ctx, types.Note{
		CommitHash: commitHash,
		Namespace:  "sessions",
		Version:    1,
		Content:    mustJSONRawMessage(t, `{"version":2,"session_id":"ses_123"}`),
	})
	if err == nil {
		t.Fatal("WriteNote() error = nil, want reserved-field validation")
	}
	if !strings.Contains(err.Error(), `reserved field "version"`) {
		t.Fatalf("WriteNote() error = %v, want reserved-version validation", err)
	}
}

func TestWriteNoteBootstrapsRef(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	if err := internalgit.WriteNote(ctx, types.Note{
		CommitHash: commitHash,
		Namespace:  "sessions",
		Version:    1,
		Content:    mustJSONRawMessage(t, `{"session_id":"ses_123"}`),
	}); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}

	repo := mustOpenRepo(t, repoRoot)
	if _, err := repo.Reference(plumbing.ReferenceName(notesRefPrefix+"sessions"), true); err != nil {
		t.Fatalf("Reference(%q) error = %v", notesRefPrefix+"sessions", err)
	}
}

func TestWriteNoteConcurrentDistinctTargets(t *testing.T) {
	repoRoot, firstCommit := initGitRepoWithCommit(t)
	secondCommit := commitFile(t, repoRoot, "b.txt", "two\n", "second")
	ctx := mustDiscoverRepo(t, repoRoot)

	targets := []string{firstCommit, secondCommit}
	errCh := make(chan error, len(targets))
	var wg sync.WaitGroup
	for i, commitHash := range targets {
		wg.Add(1)
		go func(i int, commitHash string) {
			defer wg.Done()
			errCh <- internalgit.WriteNote(ctx, types.Note{
				CommitHash: commitHash,
				Namespace:  "ext-reviews",
				Version:    1,
				Content:    mustJSONRawMessage(t, fmt.Sprintf(`{"reviewer":"qa-%d"}`, i)),
			})
		}(i, commitHash)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("WriteNote() error = %v", err)
		}
	}

	notes, err := internalgit.ListNotes(ctx, "ext-reviews")
	if err != nil {
		t.Fatalf("ListNotes() error = %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("len(ListNotes()) = %d, want 2", len(notes))
	}
}

func TestWriteNoteConcurrentOverwrite(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for _, reviewer := range []string{"qa-a", "qa-b"} {
		wg.Add(1)
		go func(reviewer string) {
			defer wg.Done()
			errCh <- internalgit.WriteNote(ctx, types.Note{
				CommitHash: commitHash,
				Namespace:  "ext-reviews",
				Version:    1,
				Content:    mustJSONRawMessage(t, fmt.Sprintf(`{"reviewer":%q}`, reviewer)),
			})
		}(reviewer)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("WriteNote() error = %v", err)
		}
	}

	note, err := internalgit.ReadNote(ctx, "ext-reviews", commitHash)
	if err != nil {
		t.Fatalf("ReadNote() error = %v", err)
	}
	got := string(note.Content)
	if got != `{"reviewer":"qa-a"}` && got != `{"reviewer":"qa-b"}` {
		t.Fatalf("ReadNote().Content = %q, want one writer to win", got)
	}
}

func TestReadNoteSplitsVersionFromContent(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	if err := internalgit.WriteNote(ctx, types.Note{
		CommitHash: commitHash,
		Namespace:  "ext-reviews",
		Version:    2,
		Content:    mustJSONRawMessage(t, `{"reviewer":"qa","verdict":"pass"}`),
	}); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}

	note, err := internalgit.ReadNote(ctx, "ext-reviews", commitHash)
	if err != nil {
		t.Fatalf("ReadNote() error = %v", err)
	}
	if note.Version != 2 {
		t.Fatalf("ReadNote().Version = %d, want 2", note.Version)
	}
	if string(note.Content) != `{"reviewer":"qa","verdict":"pass"}` {
		t.Fatalf("ReadNote().Content = %q", note.Content)
	}
}

func TestReadNoteGitNotesInterop(t *testing.T) {
	requireGitBinary(t)

	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	runGit(t, repoRoot, "notes", "--ref=opax/ext-reviews", "add", "-m", `{"version":1,"reviewer":"qa","verdict":"pass"}`, commitHash)

	note, err := internalgit.ReadNote(ctx, "ext-reviews", commitHash)
	if err != nil {
		t.Fatalf("ReadNote() error = %v", err)
	}
	if note.Version != 1 {
		t.Fatalf("ReadNote().Version = %d, want 1", note.Version)
	}
	if string(note.Content) != `{"reviewer":"qa","verdict":"pass"}` {
		t.Fatalf("ReadNote().Content = %q", note.Content)
	}
}

func TestWriteNoteGitNotesInterop(t *testing.T) {
	requireGitBinary(t)

	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	if err := internalgit.WriteNote(ctx, types.Note{
		CommitHash: commitHash,
		Namespace:  "ext-reviews",
		Version:    1,
		Content:    mustJSONRawMessage(t, `{"reviewer":"qa","verdict":"pass"}`),
	}); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}

	got := strings.TrimSpace(runGit(t, repoRoot, "notes", "--ref=opax/ext-reviews", "show", commitHash))
	if got != `{"reviewer":"qa","verdict":"pass","version":1}` {
		t.Fatalf("git notes show = %q", got)
	}
}

func TestListNotesReadsFanoutAndFlatLayouts(t *testing.T) {
	requireGitBinary(t)

	repoRoot, firstCommit := initGitRepoWithCommit(t)
	secondCommit := commitFile(t, repoRoot, "b.txt", "two\n", "second")
	ctx := mustDiscoverRepo(t, repoRoot)

	if err := internalgit.WriteNote(ctx, types.Note{
		CommitHash: firstCommit,
		Namespace:  "ext-reviews",
		Version:    1,
		Content:    mustJSONRawMessage(t, `{"reviewer":"opax"}`),
	}); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}
	runGit(t, repoRoot, "notes", "--ref=opax/ext-reviews", "add", "-m", `{"version":2,"reviewer":"git"}`, secondCommit)

	notes, err := internalgit.ListNotes(ctx, "ext-reviews")
	if err != nil {
		t.Fatalf("ListNotes() error = %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("len(ListNotes()) = %d, want 2", len(notes))
	}
	gotByHash := map[string]string{
		notes[0].CommitHash: string(notes[0].Content),
		notes[1].CommitHash: string(notes[1].Content),
	}
	if gotByHash[firstCommit] != `{"reviewer":"opax"}` || gotByHash[secondCommit] != `{"reviewer":"git"}` {
		t.Fatalf("ListNotes() contents by hash = %#v", gotByHash)
	}
}

func TestListNoteNamespaces(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	for _, namespace := range []string{"ext-reviews", "sessions"} {
		if err := internalgit.WriteNote(ctx, types.Note{
			CommitHash: commitHash,
			Namespace:  namespace,
			Version:    1,
			Content:    mustJSONRawMessage(t, `{"ok":true}`),
		}); err != nil {
			t.Fatalf("WriteNote(%q) error = %v", namespace, err)
		}
	}

	namespaces, err := internalgit.ListNoteNamespaces(ctx)
	if err != nil {
		t.Fatalf("ListNoteNamespaces() error = %v", err)
	}
	if len(namespaces) != 2 || namespaces[0] != "ext-reviews" || namespaces[1] != "sessions" {
		t.Fatalf("ListNoteNamespaces() = %#v", namespaces)
	}
}

func TestReadNoteMalformedPayload(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)
	repo := mustOpenRepo(t, repoRoot)

	installNoteRefWithBlob(t, repo, "ext-reviews", commitHash, []byte("not-json"))

	_, err := internalgit.ReadNote(ctx, "ext-reviews", commitHash)
	if !errors.Is(err, internalgit.ErrMalformedNote) {
		t.Fatalf("ReadNote() error = %v, want ErrMalformedNote", err)
	}
}

func TestListNoteNamespacesRejectsNestedRef(t *testing.T) {
	repoRoot, commitHash := initGitRepoWithCommit(t)
	ctx := mustDiscoverRepo(t, repoRoot)
	repo := mustOpenRepo(t, repoRoot)

	commit := mustCommitObject(t, repo, plumbing.NewHash(commitHash))
	ref := plumbing.NewHashReference(plumbing.ReferenceName("refs/notes/opax/ext/reviews"), commit.Hash)
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatalf("SetReference(%q) error = %v", ref.Name(), err)
	}

	_, err := internalgit.ListNoteNamespaces(ctx)
	if !errors.Is(err, internalgit.ErrMalformedNote) {
		t.Fatalf("ListNoteNamespaces() error = %v, want ErrMalformedNote", err)
	}
}

func initGitRepoWithCommit(t *testing.T) (string, string) {
	t.Helper()

	repoRoot := initGitRepo(t)
	writeTrackedFile(t, repoRoot, "README.md", "hello\n")
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "initial")
	return repoRoot, strings.TrimSpace(runGit(t, repoRoot, "rev-parse", "HEAD"))
}

func commitFile(t *testing.T, repoRoot, relativePath, contents, message string) string {
	t.Helper()

	writeTrackedFile(t, repoRoot, relativePath, contents)
	runGit(t, repoRoot, "add", relativePath)
	runGit(t, repoRoot, "commit", "-m", message)
	return strings.TrimSpace(runGit(t, repoRoot, "rev-parse", "HEAD"))
}

func mustJSONRawMessage(t *testing.T, raw string) json.RawMessage {
	t.Helper()
	var parsed json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", raw, err)
	}
	return parsed
}

func expectedNoteFanoutPath(commitHash string) string {
	return filepath.ToSlash(filepath.Join(commitHash[:2], commitHash[2:]))
}

func installNoteRefWithBlob(t *testing.T, repo *ggit.Repository, namespace, commitHash string, blob []byte) {
	t.Helper()

	blobHash := writeBlobObject(t, repo, blob)
	targetHash := plumbing.NewHash(commitHash)
	path := expectedNoteFanoutPath(targetHash.String())
	segments := strings.Split(path, "/")

	shardTreeHash := writeTreeObject(t, repo, []object.TreeEntry{{
		Name: segments[1],
		Mode: filemode.Regular,
		Hash: blobHash,
	}})
	rootTreeHash := writeTreeObject(t, repo, []object.TreeEntry{{
		Name: segments[0],
		Mode: filemode.Dir,
		Hash: shardTreeHash,
	}})

	targetCommit := mustCommitObject(t, repo, targetHash)
	noteCommitHash := writeCommitObject(t, repo, rootTreeHash, targetCommit.Hash, "opax: malformed note")
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(notesRefPrefix+namespace), noteCommitHash)); err != nil {
		t.Fatalf("SetReference(%q) error = %v", notesRefPrefix+namespace, err)
	}
}

func mustCommitObject(t *testing.T, repo *ggit.Repository, hash plumbing.Hash) *object.Commit {
	t.Helper()

	commit, err := repo.CommitObject(hash)
	if err != nil {
		t.Fatalf("CommitObject(%s) error = %v", hash, err)
	}
	return commit
}
