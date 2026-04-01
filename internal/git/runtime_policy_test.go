package git

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeGitStderrSanitizesAbsolutePathsAndCaps(t *testing.T) {
	stderr := []byte(
		"fatal: cannot lock ref 'refs/heads/opax/v1': Unable to create '/Users/dev/repo/.git/refs/heads/opax/v1.lock': File exists\n",
	)

	normalized := normalizeGitStderr(stderr)
	if strings.Contains(normalized, "/Users/dev/repo") {
		t.Fatalf("normalizeGitStderr() leaked absolute path: %q", normalized)
	}
	if !strings.Contains(normalized, "refs/heads/opax/v1") {
		t.Fatalf("normalizeGitStderr() dropped ref context: %q", normalized)
	}
	if !strings.Contains(normalized, "<path>") {
		t.Fatalf("normalizeGitStderr() missing sanitized placeholder: %q", normalized)
	}

	longStderr := []byte("fatal: " + strings.Repeat("/very/long/path/segment", 80))
	longNormalized := normalizeGitStderr(longStderr)
	if len([]byte(longNormalized)) > gitStderrMaxBytes {
		t.Fatalf(
			"normalizeGitStderr() length = %d, want <= %d",
			len([]byte(longNormalized)),
			gitStderrMaxBytes,
		)
	}
}

func TestNewGitCommandRuntimeRejectsOPAXGitBinOutsideTests(t *testing.T) {
	t.Setenv(gitBinaryOverrideEnv, "/tmp/fake-git")

	origAllow := allowTestGitOverrides
	allowTestGitOverrides = func() bool { return false }
	t.Cleanup(func() {
		allowTestGitOverrides = origAllow
	})

	_, err := newGitCommandRuntime()
	if err == nil {
		t.Fatal("newGitCommandRuntime() error = nil, want override rejection")
	}
	if !strings.Contains(err.Error(), gitBinaryOverrideEnv) {
		t.Fatalf("newGitCommandRuntime() error = %v, want %q mention", err, gitBinaryOverrideEnv)
	}
}

func TestNewGitCommandRuntimeAllowsOPAXGitBinInTests(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture setup is unix-only")
	}

	script := writeFakeGitScript(t, "echo \"git version 2.30.1\"\n")
	t.Setenv(gitBinaryOverrideEnv, script)

	runtime, err := newGitCommandRuntime()
	if err != nil {
		t.Fatalf("newGitCommandRuntime() error = %v", err)
	}

	wantPath, err := resolveExecutablePath(script)
	if err != nil {
		t.Fatalf("resolveExecutablePath(%q) error = %v", script, err)
	}
	if runtime.binaryPath != wantPath {
		t.Fatalf("runtime.binaryPath = %q, want %q", runtime.binaryPath, wantPath)
	}
}

func TestEnsureSupportedGitVersionCachesSuccessByBinaryPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture setup is unix-only")
	}

	countFile := filepath.Join(t.TempDir(), "version.count")
	script := writeFakeGitScript(
		t,
		"echo x >> \"$COUNT_FILE\"\n"+
			"if [ \"$1\" = \"version\" ]; then\n"+
			"  echo \"git version 2.30.1\"\n"+
			"  exit 0\n"+
			"fi\n"+
			"echo \"unexpected args: $@\" >&2\n"+
			"exit 1\n",
	)

	t.Setenv("COUNT_FILE", countFile)
	t.Setenv(gitBinaryOverrideEnv, script)
	if err := resetGitVersionGateCacheForTests(); err != nil {
		t.Fatalf("resetGitVersionGateCacheForTests() error = %v", err)
	}

	runtime, err := newGitCommandRuntime()
	if err != nil {
		t.Fatalf("newGitCommandRuntime() error = %v", err)
	}

	if err := ensureSupportedGitVersion(runtime.binaryPath); err != nil {
		t.Fatalf("ensureSupportedGitVersion() first call error = %v", err)
	}
	if err := ensureSupportedGitVersion(runtime.binaryPath); err != nil {
		t.Fatalf("ensureSupportedGitVersion() second call error = %v", err)
	}

	count := countLines(t, countFile)
	if count != 1 {
		t.Fatalf("version command count = %d, want 1", count)
	}
}

func TestEnsureSupportedGitVersionCachesFailureByBinaryPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture setup is unix-only")
	}

	countFile := filepath.Join(t.TempDir(), "version.count")
	script := writeFakeGitScript(
		t,
		"echo x >> \"$COUNT_FILE\"\n"+
			"if [ \"$1\" = \"version\" ]; then\n"+
			"  echo \"git version 2.29.9\"\n"+
			"  exit 0\n"+
			"fi\n"+
			"echo \"unexpected args: $@\" >&2\n"+
			"exit 1\n",
	)

	t.Setenv("COUNT_FILE", countFile)
	t.Setenv(gitBinaryOverrideEnv, script)
	if err := resetGitVersionGateCacheForTests(); err != nil {
		t.Fatalf("resetGitVersionGateCacheForTests() error = %v", err)
	}

	runtime, err := newGitCommandRuntime()
	if err != nil {
		t.Fatalf("newGitCommandRuntime() error = %v", err)
	}

	err = ensureSupportedGitVersion(runtime.binaryPath)
	if err == nil {
		t.Fatal("ensureSupportedGitVersion() first call error = nil, want minimum-version failure")
	}
	if !strings.Contains(err.Error(), "below minimum supported") {
		t.Fatalf("ensureSupportedGitVersion() first call error = %v", err)
	}

	err = ensureSupportedGitVersion(runtime.binaryPath)
	if err == nil {
		t.Fatal("ensureSupportedGitVersion() second call error = nil, want cached failure")
	}
	if !strings.Contains(err.Error(), "below minimum supported") {
		t.Fatalf("ensureSupportedGitVersion() second call error = %v", err)
	}

	count := countLines(t, countFile)
	if count != 1 {
		t.Fatalf("version command count = %d, want 1", count)
	}
}

func TestResetGitVersionGateCacheForTestsGuard(t *testing.T) {
	orig := allowTestOrCIGateReset
	allowTestOrCIGateReset = func() bool { return false }
	t.Cleanup(func() {
		allowTestOrCIGateReset = orig
	})

	err := resetGitVersionGateCacheForTests()
	if err == nil {
		t.Fatal("resetGitVersionGateCacheForTests() error = nil, want guard failure")
	}
	if !strings.Contains(err.Error(), "restricted to test or CI") {
		t.Fatalf("resetGitVersionGateCacheForTests() error = %v", err)
	}
}

func TestGitRuntimeForcesLocaleEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture setup is unix-only")
	}

	envFile := filepath.Join(t.TempDir(), "env.log")
	script := writeFakeGitScript(
		t,
		"echo \"$LC_ALL|$LANG\" > \"$ENV_FILE\"\n"+
			"for arg in \"$@\"; do\n"+
			"  if [ \"$arg\" = \"version\" ]; then\n"+
			"    echo \"git version 2.30.1\"\n"+
			"    exit 0\n"+
			"  fi\n"+
			"done\n"+
			"echo \"unexpected args: $@\" >&2\n"+
			"exit 1\n",
	)

	t.Setenv("ENV_FILE", envFile)
	t.Setenv(gitBinaryOverrideEnv, script)
	t.Setenv("LC_ALL", "fr_FR.UTF-8")
	t.Setenv("LANG", "fr_FR.UTF-8")

	gitDir := filepath.Join(t.TempDir(), "git")
	workTree := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", gitDir, err)
	}
	if err := os.MkdirAll(workTree, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", workTree, err)
	}

	backend, err := newNativeGitBackend(&RepoContext{
		GitDir:       gitDir,
		WorkTreeRoot: workTree,
	})
	if err != nil {
		t.Fatalf("newNativeGitBackend() error = %v", err)
	}

	if _, _, err := backend.runCapture(nil, "version"); err != nil {
		t.Fatalf("backend.runCapture(version) error = %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", envFile, err)
	}
	got := strings.TrimSpace(string(data))
	if got != "C|C" {
		t.Fatalf("forced locale = %q, want %q", got, "C|C")
	}
}

func writeFakeGitScript(t *testing.T, body string) string {
	t.Helper()

	scriptPath := filepath.Join(t.TempDir(), "fake-git.sh")
	content := "#!/bin/sh\nset -eu\n" + body
	if err := os.WriteFile(scriptPath, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}
	return scriptPath
}

func countLines(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return 0
	}
	return len(lines)
}
