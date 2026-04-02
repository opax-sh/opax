package git_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	internalgit "github.com/opax-sh/opax/internal/git"
	"github.com/opax-sh/opax/internal/lock"
)

func TestBuildRefspecPlan(t *testing.T) {
	plan, err := internalgit.BuildRefspecPlan("origin")
	if err != nil {
		t.Fatalf("BuildRefspecPlan() error = %v", err)
	}

	wantDefault := []string{"^refs/heads/opax/v1"}
	wantFetch := []string{
		"+refs/heads/opax/v1:refs/remotes/origin/opax/v1",
		"+refs/opax/*:refs/opax/*",
		"+refs/notes/opax/*:refs/notes/opax/*",
	}
	wantPush := []string{
		"+refs/heads/opax/v1:refs/heads/opax/v1",
		"+refs/opax/*:refs/opax/*",
		"+refs/notes/opax/*:refs/notes/opax/*",
	}

	if !reflect.DeepEqual(plan.DefaultFetchExclusions, wantDefault) {
		t.Fatalf("DefaultFetchExclusions = %v, want %v", plan.DefaultFetchExclusions, wantDefault)
	}
	if !reflect.DeepEqual(plan.OpaxFetch, wantFetch) {
		t.Fatalf("OpaxFetch = %v, want %v", plan.OpaxFetch, wantFetch)
	}
	if !reflect.DeepEqual(plan.OpaxPush, wantPush) {
		t.Fatalf("OpaxPush = %v, want %v", plan.OpaxPush, wantPush)
	}
}

func TestBuildRefspecPlanRemoteNameValidation(t *testing.T) {
	validNames := []string{"origin", "team.origin", "team/prod", "team-prod_1", "A/B.C-1_2", "a@b", "@"}
	for _, remote := range validNames {
		_, err := internalgit.BuildRefspecPlan(remote)
		if err != nil {
			t.Fatalf("BuildRefspecPlan(%q) error = %v, want nil", remote, err)
		}
	}

	invalidNames := []string{
		"",
		"-origin",
		"origin name",
		" origin",
		"origin ",
		"origin*",
		"origin?",
		"origin[1]",
		"origin\tname",
		"origin\nname",
		"a..b",
		"a.lock",
		"a//b",
		"/origin",
		"origin/",
		".origin",
		"team/.prod",
		"origin.",
		"origin~name",
		"origin^name",
		"origin:name",
		"origin\\name",
		"origin@{name",
	}
	for _, remote := range invalidNames {
		_, err := internalgit.BuildRefspecPlan(remote)
		if !errors.Is(err, internalgit.ErrRemoteNameInvalid) {
			t.Fatalf("BuildRefspecPlan(%q) error = %v, want ErrRemoteNameInvalid", remote, err)
		}
	}
}

func TestApplyRefspecPlanVersionGateNoWrites(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	script := writeVersionGateGitScript(t, "2.29.9")
	t.Setenv("OPAX_GIT_BIN", script)

	plan := mustBuildRefspecPlan(t, "origin")
	_, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
	if err == nil {
		t.Fatal("ApplyRefspecPlan() error = nil, want git minimum-version failure")
	}
	if !strings.Contains(err.Error(), "below minimum supported") {
		t.Fatalf("ApplyRefspecPlan() error = %v, want minimum-version failure", err)
	}

	fetchValues := gitConfigValues(t, repoRoot, "remote.origin.fetch")
	if containsString(fetchValues, "^refs/heads/opax/v1") {
		t.Fatalf("remote.origin.fetch = %v, expected no managed exclusion write on version failure", fetchValues)
	}
	if got := gitConfigValues(t, repoRoot, "opax.remote.origin.fetch"); len(got) != 0 {
		t.Fatalf("opax.remote.origin.fetch = %v, want empty", got)
	}
	if got := gitConfigValues(t, repoRoot, "opax.remote.origin.push"); len(got) != 0 {
		t.Fatalf("opax.remote.origin.push = %v, want empty", got)
	}
}

func TestApplyRefspecPlanMissingRemote(t *testing.T) {
	repoRoot := initGitRepo(t)
	ctx := mustDiscoverRepo(t, repoRoot)
	plan := mustBuildRefspecPlan(t, "origin")

	_, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
	if !errors.Is(err, internalgit.ErrRemoteMissing) {
		t.Fatalf("ApplyRefspecPlan() error = %v, want ErrRemoteMissing", err)
	}
}

func TestApplyRefspecPlanAllowsGitCompatibleAtRemoteName(t *testing.T) {
	repoRoot := initGitRepo(t)
	runGit(t, repoRoot, "remote", "add", "a@b", "https://example.com/opax.git")
	ctx := mustDiscoverRepo(t, repoRoot)
	plan := mustBuildRefspecPlan(t, "a@b")

	state, err := internalgit.ApplyRefspecPlan(ctx, "a@b", plan)
	if err != nil {
		t.Fatalf("ApplyRefspecPlan() error = %v", err)
	}
	if !state.DefaultFetchExclusionPresent {
		t.Fatal("DefaultFetchExclusionPresent = false, want true")
	}
	if !reflect.DeepEqual(state.OpaxFetch, plan.OpaxFetch) {
		t.Fatalf("OpaxFetch = %v, want canonical %v", state.OpaxFetch, plan.OpaxFetch)
	}
	if !reflect.DeepEqual(state.OpaxPush, plan.OpaxPush) {
		t.Fatalf("OpaxPush = %v, want canonical %v", state.OpaxPush, plan.OpaxPush)
	}
}

func TestApplyRefspecPlanRejectsRemotePushOpaxRefs(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	runGit(t, repoRoot, "config", "--local", "--add", "remote.origin.push", "+refs/notes/opax/*:refs/notes/opax/*")
	plan := mustBuildRefspecPlan(t, "origin")

	_, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
	if !errors.Is(err, internalgit.ErrDefaultSyncIsolationViolation) {
		t.Fatalf("ApplyRefspecPlan() error = %v, want ErrDefaultSyncIsolationViolation", err)
	}
	if !strings.Contains(err.Error(), "refs/notes/opax/*:refs/notes/opax/*") {
		t.Fatalf("ApplyRefspecPlan() error = %v, want offending value in error context", err)
	}
}

func TestApplyRefspecPlanRequiresPositiveFetchBaseline(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	runGit(t, repoRoot, "config", "--local", "--unset-all", "remote.origin.fetch")
	runGit(t, repoRoot, "config", "--local", "--add", "remote.origin.fetch", "^refs/heads/opax/v1")
	plan := mustBuildRefspecPlan(t, "origin")

	_, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
	if !errors.Is(err, internalgit.ErrInvalidRefspecConfig) {
		t.Fatalf("ApplyRefspecPlan() error = %v, want ErrInvalidRefspecConfig", err)
	}
}

func TestApplyRefspecPlanAddsNegativeFetchPreservingOrder(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	runGit(t, repoRoot, "config", "--local", "--add", "remote.origin.fetch", "+refs/tags/*:refs/tags/*")
	before := gitConfigValues(t, repoRoot, "remote.origin.fetch")
	plan := mustBuildRefspecPlan(t, "origin")

	state, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
	if err != nil {
		t.Fatalf("ApplyRefspecPlan() error = %v", err)
	}

	after := gitConfigValues(t, repoRoot, "remote.origin.fetch")
	if len(after) != len(before)+1 {
		t.Fatalf("remote.origin.fetch len = %d, want %d values after managed exclusion append", len(after), len(before)+1)
	}
	if !reflect.DeepEqual(after[:len(before)], before) {
		t.Fatalf("remote.origin.fetch prefix = %v, want preserved order %v", after[:len(before)], before)
	}
	if got := after[len(after)-1]; got != "^refs/heads/opax/v1" {
		t.Fatalf("remote.origin.fetch appended value = %q, want managed exclusion", got)
	}
	if !state.DefaultFetchExclusionPresent {
		t.Fatal("DefaultFetchExclusionPresent = false, want true")
	}
}

func TestApplyRefspecPlanReconcilesOpaxManagedMultivars(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	plan := mustBuildRefspecPlan(t, "origin")

	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "refs/opax/*:refs/opax/*")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "refs/heads/opax/v1:refs/remotes/origin/opax/v1")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "refs/opax/*:refs/opax/*")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "refs/notes/opax/*:refs/notes/opax/*")

	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.push", "refs/notes/opax/*:refs/notes/opax/*")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.push", "refs/heads/opax/v1:refs/heads/opax/v1")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.push", "refs/heads/opax/v1:refs/heads/opax/v1")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.push", "refs/opax/*:refs/opax/*")

	_, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
	if err != nil {
		t.Fatalf("ApplyRefspecPlan() error = %v", err)
	}

	if got := gitConfigValues(t, repoRoot, "opax.remote.origin.fetch"); !reflect.DeepEqual(got, plan.OpaxFetch) {
		t.Fatalf("opax.remote.origin.fetch = %v, want canonical %v", got, plan.OpaxFetch)
	}
	if got := gitConfigValues(t, repoRoot, "opax.remote.origin.push"); !reflect.DeepEqual(got, plan.OpaxPush) {
		t.Fatalf("opax.remote.origin.push = %v, want canonical %v", got, plan.OpaxPush)
	}
}

func TestApplyRefspecPlanIdempotentConverges(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	plan := mustBuildRefspecPlan(t, "origin")

	first, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
	if err != nil {
		t.Fatalf("ApplyRefspecPlan() first error = %v", err)
	}
	second, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
	if err != nil {
		t.Fatalf("ApplyRefspecPlan() second error = %v", err)
	}

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("ApplyRefspecPlan() state mismatch on reapply: first=%+v second=%+v", first, second)
	}
	if got := gitConfigValues(t, repoRoot, "opax.remote.origin.fetch"); !reflect.DeepEqual(got, plan.OpaxFetch) {
		t.Fatalf("opax.remote.origin.fetch = %v, want canonical %v", got, plan.OpaxFetch)
	}
	if got := gitConfigValues(t, repoRoot, "opax.remote.origin.push"); !reflect.DeepEqual(got, plan.OpaxPush) {
		t.Fatalf("opax.remote.origin.push = %v, want canonical %v", got, plan.OpaxPush)
	}
	if count := countString(gitConfigValues(t, repoRoot, "remote.origin.fetch"), "^refs/heads/opax/v1"); count != 1 {
		t.Fatalf("remote.origin.fetch managed exclusion count = %d, want 1", count)
	}
}

func TestApplyRefspecPlanLocksAndRechecks(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	plan := mustBuildRefspecPlan(t, "origin")

	lockPath := filepath.Join(ctx.CommonGitDir, "opax.lock")
	heldLock, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err != nil {
		t.Fatalf("Acquire(%q) error = %v", lockPath, err)
	}

	type applyResult struct {
		state *internalgit.RefspecState
		err   error
	}
	resultCh := make(chan applyResult, 1)
	go func() {
		state, err := internalgit.ApplyRefspecPlan(ctx, "origin", plan)
		resultCh <- applyResult{state: state, err: err}
	}()

	time.Sleep(100 * time.Millisecond)
	runGit(t, repoRoot, "config", "--local", "--add", "remote.origin.push", "+refs/opax/*:refs/opax/*")

	if err := heldLock.Release(); err != nil {
		t.Fatalf("held lock Release() error = %v", err)
	}

	select {
	case result := <-resultCh:
		if !errors.Is(result.err, internalgit.ErrDefaultSyncIsolationViolation) {
			t.Fatalf("ApplyRefspecPlan() error = %v, want ErrDefaultSyncIsolationViolation", result.err)
		}
		if result.state != nil {
			t.Fatalf("ApplyRefspecPlan() state = %+v, want nil on failed recheck", result.state)
		}
	case <-time.After(lock.DefaultTimeout + time.Second):
		t.Fatal("ApplyRefspecPlan() did not complete after lock release")
	}

	if got := gitConfigValues(t, repoRoot, "opax.remote.origin.fetch"); len(got) != 0 {
		t.Fatalf("opax.remote.origin.fetch = %v, want empty after failed under-lock recheck", got)
	}
}

func TestReadRefspecStateCanonicalizesAndReportsViolations(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	plan := mustBuildRefspecPlan(t, "origin")

	runGit(t, repoRoot, "config", "--local", "--add", "remote.origin.push", "+refs/opax/*:refs/opax/*")

	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "refs/opax/*:refs/opax/*")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "refs/heads/opax/v1:refs/remotes/origin/opax/v1")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "refs/opax/*:refs/opax/*")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "refs/notes/opax/*:refs/notes/opax/*")

	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.push", "refs/notes/opax/*:refs/notes/opax/*")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.push", "refs/heads/opax/v1:refs/heads/opax/v1")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.push", "refs/opax/*:refs/opax/*")
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.push", "refs/opax/*:refs/opax/*")

	state, err := internalgit.ReadRefspecState(ctx, "origin")
	if err != nil {
		t.Fatalf("ReadRefspecState() error = %v", err)
	}

	if state.DefaultFetchExclusionPresent {
		t.Fatal("DefaultFetchExclusionPresent = true, want false before apply")
	}
	if state.DefaultSyncIsolationEnforced {
		t.Fatal("DefaultSyncIsolationEnforced = true, want false when remote push contains Opax refs")
	}
	wantViolations := []internalgit.RefspecViolationCode{
		internalgit.RefspecViolationMissingDefaultFetchExclusion,
		internalgit.RefspecViolationOpaxRefsInRemotePush,
	}
	if !reflect.DeepEqual(state.Violations, wantViolations) {
		t.Fatalf("Violations = %v, want %v", state.Violations, wantViolations)
	}
	if !reflect.DeepEqual(state.OpaxFetch, plan.OpaxFetch) {
		t.Fatalf("OpaxFetch = %v, want canonical ordered %v", state.OpaxFetch, plan.OpaxFetch)
	}
	if !reflect.DeepEqual(state.OpaxPush, plan.OpaxPush) {
		t.Fatalf("OpaxPush = %v, want canonical ordered %v", state.OpaxPush, plan.OpaxPush)
	}
}

func TestReadRefspecStateRejectsInvalidManagedEntries(t *testing.T) {
	repoRoot, ctx := initGitRepoWithOriginRemote(t)
	runGit(t, repoRoot, "config", "--local", "--add", "opax.remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")

	state, err := internalgit.ReadRefspecState(ctx, "origin")
	if !errors.Is(err, internalgit.ErrInvalidRefspecConfig) {
		t.Fatalf("ReadRefspecState() error = %v, want ErrInvalidRefspecConfig", err)
	}
	if state != nil {
		t.Fatalf("ReadRefspecState() state = %+v, want nil on invalid managed config", state)
	}
}

func initGitRepoWithOriginRemote(t *testing.T) (string, *internalgit.RepoContext) {
	t.Helper()

	repoRoot := initGitRepo(t)
	runGit(t, repoRoot, "remote", "add", "origin", "https://example.com/opax.git")
	ctx := mustDiscoverRepo(t, repoRoot)
	return repoRoot, ctx
}

func mustBuildRefspecPlan(t *testing.T, remote string) *internalgit.RefspecPlan {
	t.Helper()

	plan, err := internalgit.BuildRefspecPlan(remote)
	if err != nil {
		t.Fatalf("BuildRefspecPlan(%q) error = %v", remote, err)
	}
	return plan
}

func gitConfigValues(t *testing.T, repoRoot, key string) []string {
	t.Helper()

	cmd := exec.Command("git", "config", "--local", "--get-all", key)
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []string{}
		}
		t.Fatalf("git config --get-all %s (dir=%s) error = %v\n%s", key, repoRoot, err, output)
	}

	lines := splitLines(strings.TrimSpace(string(output)))
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	return lines
}

func splitLines(raw string) []string {
	if raw == "" {
		return []string{}
	}
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func countString(values []string, target string) int {
	count := 0
	for _, value := range values {
		if value == target {
			count++
		}
	}
	return count
}

func writeVersionGateGitScript(t *testing.T, version string) string {
	t.Helper()

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("LookPath(git) error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "git-shim.sh")
	body := fmt.Sprintf(
		"#!/bin/sh\nset -eu\nif [ \"${1:-}\" = \"version\" ]; then\n  echo \"git version %s\"\n  exit 0\nfi\nexec %q \"$@\"\n",
		version,
		realGit,
	)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	return path
}
