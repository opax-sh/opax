package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrLockTimeout is returned when the lock cannot be acquired
	// within the timeout period.
	ErrLockTimeout = errors.New("lock: timeout waiting for lock")

	// ErrStaleLock is returned when a stale or corrupt lock was detected.
	// The lock package does not remove the file automatically.
	ErrStaleLock = errors.New("lock: stale or corrupt lock detected")

	// ErrAlreadyHeldByCurrentProcess is returned when the current process
	// attempts to acquire a lock it already holds.
	ErrAlreadyHeldByCurrentProcess = errors.New("lock: already held by current process")
)

const (
	// DefaultTimeout is the default lock acquisition timeout.
	DefaultTimeout = 5 * time.Second

	// pollInterval is the time between acquisition attempts.
	pollInterval = 50 * time.Millisecond

	// initializationGrace is the maximum time to treat an empty or
	// invalid lock file as still being initialized by the winner.
	initializationGrace = 100 * time.Millisecond
)

// Lock represents an acquired advisory file lock.
type Lock struct {
	path string
	file *os.File
}

type lockInfo struct {
	PID        int    `json:"pid"`
	AcquiredAt string `json:"acquired_at"`
}

type lockState struct {
	info           lockInfo
	hasInfo        bool
	initializing   bool
	stale          bool
	staleReason    string
	initializingAt time.Time
}

// Acquire attempts to obtain the lock at the given path.
// It blocks up to timeout, polling at short intervals.
//
// On success, the lock file is created containing the current PID
// and acquisition timestamp.
func Acquire(path string, timeout time.Duration) (*Lock, error) {
	parent := filepath.Dir(path)
	if err := ensureDirectoryExists(parent); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	allowRetry := timeout > 0
	var lastState lockState

	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			if writeErr := writeLockInfo(file); writeErr != nil {
				cleanupErr := cleanupPartialLock(path, file)
				if cleanupErr != nil {
					return nil, fmt.Errorf("lock: initialize %s: %v (cleanup failed: %v)", path, writeErr, cleanupErr)
				}
				return nil, fmt.Errorf("lock: initialize %s: %w", path, writeErr)
			}
			return &Lock{path: path, file: file}, nil
		}

		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock: acquire %s: %w", path, err)
		}

		state, stateErr := inspectLockFile(path)
		if stateErr != nil {
			return nil, stateErr
		}
		lastState = state

		if state.stale {
			return nil, staleError(path, state)
		}

		if !allowRetry || time.Now().After(deadline) {
			return nil, timeoutError(path, timeout, lastState)
		}

		time.Sleep(pollInterval)
	}
}

// Release releases the lock and removes the lock file.
// Safe to call multiple times (idempotent).
// Safe to call on a nil receiver.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}

	path := l.path
	file := l.file
	l.file = nil

	closeErr := file.Close()
	removeErr := os.Remove(path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}

	if closeErr == nil && removeErr == nil {
		return nil
	}
	if closeErr != nil && removeErr != nil {
		return fmt.Errorf("lock: release %s: close: %v; remove: %v", path, closeErr, removeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("lock: release %s: close: %w", path, closeErr)
	}
	return fmt.Errorf("lock: release %s: remove: %w", path, removeErr)
}

func ensureDirectoryExists(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("lock: directory does not exist: %s", path)
		}
		return fmt.Errorf("lock: stat directory %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("lock: parent is not a directory: %s", path)
	}
	return nil
}

func writeLockInfo(file *os.File) error {
	info := lockInfo{
		PID:        os.Getpid(),
		AcquiredAt: time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("lock: marshal lock metadata: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("lock: write lock metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("lock: sync lock metadata: %w", err)
	}
	return nil
}

func cleanupPartialLock(path string, file *os.File) error {
	closeErr := file.Close()
	removeErr := os.Remove(path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	if closeErr == nil && removeErr == nil {
		return nil
	}
	if closeErr != nil && removeErr != nil {
		return fmt.Errorf("close: %v; remove: %v", closeErr, removeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close: %w", closeErr)
	}
	return fmt.Errorf("remove: %w", removeErr)
}

func inspectLockFile(path string) (lockState, error) {
	data, modTime, err := readLockFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return lockState{}, nil
		}
		return lockState{}, fmt.Errorf("lock: read %s: %w", path, err)
	}

	state := lockState{}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		if time.Since(modTime) <= initializationGrace {
			state.initializing = true
			state.initializingAt = modTime
			return state, nil
		}
		state.stale = true
		state.staleReason = "empty lock file"
		return state, nil
	}

	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		if time.Since(modTime) <= initializationGrace {
			state.initializing = true
			state.initializingAt = modTime
			return state, nil
		}
		state.stale = true
		state.staleReason = "invalid lock metadata"
		return state, nil
	}

	state.info = info
	state.hasInfo = true

	if info.PID == os.Getpid() {
		state.stale = true
		state.staleReason = "already held by current process"
		return state, nil
	}

	alive, err := isProcessAlive(info.PID)
	if err != nil {
		return lockState{}, fmt.Errorf("lock: check pid %d for %s: %w", info.PID, path, err)
	}
	if !alive {
		state.stale = true
		state.staleReason = "holder process is not running"
	}

	return state, nil
}

func readLockFile(path string) ([]byte, time.Time, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, time.Time{}, err
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, time.Time{}, err
	}

	return data, info.ModTime(), nil
}

func isProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}

	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}

func staleError(path string, state lockState) error {
	if state.staleReason == "already held by current process" {
		return fmt.Errorf("lock: already held by current process: %s: %w", path, ErrAlreadyHeldByCurrentProcess)
	}
	if state.hasInfo {
		return fmt.Errorf("lock: stale or corrupt lock at %s (held by PID %d since %s: %s): %w", path, state.info.PID, state.info.AcquiredAt, state.staleReason, ErrStaleLock)
	}
	return fmt.Errorf("lock: stale or corrupt lock at %s (%s): %w", path, state.staleReason, ErrStaleLock)
}

func timeoutError(path string, timeout time.Duration, state lockState) error {
	if state.hasInfo {
		return fmt.Errorf("lock: timeout after %v waiting for %s (held by PID %d since %s): %w", timeout, path, state.info.PID, state.info.AcquiredAt, ErrLockTimeout)
	}
	if state.initializing {
		return fmt.Errorf("lock: timeout after %v waiting for %s (lock file still initializing): %w", timeout, path, ErrLockTimeout)
	}
	return fmt.Errorf("lock: timeout after %v waiting for %s: %w", timeout, path, ErrLockTimeout)
}
