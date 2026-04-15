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

	"github.com/opax-sh/opax/internal/types"
)

type normalizedNote struct {
	Namespace   string
	CommitHash  gitHash
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

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return err
	}
	if err := backend.ensureCommitExists(normalized.CommitHash); err != nil {
		return fmt.Errorf(
			"git: write note namespace %q commit %s: resolve target commit: %w",
			normalized.Namespace,
			normalized.CommitHash,
			err,
		)
	}

	refName := noteRefName(normalized.Namespace)
	if _, err := publishRefWithRetry(ctx, refName, func(backend *nativeGitBackend, currentRef *gitRef) (*gitRef, error) {
		nextRef, err := buildNoteWriteReference(backend, currentRef, normalized)
		if err != nil {
			return nil, err
		}
		return nextRef, nil
	}); err != nil {
		if errors.Is(err, errReferenceChanged) {
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

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := backend.ensureCommitExists(targetHash); err != nil {
		return nil, fmt.Errorf(
			"git: read note namespace %q commit %s: resolve target commit: %w",
			normalizedNamespace,
			targetHash,
			err,
		)
	}

	treeHash, err := resolveNoteNamespaceTree(backend, normalizedNamespace)
	if err != nil {
		return nil, err
	}

	payload, err := readNotePayloadAtTarget(backend, treeHash, targetHash)
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

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	treeHash, err := resolveNoteNamespaceTree(backend, normalizedNamespace)
	if err != nil {
		if errors.Is(err, ErrNoteNotFound) {
			return []types.Note{}, nil
		}
		return nil, err
	}

	notes, err := collectNotesFromTree(backend, treeHash)
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
	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return nil, err
	}

	refs, err := backend.listRefsByPrefix(opaxNotesRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("git: list note namespaces: %w", err)
	}

	seen := make(map[string]struct{})
	var namespaces []string
	for _, name := range refs {
		namespace := strings.TrimPrefix(name, opaxNotesRefPrefix)
		if namespace == "" || strings.Contains(namespace, "/") {
			return nil, fmt.Errorf("git: list note namespaces: invalid note ref %q: %w", name, ErrMalformedNote)
		}
		if _, err := normalizeNoteNamespace(namespace); err != nil {
			return nil, fmt.Errorf("git: list note namespaces: invalid note ref %q: %w", name, ErrMalformedNote)
		}
		if _, exists := seen[namespace]; exists {
			continue
		}

		seen[namespace] = struct{}{}
		namespaces = append(namespaces, namespace)
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

func normalizeNoteLookup(namespace, commitHash string) (string, gitHash, error) {
	normalizedNamespace, err := normalizeNoteNamespace(namespace)
	if err != nil {
		return "", "", err
	}

	normalizedHash, err := normalizeHash(commitHash)
	if err != nil {
		return "", "", fmt.Errorf(
			"git: note namespace %q commit hash %q is invalid: %w",
			normalizedNamespace,
			commitHash,
			err,
		)
	}
	return normalizedNamespace, normalizedHash, nil
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
	backend *nativeGitBackend,
	currentRef *gitRef,
	note *normalizedNote,
) (*gitRef, error) {
	rootTreeHash, parentHashes, err := resolveNoteWriteBase(backend, currentRef, note.Namespace)
	if err != nil {
		return nil, err
	}

	payloadBlobHash, err := backend.writeBlob(note.StoredBytes)
	if err != nil {
		return nil, err
	}

	updatedTreeHash, err := upsertNoteTree(backend, rootTreeHash, note.CommitHash, payloadBlobHash)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	commitHash, err := backend.writeCommit(gitCommitWriteRequest{
		TreeHash:       updatedTreeHash,
		ParentHashes:   parentHashes,
		Message:        fmt.Sprintf("opax: write note %s %s", note.Namespace, note.CommitHash),
		AuthorName:     opaxAuthorName,
		AuthorEmail:    opaxAuthorEmail,
		CommitterName:  opaxAuthorName,
		CommitterEmail: opaxAuthorEmail,
		When:           now,
	})
	if err != nil {
		return nil, err
	}

	return &gitRef{name: noteRefName(note.Namespace), hash: commitHash}, nil
}

func resolveNoteWriteBase(
	backend *nativeGitBackend,
	currentRef *gitRef,
	namespace string,
) (gitHash, []gitHash, error) {
	if currentRef == nil {
		emptyTreeHash, err := backend.writeTree(nil)
		if err != nil {
			return "", nil, err
		}
		return emptyTreeHash, nil, nil
	}

	commit, err := backend.readCommit(currentRef.hash)
	if err != nil {
		return "", nil, fmt.Errorf(
			"git: write note namespace %q current ref %s is not a commit: %v: %w",
			namespace,
			currentRef.hash,
			err,
			ErrMalformedNote,
		)
	}

	return commit.TreeHash, []gitHash{currentRef.hash}, nil
}

func resolveNoteNamespaceTree(backend *nativeGitBackend, namespace string) (gitHash, error) {
	ref, err := backend.readRef(noteRefName(namespace))
	if err != nil {
		return "", fmt.Errorf("git: read note ref %s: %w", noteRefName(namespace), err)
	}
	if ref == nil {
		return "", fmt.Errorf("git: note namespace %q: %w", namespace, ErrNoteNotFound)
	}

	commit, err := backend.readCommit(ref.hash)
	if err != nil {
		return "", fmt.Errorf(
			"git: note namespace %q current ref %s is not a commit: %v: %w",
			namespace,
			ref.hash,
			err,
			ErrMalformedNote,
		)
	}

	return commit.TreeHash, nil
}

func readNotePayloadAtTarget(backend *nativeGitBackend, treeHash gitHash, targetHash gitHash) (*notePayload, error) {
	rootEntries, err := backend.readTree(treeHash)
	if err != nil {
		return nil, fmt.Errorf("git: read note root tree %s: %v: %w", treeHash, err, ErrMalformedNote)
	}

	shard, leaf := notePathComponents(targetHash)
	entry, found := findTreeEntryByName(rootEntries, shard)
	if found {
		if !entryIsTree(entry) {
			return nil, fmt.Errorf(
				"git: read note for %s expected shard directory %q: %w",
				targetHash,
				shard,
				ErrMalformedNote,
			)
		}

		shardEntries, err := backend.readTree(entry.Hash)
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

		shardEntry, found := findTreeEntryByName(shardEntries, leaf)
		if found {
			if !entryIsBlob(shardEntry) {
				return nil, fmt.Errorf(
					"git: read note for %s expected blob at %q: %w",
					targetHash,
					pathpkg.Join(shard, leaf),
					ErrMalformedNote,
				)
			}
			return decodeStoredNotePayload(backend, shardEntry.Hash, targetHash.String())
		}
	}

	flatEntry, found := findTreeEntryByName(rootEntries, targetHash.String())
	if !found {
		return nil, fmt.Errorf("git: read note for %s: %w", targetHash, ErrNoteNotFound)
	}
	if !entryIsBlob(flatEntry) {
		return nil, fmt.Errorf(
			"git: read note for %s expected blob at %q: %w",
			targetHash,
			targetHash,
			ErrMalformedNote,
		)
	}
	return decodeStoredNotePayload(backend, flatEntry.Hash, targetHash.String())
}

func collectNotesFromTree(backend *nativeGitBackend, rootTreeHash gitHash) ([]types.Note, error) {
	rootEntries, err := backend.readTree(rootTreeHash)
	if err != nil {
		return nil, fmt.Errorf("git: list notes load root tree %s: %v: %w", rootTreeHash, err, ErrMalformedNote)
	}

	seen := make(map[string]struct{})
	var notes []types.Note

	for _, entry := range rootEntries {
		switch {
		case entryIsBlob(entry) && isCanonicalHash(entry.Name):
			if _, exists := seen[entry.Name]; exists {
				return nil, fmt.Errorf("git: duplicate note entry %q: %w", entry.Name, ErrMalformedNote)
			}
			payload, err := decodeStoredNotePayload(backend, entry.Hash, entry.Name)
			if err != nil {
				return nil, err
			}
			seen[entry.Name] = struct{}{}
			notes = append(notes, types.Note{
				CommitHash: entry.Name,
				Version:    payload.Version,
				Content:    payload.Content,
			})
		case entryIsTree(entry) && isNoteShard(entry.Name):
			shardEntries, err := backend.readTree(entry.Hash)
			if err != nil {
				return nil, fmt.Errorf(
					"git: list notes load shard tree %q (%s): %v: %w",
					entry.Name,
					entry.Hash,
					err,
					ErrMalformedNote,
				)
			}
			for _, shardEntry := range shardEntries {
				if !entryIsBlob(shardEntry) {
					return nil, fmt.Errorf(
						"git: list notes expected blob at %q: %w",
						pathpkg.Join(entry.Name, shardEntry.Name),
						ErrMalformedNote,
					)
				}

				commitHash := entry.Name + shardEntry.Name
				if !isCanonicalHash(commitHash) || len(shardEntry.Name) != hashHexLength-noteFanoutPrefixLen {
					return nil, fmt.Errorf(
						"git: list notes invalid fanout path %q: %w",
						pathpkg.Join(entry.Name, shardEntry.Name),
						ErrMalformedNote,
					)
				}
				if _, exists := seen[commitHash]; exists {
					return nil, fmt.Errorf("git: duplicate note entry %q: %w", commitHash, ErrMalformedNote)
				}

				payload, err := decodeStoredNotePayload(backend, shardEntry.Hash, commitHash)
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
	backend *nativeGitBackend,
	rootTreeHash gitHash,
	targetHash gitHash,
	payloadBlobHash gitHash,
) (gitHash, error) {
	rootEntries, err := backend.readTree(rootTreeHash)
	if err != nil {
		return "", fmt.Errorf("git: read note root tree %s: %w", rootTreeHash, err)
	}

	rootEntryMap := make(map[string]gitTreeEntry, len(rootEntries)+1)
	for _, entry := range rootEntries {
		rootEntryMap[entry.Name] = entry
	}

	delete(rootEntryMap, targetHash.String())

	shard, leaf := notePathComponents(targetHash)
	shardEntry, found := rootEntryMap[shard]
	var shardEntries []gitTreeEntry
	if found {
		if !entryIsTree(shardEntry) {
			return "", fmt.Errorf(
				"git: write note for %s expected shard directory %q: %w",
				targetHash,
				shard,
				ErrMalformedNote,
			)
		}
		shardEntries, err = backend.readTree(shardEntry.Hash)
		if err != nil {
			return "", fmt.Errorf(
				"git: write note for %s load shard tree %q (%s): %v: %w",
				targetHash,
				shard,
				shardEntry.Hash,
				err,
				ErrMalformedNote,
			)
		}
	}

	shardEntryMap := make(map[string]gitTreeEntry, len(shardEntries)+1)
	for _, entry := range shardEntries {
		shardEntryMap[entry.Name] = entry
	}
	shardEntryMap[leaf] = gitTreeEntry{
		Name: leaf,
		Mode: gitModeBlob,
		Type: "blob",
		Hash: payloadBlobHash,
	}

	shardTreeHash, err := writeTreeFromMap(backend, shardEntryMap)
	if err != nil {
		return "", err
	}
	rootEntryMap[shard] = gitTreeEntry{
		Name: shard,
		Mode: gitModeTree,
		Type: "tree",
		Hash: shardTreeHash,
	}

	return writeTreeFromMap(backend, rootEntryMap)
}

func decodeStoredNotePayload(backend *nativeGitBackend, blobHash gitHash, target string) (*notePayload, error) {
	data, err := backend.readBlob(blobHash)
	if err != nil {
		return nil, fmt.Errorf(
			"git: read note payload %s blob %s: %v: %w",
			target,
			blobHash,
			err,
			ErrMalformedNote,
		)
	}
	return decodeStoredNotePayloadBytes(data, target)
}

func decodeStoredNotePayloadBytes(data []byte, target string) (*notePayload, error) {
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

func noteRefName(namespace string) string {
	return opaxNotesRefPrefix + namespace
}

func notePathComponents(targetHash gitHash) (string, string) {
	hexHash := targetHash.String()
	return hexHash[:noteFanoutPrefixLen], hexHash[noteFanoutPrefixLen:]
}

func isNoteShard(s string) bool {
	return len(s) == noteFanoutPrefixLen && isCanonicalHash(s+strings.Repeat("0", hashHexLength-noteFanoutPrefixLen))
}
