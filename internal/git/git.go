// Package git provides plumbing-level git operations for the Opax data layer
// without touching the working tree. Production transport uses native Git
// commands behind typed helpers.
package git

import (
	"errors"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

var (
	// ErrNotGitRepo is returned when repo discovery cannot find a git repository.
	ErrNotGitRepo = errors.New("git: not a git repository")

	// ErrBareRepo is returned when discovery finds a bare repository, which
	// Phase 0 does not support.
	ErrBareRepo = errors.New("git: bare repositories are unsupported in Phase 0")

	// ErrTipChanged indicates the opax/v1 branch tip changed during an
	// optimistic write operation.
	ErrTipChanged = errors.New("git: opax branch tip changed")

	// ErrRecordExists indicates a record path already exists on opax/v1.
	ErrRecordExists = errors.New("git: record already exists")

	// ErrRecordNotFound indicates a deterministic record root is missing.
	ErrRecordNotFound = errors.New("git: record not found")

	// ErrFileNotFound indicates an exact opax branch path is missing.
	ErrFileNotFound = errors.New("git: file not found")

	// ErrMalformedTree indicates unexpected tree/blob layout while reading.
	ErrMalformedTree = errors.New("git: malformed opax tree state")

	// ErrNoteNotFound indicates a note is absent for a target commit/namespace.
	ErrNoteNotFound = errors.New("git: note not found")

	// ErrMalformedNote indicates unexpected tree/blob/payload layout in a notes ref.
	ErrMalformedNote = errors.New("git: malformed git note state")

	// ErrNoteConflict indicates the namespace ref changed during note publication.
	ErrNoteConflict = errors.New("git: note ref changed")

	// ErrRemoteNameInvalid indicates a remote name failed FEAT-0011 validation.
	ErrRemoteNameInvalid = errors.New("git: invalid remote name")

	// ErrRemoteMissing indicates a referenced remote does not exist.
	ErrRemoteMissing = errors.New("git: remote not found")

	// ErrDefaultSyncIsolationViolation indicates plain git push config contains
	// Opax refs and would violate default-sync isolation.
	ErrDefaultSyncIsolationViolation = errors.New("git: default-sync isolation violated")

	// ErrInvalidRefspecConfig indicates unsupported or malformed refspec config.
	ErrInvalidRefspecConfig = errors.New("git: invalid refspec config")

	errReferenceChanged    = errors.New("git: reference changed")
	errReferenceCASUnknown = errors.New("git: reference update outcome unknown")
)

const (
	opaxBranchRef       = "refs/heads/opax/v1"
	opaxBranchName      = "opax/v1"
	opaxNotesRefPrefix  = "refs/notes/opax/"
	opaxSentinelPath    = "meta/version.json"
	opaxSentinelCreator = "opax"
	opaxLayoutVersion   = 1
	opaxAuthorName      = "Opax"
	opaxAuthorEmail     = "opax@local"
	opaxInitMessage     = "opax: initialize opax/v1"
	opaxLockFilename    = "opax.lock"
	opaxBootstrapPoll   = 10 * time.Millisecond

	maxRefPublishAttempts = 8
	refPublishBackoffBase = 10 * time.Millisecond
	refPublishBackoffCap  = 100 * time.Millisecond
	noteFanoutPrefixLen   = 2
)

const (
	gitMinSupportedVersion = "2.30.0"
	gitModeBlob            = "100644"
	gitModeTree            = "040000"
)

type opaxBranchSentinel struct {
	Branch        string `json:"branch"`
	LayoutVersion int    `json:"layout_version"`
	CreatedBy     string `json:"created_by"`
}

type refPublishBuilder func(backend *nativeGitBackend, currentRef *plumbing.Reference) (*plumbing.Reference, error)

// RecordFile is one file written under a deterministic record root.
type RecordFile struct {
	Path    string
	Content []byte
}

// WriteRequest describes one append-only record write onto opax/v1.
type WriteRequest struct {
	Collection  string
	RecordID    string
	Files       []RecordFile
	ExpectedTip *plumbing.Hash
}

// WriteResult describes the published branch tip and record root.
type WriteResult struct {
	BranchTip  plumbing.Hash
	CommitHash plumbing.Hash
	RecordRoot string
}

// ReadResult describes a point-in-time record read from opax/v1.
type ReadResult struct {
	BranchTip  plumbing.Hash
	RecordRoot string
	Files      map[string][]byte
}

// RecordLocator identifies one record root discovered during WalkRecords.
type RecordLocator struct {
	BranchTip  plumbing.Hash
	Collection string
	RecordID   string
	RecordRoot string
}

type normalizedRecordFile struct {
	Path    string
	Content []byte
}

type normalizedWriteRequest struct {
	Collection  string
	RecordID    string
	Files       []normalizedRecordFile
	ExpectedTip *plumbing.Hash
	RecordRoot  string
}

type recordTreeNode struct {
	Dirs  map[string]*recordTreeNode
	Files map[string]plumbing.Hash
}

// RepoContext describes the resolved repository layout that downstream git
// plumbing code should use instead of inferring paths ad hoc.
type RepoContext struct {
	RepoRoot         string
	WorkTreeRoot     string
	GitDir           string
	CommonGitDir     string
	OpaxDir          string
	IsLinkedWorktree bool
}
