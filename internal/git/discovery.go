package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	fsstorage "github.com/go-git/go-git/v5/storage/filesystem"
)

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
