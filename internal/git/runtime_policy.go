package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	gitBinaryOverrideEnv = "OPAX_GIT_BIN"
	gitStderrMaxBytes    = 512
)

var (
	unixAbsolutePathPattern    = regexp.MustCompile(`(^|[[:space:]'"` + "`" + `])(\/[^[:space:]:'"` + "`" + `]+)`)
	windowsAbsolutePathPattern = regexp.MustCompile(`(^|[[:space:]'"` + "`" + `])([A-Za-z]:\\[^[:space:]:'"` + "`" + `]+)`)

	gitVersionGateMu    sync.Mutex
	gitVersionGateCache = map[string]gitVersionGateResult{}

	allowTestGitOverrides = func() bool {
		base := filepath.Base(os.Args[0])
		return strings.HasSuffix(base, ".test")
	}
	allowTestOrCIGateReset = func() bool {
		if allowTestGitOverrides() {
			return true
		}
		return strings.TrimSpace(os.Getenv("CI")) != "" || strings.TrimSpace(os.Getenv("GITHUB_ACTIONS")) != ""
	}
)

type gitVersionGateResult struct {
	err error
}

type gitCommandRuntime struct {
	binaryPath string
}

func newGitCommandRuntime() (*gitCommandRuntime, error) {
	binaryPath, err := resolveGitBinaryPath()
	if err != nil {
		return nil, err
	}
	return &gitCommandRuntime{binaryPath: binaryPath}, nil
}

func resolveGitBinaryPath() (string, error) {
	override := strings.TrimSpace(os.Getenv(gitBinaryOverrideEnv))
	if override != "" {
		if !allowTestGitOverrides() {
			return "", fmt.Errorf(
				"git: %s is only supported in test execution",
				gitBinaryOverrideEnv,
			)
		}
		return resolveExecutablePath(override)
	}
	return resolveExecutablePath("git")
}

func resolveExecutablePath(executable string) (string, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return "", fmt.Errorf("git: resolve binary %q: %w", executable, err)
	}
	absPath, err := filepath.Abs(path)
	if err == nil {
		path = absPath
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path), nil
}

func (r *gitCommandRuntime) command(dir string, extraEnv []string, args ...string) *exec.Cmd {
	cmd := exec.Command(r.binaryPath, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = gitCommandEnv(extraEnv)
	return cmd
}

func (r *gitCommandRuntime) runCapture(dir string, stdin []byte, extraEnv []string, args ...string) ([]byte, []byte, error) {
	cmd := r.command(dir, extraEnv, args...)
	return runCommandCapture(cmd, stdin)
}

func gitCommandEnv(extra []string) []string {
	env := append([]string{}, os.Environ()...)
	env = setEnvValue(env, "LC_ALL", "C")
	env = setEnvValue(env, "LANG", "C")

	for _, kv := range extra {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		env = setEnvValue(env, key, value)
	}

	return env
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func ensureSupportedGitVersion(binaryPath string) error {
	key := filepath.Clean(binaryPath)

	gitVersionGateMu.Lock()
	if cached, ok := gitVersionGateCache[key]; ok {
		gitVersionGateMu.Unlock()
		return cached.err
	}
	gitVersionGateMu.Unlock()

	stdout, stderr, err := runStandaloneGitCaptureWithBinary(binaryPath, nil, "version")
	if err != nil {
		return cacheGitVersionGateResult(key, wrapGitStderrError("git: check git version", stderr, err))
	}

	version, parseErr := parseGitVersion(string(stdout))
	if parseErr != nil {
		return cacheGitVersionGateResult(key, fmt.Errorf("git: check git version: %w", parseErr))
	}
	minimum, parseErr := parseGitVersion("git version " + gitMinSupportedVersion)
	if parseErr != nil {
		return cacheGitVersionGateResult(key, fmt.Errorf("git: check git version minimum: %w", parseErr))
	}
	if versionLessThan(version, minimum) {
		return cacheGitVersionGateResult(
			key,
			fmt.Errorf("git: installed git %s is below minimum supported %s", version, minimum),
		)
	}

	return cacheGitVersionGateResult(key, nil)
}

func cacheGitVersionGateResult(key string, resultErr error) error {
	gitVersionGateMu.Lock()
	gitVersionGateCache[key] = gitVersionGateResult{err: resultErr}
	gitVersionGateMu.Unlock()
	return resultErr
}

func resetGitVersionGateCacheForTests() error {
	if !allowTestOrCIGateReset() {
		return fmt.Errorf("git: version gate cache reset is restricted to test or CI execution")
	}
	gitVersionGateMu.Lock()
	gitVersionGateCache = map[string]gitVersionGateResult{}
	gitVersionGateMu.Unlock()
	return nil
}

func wrapGitStderrError(prefix string, stderr []byte, err error) error {
	if err == nil {
		return nil
	}
	stderrText := normalizeGitStderr(stderr)
	if stderrText == "" {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	return fmt.Errorf("%s: %s: %w", prefix, stderrText, err)
}

func normalizeGitStderr(stderr []byte) string {
	if len(stderr) == 0 {
		return ""
	}

	text := strings.TrimSpace(strings.ReplaceAll(string(stderr), "\r\n", "\n"))
	if text == "" {
		return ""
	}

	text = unixAbsolutePathPattern.ReplaceAllString(text, "${1}<path>")
	text = windowsAbsolutePathPattern.ReplaceAllString(text, "${1}<path>")

	raw := []byte(text)
	if len(raw) > gitStderrMaxBytes {
		raw = raw[:gitStderrMaxBytes]
		raw = bytes.TrimRight(raw, " \n\r\t")
	}

	return strings.TrimSpace(string(raw))
}
