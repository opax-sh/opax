// Package git provides plumbing-level git operations via go-git.
// It handles orphan branch management, notes, trailers, and ref operations
// for the Opax data layer without touching the working tree.
package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitstorage "github.com/go-git/go-git/v5/storage"
	fsstorage "github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/opax-sh/opax/internal/lock"
)

var (
	// ErrNotGitRepo is returned when repo discovery cannot find a git repository.
	ErrNotGitRepo = errors.New("git: not a git repository")

	// ErrBareRepo is returned when discovery finds a bare repository, which
	// Phase 0 does not support.
	ErrBareRepo = errors.New("git: bare repositories are unsupported in Phase 0")
)

const (
	opaxBranchRef       = "refs/heads/opax/v1"
	opaxBranchName      = "opax/v1"
	opaxSentinelPath    = "meta/version.json"
	opaxSentinelCreator = "opax"
	opaxLayoutVersion   = 1
	opaxAuthorName      = "Opax"
	opaxAuthorEmail     = "opax@local"
	opaxInitMessage     = "opax: initialize opax/v1"
	opaxLockFilename    = "opax.lock"
	opaxBootstrapPoll   = 10 * time.Millisecond

	maxRefPublishAttempts = 8
	refPublishBackoffBase = 10 * time.Millisecond
	refPublishBackoffCap  = 100 * time.Millisecond
)

type opaxBranchSentinel struct {
	Branch        string `json:"branch"`
	LayoutVersion int    `json:"layout_version"`
	CreatedBy     string `json:"created_by"`
}

type refPublishBuilder func(repo *ggit.Repository, currentRef *plumbing.Reference) (*plumbing.Reference, error)

// RepoContext describes the resolved repository layout that downstream git
// plumbing code should use instead of inferring paths ad hoc.
type RepoContext struct {
	RepoRoot         string
	WorkTreeRoot     string
	GitDir           string
	CommonGitDir     string
	OpaxDir          string
	IsLinkedWorktree bool
}

// DiscoverRepo resolves repository paths starting from startDir.
func DiscoverRepo(startDir string) (*RepoContext, error) {
	resolvedStart, err := normalizeStartDir(startDir)
	if err != nil {
		return nil, err
	}

	repo, err := openRepository(resolvedStart)
	if err != nil {
		return nil, err
	}

	workTreeRoot, gitDir, commonGitDir, isLinkedWorktree, err := buildRepoPaths(repo)
	if err != nil {
		return nil, err
	}

	return &RepoContext{
		RepoRoot:         workTreeRoot,
		WorkTreeRoot:     workTreeRoot,
		GitDir:           gitDir,
		CommonGitDir:     commonGitDir,
		OpaxDir:          filepath.Join(commonGitDir, "opax"),
		IsLinkedWorktree: isLinkedWorktree,
	}, nil
}

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

func normalizeStartDir(startDir string) (string, error) {
	if startDir == "" {
		startDir = "."
	}

	return normalizeExistingDir(startDir, "start dir")
}

func normalizeExistingDir(path, label string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("git: resolve %s %s: %w", label, path, err)
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("git: resolve symlinks for %s %s: %w", label, absPath, err)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("git: stat %s %s: %w", label, resolvedPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("git: %s is not a directory: %s", label, resolvedPath)
	}

	return filepath.Clean(resolvedPath), nil
}

func openRepository(startDir string) (*ggit.Repository, error) {
	repo, err := ggit.PlainOpenWithOptions(startDir, &ggit.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		if !errors.Is(err, ggit.ErrRepositoryNotExists) {
			return nil, fmt.Errorf("git: open repository %s: %w", startDir, err)
		}

		repo, bareErr := ggit.PlainOpen(startDir)
		if bareErr == nil {
			return repo, nil
		}
		if errors.Is(bareErr, ggit.ErrRepositoryNotExists) {
			return nil, ErrNotGitRepo
		}
		return nil, fmt.Errorf("git: open repository %s: %w", startDir, bareErr)
	}

	return repo, nil
}

func buildRepoPaths(repo *ggit.Repository) (string, string, string, bool, error) {
	worktree, err := repo.Worktree()
	if err != nil {
		if errors.Is(err, ggit.ErrIsBareRepository) {
			return "", "", "", false, ErrBareRepo
		}
		return "", "", "", false, fmt.Errorf("git: open worktree: %w", err)
	}

	workTreeRoot, err := normalizeExistingDir(worktree.Filesystem.Root(), "worktree root")
	if err != nil {
		return "", "", "", false, err
	}

	gitDir, err := gitDirFromRepository(repo)
	if err != nil {
		return "", "", "", false, err
	}

	commonGitDir, hasCommonDir, err := resolveCommonGitDir(gitDir)
	if err != nil {
		return "", "", "", false, err
	}

	isLinkedWorktree := hasCommonDir && filepath.Clean(commonGitDir) != filepath.Clean(gitDir)
	return workTreeRoot, gitDir, commonGitDir, isLinkedWorktree, nil
}

func gitDirFromRepository(repo *ggit.Repository) (string, error) {
	storage, ok := repo.Storer.(*fsstorage.Storage)
	if !ok {
		return "", fmt.Errorf("git: unexpected repository storage type %T", repo.Storer)
	}

	return normalizeExistingDir(storage.Filesystem().Root(), "git dir")
}

func resolveCommonGitDir(gitDir string) (string, bool, error) {
	commonDirPath := filepath.Join(gitDir, "commondir")
	data, err := os.ReadFile(commonDirPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return gitDir, false, nil
		}
		return "", false, fmt.Errorf("git: read commondir %s: %w", commonDirPath, err)
	}

	relPath := strings.TrimSpace(string(data))
	if relPath == "" {
		return "", false, fmt.Errorf("git: parse commondir %s: empty path", commonDirPath)
	}

	resolvedPath := relPath
	if !filepath.IsAbs(relPath) {
		resolvedPath = filepath.Join(gitDir, relPath)
	}
	resolvedPath = filepath.Clean(resolvedPath)
	if err := ensureExistingDir(resolvedPath, "common git dir"); err != nil {
		return "", false, err
	}
	resolvedPath, err = normalizeExistingDir(resolvedPath, "common git dir")
	if err != nil {
		return "", false, err
	}
	return resolvedPath, true, nil
}

func ensureExistingDir(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("git: %s does not exist: %s", label, path)
		}
		return fmt.Errorf("git: stat %s %s: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("git: %s is not a directory: %s", label, path)
	}
	return nil
}

func openRepoFromContext(ctx *RepoContext) (*ggit.Repository, error) {
	if ctx == nil {
		return nil, fmt.Errorf("git: repo context is nil")
	}
	if ctx.CommonGitDir == "" {
		return nil, fmt.Errorf("git: common git dir is empty")
	}

	storage := fsstorage.NewStorage(osfs.New(ctx.CommonGitDir), cache.NewObjectLRUDefault())
	repo, err := ggit.Open(storage, nil)
	if err != nil {
		return nil, fmt.Errorf("git: open repository from common git dir %s: %w", ctx.CommonGitDir, err)
	}
	return repo, nil
}

func opaxLockPath(ctx *RepoContext) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("git: repo context is nil")
	}
	if ctx.CommonGitDir == "" {
		return "", fmt.Errorf("git: common git dir is empty")
	}
	return filepath.Join(ctx.CommonGitDir, opaxLockFilename), nil
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
	tipHash, tipCommit, err := resolveOpaxBranchTip(repo)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, err)
		}
		return err
	}

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

func publishRefWithRetry(
	ctx *RepoContext,
	refName plumbing.ReferenceName,
	build refPublishBuilder,
) (*plumbing.Reference, error) {
	if build == nil {
		return nil, fmt.Errorf("git: publish ref %s: builder is nil", refName)
	}

	var lastErr error
	for attempt := 1; attempt <= maxRefPublishAttempts; attempt++ {
		repo, err := openRepoFromContext(ctx)
		if err != nil {
			return nil, err
		}

		currentRef, err := repo.Reference(refName, true)
		if err != nil {
			if errors.Is(err, plumbing.ErrReferenceNotFound) {
				currentRef = nil
			} else {
				return nil, fmt.Errorf("git: read ref %s: %w", refName, err)
			}
		}

		nextRef, err := build(repo, currentRef)
		if err != nil {
			return nil, err
		}
		if nextRef == nil {
			return nil, fmt.Errorf("git: publish ref %s: builder returned nil reference", refName)
		}
		if nextRef.Name() != refName {
			return nil, fmt.Errorf("git: publish ref %s: builder returned %s", refName, nextRef.Name())
		}

		if err := publishReference(repo, nextRef, currentRef); err == nil {
			return nextRef, nil
		} else if errors.Is(err, gitstorage.ErrReferenceHasChanged) {
			lastErr = err
		} else {
			return nil, fmt.Errorf("git: publish ref %s: %w", refName, err)
		}

		if attempt == maxRefPublishAttempts {
			break
		}
		time.Sleep(refPublishBackoff(attempt))
	}

	return nil, fmt.Errorf(
		"git: publish ref %s: retries exhausted after %d attempts: %w",
		refName,
		maxRefPublishAttempts,
		lastErr,
	)
}

func publishReference(repo *ggit.Repository, nextRef, currentRef *plumbing.Reference) error {
	if currentRef != nil {
		return repo.Storer.CheckAndSetReference(nextRef, currentRef)
	}
	return createReferenceIfAbsent(repo, nextRef)
}

func createReferenceIfAbsent(repo *ggit.Repository, ref *plumbing.Reference) error {
	storage, ok := repo.Storer.(*fsstorage.Storage)
	if !ok {
		return fmt.Errorf("git: publish ref %s: unexpected repository storage type %T", ref.Name(), repo.Storer)
	}

	content, err := refContent(ref)
	if err != nil {
		return err
	}

	refPath := filepath.Join(storage.Filesystem().Root(), filepath.FromSlash(ref.Name().String()))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		return fmt.Errorf("git: publish ref %s: create parent directory: %w", ref.Name(), err)
	}

	refFile, err := os.OpenFile(refPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return gitstorage.ErrReferenceHasChanged
		}
		return fmt.Errorf("git: publish ref %s: create ref file: %w", ref.Name(), err)
	}

	if _, err := refFile.WriteString(content); err != nil {
		_ = refFile.Close()
		_ = os.Remove(refPath)
		return fmt.Errorf("git: publish ref %s: write ref file: %w", ref.Name(), err)
	}
	if err := refFile.Close(); err != nil {
		return fmt.Errorf("git: publish ref %s: close ref file: %w", ref.Name(), err)
	}

	return nil
}

func refContent(ref *plumbing.Reference) (string, error) {
	switch ref.Type() {
	case plumbing.HashReference:
		return fmt.Sprintf("%s\n", ref.Hash()), nil
	case plumbing.SymbolicReference:
		return fmt.Sprintf("ref: %s\n", ref.Target()), nil
	default:
		return "", fmt.Errorf("git: publish ref %s: unsupported reference type %s", ref.Name(), ref.Type())
	}
}

func refPublishBackoff(attempt int) time.Duration {
	if attempt < 1 {
		return refPublishBackoffBase
	}

	delay := refPublishBackoffBase << (attempt - 1)
	if delay > refPublishBackoffCap {
		return refPublishBackoffCap
	}
	return delay
}
