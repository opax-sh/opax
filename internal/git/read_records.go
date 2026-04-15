package git

import (
	"fmt"
	pathpkg "path"
	"strings"
)

// ReadRecord reads every file under one deterministic record root.
func ReadRecord(ctx *RepoContext, collection, recordID string) (*ReadResult, error) {
	if err := validateCollection(collection); err != nil {
		return nil, err
	}
	if err := validateRecordID(collection, recordID); err != nil {
		return nil, err
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	branchTip, rootTreeHash, err := resolveReadSnapshot(backend)
	if err != nil {
		return nil, err
	}

	recordRoot := deriveRecordRoot(collection, recordID)
	recordTreeHash, err := resolveRecordRootTree(backend, rootTreeHash, recordRoot)
	if err != nil {
		return nil, err
	}

	blobByPath := make(map[string]gitHash)
	if err := collectRecordBlobHashes(backend, recordTreeHash, "", blobByPath, recordRoot); err != nil {
		return nil, err
	}

	hashes := make([]gitHash, 0, len(blobByPath))
	for _, hash := range blobByPath {
		hashes = append(hashes, hash)
	}
	blobContents, err := backend.readBlobsBatch(hashes)
	if err != nil {
		return nil, fmt.Errorf("git: read record root %q batch blob read: %v: %w", recordRoot, err, ErrMalformedTree)
	}

	files := make(map[string][]byte, len(blobByPath))
	for path, hash := range blobByPath {
		content, found := blobContents[hash]
		if !found {
			return nil, fmt.Errorf("git: read record root %q missing blob %s: %w", recordRoot, hash, ErrMalformedTree)
		}
		files[path] = content
	}

	return &ReadResult{
		BranchTip:  branchTip.String(),
		RecordRoot: recordRoot,
		Files:      files,
	}, nil
}

// ReadFileAtPath reads one blob from the current opax/v1 tip snapshot.
func ReadFileAtPath(ctx *RepoContext, path string) ([]byte, error) {
	cleanPath, err := normalizeBranchReadPath(path)
	if err != nil {
		return nil, err
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	_, rootTreeHash, err := resolveReadSnapshot(backend)
	if err != nil {
		return nil, err
	}

	currentTreeHash := rootTreeHash
	segments := strings.Split(cleanPath, "/")
	for i, segment := range segments {
		entries, err := backend.readTree(currentTreeHash)
		if err != nil {
			componentPath := strings.Join(segments[:i], "/")
			if componentPath == "" {
				componentPath = "."
			}
			return nil, fmt.Errorf("git: read file %q load tree %q (%s): %v: %w", cleanPath, componentPath, currentTreeHash, err, ErrMalformedTree)
		}
		entry, found := findTreeEntryByName(entries, segment)
		if !found {
			return nil, fmt.Errorf("git: read file %q: %w", cleanPath, ErrFileNotFound)
		}

		isLeaf := i == len(segments)-1
		if isLeaf {
			if entryIsTree(entry) {
				return nil, fmt.Errorf("git: read file %q resolves to directory: %w", cleanPath, ErrMalformedTree)
			}
			if !entryIsBlob(entry) {
				return nil, fmt.Errorf("git: read file %q expected blob at %q: %w", cleanPath, cleanPath, ErrMalformedTree)
			}
			content, err := backend.readBlob(entry.Hash)
			if err != nil {
				return nil, fmt.Errorf("git: read file %q blob %s: %v: %w", cleanPath, entry.Hash, err, ErrMalformedTree)
			}
			return content, nil
		}

		if !entryIsTree(entry) {
			componentPath := strings.Join(segments[:i+1], "/")
			return nil, fmt.Errorf("git: read file %q expected tree at %q: %w", cleanPath, componentPath, ErrMalformedTree)
		}
		currentTreeHash = entry.Hash
	}

	return nil, fmt.Errorf("git: read file %q: %w", cleanPath, ErrFileNotFound)
}

// WalkRecords enumerates all record roots under opax/v1.
func WalkRecords(ctx *RepoContext, visit func(locator RecordLocator) error) error {
	if visit == nil {
		return fmt.Errorf("git: walk records visitor is nil")
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return err
	}

	branchTip, rootTreeHash, err := resolveReadSnapshot(backend)
	if err != nil {
		return err
	}

	rootEntries, err := backend.readTree(rootTreeHash)
	if err != nil {
		return fmt.Errorf("git: walk records load root tree %s: %v: %w", rootTreeHash, err, ErrMalformedTree)
	}

	for _, collectionEntry := range rootEntries {
		collection := collectionEntry.Name
		if collection == "meta" {
			continue
		}

		if !entryIsTree(collectionEntry) {
			return fmt.Errorf("git: walk records collection %q is not a tree: %w", collection, ErrMalformedTree)
		}
		if err := validateCollection(collection); err != nil {
			return fmt.Errorf("git: walk records invalid collection %q: %w", collection, ErrMalformedTree)
		}

		if err := walkCollectionRecords(backend, branchTip, collection, collectionEntry.Hash, visit); err != nil {
			return err
		}
	}

	return nil
}

func walkCollectionRecords(
	backend *nativeGitBackend,
	branchTip gitHash,
	collection string,
	collectionTreeHash gitHash,
	visit func(locator RecordLocator) error,
) error {
	entries, err := backend.readTreeRecursive(collectionTreeHash)
	if err != nil {
		return fmt.Errorf(
			"git: walk records load collection tree %q (%s): %v: %w",
			collection,
			collectionTreeHash,
			err,
			ErrMalformedTree,
		)
	}

	seenRecordRoots := make(map[string]struct{})
	for _, entry := range entries {
		parts := strings.Split(entry.Path, "/")
		switch len(parts) {
		case 1:
			shard := parts[0]
			if !entryIsTree(entry.gitTreeEntry) {
				return fmt.Errorf("git: walk records shard %q/%q is not a tree: %w", collection, shard, ErrMalformedTree)
			}
			if !isRecordShard(shard) {
				return fmt.Errorf("git: walk records shard %q/%q is invalid: %w", collection, shard, ErrMalformedTree)
			}
		case 2:
			shard := parts[0]
			recordID := parts[1]
			if !entryIsTree(entry.gitTreeEntry) {
				return fmt.Errorf(
					"git: walk records record root %q/%q/%q is not a tree: %w",
					collection,
					shard,
					recordID,
					ErrMalformedTree,
				)
			}
			if !isRecordShard(shard) {
				return fmt.Errorf("git: walk records shard %q/%q is invalid: %w", collection, shard, ErrMalformedTree)
			}
			if err := validateRecordID(collection, recordID); err != nil {
				return fmt.Errorf("git: walk records invalid record ID %q/%q/%q: %w", collection, shard, recordID, ErrMalformedTree)
			}

			recordRoot := pathpkg.Join(collection, shard, recordID)
			if deriveRecordRoot(collection, recordID) != recordRoot {
				return fmt.Errorf(
					"git: walk records record root %q does not match deterministic shard: %w",
					recordRoot,
					ErrMalformedTree,
				)
			}
			if _, exists := seenRecordRoots[recordRoot]; exists {
				return fmt.Errorf("git: walk records duplicate record root %q: %w", recordRoot, ErrMalformedTree)
			}
			seenRecordRoots[recordRoot] = struct{}{}

			if err := visit(RecordLocator{
				BranchTip:  branchTip.String(),
				Collection: collection,
				RecordID:   recordID,
				RecordRoot: recordRoot,
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

func resolveReadSnapshot(backend *nativeGitBackend) (gitHash, gitHash, error) {
	branchTip, tipCommit, err := resolveValidatedOpaxBranchTip(backend)
	if err != nil {
		return "", "", err
	}
	return branchTip, tipCommit.TreeHash, nil
}

func resolveRecordRootTree(backend *nativeGitBackend, rootTreeHash gitHash, recordRoot string) (gitHash, error) {
	segments := strings.Split(recordRoot, "/")
	currentTreeHash := rootTreeHash
	for i, segment := range segments {
		entries, err := backend.readTree(currentTreeHash)
		if err != nil {
			return "", fmt.Errorf(
				"git: read record root %q load tree %q (%s): %v: %w",
				recordRoot,
				strings.Join(segments[:i], "/"),
				currentTreeHash,
				err,
				ErrMalformedTree,
			)
		}

		entry, found := findTreeEntryByName(entries, segment)
		if !found {
			return "", fmt.Errorf("git: read record root %q missing %q: %w", recordRoot, strings.Join(segments[:i+1], "/"), ErrRecordNotFound)
		}
		if !entryIsTree(entry) {
			return "", fmt.Errorf("git: read record root %q expected tree at %q: %w", recordRoot, strings.Join(segments[:i+1], "/"), ErrMalformedTree)
		}

		currentTreeHash = entry.Hash
	}

	return currentTreeHash, nil
}

func collectRecordBlobHashes(
	backend *nativeGitBackend,
	treeHash gitHash,
	prefix string,
	files map[string]gitHash,
	recordRoot string,
) error {
	entries, err := backend.readTree(treeHash)
	if err != nil {
		return fmt.Errorf(
			"git: read record root %q load subtree %q (%s): %v: %w",
			recordRoot,
			prefix,
			treeHash,
			err,
			ErrMalformedTree,
		)
	}

	for _, entry := range entries {
		entryPath := entry.Name
		if prefix != "" {
			entryPath = pathpkg.Join(prefix, entryPath)
		}

		if entryIsTree(entry) {
			if err := collectRecordBlobHashes(backend, entry.Hash, entryPath, files, recordRoot); err != nil {
				return err
			}
			continue
		}
		if !entryIsBlob(entry) {
			return fmt.Errorf("git: read record root %q expected blob at %q (%s): %w", recordRoot, entryPath, entry.Hash, ErrMalformedTree)
		}
		files[entryPath] = entry.Hash
	}

	return nil
}

func normalizeBranchReadPath(rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("git: file path is empty")
	}
	if strings.Contains(rawPath, "\\") {
		return "", fmt.Errorf("git: file path %q must be slash-separated", rawPath)
	}
	if pathpkg.IsAbs(rawPath) {
		return "", fmt.Errorf("git: file path %q must be relative", rawPath)
	}
	for _, segment := range strings.Split(rawPath, "/") {
		if segment == ".." {
			return "", fmt.Errorf("git: file path %q contains parent traversal", rawPath)
		}
	}

	cleanPath := pathpkg.Clean(rawPath)
	if cleanPath == "." {
		return "", fmt.Errorf("git: file path %q resolves to current directory", rawPath)
	}
	if cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return "", fmt.Errorf("git: file path %q contains parent traversal", rawPath)
	}

	return cleanPath, nil
}

func isRecordShard(shard string) bool {
	if len(shard) != 2 {
		return false
	}
	return isLowerHex(shard[0]) && isLowerHex(shard[1])
}

func isLowerHex(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')
}

func entryIsBlob(entry gitTreeEntry) bool {
	return entry.Type == "blob"
}
