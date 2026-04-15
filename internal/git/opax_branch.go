package git

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
func EnsureOpaxBranch(ctx *RepoContext) (tip string, err error) {
	lockPath, err := opaxLockPath(ctx)
	if err != nil {
		return "", err
	}

	timeout := lock.DefaultTimeout
	deadline := time.Now().Add(timeout)

	for {
		backend, err := openRepoFromContext(ctx)
		if err != nil {
			return "", err
		}

		tipHash, _, err := resolveOpaxBranchTip(backend)
		if err == nil {
			if err := validateOpaxBranch(backend); err != nil {
				return "", err
			}
			return tipHash.String(), nil
		}
		if !errors.Is(err, ErrOpaxBranchNotFound) {
			return "", err
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return "", fmt.Errorf("git: timed out waiting for opax branch bootstrap after %s", timeout)
		}

		branchLock, err := lock.Acquire(lockPath, remaining)
		if err != nil {
			switch {
			case errors.Is(err, lock.ErrAlreadyHeldByCurrentProcess):
				time.Sleep(opaxBootstrapPoll)
				continue
			case errors.Is(err, lock.ErrLockTimeout):
				return "", fmt.Errorf("git: timed out waiting for opax branch bootstrap after %s", timeout)
			default:
				return "", fmt.Errorf("git: acquire bootstrap lock %s: %w", lockPath, err)
			}
		}

		tipHash, err = ensureOpaxBranchWhileLocked(ctx)
		releaseErr := branchLock.Release()
		if err == nil && releaseErr != nil {
			err = fmt.Errorf("git: release bootstrap lock %s: %w", lockPath, releaseErr)
		}
		if err != nil {
			return "", err
		}
		return tipHash.String(), nil
	}
}

func ensureOpaxBranchWhileLocked(ctx *RepoContext) (gitHash, error) {
	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return "", err
	}

	tip, _, err := resolveOpaxBranchTip(backend)
	if err == nil {
		if err := validateOpaxBranch(backend); err != nil {
			return "", err
		}
		return tip, nil
	}
	if !errors.Is(err, ErrOpaxBranchNotFound) {
		return "", err
	}

	tip, err = createOpaxBranch(backend)
	if err != nil {
		return "", err
	}
	if err := validateOpaxBranch(backend); err != nil {
		return "", err
	}

	return tip, nil
}

// GetOpaxBranchTip returns the current opax/v1 tip if the branch exists.
func GetOpaxBranchTip(ctx *RepoContext) (string, error) {
	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return "", err
	}

	tip, _, err := resolveOpaxBranchTip(backend)
	if err != nil {
		return "", err
	}
	return tip.String(), nil
}

// ValidateOpaxBranch verifies that the branch identity and sentinel are
// correct.
func ValidateOpaxBranch(ctx *RepoContext) error {
	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return err
	}
	return validateOpaxBranch(backend)
}

func resolveOpaxBranchTip(backend *nativeGitBackend) (gitHash, *gitCommit, error) {
	ref, err := readOpaxBranchRef(backend)
	if err != nil {
		return "", nil, err
	}
	commit, err := backend.readCommit(ref.hash)
	if err != nil {
		return "", nil, fmt.Errorf("git: opax branch %s does not point to a commit: %w", opaxBranchRef, err)
	}
	return ref.hash, commit, nil
}

func readOpaxBranchRef(backend *nativeGitBackend) (*gitRef, error) {
	isSymbolic, target, err := backend.isSymbolicRef(opaxBranchRef)
	if err != nil {
		return nil, err
	}
	if isSymbolic {
		return nil, fmt.Errorf("git: opax branch %s is symbolic ref to %s", opaxBranchRef, target)
	}

	ref, err := backend.readRef(opaxBranchRef)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, ErrOpaxBranchNotFound)
	}
	return ref, nil
}

func createOpaxBranch(backend *nativeGitBackend) (gitHash, error) {
	sentinel := opaxBranchSentinel{
		Branch:        opaxBranchName,
		LayoutVersion: opaxLayoutVersion,
		CreatedBy:     opaxSentinelCreator,
	}
	data, err := json.MarshalIndent(sentinel, "", "  ")
	if err != nil {
		return "", fmt.Errorf("git: encode %s: %w", opaxSentinelPath, err)
	}
	data = append(data, '\n')

	blobHash, err := backend.writeBlob(data)
	if err != nil {
		return "", err
	}
	metaTreeHash, err := backend.writeTree([]gitTreeEntry{{
		Name: "version.json",
		Mode: gitModeBlob,
		Type: "blob",
		Hash: blobHash,
	}})
	if err != nil {
		return "", err
	}
	rootTreeHash, err := backend.writeTree([]gitTreeEntry{{
		Name: "meta",
		Mode: gitModeTree,
		Type: "tree",
		Hash: metaTreeHash,
	}})
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()
	commitHash, err := backend.writeCommit(gitCommitWriteRequest{
		TreeHash:       rootTreeHash,
		ParentHashes:   nil,
		Message:        opaxInitMessage,
		AuthorName:     opaxAuthorName,
		AuthorEmail:    opaxAuthorEmail,
		CommitterName:  opaxAuthorName,
		CommitterEmail: opaxAuthorEmail,
		When:           now,
	})
	if err != nil {
		return "", err
	}

	zero := zeroGitHash
	if err := backend.updateRefCAS(opaxBranchRef, commitHash, &zero); err != nil {
		if errors.Is(err, errReferenceChanged) {
			return "", fmt.Errorf("git: set ref %s: %w", opaxBranchRef, err)
		}
		return "", fmt.Errorf("git: set ref %s: %w", opaxBranchRef, err)
	}

	return commitHash, nil
}

func validateOpaxBranch(backend *nativeGitBackend) error {
	_, _, err := resolveValidatedOpaxBranchTip(backend)
	return err
}

func resolveValidatedOpaxBranchTip(backend *nativeGitBackend) (gitHash, *gitCommit, error) {
	tipHash, tipCommit, err := resolveOpaxBranchTip(backend)
	if err != nil {
		return "", nil, err
	}

	if err := validateResolvedOpaxBranchTip(backend, tipHash, tipCommit); err != nil {
		return "", nil, err
	}

	return tipHash, tipCommit, nil
}

func validateResolvedOpaxBranchTip(backend *nativeGitBackend, tipHash gitHash, tipCommit *gitCommit) error {
	tipSentinel, err := readOpaxSentinel(backend, tipCommit.Hash)
	if err != nil {
		return fmt.Errorf("git: validate opax branch tip %s: %w", tipHash, err)
	}
	if err := validateOpaxSentinel(tipSentinel); err != nil {
		return fmt.Errorf("git: validate opax branch tip %s: %w", tipHash, err)
	}

	rootHash, err := findLinearRootCommit(backend, tipCommit.Hash)
	if err != nil {
		return err
	}

	rootSentinel, err := readOpaxSentinel(backend, rootHash)
	if err != nil {
		return fmt.Errorf("git: validate opax branch root %s: %w", rootHash, err)
	}
	if err := validateOpaxSentinel(rootSentinel); err != nil {
		return fmt.Errorf("git: validate opax branch root %s: %w", rootHash, err)
	}

	return nil
}

func findLinearRootCommit(backend *nativeGitBackend, tipHash gitHash) (gitHash, error) {
	stdout, stderr, err := backend.runCapture(nil, "rev-list", "--min-parents=2", "--max-count=1", tipHash.String())
	if err != nil {
		return "", wrapGitStderrError(
			fmt.Sprintf("git: scan opax branch ancestry %s", tipHash),
			stderr,
			err,
		)
	}
	mergeCommit := strings.TrimSpace(string(stdout))
	if mergeCommit != "" {
		return "", fmt.Errorf("git: opax branch %s has non-linear ancestry at commit %s", opaxBranchRef, mergeCommit)
	}

	stdout, stderr, err = backend.runCapture(nil, "rev-list", "--max-parents=0", tipHash.String())
	if err != nil {
		return "", wrapGitStderrError(
			fmt.Sprintf("git: resolve root commit for %s", tipHash),
			stderr,
			err,
		)
	}
	roots := splitNonEmptyLines(stdout)
	if len(roots) == 0 {
		return "", fmt.Errorf("git: opax branch %s has no root commit", opaxBranchRef)
	}
	if len(roots) != 1 {
		return "", fmt.Errorf("git: opax branch %s has multiple roots", opaxBranchRef)
	}
	rootHash, err := parseHash(roots[0])
	if err != nil {
		return "", fmt.Errorf("git: parse root commit %q: %w", roots[0], err)
	}
	return rootHash, nil
}

func readOpaxSentinel(backend *nativeGitBackend, commitHash gitHash) (*opaxBranchSentinel, error) {
	content, err := backend.readBlobAtPath(commitHash, opaxSentinelPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("git: sentinel file missing: %s", opaxSentinelPath)
		}
		return nil, fmt.Errorf("git: read sentinel file %s: %w", opaxSentinelPath, err)
	}

	var sentinel opaxBranchSentinel
	decoder := json.NewDecoder(bytes.NewReader(content))
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
