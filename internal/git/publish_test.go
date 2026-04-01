package git

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

func TestPublishRefWithRetryRetriesOnChangedReference(t *testing.T) {
	repoRoot := initGitRepoPublish(t)

	ctx, err := DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo(%q) error = %v", repoRoot, err)
	}

	if _, err := EnsureOpaxBranch(ctx); err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}

	concurrentBackend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	attempts := 0
	refName := plumbing.ReferenceName(opaxBranchRef)

	publishedRef, err := publishRefWithRetry(ctx, refName, func(backend *nativeGitBackend, currentRef *plumbing.Reference) (*plumbing.Reference, error) {
		attempts++
		if currentRef == nil {
			t.Fatal("publish builder currentRef = nil, want existing opax branch tip")
		}

		if attempts == 1 {
			conflictHash, err := writeChildCommit(concurrentBackend, currentRef.Hash(), "opax: conflict write")
			if err != nil {
				t.Fatalf("writeChildCommit(conflict) error = %v", err)
			}
			expected := currentRef.Hash()
			if err := concurrentBackend.updateRefCAS(refName, conflictHash, &expected); err != nil {
				t.Fatalf("updateRefCAS(conflict) error = %v", err)
			}
		}

		nextHash, err := writeChildCommit(backend, currentRef.Hash(), fmt.Sprintf("opax: publish attempt %d", attempts))
		if err != nil {
			return nil, err
		}
		return plumbing.NewHashReference(refName, nextHash), nil
	})
	if err != nil {
		t.Fatalf("publishRefWithRetry() error = %v", err)
	}

	if attempts < 2 {
		t.Fatalf("publishRefWithRetry() attempts = %d, want >= 2", attempts)
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}
	currentRef, err := backend.readRef(refName)
	if err != nil {
		t.Fatalf("readRef(%s) error = %v", refName, err)
	}
	if currentRef == nil {
		t.Fatalf("readRef(%s) returned nil", refName)
	}
	if currentRef.Hash() != publishedRef.Hash() {
		t.Fatalf("published tip = %s, branch tip = %s", publishedRef.Hash(), currentRef.Hash())
	}
}

func TestPublishRefWithRetryRetriesWhenRefCreatedConcurrently(t *testing.T) {
	repoRoot := initGitRepoPublish(t)

	ctx, err := DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo(%q) error = %v", repoRoot, err)
	}

	concurrentBackend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	attempts := 0
	refName := plumbing.ReferenceName("refs/heads/opax/publish-retry-missing")
	conflictHash := plumbing.ZeroHash

	publishedRef, err := publishRefWithRetry(ctx, refName, func(backend *nativeGitBackend, currentRef *plumbing.Reference) (*plumbing.Reference, error) {
		attempts++

		if attempts == 1 {
			if currentRef != nil {
				t.Fatalf("first publish attempt currentRef = %v, want nil for missing ref", currentRef)
			}

			var err error
			conflictHash, err = writeRootCommit(concurrentBackend, "opax: conflict write")
			if err != nil {
				t.Fatalf("writeRootCommit(conflict) error = %v", err)
			}
			zero := plumbing.ZeroHash
			if err := concurrentBackend.updateRefCAS(refName, conflictHash, &zero); err != nil {
				t.Fatalf("updateRefCAS(conflict) error = %v", err)
			}

			nextHash, err := writeRootCommit(backend, "opax: publish attempt 1")
			if err != nil {
				return nil, err
			}
			return plumbing.NewHashReference(refName, nextHash), nil
		}

		if currentRef == nil {
			t.Fatal("publish builder currentRef = nil on retry, want competing ref tip")
		}
		nextHash, err := writeChildCommit(backend, currentRef.Hash(), fmt.Sprintf("opax: publish attempt %d", attempts))
		if err != nil {
			return nil, err
		}
		return plumbing.NewHashReference(refName, nextHash), nil
	})
	if err != nil {
		t.Fatalf("publishRefWithRetry() error = %v", err)
	}

	if attempts < 2 {
		t.Fatalf("publishRefWithRetry() attempts = %d, want >= 2", attempts)
	}
	if conflictHash == plumbing.ZeroHash {
		t.Fatal("conflictHash = zero hash, want recorded concurrent write")
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}
	currentRef, err := backend.readRef(refName)
	if err != nil {
		t.Fatalf("readRef(%s) error = %v", refName, err)
	}
	if currentRef == nil {
		t.Fatalf("readRef(%s) returned nil", refName)
	}
	if currentRef.Hash() != publishedRef.Hash() {
		t.Fatalf("published tip = %s, branch tip = %s", publishedRef.Hash(), currentRef.Hash())
	}

	publishedCommit, err := backend.readCommit(publishedRef.Hash())
	if err != nil {
		t.Fatalf("readCommit(%s) error = %v", publishedRef.Hash(), err)
	}
	if len(publishedCommit.ParentHashes) != 1 {
		t.Fatalf("published commit parent count = %d, want 1", len(publishedCommit.ParentHashes))
	}
	if publishedCommit.ParentHashes[0] != conflictHash {
		t.Fatalf("published parent = %s, want conflict tip %s", publishedCommit.ParentHashes[0], conflictHash)
	}
}

func TestRefPublishBackoffCaps(t *testing.T) {
	if got := refPublishBackoff(1); got != 10*time.Millisecond {
		t.Fatalf("refPublishBackoff(1) = %s, want 10ms", got)
	}
	if got := refPublishBackoff(2); got != 20*time.Millisecond {
		t.Fatalf("refPublishBackoff(2) = %s, want 20ms", got)
	}
	if got := refPublishBackoff(8); got != 100*time.Millisecond {
		t.Fatalf("refPublishBackoff(8) = %s, want 100ms cap", got)
	}
}

func writeChildCommit(backend *nativeGitBackend, parent plumbing.Hash, message string) (plumbing.Hash, error) {
	parentCommit, err := backend.readCommit(parent)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	now := time.Now().UTC()
	return backend.writeCommit(gitCommitWriteRequest{
		TreeHash:       parentCommit.TreeHash,
		ParentHashes:   []plumbing.Hash{parent},
		Message:        message,
		AuthorName:     opaxAuthorName,
		AuthorEmail:    opaxAuthorEmail,
		CommitterName:  opaxAuthorName,
		CommitterEmail: opaxAuthorEmail,
		When:           now,
	})
}

func writeRootCommit(backend *nativeGitBackend, message string) (plumbing.Hash, error) {
	treeHash, err := backend.writeTree(nil)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	now := time.Now().UTC()
	return backend.writeCommit(gitCommitWriteRequest{
		TreeHash:       treeHash,
		ParentHashes:   nil,
		Message:        message,
		AuthorName:     opaxAuthorName,
		AuthorEmail:    opaxAuthorEmail,
		CommitterName:  opaxAuthorName,
		CommitterEmail: opaxAuthorEmail,
		When:           now,
	})
}

func initGitRepoPublish(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	runGitPublish(t, t.TempDir(), "init", repoRoot)
	return repoRoot
}

func runGitPublish(t *testing.T, dir string, args ...string) string {
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
