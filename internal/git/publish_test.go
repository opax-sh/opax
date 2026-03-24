package git

import (
	"fmt"
	"testing"
	"time"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestPublishRefWithRetryRetriesOnChangedReference(t *testing.T) {
	repoRoot := t.TempDir()
	if _, err := ggit.PlainInit(repoRoot, false); err != nil {
		t.Fatalf("PlainInit(%q) error = %v", repoRoot, err)
	}

	ctx, err := DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo(%q) error = %v", repoRoot, err)
	}

	if _, err := EnsureOpaxBranch(ctx); err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}

	concurrentRepo, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	attempts := 0
	refName := plumbing.ReferenceName(opaxBranchRef)

	publishedRef, err := publishRefWithRetry(ctx, refName, func(repo *ggit.Repository, currentRef *plumbing.Reference) (*plumbing.Reference, error) {
		attempts++
		if currentRef == nil {
			t.Fatal("publish builder currentRef = nil, want existing opax branch tip")
		}

		if attempts == 1 {
			conflictHash, err := writeChildCommit(repo, currentRef.Hash(), "opax: conflict write")
			if err != nil {
				t.Fatalf("writeChildCommit(conflict) error = %v", err)
			}
			if err := concurrentRepo.Storer.CheckAndSetReference(
				plumbing.NewHashReference(refName, conflictHash),
				currentRef,
			); err != nil {
				t.Fatalf("CheckAndSetReference(conflict) error = %v", err)
			}
		}

		nextHash, err := writeChildCommit(repo, currentRef.Hash(), fmt.Sprintf("opax: publish attempt %d", attempts))
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

	repo, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}
	currentRef, err := repo.Reference(refName, true)
	if err != nil {
		t.Fatalf("Reference(%s) error = %v", refName, err)
	}
	if currentRef.Hash() != publishedRef.Hash() {
		t.Fatalf("published tip = %s, branch tip = %s", publishedRef.Hash(), currentRef.Hash())
	}
}

func TestPublishRefWithRetryRetriesWhenRefCreatedConcurrently(t *testing.T) {
	repoRoot := t.TempDir()
	if _, err := ggit.PlainInit(repoRoot, false); err != nil {
		t.Fatalf("PlainInit(%q) error = %v", repoRoot, err)
	}

	ctx, err := DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo(%q) error = %v", repoRoot, err)
	}

	concurrentRepo, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	attempts := 0
	refName := plumbing.ReferenceName("refs/heads/opax/publish-retry-missing")
	conflictHash := plumbing.ZeroHash

	publishedRef, err := publishRefWithRetry(ctx, refName, func(repo *ggit.Repository, currentRef *plumbing.Reference) (*plumbing.Reference, error) {
		attempts++

		if attempts == 1 {
			if currentRef != nil {
				t.Fatalf("first publish attempt currentRef = %v, want nil for missing ref", currentRef)
			}

			conflictHash, err = writeRootCommit(repo, "opax: conflict write")
			if err != nil {
				t.Fatalf("writeRootCommit(conflict) error = %v", err)
			}
			if err := concurrentRepo.Storer.SetReference(
				plumbing.NewHashReference(refName, conflictHash),
			); err != nil {
				t.Fatalf("SetReference(conflict) error = %v", err)
			}

			nextHash, err := writeRootCommit(repo, "opax: publish attempt 1")
			if err != nil {
				return nil, err
			}
			return plumbing.NewHashReference(refName, nextHash), nil
		}

		if currentRef == nil {
			t.Fatal("publish builder currentRef = nil on retry, want competing ref tip")
		}
		nextHash, err := writeChildCommit(repo, currentRef.Hash(), fmt.Sprintf("opax: publish attempt %d", attempts))
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

	repo, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}
	currentRef, err := repo.Reference(refName, true)
	if err != nil {
		t.Fatalf("Reference(%s) error = %v", refName, err)
	}
	if currentRef.Hash() != publishedRef.Hash() {
		t.Fatalf("published tip = %s, branch tip = %s", publishedRef.Hash(), currentRef.Hash())
	}

	publishedCommit, err := repo.CommitObject(publishedRef.Hash())
	if err != nil {
		t.Fatalf("CommitObject(%s) error = %v", publishedRef.Hash(), err)
	}
	if publishedCommit.NumParents() != 1 {
		t.Fatalf("published commit parent count = %d, want 1", publishedCommit.NumParents())
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

func writeChildCommit(repo *ggit.Repository, parent plumbing.Hash, message string) (plumbing.Hash, error) {
	parentCommit, err := repo.CommitObject(parent)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	now := time.Now().UTC()
	return writeCommit(repo, &object.Commit{
		Author: object.Signature{
			Name:  opaxAuthorName,
			Email: opaxAuthorEmail,
			When:  now,
		},
		Committer: object.Signature{
			Name:  opaxAuthorName,
			Email: opaxAuthorEmail,
			When:  now,
		},
		Message:      message,
		TreeHash:     parentCommit.TreeHash,
		ParentHashes: []plumbing.Hash{parent},
	})
}

func writeRootCommit(repo *ggit.Repository, message string) (plumbing.Hash, error) {
	treeHash, err := writeTree(repo, nil)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	now := time.Now().UTC()
	return writeCommit(repo, &object.Commit{
		Author: object.Signature{
			Name:  opaxAuthorName,
			Email: opaxAuthorEmail,
			When:  now,
		},
		Committer: object.Signature{
			Name:  opaxAuthorName,
			Email: opaxAuthorEmail,
			When:  now,
		},
		Message:  message,
		TreeHash: treeHash,
	})
}
