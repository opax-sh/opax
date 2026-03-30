package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/opax-sh/opax/internal/lock"
)

// EnsureOpaxDir creates CommonGitDir/opax if it does not already exist.
func EnsureOpaxDir(ctx *RepoContext) error {
	if ctx == nil {
		return fmt.Errorf("git: repo context is nil")
	}
	if ctx.CommonGitDir == "" {
		return fmt.Errorf("git: common git dir is empty")
	}
	if ctx.OpaxDir == "" {
		return fmt.Errorf("git: opax dir is empty")
	}
	if err := ensureExistingDir(ctx.CommonGitDir, "common git dir"); err != nil {
		return err
	}

	info, err := os.Stat(ctx.OpaxDir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("git: opax path is not a directory: %s", ctx.OpaxDir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("git: stat opax dir %s: %w", ctx.OpaxDir, err)
	}

	if err := os.MkdirAll(ctx.OpaxDir, 0o755); err != nil {
		return fmt.Errorf("git: create opax dir %s: %w", ctx.OpaxDir, err)
	}
	return nil
}

// EnsureOpaxBranch creates refs/heads/opax/v1 if absent and validates it if
// present. It returns the current branch tip after creation or validation.
func EnsureOpaxBranch(ctx *RepoContext) (tip plumbing.Hash, err error) {
	lockPath, err := opaxLockPath(ctx)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	timeout := lock.DefaultTimeout
	deadline := time.Now().Add(timeout)

	for {
		repo, err := openRepoFromContext(ctx)
		if err != nil {
			return plumbing.ZeroHash, err
		}

		tip, _, err = resolveOpaxBranchTip(repo)
		if err == nil {
			if err := validateOpaxBranch(repo); err != nil {
				return plumbing.ZeroHash, err
			}
			return tip, nil
		}
		if !errors.Is(err, plumbing.ErrReferenceNotFound) {
			return plumbing.ZeroHash, err
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return plumbing.ZeroHash, fmt.Errorf(
				"git: timed out waiting for opax branch bootstrap after %s",
				timeout,
			)
		}

		branchLock, err := lock.Acquire(lockPath, remaining)
		if err != nil {
			switch {
			case errors.Is(err, lock.ErrAlreadyHeldByCurrentProcess):
				time.Sleep(opaxBootstrapPoll)
				continue
			case errors.Is(err, lock.ErrLockTimeout):
				return plumbing.ZeroHash, fmt.Errorf(
					"git: timed out waiting for opax branch bootstrap after %s",
					timeout,
				)
			default:
				return plumbing.ZeroHash, fmt.Errorf("git: acquire bootstrap lock %s: %w", lockPath, err)
			}
		}

		tip, err = ensureOpaxBranchWhileLocked(ctx)
		releaseErr := branchLock.Release()
		if err == nil && releaseErr != nil {
			err = fmt.Errorf("git: release bootstrap lock %s: %w", lockPath, releaseErr)
		}
		if err != nil {
			return plumbing.ZeroHash, err
		}
		return tip, nil
	}
}

func ensureOpaxBranchWhileLocked(ctx *RepoContext) (plumbing.Hash, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	tip, _, err := resolveOpaxBranchTip(repo)
	if err == nil {
		if err := validateOpaxBranch(repo); err != nil {
			return plumbing.ZeroHash, err
		}
		return tip, nil
	}
	if !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return plumbing.ZeroHash, err
	}

	tip, err = createOpaxBranch(repo)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if err := validateOpaxBranch(repo); err != nil {
		return plumbing.ZeroHash, err
	}

	return tip, nil
}

// GetOpaxBranchTip returns the current opax/v1 tip if the branch exists.
func GetOpaxBranchTip(ctx *RepoContext) (plumbing.Hash, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	tip, _, err := resolveOpaxBranchTip(repo)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return plumbing.ZeroHash, fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, err)
		}
		return plumbing.ZeroHash, err
	}
	return tip, nil
}

// ValidateOpaxBranch verifies that the branch identity and sentinel are
// correct.
func ValidateOpaxBranch(ctx *RepoContext) error {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return err
	}
	return validateOpaxBranch(repo)
}

func resolveOpaxBranchTip(repo *ggit.Repository) (plumbing.Hash, *object.Commit, error) {
	refName := plumbing.ReferenceName(opaxBranchRef)
	ref, err := repo.Reference(refName, false)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	if ref.Type() == plumbing.SymbolicReference {
		return plumbing.ZeroHash, nil, fmt.Errorf(
			"git: opax branch %s is symbolic ref to %s",
			opaxBranchRef,
			ref.Target(),
		)
	}
	if ref.Type() != plumbing.HashReference {
		return plumbing.ZeroHash, nil, fmt.Errorf(
			"git: opax branch %s has unsupported reference type %v",
			opaxBranchRef,
			ref.Type(),
		)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("git: opax branch %s does not point to a commit: %w", opaxBranchRef, err)
	}
	return ref.Hash(), commit, nil
}

func createOpaxBranch(repo *ggit.Repository) (plumbing.Hash, error) {
	sentinel := opaxBranchSentinel{
		Branch:        opaxBranchName,
		LayoutVersion: opaxLayoutVersion,
		CreatedBy:     opaxSentinelCreator,
	}
	data, err := json.MarshalIndent(sentinel, "", "  ")
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: encode %s: %w", opaxSentinelPath, err)
	}
	data = append(data, '\n')

	blobHash, err := writeBlob(repo, data)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	metaTreeHash, err := writeTree(repo, []object.TreeEntry{
		{Name: "version.json", Mode: filemode.Regular, Hash: blobHash},
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}
	rootTreeHash, err := writeTree(repo, []object.TreeEntry{
		{Name: "meta", Mode: filemode.Dir, Hash: metaTreeHash},
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}

	now := time.Now().UTC()
	commitHash, err := writeCommit(repo, &object.Commit{
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
		Message:  opaxInitMessage,
		TreeHash: rootTreeHash,
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}

	ref := plumbing.NewHashReference(plumbing.ReferenceName(opaxBranchRef), commitHash)
	if err := repo.Storer.CheckAndSetReference(ref, nil); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: set ref %s: %w", opaxBranchRef, err)
	}

	return commitHash, nil
}

func validateOpaxBranch(repo *ggit.Repository) error {
	_, _, err := resolveValidatedOpaxBranchTip(repo)
	return err
}

func resolveValidatedOpaxBranchTip(repo *ggit.Repository) (plumbing.Hash, *object.Commit, error) {
	tipHash, tipCommit, err := resolveOpaxBranchTip(repo)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return plumbing.ZeroHash, nil, fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, err)
		}
		return plumbing.ZeroHash, nil, err
	}

	if err := validateResolvedOpaxBranchTip(tipHash, tipCommit); err != nil {
		return plumbing.ZeroHash, nil, err
	}

	return tipHash, tipCommit, nil
}

func validateResolvedOpaxBranchTip(tipHash plumbing.Hash, tipCommit *object.Commit) error {
	tipSentinel, err := readOpaxSentinel(tipCommit)
	if err != nil {
		return fmt.Errorf("git: validate opax branch tip %s: %w", tipHash, err)
	}
	if err := validateOpaxSentinel(tipSentinel); err != nil {
		return fmt.Errorf("git: validate opax branch tip %s: %w", tipHash, err)
	}

	rootCommit, err := findLinearRootCommit(tipCommit)
	if err != nil {
		return err
	}

	rootSentinel, err := readOpaxSentinel(rootCommit)
	if err != nil {
		return fmt.Errorf("git: validate opax branch root %s: %w", rootCommit.Hash, err)
	}
	if err := validateOpaxSentinel(rootSentinel); err != nil {
		return fmt.Errorf("git: validate opax branch root %s: %w", rootCommit.Hash, err)
	}

	return nil
}

func findLinearRootCommit(commit *object.Commit) (*object.Commit, error) {
	current := commit
	for {
		switch current.NumParents() {
		case 0:
			return current, nil
		case 1:
			parent, err := current.Parent(0)
			if err != nil {
				return nil, fmt.Errorf("git: resolve parent for commit %s: %w", current.Hash, err)
			}
			current = parent
		default:
			return nil, fmt.Errorf(
				"git: opax branch %s has non-linear ancestry at commit %s (%d parents)",
				opaxBranchRef,
				current.Hash,
				current.NumParents(),
			)
		}
	}
}

func readOpaxSentinel(commit *object.Commit) (*opaxBranchSentinel, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("git: read tree for commit %s: %w", commit.Hash, err)
	}

	file, err := tree.File(opaxSentinelPath)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, fmt.Errorf("git: sentinel file missing: %s", opaxSentinelPath)
		}
		return nil, fmt.Errorf("git: read sentinel file %s: %w", opaxSentinelPath, err)
	}

	content, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("git: read sentinel file %s contents: %w", opaxSentinelPath, err)
	}

	var sentinel opaxBranchSentinel
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sentinel); err != nil {
		return nil, fmt.Errorf("git: parse sentinel %s: %w", opaxSentinelPath, err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return &sentinel, nil
		}
		return nil, fmt.Errorf("git: parse sentinel %s trailing data: %w", opaxSentinelPath, err)
	}
	return nil, fmt.Errorf("git: parse sentinel %s trailing data", opaxSentinelPath)
}

func validateOpaxSentinel(sentinel *opaxBranchSentinel) error {
	if sentinel.Branch != opaxBranchName {
		return fmt.Errorf("git: sentinel branch = %q, want %q", sentinel.Branch, opaxBranchName)
	}
	if sentinel.LayoutVersion != opaxLayoutVersion {
		return fmt.Errorf("git: sentinel layout_version = %d, want %d", sentinel.LayoutVersion, opaxLayoutVersion)
	}
	if sentinel.CreatedBy != opaxSentinelCreator {
		return fmt.Errorf("git: sentinel created_by = %q, want %q", sentinel.CreatedBy, opaxSentinelCreator)
	}
	return nil
}

func writeBlob(repo *ggit.Repository, data []byte) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)

	writer, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: open blob writer: %w", err)
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return plumbing.ZeroHash, fmt.Errorf("git: write blob: %w", err)
	}
	if err := writer.Close(); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: close blob writer: %w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: store blob: %w", err)
	}
	return hash, nil
}

func writeTree(repo *ggit.Repository, entries []object.TreeEntry) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := (&object.Tree{Entries: entries}).Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: encode tree: %w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: store tree: %w", err)
	}
	return hash, nil
}

func writeCommit(repo *ggit.Repository, commit *object.Commit) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: encode commit: %w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: store commit: %w", err)
	}
	return hash, nil
}
