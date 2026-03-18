# FEAT-0004 — File Lock Utility

**Epic:** [EPIC-0000 — Project Foundation](../epics/EPIC-0000-foundation.md)
**Status:** Completed
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
//   - ErrStaleLock: stale or corrupt lock detected; manual cleanup required
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
   c. If JSON is empty/invalid and file age is less than initializationGrace:
      - Treat as in-progress initialization
      - Sleep 50ms and retry until timeout
   d. If JSON is empty/invalid after initializationGrace:
      - Return ErrStaleLock (manual cleanup required)
   e. Check if PID is alive (syscall.Kill(pid, 0))
   f. If PID is NOT alive → stale lock:
      - Return ErrStaleLock (manual cleanup required)
   g. If PID IS alive → lock is held:
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

    // ErrStaleLock is returned when a stale or corrupt lock was detected.
    // The lock package fails closed and does not remove the file.
    ErrStaleLock = errors.New("lock: stale or corrupt lock detected")
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

    // initializationGrace is the maximum time to treat an empty or
    // invalid lock file as still being initialized by the winner.
    initializationGrace = 100 * time.Millisecond
)
```

---

## Stale Lock Detection

A lock is stale when the process that created it is no longer running, or when the lock file remains corrupt beyond the initialization grace window. This happens when:
- `opax` crashes (SIGSEGV, SIGKILL, power loss)
- A bug prevents `defer lock.Release()` from executing (only in pathological cases)
- A crash occurs after lock file creation but before metadata is fully written

**Detection method:** `syscall.Kill(pid, 0)` — signal 0 doesn't send a signal but checks if the process exists. Returns `nil` if the process is alive, `syscall.ESRCH` if it doesn't exist, and `syscall.EPERM` if the process exists but cannot be signaled by the current user.

**Platform notes:**
- macOS/Linux: `syscall.Kill` works as described
- Windows: would need `os.FindProcess` + handle check (not a Phase 0 concern — Opax targets macOS/Linux)

**Conservative policy:** The lock package does not delete stale or corrupt lock files. False unlock is worse than false lock. Manual cleanup is preferred over accidentally removing a valid lock held by another process.

**Liveness interpretation:**
- `nil` or `syscall.EPERM` → process exists, treat lock as live
- `syscall.ESRCH` → process does not exist, treat lock as stale
- Any other error → treat as unknown and fail conservatively

### Manual Recovery

When `Acquire` returns `ErrStaleLock`:

1. Inspect `.git/opax.lock`
2. Verify no Opax write operation is currently running
3. Remove the file manually if it is confirmed stale or corrupt
4. Retry the original command

---

## Usage Pattern

Every write path in the codebase follows this pattern:

```go
func writeRecord(gitDir string, record Record) error {
    lockPath := filepath.Join(gitDir, "opax.lock")

    lk, err := lock.Acquire(lockPath, lock.DefaultTimeout)
    if errors.Is(err, lock.ErrStaleLock) {
        return fmt.Errorf("writeRecord: stale or corrupt lock at %s: %w", lockPath, err)
    }
    if err != nil {
        return fmt.Errorf("writeRecord: %w", err)
    }
    defer lk.Release()

    // ... perform write operations ...
    return nil
}
```

The lock package fails closed. Downstream consumers should surface `ErrStaleLock` clearly and require manual cleanup rather than deleting the lock automatically.

---

## Edge Cases

- **Lock file directory doesn't exist** — `Acquire` should return a clear error, not panic. For `.git/opax.lock`, the required parent directory is `.git`. If it doesn't exist, the lock cannot be created. Error: `lock: directory does not exist: {path}`.
- **Lock file is not valid JSON** — treat as stale after `initializationGrace`. A partial write (crash during lock creation) produces an incomplete file. Do not remove it automatically; return `ErrStaleLock`.
- **Lock file is empty** — treat as in-progress initialization during `initializationGrace`, then as stale if it remains empty. Do not remove it automatically.
- **Lock file PID matches current process** — this means the current process already holds the lock. This is a programming error (re-entrant locking). Return a clear error: `lock: already held by current process`.
- **Nil receiver on Release** — safe, returns nil. This supports the pattern where `Acquire` fails and `defer lock.Release()` was already deferred on a nil variable.
- **Concurrent callers in the same process** — re-entrant acquisition from the same process is treated as a programming error. Cross-process serialization is the supported coordination mechanism.
- **Timeout of zero** — a single attempt with no retry. Either acquires immediately or returns `ErrLockTimeout`.
- **Very long timeout** — no upper bound enforced. Caller controls the duration.

---

## Acceptance Criteria

- [ ] `Acquire` creates lock file atomically using `O_CREATE|O_EXCL`
- [ ] `Acquire` writes valid JSON with current PID and RFC 3339 timestamp
- [ ] `Acquire` returns `*Lock` on success
- [ ] `Acquire` blocks and retries up to timeout when lock is held by another process
- [ ] `Acquire` returns `ErrLockTimeout` after timeout, with holder PID in error message
- [ ] `Acquire` detects stale lock (dead PID), leaves the lock file in place, returns `ErrStaleLock`
- [ ] `Acquire` treats invalid/empty lock file content as in-progress initialization during `initializationGrace`, then returns `ErrStaleLock` without deleting the file
- [ ] `Release` removes the lock file
- [ ] `Release` is idempotent — calling twice does not error
- [ ] `Release` on nil receiver does not panic
- [ ] Lock file does not exist after successful `Release`
- [ ] Concurrent test: two processes race to acquire — exactly one succeeds immediately, the other acquires after the first releases
- [ ] Timeout test: acquire lock, attempt second acquire with 100ms timeout — returns `ErrLockTimeout` within reasonable margin
- [ ] Stale lock test: create lock file with non-existent PID, acquire returns `ErrStaleLock` and leaves the file in place
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
| `TestAcquireStaleLock` | Stale detection | Lock file with dead PID → `ErrStaleLock`, lock file remains |
| `TestAcquireInvalidLockFile` | Corrupt file handling | Non-JSON lock file older than grace window → `ErrStaleLock`, file remains |
| `TestAcquireEmptyLockFile` | Empty file handling | Empty lock file older than grace window → `ErrStaleLock`, file remains |
| `TestAcquireConcurrent` | Cross-process serialization | One process acquires, another blocks, second acquires after first releases |
| `TestAcquireReentrant` | Same-process detection | Current PID in lock file → clear error |
| `TestAcquireZeroTimeout` | Single attempt | Returns immediately: either success or timeout |
| `TestAcquireNoDirectory` | Missing parent dir | Returns error mentioning directory |
| `TestLockFileContent` | JSON format | Parses as `{"pid": N, "acquired_at": "..."}` with valid timestamp |
| `TestDeferPattern` | Deferred cleanup | Lock released even when function returns error early |
| `TestAcquireInitializingLockFileWaits` | Grace window | Newly created empty lock file is treated as initializing before failing stale |

---

## Implementation Plan

### Scope

Implement `internal/lock/` as the single advisory file lock utility for all Phase 0 write serialization. This package remains stdlib-only and does not depend on git, store, CAS, or CLI packages.

### API To Implement

- [ ] Create `internal/lock/lock.go`
- [ ] Create `internal/lock/lock_test.go`
- [ ] Define `type Lock struct { path string; file *os.File }`
- [ ] Define `func Acquire(path string, timeout time.Duration) (*Lock, error)`
- [ ] Define `func (l *Lock) Release() error`
- [ ] Define `ErrLockTimeout`
- [ ] Define `ErrStaleLock`
- [ ] Define `DefaultTimeout = 5 * time.Second`
- [ ] Define `pollInterval = 50 * time.Millisecond`

### Acquire Implementation

- [ ] Validate that the parent directory of `path` exists before trying to create the lock file
- [ ] Return a clear `lock: ...` error when the directory does not exist
- [ ] Attempt lock acquisition with `os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)`
- [ ] On success, write JSON lock metadata containing:
  - [ ] Current PID from `os.Getpid()`
  - [ ] UTC acquisition timestamp in RFC 3339 format
- [ ] Return `*Lock` with the open file handle preserved
- [ ] If writing lock metadata fails after file creation:
  - [ ] Close the file
  - [ ] Remove the partially created lock file
  - [ ] Return a wrapped `lock: ...` error

### Existing Lock Handling

- [ ] When `os.OpenFile` returns `os.ErrExist`, inspect the current lock file
- [ ] Read and parse lock file contents into an internal `lockInfo` struct
- [ ] Treat invalid JSON as stale lock state
- [ ] Treat empty file content as stale lock state
- [ ] If lock PID matches `os.Getpid()`, return `lock: already held by current process`
- [ ] Check process liveness with `syscall.Kill(pid, 0)`
- [ ] If PID is dead (`syscall.ESRCH`):
  - [ ] Remove the stale lock file
  - [ ] Return `ErrStaleLock`
- [ ] If PID is alive:
  - [ ] Sleep for `pollInterval`
  - [ ] Retry until timeout expires
- [ ] On timeout, return a wrapped error that includes:
  - [ ] Lock path
  - [ ] Holder PID
  - [ ] Holder acquisition timestamp
  - [ ] `ErrLockTimeout`

### Timeout Semantics

- [ ] Support zero timeout as a single immediate acquisition attempt
- [ ] Do not sleep/retry when timeout is zero
- [ ] Preserve last-known holder metadata for timeout error messages
- [ ] Use `errors.Is(err, ErrLockTimeout)` compatibility in returned timeout errors

### Release Implementation

- [ ] Return `nil` when called on a nil receiver
- [ ] Return `nil` when `l.file == nil`
- [ ] Close the underlying file handle
- [ ] Remove the lock file from disk
- [ ] Set `l.file = nil` after release
- [ ] Make double release safe and non-failing
- [ ] Return wrapped `lock: ...` errors when close/remove fail
- [ ] If both close and remove fail, return an error that preserves both failure points

### Internal Helpers

- [ ] Add an unexported `lockInfo` struct with `pid` and `acquired_at` JSON fields
- [ ] Add a helper to write lock metadata JSON
- [ ] Add a helper to read/parse lock metadata JSON
- [ ] Add a helper to determine whether a PID is alive
- [ ] Add a helper for stale lock cleanup if it simplifies `Acquire`

### Test Execution Checklist

#### Core lifecycle

- [ ] `TestAcquireSuccess`
- [ ] `TestAcquireAndRelease`
- [ ] `TestReleaseIdempotent`
- [ ] `TestReleaseNilReceiver`

#### Timeout behavior

- [ ] `TestAcquireTimeout`
- [ ] `TestAcquireTimeoutMessage`
- [ ] Assert returned error matches `ErrLockTimeout`
- [ ] Assert timeout error mentions path and holder PID
- [ ] Assert timing is within a reasonable margin, not an exact duration

#### Stale and corrupt lock handling

- [ ] `TestAcquireStaleLock`
- [ ] `TestAcquireInvalidLockFile`
- [ ] `TestAcquireEmptyLockFile`
- [ ] Verify stale lock file is removed before returning `ErrStaleLock`

#### Edge cases

- [ ] `TestAcquireReentrant`
- [ ] `TestAcquireZeroTimeout`
- [ ] `TestAcquireNoDirectory`
- [ ] `TestLockFileContent`
- [ ] `TestDeferPattern`

#### Concurrency

- [ ] `TestAcquireConcurrent`
- [ ] Coordinate two goroutines with channels
- [ ] Verify first goroutine acquires immediately
- [ ] Verify second goroutine blocks while first holds the lock
- [ ] Verify second goroutine acquires after first releases
- [ ] Verify only one lock holder exists at a time

### Verification

- [ ] `go test ./internal/lock/...` passes
- [ ] `make test` passes
- [ ] `make lint` passes
- [ ] Error messages follow the `fmt.Errorf("lock: ...")` convention
- [ ] Implementation stays stdlib-only
- [ ] Package is ready for reuse by E1.3 and E4.1 write paths

### Notes and Clarifications

- [ ] Use method form `func (l *Lock) Release() error` to match this feature spec and intended `defer lk.Release()` usage
- [ ] Treat this feature doc as authoritative over the older epic doc if they conflict on release shape
- [ ] Clarify that the required parent directory for `.git/opax.lock` is `.git`, not `.git/opax/`
- [ ] Do not add logging to the lock package; stale-lock visibility should come from the caller after receiving `ErrStaleLock`
