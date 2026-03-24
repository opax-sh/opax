// Package git provides plumbing-level git operations via go-git.
// It handles orphan branch management, notes, trailers, and ref operations
// for the Opax data layer without touching the working tree.
package git

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitstorage "github.com/go-git/go-git/v5/storage"
	fsstorage "github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/oklog/ulid/v2"
	"github.com/opax-sh/opax/internal/lock"
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
)

const (
	opaxBranchRef       = "refs/heads/opax/v1"
	opaxBranchName      = "opax/v1"
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
)

type opaxBranchSentinel struct {
	Branch        string `json:"branch"`
	LayoutVersion int    `json:"layout_version"`
	CreatedBy     string `json:"created_by"`
}

type refPublishBuilder func(repo *ggit.Repository, currentRef *plumbing.Reference) (*plumbing.Reference, error)

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

// DiscoverRepo resolves repository paths starting from startDir.
func DiscoverRepo(startDir string) (*RepoContext, error) {
	resolvedStart, err := normalizeStartDir(startDir)
	if err != nil {
		return nil, err
	}

	repo, err := openRepository(resolvedStart)
	if err != nil {
		return nil, err
	}

	workTreeRoot, gitDir, commonGitDir, isLinkedWorktree, err := buildRepoPaths(repo)
	if err != nil {
		return nil, err
	}

	return &RepoContext{
		RepoRoot:         workTreeRoot,
		WorkTreeRoot:     workTreeRoot,
		GitDir:           gitDir,
		CommonGitDir:     commonGitDir,
		OpaxDir:          filepath.Join(commonGitDir, "opax"),
		IsLinkedWorktree: isLinkedWorktree,
	}, nil
}

// EnsureOpaxDir creates CommonGitDir/opax if it does not already exist.
func EnsureOpaxDir(ctx *RepoContext) error {
	if ctx == nil {
		return fmt.Errorf("git: repo context is nil")
	}
	if ctx.CommonGitDir == "" {
		return fmt.Errorf("git: common git dir is empty")
	}
	if ctx.OpaxDir == "" {
		return fmt.Errorf("git: opax dir is empty")
	}
	if err := ensureExistingDir(ctx.CommonGitDir, "common git dir"); err != nil {
		return err
	}

	info, err := os.Stat(ctx.OpaxDir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("git: opax path is not a directory: %s", ctx.OpaxDir)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("git: stat opax dir %s: %w", ctx.OpaxDir, err)
	}

	if err := os.MkdirAll(ctx.OpaxDir, 0o755); err != nil {
		return fmt.Errorf("git: create opax dir %s: %w", ctx.OpaxDir, err)
	}
	return nil
}

// EnsureOpaxBranch creates refs/heads/opax/v1 if absent and validates it if
// present. It returns the current branch tip after creation or validation.
func EnsureOpaxBranch(ctx *RepoContext) (tip plumbing.Hash, err error) {
	lockPath, err := opaxLockPath(ctx)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	timeout := lock.DefaultTimeout
	deadline := time.Now().Add(timeout)

	for {
		repo, err := openRepoFromContext(ctx)
		if err != nil {
			return plumbing.ZeroHash, err
		}

		tip, _, err = resolveOpaxBranchTip(repo)
		if err == nil {
			if err := validateOpaxBranch(repo); err != nil {
				return plumbing.ZeroHash, err
			}
			return tip, nil
		}
		if !errors.Is(err, plumbing.ErrReferenceNotFound) {
			return plumbing.ZeroHash, err
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return plumbing.ZeroHash, fmt.Errorf(
				"git: timed out waiting for opax branch bootstrap after %s",
				timeout,
			)
		}

		branchLock, err := lock.Acquire(lockPath, remaining)
		if err != nil {
			switch {
			case errors.Is(err, lock.ErrAlreadyHeldByCurrentProcess):
				time.Sleep(opaxBootstrapPoll)
				continue
			case errors.Is(err, lock.ErrLockTimeout):
				return plumbing.ZeroHash, fmt.Errorf(
					"git: timed out waiting for opax branch bootstrap after %s",
					timeout,
				)
			default:
				return plumbing.ZeroHash, fmt.Errorf("git: acquire bootstrap lock %s: %w", lockPath, err)
			}
		}

		tip, err = ensureOpaxBranchWhileLocked(ctx)
		releaseErr := branchLock.Release()
		if err == nil && releaseErr != nil {
			err = fmt.Errorf("git: release bootstrap lock %s: %w", lockPath, releaseErr)
		}
		if err != nil {
			return plumbing.ZeroHash, err
		}
		return tip, nil
	}
}

func ensureOpaxBranchWhileLocked(ctx *RepoContext) (plumbing.Hash, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	tip, _, err := resolveOpaxBranchTip(repo)
	if err == nil {
		if err := validateOpaxBranch(repo); err != nil {
			return plumbing.ZeroHash, err
		}
		return tip, nil
	}
	if !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return plumbing.ZeroHash, err
	}

	tip, err = createOpaxBranch(repo)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if err := validateOpaxBranch(repo); err != nil {
		return plumbing.ZeroHash, err
	}

	return tip, nil
}

// GetOpaxBranchTip returns the current opax/v1 tip if the branch exists.
func GetOpaxBranchTip(ctx *RepoContext) (plumbing.Hash, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	tip, _, err := resolveOpaxBranchTip(repo)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return plumbing.ZeroHash, fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, err)
		}
		return plumbing.ZeroHash, err
	}
	return tip, nil
}

// ValidateOpaxBranch verifies that the branch identity and sentinel are
// correct.
func ValidateOpaxBranch(ctx *RepoContext) error {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return err
	}
	return validateOpaxBranch(repo)
}

// WriteRecord writes one record directory onto refs/heads/opax/v1.
func WriteRecord(ctx *RepoContext, req WriteRequest) (*WriteResult, error) {
	normalizedReq, err := normalizeWriteRequest(req)
	if err != nil {
		return nil, err
	}

	// Validate branch shape before attempting record writes.
	if err := ValidateOpaxBranch(ctx); err != nil {
		return nil, err
	}

	refName := plumbing.ReferenceName(opaxBranchRef)
	if normalizedReq.ExpectedTip != nil {
		return writeRecordWithExpectedTip(ctx, refName, normalizedReq)
	}
	return writeRecordWithRetry(ctx, refName, normalizedReq)
}

func writeRecordWithExpectedTip(
	ctx *RepoContext,
	refName plumbing.ReferenceName,
	req *normalizedWriteRequest,
) (*WriteResult, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if err := validateOpaxBranch(repo); err != nil {
		return nil, err
	}

	currentRef, err := repo.Reference(refName, true)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil, fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, err)
		}
		return nil, fmt.Errorf("git: read ref %s: %w", refName, err)
	}
	if currentRef.Hash() != *req.ExpectedTip {
		return nil, fmt.Errorf(
			"git: write record %q expected tip %s, found %s: %w",
			req.RecordID,
			*req.ExpectedTip,
			currentRef.Hash(),
			ErrTipChanged,
		)
	}

	nextRef, err := buildRecordWriteReference(repo, currentRef, req)
	if err != nil {
		return nil, err
	}
	if err := publishReference(repo, nextRef, currentRef); err != nil {
		if errors.Is(err, gitstorage.ErrReferenceHasChanged) {
			return nil, fmt.Errorf("git: write record %q: %w", req.RecordID, ErrTipChanged)
		}
		return nil, fmt.Errorf("git: publish ref %s: %w", refName, err)
	}

	return &WriteResult{
		BranchTip:  nextRef.Hash(),
		CommitHash: nextRef.Hash(),
		RecordRoot: req.RecordRoot,
	}, nil
}

func writeRecordWithRetry(
	ctx *RepoContext,
	refName plumbing.ReferenceName,
	req *normalizedWriteRequest,
) (*WriteResult, error) {
	publishedRef, err := publishRefWithRetry(ctx, refName, func(repo *ggit.Repository, currentRef *plumbing.Reference) (*plumbing.Reference, error) {
		if currentRef == nil {
			return nil, fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, plumbing.ErrReferenceNotFound)
		}
		if err := validateOpaxBranch(repo); err != nil {
			return nil, err
		}

		nextRef, err := buildRecordWriteReference(repo, currentRef, req)
		if err != nil {
			return nil, err
		}
		return nextRef, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrRecordExists):
			return nil, err
		case errors.Is(err, gitstorage.ErrReferenceHasChanged):
			return nil, fmt.Errorf("git: write record %q: %w", req.RecordID, ErrTipChanged)
		default:
			return nil, err
		}
	}

	return &WriteResult{
		BranchTip:  publishedRef.Hash(),
		CommitHash: publishedRef.Hash(),
		RecordRoot: req.RecordRoot,
	}, nil
}

func buildRecordWriteReference(
	repo *ggit.Repository,
	currentRef *plumbing.Reference,
	req *normalizedWriteRequest,
) (*plumbing.Reference, error) {
	currentCommit, err := repo.CommitObject(currentRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("git: branch %s tip %s is not a commit: %w", opaxBranchRef, currentRef.Hash(), err)
	}

	exists, err := pathExistsInTree(repo, currentCommit.TreeHash, req.RecordRoot)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("git: write record %q at %q: %w", req.RecordID, req.RecordRoot, ErrRecordExists)
	}

	recordTreeHash, err := writeRecordFilesTree(repo, req.Files)
	if err != nil {
		return nil, err
	}

	recordRootSegments := strings.Split(req.RecordRoot, "/")
	updatedRootTreeHash, err := upsertRecordTree(repo, currentCommit.TreeHash, recordRootSegments, recordTreeHash)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	commitHash, err := writeCommit(repo, &object.Commit{
		Author: object.Signature{
			Name:  opaxAuthorName,
			Email: opaxAuthorEmail,
			When:  now,
		},
		Committer: object.Signature{
			Name:  opaxAuthorName,
			Email: opaxAuthorEmail,
			When:  now,
		},
		Message:      fmt.Sprintf("opax: write %s %s", req.Collection, req.RecordID),
		TreeHash:     updatedRootTreeHash,
		ParentHashes: []plumbing.Hash{currentRef.Hash()},
	})
	if err != nil {
		return nil, err
	}

	return plumbing.NewHashReference(plumbing.ReferenceName(opaxBranchRef), commitHash), nil
}

func normalizeWriteRequest(req WriteRequest) (*normalizedWriteRequest, error) {
	if err := validateCollection(req.Collection); err != nil {
		return nil, err
	}
	if err := validateRecordID(req.Collection, req.RecordID); err != nil {
		return nil, err
	}
	if len(req.Files) == 0 {
		return nil, fmt.Errorf("git: write record %q: file set is empty", req.RecordID)
	}

	normalizedFiles := make([]normalizedRecordFile, 0, len(req.Files))
	seen := make(map[string]struct{}, len(req.Files))
	for _, file := range req.Files {
		cleanPath, err := normalizeRecordFilePath(file.Path)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[cleanPath]; exists {
			return nil, fmt.Errorf("git: duplicate record file path %q after normalization", cleanPath)
		}
		seen[cleanPath] = struct{}{}
		normalizedFiles = append(normalizedFiles, normalizedRecordFile{
			Path:    cleanPath,
			Content: file.Content,
		})
	}

	var expectedTip *plumbing.Hash
	if req.ExpectedTip != nil {
		copied := *req.ExpectedTip
		expectedTip = &copied
	}

	return &normalizedWriteRequest{
		Collection:  req.Collection,
		RecordID:    req.RecordID,
		Files:       normalizedFiles,
		ExpectedTip: expectedTip,
		RecordRoot:  deriveRecordRoot(req.Collection, req.RecordID),
	}, nil
}

func validateCollection(collection string) error {
	if collection == "sessions" || collection == "saves" {
		return nil
	}
	if strings.HasPrefix(collection, "ext-") {
		pluginName := strings.TrimPrefix(collection, "ext-")
		if err := validatePluginName(pluginName); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("git: invalid collection %q", collection)
}

func validatePluginName(pluginName string) error {
	if pluginName == "" {
		return fmt.Errorf("git: extension collection requires plugin name")
	}
	for _, ch := range pluginName {
		isLowerAlpha := ch >= 'a' && ch <= 'z'
		isDigit := ch >= '0' && ch <= '9'
		if !isLowerAlpha && !isDigit && ch != '-' {
			return fmt.Errorf("git: invalid extension plugin name %q", pluginName)
		}
	}
	return nil
}

func validateRecordID(collection, recordID string) error {
	if collection == "sessions" {
		return validateRecordIDWithPrefix(recordID, "ses_")
	}
	if collection == "saves" {
		return validateRecordIDWithPrefix(recordID, "sav_")
	}
	return validateExtensionRecordID(recordID)
}

func validateRecordIDWithPrefix(recordID, prefix string) error {
	if !strings.HasPrefix(recordID, prefix) {
		return fmt.Errorf("git: record ID %q must start with %q", recordID, prefix)
	}
	if err := validateULIDSuffix(recordID[len(prefix):], recordID); err != nil {
		return err
	}
	return nil
}

func validateExtensionRecordID(recordID string) error {
	separator := strings.IndexByte(recordID, '_')
	if separator <= 0 {
		return fmt.Errorf("git: extension record ID %q must use {prefix}_{ULID} format", recordID)
	}
	if separator != strings.LastIndexByte(recordID, '_') {
		return fmt.Errorf("git: extension record ID %q must contain exactly one underscore", recordID)
	}

	prefix := recordID[:separator]
	if len(prefix) < 2 || len(prefix) > 4 {
		return fmt.Errorf("git: extension record ID prefix %q must be 2-4 characters", prefix)
	}
	if prefix == "ses" || prefix == "sav" {
		return fmt.Errorf("git: extension record ID prefix %q collides with first-party prefixes", prefix)
	}
	for _, ch := range prefix {
		isLowerAlpha := ch >= 'a' && ch <= 'z'
		isDigit := ch >= '0' && ch <= '9'
		if !isLowerAlpha && !isDigit {
			return fmt.Errorf("git: extension record ID prefix %q must be lowercase alphanumeric", prefix)
		}
	}

	if err := validateULIDSuffix(recordID[separator+1:], recordID); err != nil {
		return err
	}
	return nil
}

func validateULIDSuffix(suffix, fullID string) error {
	if _, err := ulid.ParseStrict(suffix); err != nil {
		return fmt.Errorf("git: record ID %q has invalid ULID suffix: %w", fullID, err)
	}
	return nil
}

func normalizeRecordFilePath(rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("git: record file path is empty")
	}
	if strings.Contains(rawPath, "\\") {
		return "", fmt.Errorf("git: record file path %q must be slash-separated", rawPath)
	}
	if pathpkg.IsAbs(rawPath) {
		return "", fmt.Errorf("git: record file path %q must be relative", rawPath)
	}

	cleanPath := pathpkg.Clean(rawPath)
	if cleanPath == "." {
		return "", fmt.Errorf("git: record file path %q resolves to current directory", rawPath)
	}
	if cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return "", fmt.Errorf("git: record file path %q contains parent traversal", rawPath)
	}
	return cleanPath, nil
}

func deriveRecordRoot(collection, recordID string) string {
	hash := sha256.Sum256([]byte(recordID))
	shard := fmt.Sprintf("%x", hash[:])[:2]
	return pathpkg.Join(collection, shard, recordID)
}

func pathExistsInTree(repo *ggit.Repository, rootTreeHash plumbing.Hash, targetPath string) (bool, error) {
	segments := strings.Split(targetPath, "/")
	if len(segments) == 0 {
		return false, fmt.Errorf("git: empty path lookup")
	}

	currentTreeHash := rootTreeHash
	for i, segment := range segments {
		tree, err := repo.TreeObject(currentTreeHash)
		if err != nil {
			return false, fmt.Errorf("git: read tree %s while checking %q: %w", currentTreeHash, targetPath, err)
		}

		entry, found := findTreeEntryByName(tree.Entries, segment)
		if !found {
			return false, nil
		}

		if i == len(segments)-1 {
			return true, nil
		}
		if entry.Mode != filemode.Dir {
			componentPath := strings.Join(segments[:i+1], "/")
			return false, fmt.Errorf("git: path component %q for %q is not a directory", componentPath, targetPath)
		}
		currentTreeHash = entry.Hash
	}
	return false, nil
}

func writeRecordFilesTree(repo *ggit.Repository, files []normalizedRecordFile) (plumbing.Hash, error) {
	root := &recordTreeNode{
		Dirs:  make(map[string]*recordTreeNode),
		Files: make(map[string]plumbing.Hash),
	}

	for _, file := range files {
		blobHash, err := writeBlob(repo, file.Content)
		if err != nil {
			return plumbing.ZeroHash, err
		}

		parts := strings.Split(file.Path, "/")
		node := root
		for i, part := range parts {
			isLeaf := i == len(parts)-1
			if part == "" {
				return plumbing.ZeroHash, fmt.Errorf("git: record file path %q contains empty segment", file.Path)
			}

			if isLeaf {
				if _, exists := node.Dirs[part]; exists {
					return plumbing.ZeroHash, fmt.Errorf("git: record file path %q collides with directory", file.Path)
				}
				if _, exists := node.Files[part]; exists {
					return plumbing.ZeroHash, fmt.Errorf("git: duplicate record file path %q", file.Path)
				}
				node.Files[part] = blobHash
				continue
			}

			if _, exists := node.Files[part]; exists {
				return plumbing.ZeroHash, fmt.Errorf("git: record file path %q collides with existing file path", file.Path)
			}
			child := node.Dirs[part]
			if child == nil {
				child = &recordTreeNode{
					Dirs:  make(map[string]*recordTreeNode),
					Files: make(map[string]plumbing.Hash),
				}
				node.Dirs[part] = child
			}
			node = child
		}
	}

	return writeRecordTreeNode(repo, root)
}

func writeRecordTreeNode(repo *ggit.Repository, node *recordTreeNode) (plumbing.Hash, error) {
	entries := make([]object.TreeEntry, 0, len(node.Dirs)+len(node.Files))
	for name, dirNode := range node.Dirs {
		hash, err := writeRecordTreeNode(repo, dirNode)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, object.TreeEntry{
			Name: name,
			Mode: filemode.Dir,
			Hash: hash,
		})
	}
	for name, hash := range node.Files {
		entries = append(entries, object.TreeEntry{
			Name: name,
			Mode: filemode.Regular,
			Hash: hash,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	return writeTree(repo, entries)
}

func upsertRecordTree(
	repo *ggit.Repository,
	rootTreeHash plumbing.Hash,
	segments []string,
	recordTreeHash plumbing.Hash,
) (plumbing.Hash, error) {
	currentTree, err := repo.TreeObject(rootTreeHash)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: read root tree %s: %w", rootTreeHash, err)
	}

	return upsertRecordTreeRecursive(repo, currentTree, segments, recordTreeHash)
}

func upsertRecordTreeRecursive(
	repo *ggit.Repository,
	tree *object.Tree,
	segments []string,
	recordTreeHash plumbing.Hash,
) (plumbing.Hash, error) {
	if len(segments) == 0 {
		return plumbing.ZeroHash, fmt.Errorf("git: upsert record path: no path segments")
	}

	entries := make(map[string]object.TreeEntry, len(tree.Entries)+1)
	for _, entry := range tree.Entries {
		entries[entry.Name] = entry
	}

	segment := segments[0]
	if len(segments) == 1 {
		entries[segment] = object.TreeEntry{
			Name: segment,
			Mode: filemode.Dir,
			Hash: recordTreeHash,
		}
		return writeTree(repo, entriesFromMap(entries))
	}

	var childTree *object.Tree
	childEntry, found := entries[segment]
	if found {
		if childEntry.Mode != filemode.Dir {
			return plumbing.ZeroHash, fmt.Errorf("git: path component %q is not a directory", segment)
		}

		var err error
		childTree, err = repo.TreeObject(childEntry.Hash)
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf("git: read tree for %q: %w", segment, err)
		}
	} else {
		childTree = &object.Tree{}
	}

	updatedChildHash, err := upsertRecordTreeRecursive(repo, childTree, segments[1:], recordTreeHash)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	entries[segment] = object.TreeEntry{
		Name: segment,
		Mode: filemode.Dir,
		Hash: updatedChildHash,
	}
	return writeTree(repo, entriesFromMap(entries))
}

func entriesFromMap(entries map[string]object.TreeEntry) []object.TreeEntry {
	sortedNames := make([]string, 0, len(entries))
	for name := range entries {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	result := make([]object.TreeEntry, 0, len(sortedNames))
	for _, name := range sortedNames {
		result = append(result, entries[name])
	}
	return result
}

func findTreeEntryByName(entries []object.TreeEntry, name string) (object.TreeEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return object.TreeEntry{}, false
}

func normalizeStartDir(startDir string) (string, error) {
	if startDir == "" {
		startDir = "."
	}

	return normalizeExistingDir(startDir, "start dir")
}

func normalizeExistingDir(path, label string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("git: resolve %s %s: %w", label, path, err)
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("git: resolve symlinks for %s %s: %w", label, absPath, err)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("git: stat %s %s: %w", label, resolvedPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("git: %s is not a directory: %s", label, resolvedPath)
	}

	return filepath.Clean(resolvedPath), nil
}

func openRepository(startDir string) (*ggit.Repository, error) {
	repo, err := ggit.PlainOpenWithOptions(startDir, &ggit.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		if !errors.Is(err, ggit.ErrRepositoryNotExists) {
			return nil, fmt.Errorf("git: open repository %s: %w", startDir, err)
		}

		repo, bareErr := ggit.PlainOpen(startDir)
		if bareErr == nil {
			return repo, nil
		}
		if errors.Is(bareErr, ggit.ErrRepositoryNotExists) {
			return nil, ErrNotGitRepo
		}
		return nil, fmt.Errorf("git: open repository %s: %w", startDir, bareErr)
	}

	return repo, nil
}

func buildRepoPaths(repo *ggit.Repository) (string, string, string, bool, error) {
	worktree, err := repo.Worktree()
	if err != nil {
		if errors.Is(err, ggit.ErrIsBareRepository) {
			return "", "", "", false, ErrBareRepo
		}
		return "", "", "", false, fmt.Errorf("git: open worktree: %w", err)
	}

	workTreeRoot, err := normalizeExistingDir(worktree.Filesystem.Root(), "worktree root")
	if err != nil {
		return "", "", "", false, err
	}

	gitDir, err := gitDirFromRepository(repo)
	if err != nil {
		return "", "", "", false, err
	}

	commonGitDir, hasCommonDir, err := resolveCommonGitDir(gitDir)
	if err != nil {
		return "", "", "", false, err
	}

	isLinkedWorktree := hasCommonDir && filepath.Clean(commonGitDir) != filepath.Clean(gitDir)
	return workTreeRoot, gitDir, commonGitDir, isLinkedWorktree, nil
}

func gitDirFromRepository(repo *ggit.Repository) (string, error) {
	storage, ok := repo.Storer.(*fsstorage.Storage)
	if !ok {
		return "", fmt.Errorf("git: unexpected repository storage type %T", repo.Storer)
	}

	return normalizeExistingDir(storage.Filesystem().Root(), "git dir")
}

func resolveCommonGitDir(gitDir string) (string, bool, error) {
	commonDirPath := filepath.Join(gitDir, "commondir")
	data, err := os.ReadFile(commonDirPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return gitDir, false, nil
		}
		return "", false, fmt.Errorf("git: read commondir %s: %w", commonDirPath, err)
	}

	relPath := strings.TrimSpace(string(data))
	if relPath == "" {
		return "", false, fmt.Errorf("git: parse commondir %s: empty path", commonDirPath)
	}

	resolvedPath := relPath
	if !filepath.IsAbs(relPath) {
		resolvedPath = filepath.Join(gitDir, relPath)
	}
	resolvedPath = filepath.Clean(resolvedPath)
	if err := ensureExistingDir(resolvedPath, "common git dir"); err != nil {
		return "", false, err
	}
	resolvedPath, err = normalizeExistingDir(resolvedPath, "common git dir")
	if err != nil {
		return "", false, err
	}
	return resolvedPath, true, nil
}

func ensureExistingDir(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("git: %s does not exist: %s", label, path)
		}
		return fmt.Errorf("git: stat %s %s: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("git: %s is not a directory: %s", label, path)
	}
	return nil
}

func openRepoFromContext(ctx *RepoContext) (*ggit.Repository, error) {
	if ctx == nil {
		return nil, fmt.Errorf("git: repo context is nil")
	}
	if ctx.CommonGitDir == "" {
		return nil, fmt.Errorf("git: common git dir is empty")
	}

	storage := fsstorage.NewStorage(osfs.New(ctx.CommonGitDir), cache.NewObjectLRUDefault())
	repo, err := ggit.Open(storage, nil)
	if err != nil {
		return nil, fmt.Errorf("git: open repository from common git dir %s: %w", ctx.CommonGitDir, err)
	}
	return repo, nil
}

func opaxLockPath(ctx *RepoContext) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("git: repo context is nil")
	}
	if ctx.CommonGitDir == "" {
		return "", fmt.Errorf("git: common git dir is empty")
	}
	return filepath.Join(ctx.CommonGitDir, opaxLockFilename), nil
}

func resolveOpaxBranchTip(repo *ggit.Repository) (plumbing.Hash, *object.Commit, error) {
	refName := plumbing.ReferenceName(opaxBranchRef)
	ref, err := repo.Reference(refName, false)
	if err != nil {
		return plumbing.ZeroHash, nil, err
	}
	if ref.Type() == plumbing.SymbolicReference {
		return plumbing.ZeroHash, nil, fmt.Errorf(
			"git: opax branch %s is symbolic ref to %s",
			opaxBranchRef,
			ref.Target(),
		)
	}
	if ref.Type() != plumbing.HashReference {
		return plumbing.ZeroHash, nil, fmt.Errorf(
			"git: opax branch %s has unsupported reference type %v",
			opaxBranchRef,
			ref.Type(),
		)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf("git: opax branch %s does not point to a commit: %w", opaxBranchRef, err)
	}
	return ref.Hash(), commit, nil
}

func createOpaxBranch(repo *ggit.Repository) (plumbing.Hash, error) {
	sentinel := opaxBranchSentinel{
		Branch:        opaxBranchName,
		LayoutVersion: opaxLayoutVersion,
		CreatedBy:     opaxSentinelCreator,
	}
	data, err := json.MarshalIndent(sentinel, "", "  ")
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: encode %s: %w", opaxSentinelPath, err)
	}
	data = append(data, '\n')

	blobHash, err := writeBlob(repo, data)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	metaTreeHash, err := writeTree(repo, []object.TreeEntry{
		{Name: "version.json", Mode: filemode.Regular, Hash: blobHash},
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}
	rootTreeHash, err := writeTree(repo, []object.TreeEntry{
		{Name: "meta", Mode: filemode.Dir, Hash: metaTreeHash},
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}

	now := time.Now().UTC()
	commitHash, err := writeCommit(repo, &object.Commit{
		Author: object.Signature{
			Name:  opaxAuthorName,
			Email: opaxAuthorEmail,
			When:  now,
		},
		Committer: object.Signature{
			Name:  opaxAuthorName,
			Email: opaxAuthorEmail,
			When:  now,
		},
		Message:  opaxInitMessage,
		TreeHash: rootTreeHash,
	})
	if err != nil {
		return plumbing.ZeroHash, err
	}

	ref := plumbing.NewHashReference(plumbing.ReferenceName(opaxBranchRef), commitHash)
	if err := repo.Storer.CheckAndSetReference(ref, nil); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: set ref %s: %w", opaxBranchRef, err)
	}

	return commitHash, nil
}

func validateOpaxBranch(repo *ggit.Repository) error {
	tipHash, tipCommit, err := resolveOpaxBranchTip(repo)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, err)
		}
		return err
	}

	tipSentinel, err := readOpaxSentinel(tipCommit)
	if err != nil {
		return fmt.Errorf("git: validate opax branch tip %s: %w", tipHash, err)
	}
	if err := validateOpaxSentinel(tipSentinel); err != nil {
		return fmt.Errorf("git: validate opax branch tip %s: %w", tipHash, err)
	}

	rootCommit, err := findLinearRootCommit(tipCommit)
	if err != nil {
		return err
	}

	rootSentinel, err := readOpaxSentinel(rootCommit)
	if err != nil {
		return fmt.Errorf("git: validate opax branch root %s: %w", rootCommit.Hash, err)
	}
	if err := validateOpaxSentinel(rootSentinel); err != nil {
		return fmt.Errorf("git: validate opax branch root %s: %w", rootCommit.Hash, err)
	}

	return nil
}

func findLinearRootCommit(commit *object.Commit) (*object.Commit, error) {
	current := commit
	for {
		switch current.NumParents() {
		case 0:
			return current, nil
		case 1:
			parent, err := current.Parent(0)
			if err != nil {
				return nil, fmt.Errorf("git: resolve parent for commit %s: %w", current.Hash, err)
			}
			current = parent
		default:
			return nil, fmt.Errorf(
				"git: opax branch %s has non-linear ancestry at commit %s (%d parents)",
				opaxBranchRef,
				current.Hash,
				current.NumParents(),
			)
		}
	}
}

func readOpaxSentinel(commit *object.Commit) (*opaxBranchSentinel, error) {
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("git: read tree for commit %s: %w", commit.Hash, err)
	}

	file, err := tree.File(opaxSentinelPath)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return nil, fmt.Errorf("git: sentinel file missing: %s", opaxSentinelPath)
		}
		return nil, fmt.Errorf("git: read sentinel file %s: %w", opaxSentinelPath, err)
	}

	content, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("git: read sentinel file %s contents: %w", opaxSentinelPath, err)
	}

	var sentinel opaxBranchSentinel
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sentinel); err != nil {
		return nil, fmt.Errorf("git: parse sentinel %s: %w", opaxSentinelPath, err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return &sentinel, nil
		}
		return nil, fmt.Errorf("git: parse sentinel %s trailing data: %w", opaxSentinelPath, err)
	}
	return nil, fmt.Errorf("git: parse sentinel %s trailing data", opaxSentinelPath)
}

func validateOpaxSentinel(sentinel *opaxBranchSentinel) error {
	if sentinel.Branch != opaxBranchName {
		return fmt.Errorf("git: sentinel branch = %q, want %q", sentinel.Branch, opaxBranchName)
	}
	if sentinel.LayoutVersion != opaxLayoutVersion {
		return fmt.Errorf("git: sentinel layout_version = %d, want %d", sentinel.LayoutVersion, opaxLayoutVersion)
	}
	if sentinel.CreatedBy != opaxSentinelCreator {
		return fmt.Errorf("git: sentinel created_by = %q, want %q", sentinel.CreatedBy, opaxSentinelCreator)
	}
	return nil
}

func writeBlob(repo *ggit.Repository, data []byte) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)

	writer, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: open blob writer: %w", err)
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return plumbing.ZeroHash, fmt.Errorf("git: write blob: %w", err)
	}
	if err := writer.Close(); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: close blob writer: %w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: store blob: %w", err)
	}
	return hash, nil
}

func writeTree(repo *ggit.Repository, entries []object.TreeEntry) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := (&object.Tree{Entries: entries}).Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: encode tree: %w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: store tree: %w", err)
	}
	return hash, nil
}

func writeCommit(repo *ggit.Repository, commit *object.Commit) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := commit.Encode(obj); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: encode commit: %w", err)
	}

	hash, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: store commit: %w", err)
	}
	return hash, nil
}

func publishRefWithRetry(
	ctx *RepoContext,
	refName plumbing.ReferenceName,
	build refPublishBuilder,
) (*plumbing.Reference, error) {
	if build == nil {
		return nil, fmt.Errorf("git: publish ref %s: builder is nil", refName)
	}

	var lastErr error
	for attempt := 1; attempt <= maxRefPublishAttempts; attempt++ {
		repo, err := openRepoFromContext(ctx)
		if err != nil {
			return nil, err
		}

		currentRef, err := repo.Reference(refName, true)
		if err != nil {
			if errors.Is(err, plumbing.ErrReferenceNotFound) {
				currentRef = nil
			} else {
				return nil, fmt.Errorf("git: read ref %s: %w", refName, err)
			}
		}

		nextRef, err := build(repo, currentRef)
		if err != nil {
			return nil, err
		}
		if nextRef == nil {
			return nil, fmt.Errorf("git: publish ref %s: builder returned nil reference", refName)
		}
		if nextRef.Name() != refName {
			return nil, fmt.Errorf("git: publish ref %s: builder returned %s", refName, nextRef.Name())
		}

		if err := publishReference(repo, nextRef, currentRef); err == nil {
			return nextRef, nil
		} else if errors.Is(err, gitstorage.ErrReferenceHasChanged) {
			lastErr = err
		} else {
			return nil, fmt.Errorf("git: publish ref %s: %w", refName, err)
		}

		if attempt == maxRefPublishAttempts {
			break
		}
		time.Sleep(refPublishBackoff(attempt))
	}

	return nil, fmt.Errorf(
		"git: publish ref %s: retries exhausted after %d attempts: %w",
		refName,
		maxRefPublishAttempts,
		lastErr,
	)
}

func publishReference(repo *ggit.Repository, nextRef, currentRef *plumbing.Reference) error {
	if currentRef != nil {
		return repo.Storer.CheckAndSetReference(nextRef, currentRef)
	}
	return createReferenceIfAbsent(repo, nextRef)
}

func createReferenceIfAbsent(repo *ggit.Repository, ref *plumbing.Reference) error {
	storage, ok := repo.Storer.(*fsstorage.Storage)
	if !ok {
		return fmt.Errorf("git: publish ref %s: unexpected repository storage type %T", ref.Name(), repo.Storer)
	}

	content, err := refContent(ref)
	if err != nil {
		return err
	}

	refPath := filepath.Join(storage.Filesystem().Root(), filepath.FromSlash(ref.Name().String()))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		return fmt.Errorf("git: publish ref %s: create parent directory: %w", ref.Name(), err)
	}

	refFile, err := os.OpenFile(refPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return gitstorage.ErrReferenceHasChanged
		}
		return fmt.Errorf("git: publish ref %s: create ref file: %w", ref.Name(), err)
	}

	if _, err := refFile.WriteString(content); err != nil {
		_ = refFile.Close()
		_ = os.Remove(refPath)
		return fmt.Errorf("git: publish ref %s: write ref file: %w", ref.Name(), err)
	}
	if err := refFile.Close(); err != nil {
		return fmt.Errorf("git: publish ref %s: close ref file: %w", ref.Name(), err)
	}

	return nil
}

func refContent(ref *plumbing.Reference) (string, error) {
	switch ref.Type() {
	case plumbing.HashReference:
		return fmt.Sprintf("%s\n", ref.Hash()), nil
	case plumbing.SymbolicReference:
		return fmt.Sprintf("ref: %s\n", ref.Target()), nil
	default:
		return "", fmt.Errorf("git: publish ref %s: unsupported reference type %s", ref.Name(), ref.Type())
	}
}

func refPublishBackoff(attempt int) time.Duration {
	if attempt < 1 {
		return refPublishBackoffBase
	}

	delay := refPublishBackoffBase << (attempt - 1)
	if delay > refPublishBackoffCap {
		return refPublishBackoffCap
	}
	return delay
}
