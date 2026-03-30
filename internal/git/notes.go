package git

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	pathpkg "path"
	"slices"
	"strings"
	"time"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitstorage "github.com/go-git/go-git/v5/storage"
	"github.com/opax-sh/opax/internal/types"
)

type normalizedNote struct {
	Namespace   string
	CommitHash  plumbing.Hash
	Version     int
	Content     json.RawMessage
	StoredBytes []byte
}

type notePayload struct {
	Version int
	Content json.RawMessage
}

// WriteNote writes one JSON note under refs/notes/opax/{namespace}.
func WriteNote(ctx *RepoContext, note types.Note) error {
	normalized, err := normalizeNote(note)
	if err != nil {
		return err
	}

	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return err
	}
	if _, err := repo.CommitObject(normalized.CommitHash); err != nil {
		return fmt.Errorf(
			"git: write note namespace %q commit %s: resolve target commit: %w",
			normalized.Namespace,
			normalized.CommitHash,
			err,
		)
	}

	refName := noteRefName(normalized.Namespace)
	if _, err := publishRefWithRetry(ctx, refName, func(repo *ggit.Repository, currentRef *plumbing.Reference) (*plumbing.Reference, error) {
		nextRef, err := buildNoteWriteReference(repo, currentRef, normalized)
		if err != nil {
			return nil, err
		}
		return nextRef, nil
	}); err != nil {
		if errors.Is(err, gitstorage.ErrReferenceHasChanged) {
			return fmt.Errorf(
				"git: write note namespace %q commit %s: %w",
				normalized.Namespace,
				normalized.CommitHash,
				ErrNoteConflict,
			)
		}
		return err
	}

	return nil
}

// ReadNote returns one note by namespace and target commit hash.
func ReadNote(ctx *RepoContext, namespace, commitHash string) (*types.Note, error) {
	normalizedNamespace, targetHash, err := normalizeNoteLookup(namespace, commitHash)
	if err != nil {
		return nil, err
	}

	repo, tree, err := resolveNoteNamespaceTree(ctx, normalizedNamespace)
	if err != nil {
		return nil, err
	}

	payload, err := readNotePayloadAtTarget(repo, tree, targetHash)
	if err != nil {
		return nil, err
	}

	return &types.Note{
		CommitHash: targetHash.String(),
		Namespace:  normalizedNamespace,
		Content:    payload.Content,
		Version:    payload.Version,
	}, nil
}

// ListNotes enumerates every decodable note stored in one namespace.
func ListNotes(ctx *RepoContext, namespace string) ([]types.Note, error) {
	normalizedNamespace, err := normalizeNoteNamespace(namespace)
	if err != nil {
		return nil, err
	}

	repo, tree, err := resolveNoteNamespaceTree(ctx, normalizedNamespace)
	if err != nil {
		if errors.Is(err, ErrNoteNotFound) {
			return []types.Note{}, nil
		}
		return nil, err
	}

	notes, err := collectNotesFromTree(repo, tree)
	if err != nil {
		return nil, err
	}
	for i := range notes {
		notes[i].Namespace = normalizedNamespace
	}

	slices.SortFunc(notes, func(a, b types.Note) int {
		return strings.Compare(a.CommitHash, b.CommitHash)
	})

	return notes, nil
}

// ListNoteNamespaces enumerates direct namespace refs under refs/notes/opax/.
func ListNoteNamespaces(ctx *RepoContext) ([]string, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	iter, err := repo.References()
	if err != nil {
		return nil, fmt.Errorf("git: list note namespaces: iterate refs: %w", err)
	}
	defer iter.Close()

	seen := make(map[string]struct{})
	var namespaces []string
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().String()
		if !strings.HasPrefix(name, opaxNotesRefPrefix) {
			return nil
		}

		namespace := strings.TrimPrefix(name, opaxNotesRefPrefix)
		if namespace == "" || strings.Contains(namespace, "/") {
			return fmt.Errorf("git: list note namespaces: invalid note ref %q: %w", name, ErrMalformedNote)
		}
		if _, err := normalizeNoteNamespace(namespace); err != nil {
			return fmt.Errorf("git: list note namespaces: invalid note ref %q: %w", name, ErrMalformedNote)
		}
		if _, exists := seen[namespace]; exists {
			return nil
		}

		seen[namespace] = struct{}{}
		namespaces = append(namespaces, namespace)
		return nil
	}); err != nil {
		return nil, err
	}

	slices.Sort(namespaces)
	return namespaces, nil
}

func normalizeNote(note types.Note) (*normalizedNote, error) {
	namespace, commitHash, err := normalizeNoteLookup(note.Namespace, note.CommitHash)
	if err != nil {
		return nil, err
	}
	if note.Version <= 0 {
		return nil, fmt.Errorf(
			"git: write note namespace %q commit %s: version must be > 0",
			namespace,
			commitHash,
		)
	}

	contentFields, err := decodeJSONFields(note.Content)
	if err != nil {
		return nil, fmt.Errorf(
			"git: write note namespace %q commit %s: invalid content: %w",
			namespace,
			commitHash,
			err,
		)
	}
	if _, hasVersion := contentFields["version"]; hasVersion {
		return nil, fmt.Errorf(
			"git: write note namespace %q commit %s: content must not contain reserved field \"version\"",
			namespace,
			commitHash,
		)
	}

	content, err := marshalJSONFields(contentFields)
	if err != nil {
		return nil, fmt.Errorf(
			"git: write note namespace %q commit %s: encode content: %w",
			namespace,
			commitHash,
			err,
		)
	}

	storedFields := make(map[string]json.RawMessage, len(contentFields)+1)
	versionRaw, err := json.Marshal(note.Version)
	if err != nil {
		return nil, fmt.Errorf(
			"git: write note namespace %q commit %s: encode version: %w",
			namespace,
			commitHash,
			err,
		)
	}
	storedFields["version"] = json.RawMessage(versionRaw)
	for key, value := range contentFields {
		storedFields[key] = value
	}

	storedBytes, err := marshalJSONFields(storedFields)
	if err != nil {
		return nil, fmt.Errorf(
			"git: write note namespace %q commit %s: encode payload: %w",
			namespace,
			commitHash,
			err,
		)
	}

	return &normalizedNote{
		Namespace:   namespace,
		CommitHash:  commitHash,
		Version:     note.Version,
		Content:     content,
		StoredBytes: storedBytes,
	}, nil
}

func normalizeNoteLookup(namespace, commitHash string) (string, plumbing.Hash, error) {
	normalizedNamespace, err := normalizeNoteNamespace(namespace)
	if err != nil {
		return "", plumbing.ZeroHash, err
	}

	trimmedHash := strings.TrimSpace(strings.ToLower(commitHash))
	if !plumbing.IsHash(trimmedHash) {
		return "", plumbing.ZeroHash, fmt.Errorf(
			"git: note namespace %q commit hash %q is invalid",
			normalizedNamespace,
			commitHash,
		)
	}

	return normalizedNamespace, plumbing.NewHash(trimmedHash), nil
}

func normalizeNoteNamespace(namespace string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", fmt.Errorf("git: note namespace is empty")
	}
	if strings.Contains(namespace, "/") {
		return "", fmt.Errorf("git: note namespace %q must not contain slash", namespace)
	}
	if strings.Contains(namespace, "..") {
		return "", fmt.Errorf("git: note namespace %q must not contain \"..\"", namespace)
	}
	for _, ch := range namespace {
		isLowerAlpha := ch >= 'a' && ch <= 'z'
		isDigit := ch >= '0' && ch <= '9'
		if !isLowerAlpha && !isDigit && ch != '-' {
			return "", fmt.Errorf("git: note namespace %q must be lowercase alphanumeric or dash", namespace)
		}
	}
	if strings.HasPrefix(namespace, "ext-") {
		if err := validatePluginName(strings.TrimPrefix(namespace, "ext-")); err != nil {
			return "", err
		}
	}

	return namespace, nil
}

func decodeJSONFields(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("JSON object is empty")
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("must be a JSON object")
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return fields, nil
		}
		return nil, err
	}

	return nil, fmt.Errorf("must contain a single JSON value")
}

func marshalJSONFields(fields map[string]json.RawMessage) (json.RawMessage, error) {
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func buildNoteWriteReference(
	repo *ggit.Repository,
	currentRef *plumbing.Reference,
	note *normalizedNote,
) (*plumbing.Reference, error) {
	rootTreeHash, parentHashes, err := resolveNoteWriteBase(repo, currentRef, note.Namespace)
	if err != nil {
		return nil, err
	}

	payloadBlobHash, err := writeBlob(repo, note.StoredBytes)
	if err != nil {
		return nil, err
	}

	updatedTreeHash, err := upsertNoteTree(repo, rootTreeHash, note.CommitHash, payloadBlobHash)
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
		Message:      fmt.Sprintf("opax: write note %s %s", note.Namespace, note.CommitHash),
		TreeHash:     updatedTreeHash,
		ParentHashes: parentHashes,
	})
	if err != nil {
		return nil, err
	}

	return plumbing.NewHashReference(noteRefName(note.Namespace), commitHash), nil
}

func resolveNoteWriteBase(
	repo *ggit.Repository,
	currentRef *plumbing.Reference,
	namespace string,
) (plumbing.Hash, []plumbing.Hash, error) {
	if currentRef == nil {
		emptyTreeHash, err := writeTree(repo, nil)
		if err != nil {
			return plumbing.ZeroHash, nil, err
		}
		return emptyTreeHash, nil, nil
	}

	commit, err := repo.CommitObject(currentRef.Hash())
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf(
			"git: write note namespace %q current ref %s is not a commit: %v: %w",
			namespace,
			currentRef.Hash(),
			err,
			ErrMalformedNote,
		)
	}

	tree, err := commit.Tree()
	if err != nil {
		return plumbing.ZeroHash, nil, fmt.Errorf(
			"git: write note namespace %q read tree for %s: %v: %w",
			namespace,
			commit.Hash,
			err,
			ErrMalformedNote,
		)
	}

	return tree.Hash, []plumbing.Hash{currentRef.Hash()}, nil
}

func resolveNoteNamespaceTree(ctx *RepoContext, namespace string) (*ggit.Repository, *object.Tree, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	ref, err := repo.Reference(noteRefName(namespace), true)
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil, nil, fmt.Errorf("git: note namespace %q: %w", namespace, ErrNoteNotFound)
		}
		return nil, nil, fmt.Errorf("git: read note ref %s: %w", noteRefName(namespace), err)
	}

	commit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, nil, fmt.Errorf(
			"git: note namespace %q current ref %s is not a commit: %v: %w",
			namespace,
			ref.Hash(),
			err,
			ErrMalformedNote,
		)
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, nil, fmt.Errorf(
			"git: note namespace %q read tree for %s: %v: %w",
			namespace,
			commit.Hash,
			err,
			ErrMalformedNote,
		)
	}

	return repo, tree, nil
}

func readNotePayloadAtTarget(repo *ggit.Repository, tree *object.Tree, targetHash plumbing.Hash) (*notePayload, error) {
	shard, leaf := notePathComponents(targetHash)

	entry, found := findTreeEntryByName(tree.Entries, shard)
	if found {
		if entry.Mode != filemode.Dir {
			return nil, fmt.Errorf(
				"git: read note for %s expected shard directory %q: %w",
				targetHash,
				shard,
				ErrMalformedNote,
			)
		}

		shardTree, err := repo.TreeObject(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf(
				"git: read note for %s load shard tree %q (%s): %v: %w",
				targetHash,
				shard,
				entry.Hash,
				err,
				ErrMalformedNote,
			)
		}

		shardEntry, found := findTreeEntryByName(shardTree.Entries, leaf)
		if found {
			if shardEntry.Mode != filemode.Regular {
				return nil, fmt.Errorf(
					"git: read note for %s expected blob at %q: %w",
					targetHash,
					pathpkg.Join(shard, leaf),
					ErrMalformedNote,
				)
			}
			return decodeStoredNotePayload(repo, shardEntry.Hash, targetHash.String())
		}
	}

	flatEntry, found := findTreeEntryByName(tree.Entries, targetHash.String())
	if !found {
		return nil, fmt.Errorf("git: read note for %s: %w", targetHash, ErrNoteNotFound)
	}
	if flatEntry.Mode != filemode.Regular {
		return nil, fmt.Errorf(
			"git: read note for %s expected blob at %q: %w",
			targetHash,
			targetHash,
			ErrMalformedNote,
		)
	}
	return decodeStoredNotePayload(repo, flatEntry.Hash, targetHash.String())
}

func collectNotesFromTree(repo *ggit.Repository, rootTree *object.Tree) ([]types.Note, error) {
	seen := make(map[string]struct{})
	var notes []types.Note

	for _, entry := range rootTree.Entries {
		switch {
		case entry.Mode == filemode.Regular && isCanonicalHash(entry.Name):
			if _, exists := seen[entry.Name]; exists {
				return nil, fmt.Errorf("git: duplicate note entry %q: %w", entry.Name, ErrMalformedNote)
			}
			payload, err := decodeStoredNotePayload(repo, entry.Hash, entry.Name)
			if err != nil {
				return nil, err
			}
			seen[entry.Name] = struct{}{}
			notes = append(notes, types.Note{
				CommitHash: entry.Name,
				Version:    payload.Version,
				Content:    payload.Content,
			})
		case entry.Mode == filemode.Dir && isNoteShard(entry.Name):
			shardTree, err := repo.TreeObject(entry.Hash)
			if err != nil {
				return nil, fmt.Errorf(
					"git: list notes load shard tree %q (%s): %v: %w",
					entry.Name,
					entry.Hash,
					err,
					ErrMalformedNote,
				)
			}
			for _, shardEntry := range shardTree.Entries {
				if shardEntry.Mode != filemode.Regular {
					return nil, fmt.Errorf(
						"git: list notes expected blob at %q: %w",
						pathpkg.Join(entry.Name, shardEntry.Name),
						ErrMalformedNote,
					)
				}

				commitHash := entry.Name + shardEntry.Name
				if !isCanonicalHash(commitHash) || len(shardEntry.Name) != len(plumbing.ZeroHash.String())-noteFanoutPrefixLen {
					return nil, fmt.Errorf(
						"git: list notes invalid fanout path %q: %w",
						pathpkg.Join(entry.Name, shardEntry.Name),
						ErrMalformedNote,
					)
				}
				if _, exists := seen[commitHash]; exists {
					return nil, fmt.Errorf("git: duplicate note entry %q: %w", commitHash, ErrMalformedNote)
				}

				payload, err := decodeStoredNotePayload(repo, shardEntry.Hash, commitHash)
				if err != nil {
					return nil, err
				}
				seen[commitHash] = struct{}{}
				notes = append(notes, types.Note{
					CommitHash: commitHash,
					Version:    payload.Version,
					Content:    payload.Content,
				})
			}
		default:
			return nil, fmt.Errorf(
				"git: list notes unexpected entry %q in notes tree: %w",
				entry.Name,
				ErrMalformedNote,
			)
		}
	}

	return notes, nil
}

func upsertNoteTree(
	repo *ggit.Repository,
	rootTreeHash plumbing.Hash,
	targetHash plumbing.Hash,
	payloadBlobHash plumbing.Hash,
) (plumbing.Hash, error) {
	rootTree, err := repo.TreeObject(rootTreeHash)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("git: read note root tree %s: %w", rootTreeHash, err)
	}

	rootEntries := make(map[string]object.TreeEntry, len(rootTree.Entries)+1)
	for _, entry := range rootTree.Entries {
		rootEntries[entry.Name] = entry
	}

	delete(rootEntries, targetHash.String())

	shard, leaf := notePathComponents(targetHash)
	var shardTree *object.Tree
	if existingShard, found := rootEntries[shard]; found {
		if existingShard.Mode != filemode.Dir {
			return plumbing.ZeroHash, fmt.Errorf(
				"git: write note for %s expected shard directory %q: %w",
				targetHash,
				shard,
				ErrMalformedNote,
			)
		}
		shardTree, err = repo.TreeObject(existingShard.Hash)
		if err != nil {
			return plumbing.ZeroHash, fmt.Errorf(
				"git: write note for %s load shard tree %q (%s): %v: %w",
				targetHash,
				shard,
				existingShard.Hash,
				err,
				ErrMalformedNote,
			)
		}
	} else {
		shardTree = &object.Tree{}
	}

	shardEntries := make(map[string]object.TreeEntry, len(shardTree.Entries)+1)
	for _, entry := range shardTree.Entries {
		shardEntries[entry.Name] = entry
	}
	shardEntries[leaf] = object.TreeEntry{
		Name: leaf,
		Mode: filemode.Regular,
		Hash: payloadBlobHash,
	}

	shardTreeHash, err := writeTree(repo, entriesFromMap(shardEntries))
	if err != nil {
		return plumbing.ZeroHash, err
	}
	rootEntries[shard] = object.TreeEntry{
		Name: shard,
		Mode: filemode.Dir,
		Hash: shardTreeHash,
	}

	return writeTree(repo, entriesFromMap(rootEntries))
}

func decodeStoredNotePayload(repo *ggit.Repository, blobHash plumbing.Hash, target string) (*notePayload, error) {
	data, err := readBlobContent(repo, blobHash)
	if err != nil {
		return nil, fmt.Errorf(
			"git: read note payload %s blob %s: %v: %w",
			target,
			blobHash,
			err,
			ErrMalformedNote,
		)
	}

	fields, err := decodeJSONFields(data)
	if err != nil {
		return nil, fmt.Errorf("git: decode note payload %s: %v: %w", target, err, ErrMalformedNote)
	}

	versionRaw, ok := fields["version"]
	if !ok {
		return nil, fmt.Errorf("git: decode note payload %s missing version: %w", target, ErrMalformedNote)
	}
	delete(fields, "version")

	var version int
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return nil, fmt.Errorf("git: decode note payload %s invalid version: %v: %w", target, err, ErrMalformedNote)
	}
	if version <= 0 {
		return nil, fmt.Errorf("git: decode note payload %s version must be > 0: %w", target, ErrMalformedNote)
	}

	content, err := marshalJSONFields(fields)
	if err != nil {
		return nil, fmt.Errorf("git: decode note payload %s encode content: %v: %w", target, err, ErrMalformedNote)
	}

	return &notePayload{
		Version: version,
		Content: content,
	}, nil
}

func noteRefName(namespace string) plumbing.ReferenceName {
	return plumbing.ReferenceName(opaxNotesRefPrefix + namespace)
}

func notePathComponents(targetHash plumbing.Hash) (string, string) {
	hexHash := targetHash.String()
	return hexHash[:noteFanoutPrefixLen], hexHash[noteFanoutPrefixLen:]
}

func isCanonicalHash(s string) bool {
	return plumbing.IsHash(s)
}

func isNoteShard(s string) bool {
	return len(s) == noteFanoutPrefixLen && plumbing.IsHash(s+strings.Repeat("0", len(plumbing.ZeroHash.String())-noteFanoutPrefixLen))
}
