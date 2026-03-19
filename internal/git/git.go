// Package git provides plumbing-level git operations via go-git.
// It handles orphan branch management, notes, trailers, and ref operations
// for the Opax data layer without touching the working tree.
package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ggit "github.com/go-git/go-git/v5"
)

var (
	// ErrNotGitRepo is returned when repo discovery cannot find a git repository.
	ErrNotGitRepo = errors.New("git: not a git repository")

	// ErrBareRepo is returned when discovery finds a bare repository, which
	// Phase 0 does not support.
	ErrBareRepo = errors.New("git: bare repositories are unsupported in Phase 0")
)

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

	repoRoot, gitEntry, err := findGitEntry(resolvedStart)
	if err != nil {
		return nil, err
	}

	gitDir, commonGitDir, isLinkedWorktree, err := resolveGitPaths(gitEntry)
	if err != nil {
		return nil, err
	}

	if err := validateRepository(repoRoot); err != nil {
		return nil, err
	}

	return &RepoContext{
		RepoRoot:         repoRoot,
		WorkTreeRoot:     repoRoot,
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

func normalizeStartDir(startDir string) (string, error) {
	if startDir == "" {
		startDir = "."
	}

	absPath, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("git: resolve start dir %s: %w", startDir, err)
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("git: resolve symlinks for %s: %w", absPath, err)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("git: stat start dir %s: %w", resolvedPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("git: start dir is not a directory: %s", resolvedPath)
	}

	return filepath.Clean(resolvedPath), nil
}

func findGitEntry(startDir string) (string, string, error) {
	current := startDir
	for {
		gitEntry := filepath.Join(current, ".git")
		if _, err := os.Lstat(gitEntry); err == nil {
			return current, gitEntry, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("git: inspect %s: %w", gitEntry, err)
		}

		if !isInternalGitDir(current) && looksLikeBareRepo(current) {
			return "", "", ErrBareRepo
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", "", ErrNotGitRepo
		}
		current = parent
	}
}

func isInternalGitDir(path string) bool {
	return filepath.Base(path) == ".git"
}

func looksLikeBareRepo(path string) bool {
	if fileExists(filepath.Join(path, "HEAD")) && dirExists(filepath.Join(path, "objects")) && dirExists(filepath.Join(path, "refs")) {
		return true
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func resolveGitPaths(gitEntry string) (string, string, bool, error) {
	entryInfo, err := os.Lstat(gitEntry)
	if err != nil {
		return "", "", false, fmt.Errorf("git: stat %s: %w", gitEntry, err)
	}

	var gitDir string
	if entryInfo.IsDir() {
		gitDir = filepath.Clean(gitEntry)
	} else {
		gitDir, err = parseGitFile(gitEntry)
		if err != nil {
			return "", "", false, err
		}
	}
	if err := ensureExistingDir(gitDir, "git dir"); err != nil {
		return "", "", false, err
	}

	commonGitDir, hasCommonDir, err := resolveCommonGitDir(gitDir)
	if err != nil {
		return "", "", false, err
	}

	isLinkedWorktree := hasCommonDir && filepath.Clean(commonGitDir) != filepath.Clean(gitDir)
	return gitDir, commonGitDir, isLinkedWorktree, nil
}

func parseGitFile(gitFilePath string) (string, error) {
	data, err := os.ReadFile(gitFilePath)
	if err != nil {
		return "", fmt.Errorf("git: read gitdir file %s: %w", gitFilePath, err)
	}

	content := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(content, prefix) {
		return "", fmt.Errorf("git: parse gitdir file %s: invalid format", gitFilePath)
	}

	rawPath := strings.TrimSpace(strings.TrimPrefix(content, prefix))
	if rawPath == "" {
		return "", fmt.Errorf("git: parse gitdir file %s: empty gitdir path", gitFilePath)
	}

	if !filepath.IsAbs(rawPath) {
		rawPath = filepath.Join(filepath.Dir(gitFilePath), rawPath)
	}
	return filepath.Clean(rawPath), nil
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

func validateRepository(repoRoot string) error {
	repo, err := ggit.PlainOpenWithOptions(repoRoot, &ggit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return fmt.Errorf("git: open repository %s: %w", repoRoot, err)
	}

	if _, err := repo.Worktree(); err != nil {
		if errors.Is(err, ggit.ErrIsBareRepository) {
			return ErrBareRepo
		}
		return fmt.Errorf("git: open worktree %s: %w", repoRoot, err)
	}

	return nil
}
