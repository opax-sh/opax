package git_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	internalgit "github.com/opax-sh/opax/internal/git"
)

func TestDiscoverRepoStandard(t *testing.T) {
	repoRoot := initGitRepo(t)

	ctx, err := internalgit.DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	assertRepoContext(t, ctx, repoRoot, filepath.Join(repoRoot, ".git"), filepath.Join(repoRoot, ".git"), false)
}

func TestDiscoverRepoNestedPath(t *testing.T) {
	repoRoot := initGitRepo(t)
	nested := filepath.Join(repoRoot, "internal", "git")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", nested, err)
	}

	ctx, err := internalgit.DiscoverRepo(nested)
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	assertRepoContext(t, ctx, repoRoot, filepath.Join(repoRoot, ".git"), filepath.Join(repoRoot, ".git"), false)
}

func TestDiscoverRepoFromGitAdminSubdirectory(t *testing.T) {
	repoRoot := initGitRepo(t)
	adminSubdir := filepath.Join(repoRoot, ".git", "opax")
	if err := os.MkdirAll(adminSubdir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", adminSubdir, err)
	}

	ctx, err := internalgit.DiscoverRepo(adminSubdir)
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	assertRepoContext(t, ctx, repoRoot, filepath.Join(repoRoot, ".git"), filepath.Join(repoRoot, ".git"), false)
}

func TestDiscoverRepoLinkedWorktree(t *testing.T) {
	requireGitBinary(t)

	mainRepo := initGitRepo(t)
	writeTrackedFile(t, mainRepo, "README.md", "main repo\n")
	runGit(t, mainRepo, "add", "README.md")
	runGit(t, mainRepo, "commit", "-m", "initial")

	worktreeRoot := filepath.Join(t.TempDir(), "linked-worktree")
	runGit(t, mainRepo, "worktree", "add", worktreeRoot)

	ctx, err := internalgit.DiscoverRepo(worktreeRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	gitDirFromFile := readGitDirFromFile(t, filepath.Join(worktreeRoot, ".git"))
	assertRepoContext(t, ctx, worktreeRoot, gitDirFromFile, filepath.Join(mainRepo, ".git"), true)
}

func TestDiscoverRepoSubmodule(t *testing.T) {
	requireGitBinary(t)

	submoduleRepo := initGitRepo(t)
	writeTrackedFile(t, submoduleRepo, "module.txt", "submodule\n")
	runGit(t, submoduleRepo, "add", "module.txt")
	runGit(t, submoduleRepo, "commit", "-m", "submodule init")

	parentRepo := initGitRepo(t)
	writeTrackedFile(t, parentRepo, "README.md", "parent\n")
	runGit(t, parentRepo, "add", "README.md")
	runGit(t, parentRepo, "commit", "-m", "parent init")
	runGit(t, parentRepo, "-c", "protocol.file.allow=always", "submodule", "add", submoduleRepo, "vendor/memory")

	submoduleRoot := filepath.Join(parentRepo, "vendor", "memory")
	ctx, err := internalgit.DiscoverRepo(submoduleRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	gitDir := readGitDirFromFile(t, filepath.Join(submoduleRoot, ".git"))
	assertRepoContext(t, ctx, submoduleRoot, gitDir, gitDir, false)
}

func TestDiscoverRepoBareRepo(t *testing.T) {
	requireGitBinary(t)

	bareRepo := filepath.Join(t.TempDir(), "bare.git")
	runGit(t, t.TempDir(), "init", "--bare", bareRepo)

	_, err := internalgit.DiscoverRepo(bareRepo)
	if !errors.Is(err, internalgit.ErrBareRepo) {
		t.Fatalf("DiscoverRepo() error = %v, want ErrBareRepo", err)
	}
}

func TestDiscoverRepoGitFileIndirection(t *testing.T) {
	requireGitBinary(t)

	workspace := t.TempDir()
	repoRoot := filepath.Join(workspace, "repo")
	gitDir := filepath.Join(workspace, "gitdir")
	runGit(t, workspace, "init", "--separate-git-dir", gitDir, repoRoot)

	relGitDir, err := filepath.Rel(repoRoot, gitDir)
	if err != nil {
		t.Fatalf("filepath.Rel() error = %v", err)
	}
	gitFile := filepath.Join(repoRoot, ".git")
	if err := os.WriteFile(gitFile, []byte(fmt.Sprintf("gitdir: %s\n", relGitDir)), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", gitFile, err)
	}

	ctx, err := internalgit.DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	assertRepoContext(t, ctx, repoRoot, gitDir, gitDir, false)
}

func TestDiscoverRepoMalformedGitFile(t *testing.T) {
	repoRoot := t.TempDir()
	gitFile := filepath.Join(repoRoot, ".git")
	if err := os.WriteFile(gitFile, []byte("not-a-gitdir"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", gitFile, err)
	}

	_, err := internalgit.DiscoverRepo(repoRoot)
	if err == nil {
		t.Fatal("DiscoverRepo() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), gitFile) {
		t.Fatalf("DiscoverRepo() error = %v, want path %q", err, gitFile)
	}
}

func TestDiscoverRepoMissingCommonDirTarget(t *testing.T) {
	repoRoot := t.TempDir()
	gitDir := filepath.Join(repoRoot, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", gitDir, err)
	}
	commondir := filepath.Join(gitDir, "commondir")
	if err := os.WriteFile(commondir, []byte("../missing-common\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", commondir, err)
	}

	_, err := internalgit.DiscoverRepo(repoRoot)
	if err == nil {
		t.Fatal("DiscoverRepo() error = nil, want missing common git dir error")
	}
	if !strings.Contains(err.Error(), "common git dir does not exist") {
		t.Fatalf("DiscoverRepo() error = %v, want missing common git dir message", err)
	}
}

func TestDiscoverRepoNoRepository(t *testing.T) {
	_, err := internalgit.DiscoverRepo(t.TempDir())
	if !errors.Is(err, internalgit.ErrNotGitRepo) {
		t.Fatalf("DiscoverRepo() error = %v, want ErrNotGitRepo", err)
	}
}

func TestDiscoverRepoSymlinkedStartDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is not reliable on windows CI")
	}

	repoRoot := initGitRepo(t)
	nested := filepath.Join(repoRoot, "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", nested, err)
	}

	linkRoot := filepath.Join(t.TempDir(), "repo-link")
	if err := os.Symlink(repoRoot, linkRoot); err != nil {
		t.Fatalf("Symlink(%q, %q) error = %v", repoRoot, linkRoot, err)
	}

	ctx, err := internalgit.DiscoverRepo(filepath.Join(linkRoot, "pkg"))
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	assertRepoContext(t, ctx, repoRoot, filepath.Join(repoRoot, ".git"), filepath.Join(repoRoot, ".git"), false)
}

func TestEnsureOpaxDir(t *testing.T) {
	commonGitDir := filepath.Join(t.TempDir(), ".git")
	if err := os.MkdirAll(commonGitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", commonGitDir, err)
	}

	ctx := &internalgit.RepoContext{
		CommonGitDir: commonGitDir,
		OpaxDir:      filepath.Join(commonGitDir, "opax"),
	}

	if err := internalgit.EnsureOpaxDir(ctx); err != nil {
		t.Fatalf("EnsureOpaxDir() first call error = %v", err)
	}
	if err := internalgit.EnsureOpaxDir(ctx); err != nil {
		t.Fatalf("EnsureOpaxDir() second call error = %v", err)
	}

	info, err := os.Stat(ctx.OpaxDir)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", ctx.OpaxDir, err)
	}
	if !info.IsDir() {
		t.Fatalf("Stat(%q) returned non-directory", ctx.OpaxDir)
	}
}

func TestEnsureOpaxDirExistingFile(t *testing.T) {
	commonGitDir := filepath.Join(t.TempDir(), ".git")
	if err := os.MkdirAll(commonGitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", commonGitDir, err)
	}

	opaxPath := filepath.Join(commonGitDir, "opax")
	if err := os.WriteFile(opaxPath, []byte("file"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", opaxPath, err)
	}

	ctx := &internalgit.RepoContext{
		CommonGitDir: commonGitDir,
		OpaxDir:      opaxPath,
	}

	err := internalgit.EnsureOpaxDir(ctx)
	if err == nil {
		t.Fatal("EnsureOpaxDir() error = nil, want non-directory error")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("EnsureOpaxDir() error = %v, want non-directory message", err)
	}
}

func assertRepoContext(t *testing.T, ctx *internalgit.RepoContext, repoRoot, gitDir, commonGitDir string, linked bool) {
	t.Helper()

	wantRepoRoot := canonicalPath(t, repoRoot)
	wantGitDir := canonicalPath(t, gitDir)
	wantCommonGitDir := canonicalPath(t, commonGitDir)
	wantOpaxDir := filepath.Join(wantCommonGitDir, "opax")

	if ctx.RepoRoot != wantRepoRoot {
		t.Fatalf("RepoRoot = %q, want %q", ctx.RepoRoot, wantRepoRoot)
	}
	if ctx.WorkTreeRoot != wantRepoRoot {
		t.Fatalf("WorkTreeRoot = %q, want %q", ctx.WorkTreeRoot, wantRepoRoot)
	}
	if ctx.GitDir != wantGitDir {
		t.Fatalf("GitDir = %q, want %q", ctx.GitDir, wantGitDir)
	}
	if ctx.CommonGitDir != wantCommonGitDir {
		t.Fatalf("CommonGitDir = %q, want %q", ctx.CommonGitDir, wantCommonGitDir)
	}
	if ctx.OpaxDir != wantOpaxDir {
		t.Fatalf("OpaxDir = %q, want %q", ctx.OpaxDir, wantOpaxDir)
	}
	if ctx.IsLinkedWorktree != linked {
		t.Fatalf("IsLinkedWorktree = %v, want %v", ctx.IsLinkedWorktree, linked)
	}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()

	resolvedPath, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolvedPath)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("EvalSymlinks(%q) error = %v", path, err)
	}
	return filepath.Clean(path)
}

func initGitRepo(t *testing.T) string {
	t.Helper()

	repoRoot := t.TempDir()
	runGit(t, t.TempDir(), "init", repoRoot)
	return repoRoot
}

func writeTrackedFile(t *testing.T, repoRoot, relativePath, contents string) {
	t.Helper()

	path := filepath.Join(repoRoot, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func readGitDirFromFile(t *testing.T, gitFilePath string) string {
	t.Helper()

	data, err := os.ReadFile(gitFilePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", gitFilePath, err)
	}

	content := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(content, prefix) {
		t.Fatalf("git file %q = %q, want gitdir prefix", gitFilePath, content)
	}
	path := strings.TrimSpace(strings.TrimPrefix(content, prefix))
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(gitFilePath), path)
	}
	return filepath.Clean(path)
}

func requireGitBinary(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
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
