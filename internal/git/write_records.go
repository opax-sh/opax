package git

import (
	"crypto/sha256"
	"errors"
	"fmt"
	pathpkg "path"
	"sort"
	"strings"
	"time"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitstorage "github.com/go-git/go-git/v5/storage"
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
	repo, err := openRepoFromContext(ctx)
	if err != nil {
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
	sortTreeEntries(entries)

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
	result := make([]object.TreeEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sortTreeEntries(result)
	return result
}

func sortTreeEntries(entries []object.TreeEntry) {
	sort.Sort(object.TreeEntrySorter(entries))
}

func findTreeEntryByName(entries []object.TreeEntry, name string) (object.TreeEntry, bool) {
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return object.TreeEntry{}, false
}
