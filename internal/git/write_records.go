package git

import (
	"crypto/sha256"
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/oklog/ulid/v2"
)

// WriteRecord writes one record directory onto refs/heads/opax/v1.
func WriteRecord(ctx *RepoContext, req WriteRequest) (*WriteResult, error) {
	normalizedReq, err := normalizeWriteRequest(req)
	if err != nil {
		return nil, err
	}

	// Validate branch shape once before attempting record writes. Re-validating
	// inside publish retries would re-walk branch ancestry on every conflict.
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
	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	currentRef, err := backend.readRef(refName)
	if err != nil {
		return nil, err
	}
	if currentRef == nil {
		return nil, fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, plumbing.ErrReferenceNotFound)
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

	nextRef, err := buildRecordWriteReference(backend, currentRef, req)
	if err != nil {
		return nil, err
	}
	if err := publishReference(backend, nextRef, currentRef); err != nil {
		if errors.Is(err, errReferenceChanged) {
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
	publishedRef, err := publishRefWithRetry(ctx, refName, func(backend *nativeGitBackend, currentRef *plumbing.Reference) (*plumbing.Reference, error) {
		if currentRef == nil {
			return nil, fmt.Errorf("git: opax branch %s not found: %w", opaxBranchRef, plumbing.ErrReferenceNotFound)
		}

		nextRef, err := buildRecordWriteReference(backend, currentRef, req)
		if err != nil {
			return nil, err
		}
		return nextRef, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrRecordExists):
			return nil, err
		case errors.Is(err, errReferenceChanged):
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
	backend *nativeGitBackend,
	currentRef *plumbing.Reference,
	req *normalizedWriteRequest,
) (*plumbing.Reference, error) {
	currentCommit, err := backend.readCommit(currentRef.Hash())
	if err != nil {
		return nil, fmt.Errorf("git: branch %s tip %s is not a commit: %w", opaxBranchRef, currentRef.Hash(), err)
	}

	exists, err := pathExistsInTree(backend, currentCommit.TreeHash, req.RecordRoot)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("git: write record %q at %q: %w", req.RecordID, req.RecordRoot, ErrRecordExists)
	}

	recordTreeHash, err := writeRecordFilesTree(backend, req.Files)
	if err != nil {
		return nil, err
	}

	recordRootSegments := strings.Split(req.RecordRoot, "/")
	updatedRootTreeHash, err := upsertRecordTree(backend, currentCommit.TreeHash, recordRootSegments, recordTreeHash)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	commitHash, err := backend.writeCommit(gitCommitWriteRequest{
		TreeHash:       updatedRootTreeHash,
		ParentHashes:   []plumbing.Hash{currentRef.Hash()},
		Message:        fmt.Sprintf("opax: write %s %s", req.Collection, req.RecordID),
		AuthorName:     opaxAuthorName,
		AuthorEmail:    opaxAuthorEmail,
		CommitterName:  opaxAuthorName,
		CommitterEmail: opaxAuthorEmail,
		When:           now,
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
	for _, segment := range strings.Split(rawPath, "/") {
		if segment == ".." {
			return "", fmt.Errorf("git: record file path %q contains parent traversal", rawPath)
		}
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

func pathExistsInTree(backend *nativeGitBackend, rootTreeHash plumbing.Hash, targetPath string) (bool, error) {
	segments := strings.Split(targetPath, "/")
	if len(segments) == 0 {
		return false, fmt.Errorf("git: empty path lookup")
	}

	currentTreeHash := rootTreeHash
	for i, segment := range segments {
		entries, err := backend.readTree(currentTreeHash)
		if err != nil {
			return false, fmt.Errorf("git: read tree %s while checking %q: %w", currentTreeHash, targetPath, err)
		}

		entry, found := findTreeEntryByName(entries, segment)
		if !found {
			return false, nil
		}

		if i == len(segments)-1 {
			return true, nil
		}
		if !entryIsTree(entry) {
			componentPath := strings.Join(segments[:i+1], "/")
			return false, fmt.Errorf("git: path component %q for %q is not a directory", componentPath, targetPath)
		}
		currentTreeHash = entry.Hash
	}
	return false, nil
}

func writeRecordFilesTree(backend *nativeGitBackend, files []normalizedRecordFile) (plumbing.Hash, error) {
	root := &recordTreeNode{
		Dirs:  make(map[string]*recordTreeNode),
		Files: make(map[string]plumbing.Hash),
	}

	for _, file := range files {
		blobHash, err := backend.writeBlob(file.Content)
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

	return writeRecordTreeNode(backend, root)
}

func writeRecordTreeNode(backend *nativeGitBackend, node *recordTreeNode) (plumbing.Hash, error) {
	entries := make([]gitTreeEntry, 0, len(node.Dirs)+len(node.Files))
	for name, dirNode := range node.Dirs {
		hash, err := writeRecordTreeNode(backend, dirNode)
		if err != nil {
			return plumbing.ZeroHash, err
		}
		entries = append(entries, gitTreeEntry{
			Name: name,
			Mode: gitModeTree,
			Type: "tree",
			Hash: hash,
		})
	}
	for name, hash := range node.Files {
		entries = append(entries, gitTreeEntry{
			Name: name,
			Mode: gitModeBlob,
			Type: "blob",
			Hash: hash,
		})
	}

	return backend.writeTree(entries)
}

func upsertRecordTree(
	backend *nativeGitBackend,
	rootTreeHash plumbing.Hash,
	segments []string,
	recordTreeHash plumbing.Hash,
) (plumbing.Hash, error) {
	entries, err := backend.readTree(rootTreeHash)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: read root tree %s: %w", rootTreeHash, err)
	}

	return upsertRecordTreeRecursive(backend, entries, segments, recordTreeHash)
}

func upsertRecordTreeRecursive(
	backend *nativeGitBackend,
	entries []gitTreeEntry,
	segments []string,
	recordTreeHash plumbing.Hash,
) (plumbing.Hash, error) {
	if len(segments) == 0 {
		return plumbing.ZeroHash, fmt.Errorf("git: upsert record path: no path segments")
	}

	entryMap := make(map[string]gitTreeEntry, len(entries)+1)
	for _, entry := range entries {
		entryMap[entry.Name] = entry
	}

	segment := segments[0]
	if len(segments) == 1 {
		entryMap[segment] = gitTreeEntry{
			Name: segment,
			Mode: gitModeTree,
			Type: "tree",
			Hash: recordTreeHash,
		}
		return writeTreeFromMap(backend, entryMap)
	}

	childEntry, found := entryMap[segment]
	var childEntries []gitTreeEntry
	if found {
		if !entryIsTree(childEntry) {
			return plumbing.ZeroHash, fmt.Errorf("git: path component %q is not a directory", segment)
		}

		var err error
		childEntries, err = backend.readTree(childEntry.Hash)
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf("git: read tree for %q: %w", segment, err)
		}
	}

	updatedChildHash, err := upsertRecordTreeRecursive(backend, childEntries, segments[1:], recordTreeHash)
	if err != nil {
		return plumbing.ZeroHash, err
	}

	entryMap[segment] = gitTreeEntry{
		Name: segment,
		Mode: gitModeTree,
		Type: "tree",
		Hash: updatedChildHash,
	}
	return writeTreeFromMap(backend, entryMap)
}

func writeTreeFromMap(backend *nativeGitBackend, entries map[string]gitTreeEntry) (plumbing.Hash, error) {
	ordered := make([]gitTreeEntry, 0, len(entries))
	for _, entry := range entries {
		ordered = append(ordered, entry)
	}
	return backend.writeTree(ordered)
}

func findTreeEntryByName(entries []gitTreeEntry, name string) (gitTreeEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return gitTreeEntry{}, false
}

func entryIsTree(entry gitTreeEntry) bool {
	return entry.Type == "tree" && entry.Mode == gitModeTree
}
