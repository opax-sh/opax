package lock_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opax-sh/opax/internal/lock"
)

func TestAcquireSuccess(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")

	lk, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer lk.Release()

	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("Stat(%q) error = %v", lockPath, err)
	}
}

func TestAcquireAndRelease(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")

	lk, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("Stat(%q) after acquire error = %v", lockPath, err)
	}

	if err := lk.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(%q) after release error = %v, want not exist", lockPath, err)
	}
}

func TestReleaseIdempotent(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")

	lk, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	if err := lk.Release(); err != nil {
		t.Fatalf("first Release() error = %v", err)
	}
	if err := lk.Release(); err != nil {
		t.Fatalf("second Release() error = %v, want nil", err)
	}
}

func TestReleaseNilReceiver(t *testing.T) {
	var lk *lock.Lock
	if err := lk.Release(); err != nil {
		t.Fatalf("Release() on nil receiver error = %v, want nil", err)
	}
}

func TestAcquireTimeout(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")

	cmd := startLockHelper(t, lockPath, 500*time.Millisecond)
	defer waitForHelperExit(t, cmd)

	start := time.Now()
	_, err := lock.Acquire(lockPath, 100*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, lock.ErrLockTimeout) {
		t.Fatalf("Acquire() second error = %v, want ErrLockTimeout", err)
	}
	if elapsed < 90*time.Millisecond || elapsed > 350*time.Millisecond {
		t.Fatalf("Acquire() timeout elapsed = %v, want reasonable margin around 100ms", elapsed)
	}
}

func TestAcquireTimeoutMessage(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")

	cmd := startLockHelper(t, lockPath, 500*time.Millisecond)
	defer waitForHelperExit(t, cmd)

	_, err := lock.Acquire(lockPath, 100*time.Millisecond)
	if !errors.Is(err, lock.ErrLockTimeout) {
		t.Fatalf("Acquire() second error = %v, want ErrLockTimeout", err)
	}
	if !strings.Contains(err.Error(), lockPath) {
		t.Fatalf("timeout error %q should mention path %q", err, lockPath)
	}
	if !strings.Contains(err.Error(), "held by PID") {
		t.Fatalf("timeout error %q should mention holder PID", err)
	}
}

func TestAcquireStaleLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")
	writeOldLockFile(t, lockPath, []byte(`{"pid":999999,"acquired_at":"2026-03-17T10:30:00Z"}`))

	_, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if !errors.Is(err, lock.ErrStaleLock) {
		t.Fatalf("Acquire() error = %v, want ErrStaleLock", err)
	}
	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Fatalf("Stat(%q) after stale detection error = %v, want file to remain", lockPath, statErr)
	}
}

func TestAcquireInvalidLockFile(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")
	writeOldLockFile(t, lockPath, []byte("not-json"))

	_, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if !errors.Is(err, lock.ErrStaleLock) {
		t.Fatalf("Acquire() error = %v, want ErrStaleLock", err)
	}
	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Fatalf("Stat(%q) after invalid lock detection error = %v, want file to remain", lockPath, statErr)
	}
}

func TestAcquireEmptyLockFile(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")
	writeOldLockFile(t, lockPath, nil)

	_, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if !errors.Is(err, lock.ErrStaleLock) {
		t.Fatalf("Acquire() error = %v, want ErrStaleLock", err)
	}
	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Fatalf("Stat(%q) after empty lock detection error = %v, want file to remain", lockPath, statErr)
	}
}

func TestAcquireInitializingLockFileWaits(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", lockPath, err)
	}

	start := time.Now()
	_, err := lock.Acquire(lockPath, 250*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, lock.ErrStaleLock) {
		t.Fatalf("Acquire() error = %v, want ErrStaleLock", err)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("Acquire() elapsed = %v, want wait through initialization grace", elapsed)
	}
}

func TestAcquireConcurrent(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")

	cmd := startLockHelper(t, lockPath, 200*time.Millisecond)
	defer waitForHelperExit(t, cmd)

	start := time.Now()
	lk, err := lock.Acquire(lockPath, 750*time.Millisecond)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer lk.Release()
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("Acquire() elapsed = %v, want to block until helper releases lock", elapsed)
	}
}

func TestAcquireReentrant(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")
	writeOldLockFile(t, lockPath, []byte(fmt.Sprintf(`{"pid":%d,"acquired_at":"2026-03-17T10:30:00Z"}`, os.Getpid())))

	_, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err == nil {
		t.Fatal("Acquire() error = nil, want reentrant error")
	}
	if strings.Contains(err.Error(), lock.ErrStaleLock.Error()) {
		t.Fatalf("Acquire() error = %v, want explicit reentrant error without ErrStaleLock", err)
	}
	if !strings.Contains(err.Error(), "already held by current process") {
		t.Fatalf("Acquire() error = %v, want current process message", err)
	}
}

func TestAcquireZeroTimeout(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")

	cmd := startLockHelper(t, lockPath, 500*time.Millisecond)
	defer waitForHelperExit(t, cmd)

	start := time.Now()
	_, err := lock.Acquire(lockPath, 0)
	elapsed := time.Since(start)
	if !errors.Is(err, lock.ErrLockTimeout) {
		t.Fatalf("Acquire() second error = %v, want ErrLockTimeout", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("Acquire() zero-timeout elapsed = %v, want immediate return", elapsed)
	}
}

func TestAcquireNoDirectory(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "missing")
	lockPath := filepath.Join(missingDir, "opax.lock")

	_, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err == nil {
		t.Fatal("Acquire() error = nil, want missing directory error")
	}
	if !strings.Contains(err.Error(), "directory does not exist") {
		t.Fatalf("Acquire() error = %v, want missing directory message", err)
	}
	if !strings.Contains(err.Error(), missingDir) {
		t.Fatalf("Acquire() error = %v, want directory path %q", err, missingDir)
	}
}

func TestLockFileContent(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")

	lk, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer lk.Release()

	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", lockPath, err)
	}

	var payload struct {
		PID        int    `json:"pid"`
		AcquiredAt string `json:"acquired_at"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload.PID != os.Getpid() {
		t.Fatalf("payload PID = %d, want %d", payload.PID, os.Getpid())
	}
	if payload.AcquiredAt == "" {
		t.Fatal("payload AcquiredAt is empty")
	}
	if _, err := time.Parse(time.RFC3339, payload.AcquiredAt); err != nil {
		t.Fatalf("time.Parse(%q) error = %v", payload.AcquiredAt, err)
	}
}

func TestDeferPattern(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "opax.lock")
	wantErr := errors.New("boom")

	err := func() error {
		lk, err := lock.Acquire(lockPath, lock.DefaultTimeout)
		if err != nil {
			return err
		}
		defer lk.Release()
		return wantErr
	}()
	if !errors.Is(err, wantErr) {
		t.Fatalf("wrapped error = %v, want %v", err, wantErr)
	}
	if _, statErr := os.Stat(lockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Stat(%q) error = %v, want not exist after deferred release", lockPath, statErr)
	}
}

func writeOldLockFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	old := time.Now().Add(-time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", path, err)
	}
}

func startLockHelper(t *testing.T, lockPath string, hold time.Duration) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestLockHelperProcess", "--", lockPath, strconv.FormatInt(hold.Milliseconds(), 10))
	cmd.Env = append(os.Environ(), "GO_WANT_LOCK_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("helper readiness read error = %v", err)
	}
	if strings.TrimSpace(line) != "acquired" {
		t.Fatalf("helper readiness = %q, want acquired", line)
	}
	return cmd
}

func waitForHelperExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 0 {
			return
		}
		t.Fatalf("helper process error = %v", err)
	}
}

func TestLockHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LOCK_HELPER") != "1" {
		return
	}

	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep == -1 || len(args) != sep+3 {
		fmt.Fprintln(os.Stderr, "missing helper args")
		os.Exit(2)
	}

	lockPath := args[sep+1]
	holdMS, err := strconv.ParseInt(args[sep+2], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse hold duration: %v\n", err)
		os.Exit(2)
	}

	lk, err := lock.Acquire(lockPath, lock.DefaultTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "helper acquire: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("acquired")
	time.Sleep(time.Duration(holdMS) * time.Millisecond)
	if err := lk.Release(); err != nil {
		fmt.Fprintf(os.Stderr, "helper release: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}
