package git

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUpdateRefCASClassifiesConflictFromPostConditionProbe(t *testing.T) {
	backend := newRefCASTestBackend(t)

	expected := gitHash("1111111111111111111111111111111111111111")
	newHash := gitHash("2222222222222222222222222222222222222222")
	liveHash := gitHash("3333333333333333333333333333333333333333")

	t.Setenv("UPDATE_REF_STDERR", "fatal: update-ref failed")
	t.Setenv("REF_PRESENT", "1")
	t.Setenv("REF_HASH", liveHash.String())
	t.Setenv("FOREACH_REF_FAIL", "0")

	err := backend.updateRefCAS(opaxBranchRef, newHash, &expected)
	if !errors.Is(err, errReferenceChanged) {
		t.Fatalf("updateRefCAS() error = %v, want errReferenceChanged", err)
	}
	if errors.Is(err, errReferenceCASUnknown) {
		t.Fatalf("updateRefCAS() error = %v, want known conflict classification", err)
	}
}

func TestUpdateRefCASClassifiesUnknownWhenRefUnchanged(t *testing.T) {
	backend := newRefCASTestBackend(t)

	expected := gitHash("1111111111111111111111111111111111111111")
	newHash := gitHash("2222222222222222222222222222222222222222")

	t.Setenv("UPDATE_REF_STDERR", "fatal: update-ref failed")
	t.Setenv("REF_PRESENT", "1")
	t.Setenv("REF_HASH", expected.String())
	t.Setenv("FOREACH_REF_FAIL", "0")

	err := backend.updateRefCAS(opaxBranchRef, newHash, &expected)
	if !errors.Is(err, errReferenceCASUnknown) {
		t.Fatalf("updateRefCAS() error = %v, want errReferenceCASUnknown", err)
	}
	if errors.Is(err, errReferenceChanged) {
		t.Fatalf("updateRefCAS() error = %v, want unknown outcome (not conflict)", err)
	}
}

func TestUpdateRefCASClassifiesAppliedWhenLiveRefMatchesNewHash(t *testing.T) {
	backend := newRefCASTestBackend(t)

	expected := gitHash("1111111111111111111111111111111111111111")
	newHash := gitHash("2222222222222222222222222222222222222222")

	t.Setenv("UPDATE_REF_STDERR", "fatal: update-ref failed")
	t.Setenv("REF_PRESENT", "1")
	t.Setenv("REF_HASH", newHash.String())
	t.Setenv("FOREACH_REF_FAIL", "0")

	if err := backend.updateRefCAS(opaxBranchRef, newHash, &expected); err != nil {
		t.Fatalf("updateRefCAS() error = %v, want nil", err)
	}
}

func TestUpdateRefCASFallsBackToStderrConflictWhenProbeFails(t *testing.T) {
	backend := newRefCASTestBackend(t)

	expected := gitHash("1111111111111111111111111111111111111111")
	newHash := gitHash("2222222222222222222222222222222222222222")

	t.Setenv("UPDATE_REF_STDERR", "fatal: cannot lock ref 'refs/heads/opax/v1': is at 3333333 but expected 1111111")
	t.Setenv("FOREACH_REF_FAIL", "1")
	t.Setenv("FOREACH_REF_STDERR", "fatal: probe failed")

	err := backend.updateRefCAS(opaxBranchRef, newHash, &expected)
	if !errors.Is(err, errReferenceChanged) {
		t.Fatalf("updateRefCAS() error = %v, want errReferenceChanged", err)
	}
}

func newRefCASTestBackend(t *testing.T) *nativeGitBackend {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture setup is unix-only")
	}

	scriptPath := writeFakeGitScript(t, `
while [ "$#" -gt 0 ]; do
	case "$1" in
		--git-dir|--work-tree)
			shift 2
			;;
		*)
			break
			;;
	esac
done

cmd="${1:-}"
if [ -n "$cmd" ]; then
	shift
fi

case "$cmd" in
	version)
		echo "git version 2.30.1"
		exit 0
		;;
	update-ref)
		exit_code="${UPDATE_REF_EXIT_CODE:-1}"
		if [ "$exit_code" = "0" ]; then
			exit 0
		fi
		echo "${UPDATE_REF_STDERR:-fatal: update-ref failed}" >&2
		exit "$exit_code"
		;;
	for-each-ref)
		if [ "${FOREACH_REF_FAIL:-0}" = "1" ]; then
			echo "${FOREACH_REF_STDERR:-fatal: for-each-ref failed}" >&2
			exit 1
		fi
		if [ "${REF_PRESENT:-1}" = "1" ]; then
			printf "%s\n" "${REF_HASH:-0000000000000000000000000000000000000000}"
		fi
		exit 0
		;;
	*)
		echo "unexpected git command: $cmd $*" >&2
		exit 1
		;;
esac
`)
	t.Setenv(gitBinaryOverrideEnv, scriptPath)

	gitDir := filepath.Join(t.TempDir(), "git")
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := resetGitVersionGateCacheForTests(); err != nil {
		t.Fatalf("resetGitVersionGateCacheForTests() error = %v", err)
	}
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir git dir %q error = %v", gitDir, err)
	}
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree %q error = %v", worktree, err)
	}

	backend, err := newNativeGitBackend(&RepoContext{
		GitDir:       gitDir,
		WorkTreeRoot: worktree,
	})
	if err != nil {
		t.Fatalf("newNativeGitBackend() error = %v", err)
	}
	return backend
}
