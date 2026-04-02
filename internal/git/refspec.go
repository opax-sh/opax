package git

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/opax-sh/opax/internal/lock"
)

// RefspecPlan is the FEAT-0011 canonical plan for one remote.
type RefspecPlan struct {
	DefaultFetchExclusions []string
	OpaxFetch              []string
	OpaxPush               []string
}

// RefspecViolationCode is a stable, machine-readable state violation code.
type RefspecViolationCode string

const (
	RefspecViolationMissingDefaultFetchExclusion RefspecViolationCode = "missing_default_fetch_exclusion"
	RefspecViolationOpaxRefsInRemotePush         RefspecViolationCode = "opax_refs_in_remote_push"
	RefspecViolationInvalidOpaxManagedConfig     RefspecViolationCode = "invalid_opax_managed_config"
)

// RefspecState reflects current refspec/isolation state for one remote.
type RefspecState struct {
	DefaultFetchExclusionPresent bool
	DefaultSyncIsolationEnforced bool
	Violations                   []RefspecViolationCode
	OpaxFetch                    []string
	OpaxPush                     []string
}

const (
	refspecDefaultFetchExclusion = "^refs/heads/opax/v1"

	refspecManagedOffendingLimit = 5
	refspecManagedValueMaxLen    = 160
)

type refspecPreflightState struct {
	defaultFetchExclusionCount int
}

// BuildRefspecPlan validates remote format and returns canonical managed values.
func BuildRefspecPlan(remote string) (*RefspecPlan, error) {
	if err := validateRemoteName(remote); err != nil {
		return nil, err
	}

	return &RefspecPlan{
		DefaultFetchExclusions: []string{refspecDefaultFetchExclusion},
		OpaxFetch: []string{
			fmt.Sprintf("+%s:refs/remotes/%s/opax/v1", opaxBranchRef, remote),
			"+refs/opax/*:refs/opax/*",
			"+refs/notes/opax/*:refs/notes/opax/*",
		},
		OpaxPush: []string{
			fmt.Sprintf("+%s:%s", opaxBranchRef, opaxBranchRef),
			"+refs/opax/*:refs/opax/*",
			"+refs/notes/opax/*:refs/notes/opax/*",
		},
	}, nil
}

// ApplyRefspecPlan applies the FEAT-0011 configuration for one remote.
func ApplyRefspecPlan(ctx *RepoContext, remote string, plan *RefspecPlan) (_ *RefspecState, err error) {
	canonicalPlan, err := BuildRefspecPlan(remote)
	if err != nil {
		return nil, err
	}
	if err := validateRefspecPlan(plan, canonicalPlan); err != nil {
		return nil, err
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if _, err := validateRefspecApplyPreflight(backend, remote, canonicalPlan); err != nil {
		return nil, err
	}

	cfgLock, lockPath, err := acquireRefspecLock(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if releaseErr := cfgLock.Release(); releaseErr != nil && err == nil {
			err = fmt.Errorf("git: release refspec lock %s: %w", lockPath, releaseErr)
		}
	}()

	preflightState, err := validateRefspecApplyPreflight(backend, remote, canonicalPlan)
	if err != nil {
		return nil, err
	}

	if preflightState.defaultFetchExclusionCount == 0 {
		if err := addConfigMultivarValue(
			backend,
			remoteFetchConfigKey(remote),
			refspecDefaultFetchExclusion,
		); err != nil {
			return nil, err
		}
	}

	if err := reconcileManagedMultivar(
		backend,
		opaxRemoteFetchConfigKey(remote),
		canonicalPlan.OpaxFetch,
	); err != nil {
		return nil, err
	}
	if err := reconcileManagedMultivar(
		backend,
		opaxRemotePushConfigKey(remote),
		canonicalPlan.OpaxPush,
	); err != nil {
		return nil, err
	}

	return readRefspecStateWithBackend(backend, remote, canonicalPlan)
}

// ReadRefspecState reads FEAT-0011 refspec state for one remote.
func ReadRefspecState(ctx *RepoContext, remote string) (*RefspecState, error) {
	canonicalPlan, err := BuildRefspecPlan(remote)
	if err != nil {
		return nil, err
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return readRefspecStateWithBackend(backend, remote, canonicalPlan)
}

func validateRemoteName(remote string) error {
	if remote == "" {
		return fmt.Errorf("git: remote name is empty: %w", ErrRemoteNameInvalid)
	}
	if strings.HasPrefix(remote, "-") {
		return fmt.Errorf("git: remote name %q starts with '-': %w", remote, ErrRemoteNameInvalid)
	}
	if strings.TrimSpace(remote) != remote {
		return fmt.Errorf("git: remote name %q contains leading/trailing whitespace: %w", remote, ErrRemoteNameInvalid)
	}

	for i := 0; i < len(remote); i++ {
		ch := remote[i]
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '.', ch == '_', ch == '/', ch == '-':
		default:
			return fmt.Errorf("git: remote name %q contains invalid character %q: %w", remote, ch, ErrRemoteNameInvalid)
		}
	}

	return nil
}

func validateRefspecPlan(plan, canonical *RefspecPlan) error {
	if plan == nil {
		return fmt.Errorf("git: refspec plan is nil: %w", ErrInvalidRefspecConfig)
	}

	if !stringSlicesEqual(plan.DefaultFetchExclusions, canonical.DefaultFetchExclusions) ||
		!stringSlicesEqual(plan.OpaxFetch, canonical.OpaxFetch) ||
		!stringSlicesEqual(plan.OpaxPush, canonical.OpaxPush) {
		return fmt.Errorf("git: refspec plan does not match canonical values: %w", ErrInvalidRefspecConfig)
	}

	return nil
}

func readRefspecStateWithBackend(
	backend *nativeGitBackend,
	remote string,
	canonicalPlan *RefspecPlan,
) (*RefspecState, error) {
	if err := ensureRemoteExists(backend, remote); err != nil {
		return nil, err
	}

	remoteFetchValues, err := readConfigMultivarValues(backend, remoteFetchConfigKey(remote))
	if err != nil {
		return nil, err
	}
	defaultExclusionPresent := countExactValues(remoteFetchValues, refspecDefaultFetchExclusion) > 0

	remotePushValues, err := readConfigMultivarValues(backend, remotePushConfigKey(remote))
	if err != nil {
		return nil, err
	}
	pushOpaxOffenders := collectRemotePushOpaxRefspecs(remotePushValues)

	managedFetchValues, err := readConfigMultivarValues(backend, opaxRemoteFetchConfigKey(remote))
	if err != nil {
		return nil, err
	}
	opaxFetch, err := parseManagedRefspecValues(
		opaxRemoteFetchConfigKey(remote),
		managedFetchValues,
		canonicalPlan.OpaxFetch,
	)
	if err != nil {
		return nil, err
	}

	managedPushValues, err := readConfigMultivarValues(backend, opaxRemotePushConfigKey(remote))
	if err != nil {
		return nil, err
	}
	opaxPush, err := parseManagedRefspecValues(
		opaxRemotePushConfigKey(remote),
		managedPushValues,
		canonicalPlan.OpaxPush,
	)
	if err != nil {
		return nil, err
	}

	violations := make([]RefspecViolationCode, 0, 2)
	if !defaultExclusionPresent {
		violations = append(violations, RefspecViolationMissingDefaultFetchExclusion)
	}
	if len(pushOpaxOffenders) > 0 {
		violations = append(violations, RefspecViolationOpaxRefsInRemotePush)
	}

	return &RefspecState{
		DefaultFetchExclusionPresent: defaultExclusionPresent,
		DefaultSyncIsolationEnforced: defaultExclusionPresent && len(pushOpaxOffenders) == 0,
		Violations:                   violations,
		OpaxFetch:                    opaxFetch,
		OpaxPush:                     opaxPush,
	}, nil
}

func validateRefspecApplyPreflight(
	backend *nativeGitBackend,
	remote string,
	canonicalPlan *RefspecPlan,
) (*refspecPreflightState, error) {
	if err := ensureRemoteExists(backend, remote); err != nil {
		return nil, err
	}

	remoteFetchValues, err := readConfigMultivarValues(backend, remoteFetchConfigKey(remote))
	if err != nil {
		return nil, err
	}
	if !hasPositiveBranchFetch(remoteFetchValues) {
		return nil, fmt.Errorf(
			"git: %s must include at least one positive branch mapping: %w",
			remoteFetchConfigKey(remote),
			ErrInvalidRefspecConfig,
		)
	}
	defaultFetchExclusionCount := countExactValues(remoteFetchValues, refspecDefaultFetchExclusion)
	if defaultFetchExclusionCount > 1 {
		return nil, fmt.Errorf(
			"git: %s contains duplicate managed exclusion %q: %w",
			remoteFetchConfigKey(remote),
			refspecDefaultFetchExclusion,
			ErrInvalidRefspecConfig,
		)
	}

	remotePushValues, err := readConfigMultivarValues(backend, remotePushConfigKey(remote))
	if err != nil {
		return nil, err
	}
	pushOpaxOffenders := collectRemotePushOpaxRefspecs(remotePushValues)
	if len(pushOpaxOffenders) > 0 {
		return nil, fmt.Errorf(
			"git: %s contains Opax refs [%s]: %w",
			remotePushConfigKey(remote),
			strings.Join(pushOpaxOffenders, ", "),
			ErrDefaultSyncIsolationViolation,
		)
	}

	managedFetchValues, err := readConfigMultivarValues(backend, opaxRemoteFetchConfigKey(remote))
	if err != nil {
		return nil, err
	}
	if _, err := parseManagedRefspecValues(
		opaxRemoteFetchConfigKey(remote),
		managedFetchValues,
		canonicalPlan.OpaxFetch,
	); err != nil {
		return nil, err
	}

	managedPushValues, err := readConfigMultivarValues(backend, opaxRemotePushConfigKey(remote))
	if err != nil {
		return nil, err
	}
	if _, err := parseManagedRefspecValues(
		opaxRemotePushConfigKey(remote),
		managedPushValues,
		canonicalPlan.OpaxPush,
	); err != nil {
		return nil, err
	}

	return &refspecPreflightState{
		defaultFetchExclusionCount: defaultFetchExclusionCount,
	}, nil
}

func acquireRefspecLock(ctx *RepoContext) (*lock.Lock, string, error) {
	lockPath, err := opaxLockPath(ctx)
	if err != nil {
		return nil, "", err
	}

	timeout := lock.DefaultTimeout
	deadline := time.Now().Add(timeout)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, lockPath, fmt.Errorf("git: timed out waiting for refspec lock %s after %s", lockPath, timeout)
		}

		cfgLock, err := lock.Acquire(lockPath, remaining)
		if err == nil {
			return cfgLock, lockPath, nil
		}

		switch {
		case errors.Is(err, lock.ErrAlreadyHeldByCurrentProcess):
			time.Sleep(opaxBootstrapPoll)
			continue
		case errors.Is(err, lock.ErrLockTimeout):
			return nil, lockPath, fmt.Errorf("git: timed out waiting for refspec lock %s after %s: %w", lockPath, timeout, err)
		default:
			return nil, lockPath, fmt.Errorf("git: acquire refspec lock %s: %w", lockPath, err)
		}
	}
}

func ensureRemoteExists(backend *nativeGitBackend, remote string) error {
	_, stderr, err := backend.runCapture(nil, "remote", "get-url", remote)
	if err == nil {
		return nil
	}

	stderrText := strings.ToLower(normalizeGitStderr(stderr))
	if strings.Contains(stderrText, "no such remote") {
		return fmt.Errorf("git: remote %q not found: %w", remote, ErrRemoteMissing)
	}
	return wrapGitStderrError(fmt.Sprintf("git: verify remote %q", remote), stderr, err)
}

func readConfigMultivarValues(backend *nativeGitBackend, key string) ([]string, error) {
	stdout, stderr, err := backend.runCapture(nil, "config", "--local", "--get-all", key)
	if err != nil {
		if isMissingConfigGetResult(err, stderr) {
			return []string{}, nil
		}
		return nil, wrapGitStderrError(fmt.Sprintf("git: read config %s", key), stderr, err)
	}

	values := splitNonEmptyLines(stdout)
	if len(values) == 0 {
		return []string{}, nil
	}
	return values, nil
}

func addConfigMultivarValue(backend *nativeGitBackend, key, value string) error {
	_, stderr, err := backend.runCapture(nil, "config", "--local", "--add", key, value)
	if err != nil {
		return wrapGitStderrError(fmt.Sprintf("git: add config %s", key), stderr, err)
	}
	return nil
}

func reconcileManagedMultivar(backend *nativeGitBackend, key string, canonicalValues []string) error {
	_, stderr, err := backend.runCapture(nil, "config", "--local", "--unset-all", key)
	if err != nil && !isMissingConfigUnsetResult(err, stderr) {
		return wrapGitStderrError(fmt.Sprintf("git: clear config %s", key), stderr, err)
	}

	for _, value := range canonicalValues {
		if err := addConfigMultivarValue(backend, key, value); err != nil {
			return err
		}
	}

	return nil
}

func parseManagedRefspecValues(key string, rawValues, canonicalValues []string) ([]string, error) {
	semanticToCanonical := make(map[string]string, len(canonicalValues))
	for _, value := range canonicalValues {
		semanticToCanonical[strings.TrimPrefix(value, "+")] = value
	}

	seen := make(map[string]bool, len(canonicalValues))
	for _, raw := range rawValues {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fmt.Errorf("git: %s contains empty managed value: %w", key, ErrInvalidRefspecConfig)
		}

		canonical, ok := semanticToCanonical[strings.TrimPrefix(value, "+")]
		if !ok {
			return nil, fmt.Errorf("git: %s contains invalid managed value %q: %w", key, value, ErrInvalidRefspecConfig)
		}
		seen[canonical] = true
	}

	ordered := make([]string, 0, len(canonicalValues))
	for _, canonical := range canonicalValues {
		if seen[canonical] {
			ordered = append(ordered, canonical)
		}
	}
	return ordered, nil
}

func hasPositiveBranchFetch(values []string) bool {
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || strings.HasPrefix(value, "^") {
			continue
		}

		positive := strings.TrimPrefix(value, "+")
		src, _, ok := strings.Cut(positive, ":")
		if !ok {
			continue
		}
		if strings.HasPrefix(src, "refs/heads/") {
			return true
		}
	}
	return false
}

func collectRemotePushOpaxRefspecs(rawValues []string) []string {
	seen := map[string]bool{}
	for _, raw := range rawValues {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if !refspecTargetsOpaxRef(value) {
			continue
		}
		bounded := value
		if len(bounded) > refspecManagedValueMaxLen {
			bounded = bounded[:refspecManagedValueMaxLen]
		}
		seen[bounded] = true
	}

	if len(seen) == 0 {
		return nil
	}

	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	if len(result) > refspecManagedOffendingLimit {
		result = result[:refspecManagedOffendingLimit]
	}
	return result
}

func refspecTargetsOpaxRef(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}

	normalized := strings.TrimPrefix(trimmed, "+")
	normalized = strings.TrimPrefix(normalized, "^")
	parts := strings.Split(normalized, ":")
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		switch {
		case token == opaxBranchRef:
			return true
		case strings.HasPrefix(token, "refs/heads/opax/"):
			return true
		case strings.HasPrefix(token, "refs/opax/"):
			return true
		case strings.HasPrefix(token, "refs/notes/opax/"):
			return true
		}
	}
	return false
}

func countExactValues(values []string, target string) int {
	count := 0
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			count++
		}
	}
	return count
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func isMissingConfigGetResult(err error, stderr []byte) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	if exitErr.ExitCode() != 1 {
		return false
	}
	return strings.TrimSpace(string(stderr)) == ""
}

func isMissingConfigUnsetResult(err error, stderr []byte) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	code := exitErr.ExitCode()
	if code != 1 && code != 5 {
		return false
	}
	stderrText := strings.ToLower(normalizeGitStderr(stderr))
	if stderrText == "" {
		return true
	}
	return strings.Contains(stderrText, "no such section or key")
}

func remoteFetchConfigKey(remote string) string {
	return fmt.Sprintf("remote.%s.fetch", remote)
}

func remotePushConfigKey(remote string) string {
	return fmt.Sprintf("remote.%s.push", remote)
}

func opaxRemoteFetchConfigKey(remote string) string {
	return fmt.Sprintf("opax.remote.%s.fetch", remote)
}

func opaxRemotePushConfigKey(remote string) string {
	return fmt.Sprintf("opax.remote.%s.push", remote)
}
