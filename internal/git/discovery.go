package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverRepo resolves repository paths starting from startDir.
func DiscoverRepo(startDir string) (*RepoContext, error) {
	resolvedStart, err := normalizeStartDir(startDir)
	if err != nil {
		return nil, err
	}
	runtime, err := newGitCommandRuntime()
	if err != nil {
		return nil, err
	}
	if err := ensureSupportedGitVersion(runtime.binaryPath); err != nil {
		return nil, err
	}

	gitDir, commonGitDir, isBare, err := discoverRepoCore(runtime, resolvedStart)
	if err != nil {
		return nil, err
	}
	if isBare {
		return nil, ErrBareRepo
	}

	workTreeRoot, err := discoverWorkTreeRoot(runtime, resolvedStart)
	if err != nil {
		return nil, err
	}

	isLinkedWorktree := filepath.Clean(commonGitDir) != filepath.Clean(gitDir)
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

func discoverRepoCore(runtime *gitCommandRuntime, startDir string) (gitDir string, commonGitDir string, isBare bool, err error) {
	stdout, stderr, runErr := runGitFromDirCapture(runtime, startDir,
		"rev-parse",
		"--path-format=absolute",
		"--absolute-git-dir",
		"--git-common-dir",
		"--is-bare-repository",
	)
	if runErr != nil {
		if probeErr := probeDiscoveryFailure(startDir); probeErr != nil {
			return "", "", false, probeErr
		}
		if isNotGitRepository(stderr) {
			return "", "", false, ErrNotGitRepo
		}
		return "", "", false, wrapGitStderrError(
			fmt.Sprintf("git: discover repository from %s", startDir),
			stderr,
			runErr,
		)
	}

	lines := splitNonEmptyLines(stdout)
	if len(lines) != 3 {
		return "", "", false, fmt.Errorf("git: discover repository from %s: unexpected rev-parse output", startDir)
	}

	resolvedGitDir, err := normalizeExistingDir(lines[0], "git dir")
	if err != nil {
		return "", "", false, err
	}
	resolvedCommonGitDir, err := normalizeExistingDir(lines[1], "common git dir")
	if err != nil {
		return "", "", false, err
	}

	bareRaw := strings.TrimSpace(lines[2])
	if bareRaw != "true" && bareRaw != "false" {
		return "", "", false, fmt.Errorf("git: discover repository from %s: invalid bare flag %q", startDir, bareRaw)
	}

	return resolvedGitDir, resolvedCommonGitDir, bareRaw == "true", nil
}

func discoverWorkTreeRoot(runtime *gitCommandRuntime, startDir string) (string, error) {
	stdout, stderr, err := runGitFromDirCapture(
		runtime,
		startDir,
		"rev-parse",
		"--path-format=absolute",
		"--show-toplevel",
	)
	if err != nil {
		if isNotGitRepository(stderr) {
			return "", ErrNotGitRepo
		}
		return "", wrapGitStderrError(
			fmt.Sprintf("git: discover worktree root from %s", startDir),
			stderr,
			err,
		)
	}

	line := strings.TrimSpace(string(stdout))
	if line == "" {
		return "", fmt.Errorf("git: discover worktree root from %s: empty output", startDir)
	}
	return normalizeExistingDir(line, "worktree root")
}

func runGitFromDirCapture(runtime *gitCommandRuntime, dir string, args ...string) ([]byte, []byte, error) {
	if runtime == nil {
		return nil, nil, fmt.Errorf("git: runtime is nil")
	}
	gitArgs := append([]string{"-C", dir}, args...)
	return runtime.runCapture("", nil, nil, gitArgs...)
}

func isNotGitRepository(stderr []byte) bool {
	message := strings.ToLower(strings.TrimSpace(string(stderr)))
	return strings.Contains(message, "not a git repository") || strings.Contains(message, "outside repository")
}

func splitNonEmptyLines(data []byte) []string {
	raw := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func probeDiscoveryFailure(startDir string) error {
	gitPath := filepath.Join(startDir, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return nil
	}
	if info.IsDir() {
		return nil
	}

	data, err := os.ReadFile(gitPath)
	if err != nil {
		return nil
	}
	content := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(content, prefix) {
		return fmt.Errorf("git: parse git file %s: gitdir:  prefix missing", gitPath)
	}

	gitDirPath := strings.TrimSpace(strings.TrimPrefix(content, prefix))
	if gitDirPath == "" {
		return fmt.Errorf("git: parse git file %s: empty gitdir path", gitPath)
	}
	if !filepath.IsAbs(gitDirPath) {
		gitDirPath = filepath.Join(startDir, gitDirPath)
	}
	gitDirPath = filepath.Clean(gitDirPath)

	commondirPath := filepath.Join(gitDirPath, "commondir")
	commondirData, err := os.ReadFile(commondirPath)
	if err != nil {
		return nil
	}
	relativeCommonDir := strings.TrimSpace(string(commondirData))
	if relativeCommonDir == "" {
		return fmt.Errorf("git: parse commondir %s: empty path", commondirPath)
	}

	resolvedCommonDir := relativeCommonDir
	if !filepath.IsAbs(relativeCommonDir) {
		resolvedCommonDir = filepath.Join(gitDirPath, relativeCommonDir)
	}
	resolvedCommonDir = filepath.Clean(resolvedCommonDir)
	if _, err := os.Stat(resolvedCommonDir); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("git: common git dir does not exist: %s", resolvedCommonDir)
		}
		return fmt.Errorf("git: stat common git dir %s: %w", resolvedCommonDir, err)
	}

	return nil
}

func openRepoFromContext(ctx *RepoContext) (*nativeGitBackend, error) {
	backend, err := newNativeGitBackend(ctx)
	if err != nil {
		return nil, err
	}
	if err := backend.ensureSupportedGitVersion(); err != nil {
		return nil, err
	}
	return backend, nil
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
