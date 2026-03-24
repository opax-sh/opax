package git_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	internalgit "github.com/opax-sh/opax/internal/git"
	"github.com/opax-sh/opax/internal/lock"
)

const (
	opaxBranchRef    = "refs/heads/opax/v1"
	opaxSentinelPath = "meta/version.json"
)

type opaxSentinel struct {
	Branch        string `json:"branch"`
	LayoutVersion int    `json:"layout_version"`
	CreatedBy     string `json:"created_by"`
}

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

func TestDiscoverRepoGitFileIndirectionNestedPath(t *testing.T) {
	requireGitBinary(t)

	workspace := t.TempDir()
	repoRoot := filepath.Join(workspace, "repo")
	gitDir := filepath.Join(workspace, "gitdir")
	runGit(t, workspace, "init", "--separate-git-dir", gitDir, repoRoot)

	nested := filepath.Join(repoRoot, "internal", "git")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", nested, err)
	}

	ctx, err := internalgit.DiscoverRepo(nested)
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	assertRepoContext(t, ctx, repoRoot, gitDir, gitDir, false)
}

func TestDiscoverRepoGitDirSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup is not reliable on windows CI")
	}

	repoRoot := initGitRepo(t)
	realGitDir := filepath.Join(repoRoot, ".git.real")
	if err := os.Rename(filepath.Join(repoRoot, ".git"), realGitDir); err != nil {
		t.Fatalf("Rename(.git, .git.real) error = %v", err)
	}
	if err := os.Symlink(realGitDir, filepath.Join(repoRoot, ".git")); err != nil {
		t.Fatalf("Symlink(%q, %q) error = %v", realGitDir, filepath.Join(repoRoot, ".git"), err)
	}

	ctx, err := internalgit.DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo() error = %v", err)
	}

	assertRepoContext(t, ctx, repoRoot, realGitDir, realGitDir, false)
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
	if !strings.Contains(err.Error(), "gitdir:  prefix") {
		t.Fatalf("DiscoverRepo() error = %v, want malformed gitdir message", err)
	}
}

func TestDiscoverRepoMissingCommonDirTarget(t *testing.T) {
	requireGitBinary(t)

	mainRepo := initGitRepo(t)
	writeTrackedFile(t, mainRepo, "README.md", "main repo\n")
	runGit(t, mainRepo, "add", "README.md")
	runGit(t, mainRepo, "commit", "-m", "initial")

	worktreeRoot := filepath.Join(t.TempDir(), "linked-worktree")
	runGit(t, mainRepo, "worktree", "add", worktreeRoot)

	gitDir := readGitDirFromFile(t, filepath.Join(worktreeRoot, ".git"))
	commondir := filepath.Join(gitDir, "commondir")
	if err := os.WriteFile(commondir, []byte("../missing-common\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", commondir, err)
	}

	_, err := internalgit.DiscoverRepo(worktreeRoot)
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

func TestEnsureOpaxBranchCreatesRoot(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	tip, err := internalgit.EnsureOpaxBranch(ctx)
	if err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}

	repo := mustOpenRepo(t, repoRoot)
	commit, err := repo.CommitObject(tip)
	if err != nil {
		t.Fatalf("CommitObject(%s) error = %v", tip, err)
	}
	if commit.NumParents() != 0 {
		t.Fatalf("NumParents() = %d, want 0", commit.NumParents())
	}
	if commit.Message != "opax: initialize opax/v1" {
		t.Fatalf("Message = %q, want %q", commit.Message, "opax: initialize opax/v1")
	}
	if commit.Author.Name != "Opax" || commit.Author.Email != "opax@local" {
		t.Fatalf("Author = %q <%s>, want Opax <opax@local>", commit.Author.Name, commit.Author.Email)
	}
	if commit.Committer.Name != "Opax" || commit.Committer.Email != "opax@local" {
		t.Fatalf("Committer = %q <%s>, want Opax <opax@local>", commit.Committer.Name, commit.Committer.Email)
	}

	sentinel := readSentinelFromCommit(t, commit)
	assertExpectedSentinel(t, sentinel)
}

func TestEnsureOpaxBranchSentinel(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	tip, err := internalgit.EnsureOpaxBranch(ctx)
	if err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}

	repo := mustOpenRepo(t, repoRoot)
	commit, err := repo.CommitObject(tip)
	if err != nil {
		t.Fatalf("CommitObject(%s) error = %v", tip, err)
	}

	sentinel := readSentinelFromCommit(t, commit)
	assertExpectedSentinel(t, sentinel)
}

func TestEnsureOpaxBranchIdempotent(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	firstTip, err := internalgit.EnsureOpaxBranch(ctx)
	if err != nil {
		t.Fatalf("EnsureOpaxBranch() first call error = %v", err)
	}
	secondTip, err := internalgit.EnsureOpaxBranch(ctx)
	if err != nil {
		t.Fatalf("EnsureOpaxBranch() second call error = %v", err)
	}
	if secondTip != firstTip {
		t.Fatalf("tip changed: first=%s second=%s", firstTip, secondTip)
	}
}

func TestEnsureOpaxBranchConcurrentBootstrap(t *testing.T) {
	requireGitBinary(t)

	mainRepo := initGitRepo(t)
	writeTrackedFile(t, mainRepo, "README.md", "main repo\n")
	runGit(t, mainRepo, "add", "README.md")
	runGit(t, mainRepo, "commit", "-m", "initial")

	worktreeRoot := filepath.Join(t.TempDir(), "linked-worktree")
	runGit(t, mainRepo, "worktree", "add", worktreeRoot)

	mainCtx := mustDiscoverRepo(t, mainRepo)
	worktreeCtx := mustDiscoverRepo(t, worktreeRoot)

	const writers = 12
	errCh := make(chan error, writers)
	tipCh := make(chan plumbing.Hash, writers)
	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			ctx := mainCtx
			if i%2 == 1 {
				ctx = worktreeCtx
			}

			tip, err := internalgit.EnsureOpaxBranch(ctx)
			if err != nil {
				errCh <- err
				return
			}
			tipCh <- tip
		}(i)
	}

	wg.Wait()
	close(errCh)
	close(tipCh)

	for err := range errCh {
		t.Fatalf("EnsureOpaxBranch() concurrent call error = %v", err)
	}

	var expectedTip plumbing.Hash
	for tip := range tipCh {
		if expectedTip == plumbing.ZeroHash {
			expectedTip = tip
			continue
		}
		if tip != expectedTip {
			t.Fatalf("concurrent EnsureOpaxBranch() tip mismatch: got=%s want=%s", tip, expectedTip)
		}
	}
	if expectedTip == plumbing.ZeroHash {
		t.Fatal("EnsureOpaxBranch() returned zero hash for all concurrent callers")
	}

	repo := mustOpenRepo(t, mainRepo)
	ref, err := repo.Reference(plumbing.ReferenceName(opaxBranchRef), true)
	if err != nil {
		t.Fatalf("Reference(%s) error = %v", opaxBranchRef, err)
	}
	if ref.Hash() != expectedTip {
		t.Fatalf("branch tip = %s, want %s", ref.Hash(), expectedTip)
	}

	commit, err := repo.CommitObject(expectedTip)
	if err != nil {
		t.Fatalf("CommitObject(%s) error = %v", expectedTip, err)
	}

	historyLen := 0
	current := commit
	for {
		historyLen++
		switch current.NumParents() {
		case 0:
			goto done
		case 1:
			current, err = current.Parent(0)
			if err != nil {
				t.Fatalf("Parent(0) error = %v", err)
			}
		default:
			t.Fatalf("NumParents() = %d, want linear history", current.NumParents())
		}
	}

done:
	if historyLen != 1 {
		t.Fatalf("opax/v1 history length = %d, want 1", historyLen)
	}
	assertExpectedSentinel(t, readSentinelFromCommit(t, commit))
}

func TestEnsureOpaxBranchRecoversAfterSelfHeldLockReleaseWithoutBranch(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	lockPath := filepath.Join(ctx.CommonGitDir, "opax.lock")
	heldLock, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err != nil {
		t.Fatalf("Acquire(%q) error = %v", lockPath, err)
	}
	defer heldLock.Release()

	type ensureResult struct {
		tip plumbing.Hash
		err error
	}
	resultCh := make(chan ensureResult, 1)
	go func() {
		tip, err := internalgit.EnsureOpaxBranch(ctx)
		resultCh <- ensureResult{tip: tip, err: err}
	}()

	select {
	case result := <-resultCh:
		t.Fatalf("EnsureOpaxBranch() returned while lock held: tip=%s err=%v", result.tip, result.err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := heldLock.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("EnsureOpaxBranch() error after lock release = %v", result.err)
		}
		if result.tip == plumbing.ZeroHash {
			t.Fatal("EnsureOpaxBranch() tip = zero hash after lock release")
		}
	case <-time.After(lock.DefaultTimeout + time.Second):
		t.Fatal("EnsureOpaxBranch() did not finish after lock release")
	}

	if err := internalgit.ValidateOpaxBranch(ctx); err != nil {
		t.Fatalf("ValidateOpaxBranch() error after recovered bootstrap = %v", err)
	}
}

func TestValidateOpaxBranchRejectsSymbolicRef(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	tip, err := internalgit.EnsureOpaxBranch(ctx)
	if err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}

	repo := mustOpenRepo(t, repoRoot)
	symbolTarget := plumbing.ReferenceName("refs/heads/opax-v1-concrete")
	if err := repo.Storer.SetReference(plumbing.NewHashReference(symbolTarget, tip)); err != nil {
		t.Fatalf("SetReference(%s) error = %v", symbolTarget, err)
	}
	if err := repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.ReferenceName(opaxBranchRef), symbolTarget)); err != nil {
		t.Fatalf("SetReference(symbolic %s -> %s) error = %v", opaxBranchRef, symbolTarget, err)
	}

	err = internalgit.ValidateOpaxBranch(ctx)
	if err == nil {
		t.Fatal("ValidateOpaxBranch() error = nil, want symbolic-ref validation error")
	}
	if !strings.Contains(err.Error(), "is symbolic") {
		t.Fatalf("ValidateOpaxBranch() error = %v, want symbolic-ref message", err)
	}
}

func TestValidateOpaxBranchMissingSentinel(t *testing.T) {
	repoRoot := initGitRepo(t)
	runGit(t, repoRoot, "checkout", "--orphan", "opax/v1")
	writeTrackedFile(t, repoRoot, "not-sentinel.txt", "missing sentinel\n")
	runGit(t, repoRoot, "add", "not-sentinel.txt")
	runGit(t, repoRoot, "commit", "-m", "orphan without sentinel")

	ctx := mustDiscoverRepo(t, repoRoot)
	err := internalgit.ValidateOpaxBranch(ctx)
	if err == nil {
		t.Fatal("ValidateOpaxBranch() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "sentinel file missing") {
		t.Fatalf("ValidateOpaxBranch() error = %v, want missing sentinel message", err)
	}
}

func TestValidateOpaxBranchWrongPayload(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)

	if _, err := internalgit.EnsureOpaxBranch(ctx); err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}

	runGit(t, repoRoot, "checkout", "opax/v1")
	writeTrackedFile(t, repoRoot, opaxSentinelPath, `{"branch":"opax/v1","layout_version":1,"created_by":"not-opax"}`)
	runGit(t, repoRoot, "add", opaxSentinelPath)
	runGit(t, repoRoot, "commit", "-m", "corrupt sentinel payload")

	err := internalgit.ValidateOpaxBranch(ctx)
	if err == nil {
		t.Fatal("ValidateOpaxBranch() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "created_by") {
		t.Fatalf("ValidateOpaxBranch() error = %v, want created_by mismatch", err)
	}
}

func TestValidateOpaxBranchNonCommitRef(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)
	repo := mustOpenRepo(t, repoRoot)

	blobHash := writeBlobObject(t, repo, []byte("non-commit ref target"))
	if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.ReferenceName(opaxBranchRef), blobHash)); err != nil {
		t.Fatalf("SetReference(%s) error = %v", opaxBranchRef, err)
	}

	err := internalgit.ValidateOpaxBranch(ctx)
	if err == nil {
		t.Fatal("ValidateOpaxBranch() error = nil, want non-commit ref error")
	}
	if !strings.Contains(err.Error(), "does not point to a commit") {
		t.Fatalf("ValidateOpaxBranch() error = %v, want non-commit message", err)
	}
}

func TestValidateOpaxBranchNonOrphanRoot(t *testing.T) {
	repoRoot := initGitRepo(t)
	writeTrackedFile(t, repoRoot, "README.md", "source history\n")
	runGit(t, repoRoot, "add", "README.md")
	runGit(t, repoRoot, "commit", "-m", "source root")
	runGit(t, repoRoot, "checkout", "-b", "opax/v1")
	writeTrackedFile(t, repoRoot, opaxSentinelPath, expectedSentinelJSON())
	runGit(t, repoRoot, "add", opaxSentinelPath)
	runGit(t, repoRoot, "commit", "-m", "add sentinel later")

	ctx := mustDiscoverRepo(t, repoRoot)
	err := internalgit.ValidateOpaxBranch(ctx)
	if err == nil {
		t.Fatal("ValidateOpaxBranch() error = nil, want non-orphan root validation error")
	}
	if !strings.Contains(err.Error(), "validate opax branch root") {
		t.Fatalf("ValidateOpaxBranch() error = %v, want root-validation message", err)
	}
}

func TestEnsureOpaxBranchLinkedWorktreeUsesCommonRepoState(t *testing.T) {
	requireGitBinary(t)

	mainRepo := initGitRepo(t)
	writeTrackedFile(t, mainRepo, "README.md", "main repo\n")
	runGit(t, mainRepo, "add", "README.md")
	runGit(t, mainRepo, "commit", "-m", "initial")

	worktreeRoot := filepath.Join(t.TempDir(), "linked-worktree")
	runGit(t, mainRepo, "worktree", "add", worktreeRoot)

	worktreeCtx := mustDiscoverRepo(t, worktreeRoot)
	worktreeTip, err := internalgit.EnsureOpaxBranch(worktreeCtx)
	if err != nil {
		t.Fatalf("EnsureOpaxBranch() from worktree error = %v", err)
	}

	mainCtx := mustDiscoverRepo(t, mainRepo)
	mainTip, err := internalgit.GetOpaxBranchTip(mainCtx)
	if err != nil {
		t.Fatalf("GetOpaxBranchTip() from main repo error = %v", err)
	}
	if mainTip != worktreeTip {
		t.Fatalf("GetOpaxBranchTip() mismatch: main=%s worktree=%s", mainTip, worktreeTip)
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

func mustDiscoverRepo(t *testing.T, repoRoot string) *internalgit.RepoContext {
	t.Helper()

	ctx, err := internalgit.DiscoverRepo(repoRoot)
	if err != nil {
		t.Fatalf("DiscoverRepo(%q) error = %v", repoRoot, err)
	}
	return ctx
}

func mustOpenRepo(t *testing.T, repoRoot string) *ggit.Repository {
	t.Helper()

	repo, err := ggit.PlainOpenWithOptions(repoRoot, &ggit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		t.Fatalf("PlainOpenWithOptions(%q) error = %v", repoRoot, err)
	}
	return repo
}

func readSentinelFromCommit(t *testing.T, commit *object.Commit) opaxSentinel {
	t.Helper()

	tree, err := commit.Tree()
	if err != nil {
		t.Fatalf("Tree(%s) error = %v", commit.Hash, err)
	}

	file, err := tree.File(opaxSentinelPath)
	if err != nil {
		t.Fatalf("File(%q) error = %v", opaxSentinelPath, err)
	}

	content, err := file.Contents()
	if err != nil {
		t.Fatalf("Contents(%q) error = %v", opaxSentinelPath, err)
	}

	var sentinel opaxSentinel
	if err := json.Unmarshal([]byte(content), &sentinel); err != nil {
		t.Fatalf("Unmarshal sentinel error = %v", err)
	}
	return sentinel
}

func assertExpectedSentinel(t *testing.T, sentinel opaxSentinel) {
	t.Helper()

	expected := opaxSentinel{
		Branch:        "opax/v1",
		LayoutVersion: 1,
		CreatedBy:     "opax",
	}
	if sentinel != expected {
		t.Fatalf("sentinel = %+v, want %+v", sentinel, expected)
	}
}

func expectedSentinelJSON() string {
	return `{"branch":"opax/v1","layout_version":1,"created_by":"opax"}`
}

func writeBlobObject(t *testing.T, repo *ggit.Repository, data []byte) plumbing.Hash {
	t.Helper()

	obj := repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)

	writer, err := obj.Writer()
	if err != nil {
		t.Fatalf("Writer() error = %v", err)
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		t.Fatalf("SetEncodedObject() error = %v", err)
	}
	return hash
}
