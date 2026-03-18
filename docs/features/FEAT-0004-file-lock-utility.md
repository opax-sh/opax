# FEAT-0004 — File Lock Utility

**Epic:** [EPIC-0000 — Project Foundation](../epics/EPIC-0000-foundation.md)
**Status:** in-progress
**Dependencies:** FEAT-0001 (stdlib only, no external deps needed)
**Dependents:** All downstream write paths (git plumbing writes, write orchestrator) — every write path acquires this lock

---

## Problem

Architecture invariant #7 states: "`.git/opax.lock` for all writes to the consolidated branch. No concurrent writes in Phase 0." Every operation that modifies the `opax/v1` orphan branch or CAS must serialize through a single lock to prevent tree corruption.

Git itself uses `.git/index.lock` for similar purposes. Opax needs its own lock at `.git/opax.lock` because Opax operations don't use the git index — they use plumbing commands that bypass the working tree entirely.

The lock must handle:
- Normal acquisition and release
- Timeout when another process holds the lock
- Stale locks from crashed processes
- Deferred cleanup via Go's `defer` pattern

---

## Design

### Package

`internal/lock/` — depends on stdlib only (`os`, `time`, `encoding/json`, `fmt`, `syscall`). No external dependencies.

### Files

| File | Contents |
|---|---|
| `internal/lock/lock.go` | Lock type, Acquire, Release, stale detection |
| `internal/lock/lock_test.go` | Table-driven tests including concurrency |

### Lock Mechanism

**Atomic creation** via `os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)`. The `O_CREATE|O_EXCL` combination is atomic on all POSIX filesystems — the kernel guarantees that only one process can create the file. If the file already exists, the call returns `os.ErrExist`.

This is the same mechanism git uses for `.git/index.lock`. It does not require `flock()` or `fcntl()` advisory locks, which have portability issues across NFS and macOS.

---

## Specification

### Lock Type

```go
// Lock represents an acquired advisory file lock.
type Lock struct {
    path string
    file *os.File
}
```

Unexported fields — callers interact only through `Acquire` and `Release`.

### Acquire

```go
// Acquire attempts to obtain the lock at the given path.
// It blocks up to timeout, polling at short intervals.
//
// On success, the lock file is created containing the current PID
// and acquisition timestamp.
//
// Errors:
//   - ErrLockTimeout: timeout expired, lock held by another process
//   - ErrStaleLock: stale lock was detected and removed (caller should retry)
func Acquire(path string, timeout time.Duration) (*Lock, error)
```

**Algorithm:**

```
1. Try os.OpenFile(path, O_CREATE|O_EXCL|O_WRONLY, 0644)
2. If success:
   a. Write {"pid": <current PID>, "acquired_at": "<now RFC3339>"}
   b. Return &Lock{path, file}
3. If os.ErrExist:
   a. Read the lock file content
   b. Parse PID from JSON
   c. Check if PID is alive (syscall.Kill(pid, 0))
   d. If PID is NOT alive → stale lock:
      - Remove the lock file
      - Return ErrStaleLock (caller retries)
   e. If PID IS alive → lock is held:
      - Sleep 50ms
      - If elapsed > timeout → return ErrLockTimeout
      - Go to step 1
4. If other error → return wrapped error
```

### Release

```go
// Release releases the lock and removes the lock file.
// Safe to call multiple times (idempotent).
// Safe to call on a nil receiver.
func (l *Lock) Release() error
```

**Algorithm:**

```
1. If l is nil or l.file is nil → return nil (idempotent)
2. Close the file handle
3. Remove the lock file (os.Remove)
4. Set l.file = nil (prevent double-release issues)
5. Return any error from close/remove
```

### Error Types

```go
var (
    // ErrLockTimeout is returned when the lock cannot be acquired
    // within the timeout period.
    ErrLockTimeout = errors.New("lock: timeout waiting for lock")

    // ErrStaleLock is returned when a stale lock was detected and
    // removed. The caller should retry acquisition.
    ErrStaleLock = errors.New("lock: stale lock removed")
)
```

`ErrLockTimeout` should include context when returned:

```go
fmt.Errorf("lock: timeout after %v waiting for %s (held by PID %d since %s): %w",
    timeout, path, holderPID, acquiredAt, ErrLockTimeout)
```

### Lock File Content

```json
{"pid": 12345, "acquired_at": "2026-03-17T10:30:00Z"}
```

Internal struct (unexported):

```go
type lockInfo struct {
    PID        int    `json:"pid"`
    AcquiredAt string `json:"acquired_at"`
}
```

### Constants

```go
const (
    // DefaultTimeout is the default lock acquisition timeout.
    DefaultTimeout = 5 * time.Second

    // pollInterval is the time between acquisition attempts.
    pollInterval = 50 * time.Millisecond
)
```

---

## Stale Lock Detection

A lock is stale when the process that created it is no longer running. This happens when:
- `opax` crashes (SIGSEGV, SIGKILL, power loss)
- A bug prevents `defer lock.Release()` from executing (only in pathological cases)

**Detection method:** `syscall.Kill(pid, 0)` — signal 0 doesn't send a signal but checks if the process exists. Returns `nil` if the process is alive, `syscall.ESRCH` if it doesn't exist.

**Platform notes:**
- macOS/Linux: `syscall.Kill` works as described
- Windows: would need `os.FindProcess` + handle check (not a Phase 0 concern — Opax targets macOS/Linux)

**Race condition:** Between reading the lock file and checking the PID, the process could exit and a new process could reuse the PID. This is extremely unlikely (PID space is large, race window is microseconds) and the consequence is benign (we wait for the timeout instead of cleaning up a stale lock).

---

## Usage Pattern

Every write path in the codebase follows this pattern:

```go
func writeRecord(gitDir string, record Record) error {
    lockPath := filepath.Join(gitDir, "opax.lock")

    lk, err := lock.Acquire(lockPath, lock.DefaultTimeout)
    if errors.Is(err, lock.ErrStaleLock) {
        // Stale lock was cleaned up, retry once
        lk, err = lock.Acquire(lockPath, lock.DefaultTimeout)
    }
    if err != nil {
        return fmt.Errorf("writeRecord: %w", err)
    }
    defer lk.Release()

    // ... perform write operations ...
    return nil
}
```

The stale-lock-then-retry pattern is documented here for downstream consumers (E1.3, E4.1). The lock package returns `ErrStaleLock` rather than silently retrying because the caller may want to log or handle the stale lock condition.

---

## Edge Cases

- **Lock file directory doesn't exist** — `Acquire` should return a clear error, not panic. The `.git/opax/` directory is created by `opax init` (E9.1). If it doesn't exist, the lock cannot be created. Error: `lock: directory does not exist: {path}`.
- **Lock file is not valid JSON** — treat as stale. A partial write (crash during lock creation) produces an incomplete file. Remove it and return `ErrStaleLock`.
- **Lock file is empty** — treat as stale. Same reasoning as invalid JSON.
- **Lock file PID matches current process** — this means the current process already holds the lock. This is a programming error (re-entrant locking). Return a clear error: `lock: already held by current process`.
- **Nil receiver on Release** — safe, returns nil. This supports the pattern where `Acquire` fails and `defer lock.Release()` was already deferred on a nil variable.
- **Concurrent goroutines** — the file-level lock serializes across processes. Within a single process, callers are responsible for not calling `Acquire` from multiple goroutines on the same path simultaneously (or handling `ErrLockTimeout` gracefully). This is acceptable because Phase 0 has no concurrent write paths within a single `opax` invocation.
- **Timeout of zero** — a single attempt with no retry. Either acquires immediately or returns `ErrLockTimeout`.
- **Very long timeout** — no upper bound enforced. Caller controls the duration.

---

## Acceptance Criteria

- [ ] `Acquire` creates lock file atomically using `O_CREATE|O_EXCL`
- [ ] `Acquire` writes valid JSON with current PID and RFC 3339 timestamp
- [ ] `Acquire` returns `*Lock` on success
- [ ] `Acquire` blocks and retries up to timeout when lock is held by another process
- [ ] `Acquire` returns `ErrLockTimeout` after timeout, with holder PID in error message
- [ ] `Acquire` detects stale lock (dead PID), removes lock file, returns `ErrStaleLock`
- [ ] `Acquire` treats invalid/empty lock file content as stale
- [ ] `Release` removes the lock file
- [ ] `Release` is idempotent — calling twice does not error
- [ ] `Release` on nil receiver does not panic
- [ ] Lock file does not exist after successful `Release`
- [ ] Concurrent test: two goroutines race to acquire — exactly one succeeds, other eventually acquires after first releases
- [ ] Timeout test: acquire lock, attempt second acquire with 100ms timeout — returns `ErrLockTimeout` within reasonable margin
- [ ] Stale lock test: create lock file with non-existent PID, acquire succeeds after stale removal
- [ ] Deferred cleanup: `defer lock.Release()` works correctly in normal return and early-return error paths
- [ ] Error messages follow `fmt.Errorf("lock: ...")` convention
- [ ] Table-driven tests, stdlib `testing` only

---

## Test Plan

| Test | What it verifies | Pass condition |
|---|---|---|
| `TestAcquireSuccess` | Normal acquisition | Lock file created, contains valid JSON with current PID |
| `TestAcquireAndRelease` | Full lifecycle | Lock file exists after acquire, gone after release |
| `TestReleaseIdempotent` | Double release safety | Second release returns nil, no panic |
| `TestReleaseNilReceiver` | Nil safety | No panic, returns nil |
| `TestAcquireTimeout` | Timeout behavior | Held lock causes `ErrLockTimeout` within margin of timeout value |
| `TestAcquireTimeoutMessage` | Error detail | Error message includes holder PID and file path |
| `TestAcquireStaleLock` | Stale detection | Lock file with dead PID → `ErrStaleLock`, lock file removed |
| `TestAcquireInvalidLockFile` | Corrupt file handling | Non-JSON lock file → treated as stale |
| `TestAcquireEmptyLockFile` | Empty file handling | Empty lock file → treated as stale |
| `TestAcquireConcurrent` | Cross-goroutine serialization | Two goroutines: first acquires, second blocks, second acquires after first releases |
| `TestAcquireReentrant` | Same-process detection | Current PID in lock file → clear error |
| `TestAcquireZeroTimeout` | Single attempt | Returns immediately: either success or timeout |
| `TestAcquireNoDirectory` | Missing parent dir | Returns error mentioning directory |
| `TestLockFileContent` | JSON format | Parses as `{"pid": N, "acquired_at": "..."}` with valid timestamp |
| `TestDeferPattern` | Deferred cleanup | Lock released even when function returns error early |
