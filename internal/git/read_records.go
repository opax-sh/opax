package git

import (
	"fmt"
	"io"
	pathpkg "path"
	"strings"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ReadRecord reads every file under one deterministic record root.
func ReadRecord(ctx *RepoContext, collection, recordID string) (*ReadResult, error) {
	if err := validateCollection(collection); err != nil {
		return nil, err
	}
	if err := validateRecordID(collection, recordID); err != nil {
		return nil, err
	}

	repo, branchTip, rootTree, err := resolveReadSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	recordRoot := deriveRecordRoot(collection, recordID)
	recordTree, err := resolveRecordRootTree(repo, rootTree, recordRoot)
	if err != nil {
		return nil, err
	}

	files := make(map[string][]byte)
	if err := collectRecordFiles(repo, recordTree, "", files, recordRoot); err != nil {
		return nil, err
	}

	return &ReadResult{
		BranchTip:  branchTip,
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

	repo, _, rootTree, err := resolveReadSnapshot(ctx)
	if err != nil {
		return nil, err
	}

	currentTree := rootTree
	segments := strings.Split(cleanPath, "/")
	for i, segment := range segments {
		entry, found := findTreeEntryByName(currentTree.Entries, segment)
		if !found {
			return nil, fmt.Errorf("git: read file %q: %w", cleanPath, ErrFileNotFound)
		}

		isLeaf := i == len(segments)-1
		if isLeaf {
			if entry.Mode == filemode.Dir {
				return nil, fmt.Errorf("git: read file %q resolves to directory: %w", cleanPath, ErrMalformedTree)
			}
			content, err := readBlobContent(repo, entry.Hash)
			if err != nil {
				return nil, fmt.Errorf("git: read file %q blob %s: %v: %w", cleanPath, entry.Hash, err, ErrMalformedTree)
			}
			return content, nil
		}

		if entry.Mode != filemode.Dir {
			componentPath := strings.Join(segments[:i+1], "/")
			return nil, fmt.Errorf("git: read file %q expected tree at %q: %w", cleanPath, componentPath, ErrMalformedTree)
		}

		nextTree, err := repo.TreeObject(entry.Hash)
		if err != nil {
			componentPath := strings.Join(segments[:i+1], "/")
			return nil, fmt.Errorf("git: read file %q load tree %q (%s): %v: %w", cleanPath, componentPath, entry.Hash, err, ErrMalformedTree)
		}
		currentTree = nextTree
	}

	return nil, fmt.Errorf("git: read file %q: %w", cleanPath, ErrFileNotFound)
}

// WalkRecords enumerates all record roots under opax/v1.
func WalkRecords(ctx *RepoContext, visit func(locator RecordLocator) error) error {
	if visit == nil {
		return fmt.Errorf("git: walk records visitor is nil")
	}

	repo, branchTip, rootTree, err := resolveReadSnapshot(ctx)
	if err != nil {
		return err
	}

	for _, collectionEntry := range rootTree.Entries {
		collection := collectionEntry.Name
		if collection == "meta" {
			continue
		}

		if collectionEntry.Mode != filemode.Dir {
			return fmt.Errorf("git: walk records collection %q is not a tree: %w", collection, ErrMalformedTree)
		}
		if err := validateCollection(collection); err != nil {
			return fmt.Errorf("git: walk records invalid collection %q: %w", collection, ErrMalformedTree)
		}

		collectionTree, err := repo.TreeObject(collectionEntry.Hash)
		if err != nil {
			return fmt.Errorf(
				"git: walk records load collection tree %q (%s): %v: %w",
				collection,
				collectionEntry.Hash,
				err,
				ErrMalformedTree,
			)
		}

		if err := walkCollectionRecords(repo, branchTip, collection, collectionTree, visit); err != nil {
			return err
		}
	}

	return nil
}

func walkCollectionRecords(
	repo *ggit.Repository,
	branchTip plumbing.Hash,
	collection string,
	collectionTree *object.Tree,
	visit func(locator RecordLocator) error,
) error {
	for _, shardEntry := range collectionTree.Entries {
		shard := shardEntry.Name
		if shardEntry.Mode != filemode.Dir {
			return fmt.Errorf("git: walk records shard %q/%q is not a tree: %w", collection, shard, ErrMalformedTree)
		}
		if !isRecordShard(shard) {
			return fmt.Errorf("git: walk records shard %q/%q is invalid: %w", collection, shard, ErrMalformedTree)
		}

		shardTree, err := repo.TreeObject(shardEntry.Hash)
		if err != nil {
			return fmt.Errorf(
				"git: walk records load shard tree %q/%q (%s): %v: %w",
				collection,
				shard,
				shardEntry.Hash,
				err,
				ErrMalformedTree,
			)
		}

		for _, recordEntry := range shardTree.Entries {
			recordID := recordEntry.Name
			if recordEntry.Mode != filemode.Dir {
				return fmt.Errorf(
					"git: walk records record root %q/%q/%q is not a tree: %w",
					collection,
					shard,
					recordID,
					ErrMalformedTree,
				)
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

			if _, err := repo.TreeObject(recordEntry.Hash); err != nil {
				return fmt.Errorf(
					"git: walk records load record tree %q (%s): %v: %w",
					recordRoot,
					recordEntry.Hash,
					err,
					ErrMalformedTree,
				)
			}

			if err := visit(RecordLocator{
				BranchTip:  branchTip,
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

func resolveReadSnapshot(ctx *RepoContext) (*ggit.Repository, plumbing.Hash, *object.Tree, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, plumbing.ZeroHash, nil, err
	}

	branchTip, tipCommit, err := resolveValidatedOpaxBranchTip(repo)
	if err != nil {
		return nil, plumbing.ZeroHash, nil, err
	}

	rootTree, err := tipCommit.Tree()
	if err != nil {
		return nil, plumbing.ZeroHash, nil, fmt.Errorf("git: read tree for opax branch tip %s: %w", branchTip, err)
	}

	return repo, branchTip, rootTree, nil
}

func resolveRecordRootTree(repo *ggit.Repository, rootTree *object.Tree, recordRoot string) (*object.Tree, error) {
	segments := strings.Split(recordRoot, "/")
	currentTree := rootTree
	for i, segment := range segments {
		entry, found := findTreeEntryByName(currentTree.Entries, segment)
		if !found {
			return nil, fmt.Errorf("git: read record root %q missing %q: %w", recordRoot, strings.Join(segments[:i+1], "/"), ErrRecordNotFound)
		}
		if entry.Mode != filemode.Dir {
			return nil, fmt.Errorf("git: read record root %q expected tree at %q: %w", recordRoot, strings.Join(segments[:i+1], "/"), ErrMalformedTree)
		}

		nextTree, err := repo.TreeObject(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf(
				"git: read record root %q load tree %q (%s): %v: %w",
				recordRoot,
				strings.Join(segments[:i+1], "/"),
				entry.Hash,
				err,
				ErrMalformedTree,
			)
		}
		currentTree = nextTree
	}

	return currentTree, nil
}

func collectRecordFiles(
	repo *ggit.Repository,
	tree *object.Tree,
	prefix string,
	files map[string][]byte,
	recordRoot string,
) error {
	for _, entry := range tree.Entries {
		entryPath := entry.Name
		if prefix != "" {
			entryPath = pathpkg.Join(prefix, entryPath)
		}

		if entry.Mode == filemode.Dir {
			childTree, err := repo.TreeObject(entry.Hash)
			if err != nil {
				return fmt.Errorf(
					"git: read record root %q load subtree %q (%s): %v: %w",
					recordRoot,
					entryPath,
					entry.Hash,
					err,
					ErrMalformedTree,
				)
			}
			if err := collectRecordFiles(repo, childTree, entryPath, files, recordRoot); err != nil {
				return err
			}
			continue
		}

		content, err := readBlobContent(repo, entry.Hash)
		if err != nil {
			return fmt.Errorf(
				"git: read record root %q load blob %q (%s): %v: %w",
				recordRoot,
				entryPath,
				entry.Hash,
				err,
				ErrMalformedTree,
			)
		}
		files[entryPath] = content
	}

	return nil
}

func readBlobContent(repo *ggit.Repository, hash plumbing.Hash) ([]byte, error) {
	blob, err := repo.BlobObject(hash)
	if err != nil {
		return nil, err
	}

	reader, err := blob.Reader()
	if err != nil {
		return nil, err
	}

	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}

	return content, nil
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
