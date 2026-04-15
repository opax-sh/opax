package git

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/opax-sh/opax/internal/types"
)

const checkpointDWalkRecordsCallCeiling = 12

func TestCheckpointDWalkRecordsRespectsCallCeiling(t *testing.T) {
	harness := newCheckpointCGitHarness(t)
	ctx := seedCheckpointCRecordFixture(t)

	sessionID := types.NewSessionID().String()
	if _, err := WriteRecord(ctx, WriteRequest{
		Collection: "sessions",
		RecordID:   sessionID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-d"}`)},
		},
	}); err != nil {
		t.Fatalf("WriteRecord(sessions) error = %v", err)
	}

	saveID := types.NewSaveID().String()
	if _, err := WriteRecord(ctx, WriteRequest{
		Collection: "saves",
		RecordID:   saveID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-d"}`)},
		},
	}); err != nil {
		t.Fatalf("WriteRecord(saves) error = %v", err)
	}

	extSuffix := sessionID[len("ses_"):]
	extensionID := "wrk_" + extSuffix
	if _, err := WriteRecord(ctx, WriteRequest{
		Collection: "ext-workflows",
		RecordID:   extensionID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-d"}`)},
		},
	}); err != nil {
		t.Fatalf("WriteRecord(ext-workflows) error = %v", err)
	}

	warmVersionGateThenClearLog(t, harness, ctx)

	var first []string
	if err := WalkRecords(ctx, func(locator RecordLocator) error {
		first = append(first, locator.RecordRoot)
		return nil
	}); err != nil {
		t.Fatalf("WalkRecords() error = %v", err)
	}

	if len(first) != 3 {
		t.Fatalf("WalkRecords() emitted %d records, want 3", len(first))
	}

	lines := harness.readLogLines(t)
	if len(lines) > checkpointDWalkRecordsCallCeiling {
		t.Fatalf(
			"WalkRecords() git call count = %d, want <= %d\ncalls:\n%s",
			len(lines),
			checkpointDWalkRecordsCallCeiling,
			strings.Join(lines, "\n"),
		)
	}

	recursiveCount := countGitCallsWithSequence(lines, "ls-tree", "-r")
	if recursiveCount != 3 {
		t.Fatalf("WalkRecords() recursive ls-tree calls = %d, want 3", recursiveCount)
	}

	wantSorted := []string{
		deriveRecordRoot("sessions", sessionID),
		deriveRecordRoot("saves", saveID),
		deriveRecordRoot("ext-workflows", extensionID),
	}
	slices.Sort(wantSorted)

	gotSorted := append([]string(nil), first...)
	slices.Sort(gotSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Fatalf("WalkRecords() roots = %#v, want %#v", gotSorted, wantSorted)
	}

	var second []string
	if err := WalkRecords(ctx, func(locator RecordLocator) error {
		second = append(second, locator.RecordRoot)
		return nil
	}); err != nil {
		t.Fatalf("WalkRecords() second pass error = %v", err)
	}
	if !slices.Equal(first, second) {
		t.Fatalf("WalkRecords() first order = %#v, second order = %#v", first, second)
	}
}

func TestCheckpointDWalkRecordsMalformedLayoutMapsToMalformedTree(t *testing.T) {
	ctx := seedCheckpointCRecordFixture(t)
	injectCheckpointDMalformedShardBlob(t, ctx, "sessions", "aa")

	err := WalkRecords(ctx, func(locator RecordLocator) error { return nil })
	if !errors.Is(err, ErrMalformedTree) {
		t.Fatalf("WalkRecords() error = %v, want ErrMalformedTree", err)
	}
}

func injectCheckpointDMalformedShardBlob(t *testing.T, ctx *RepoContext, collection, shard string) {
	t.Helper()

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	tipHash, tipCommit, err := resolveValidatedOpaxBranchTip(backend)
	if err != nil {
		t.Fatalf("resolveValidatedOpaxBranchTip() error = %v", err)
	}

	rootEntries, err := backend.readTree(tipCommit.TreeHash)
	if err != nil {
		t.Fatalf("readTree(%s) error = %v", tipCommit.TreeHash, err)
	}

	blobHash, err := backend.writeBlob([]byte("checkpoint-d-malformed"))
	if err != nil {
		t.Fatalf("writeBlob() error = %v", err)
	}

	collectionTreeHash, err := backend.writeTree([]gitTreeEntry{
		{Name: shard, Mode: gitModeBlob, Type: "blob", Hash: blobHash},
	})
	if err != nil {
		t.Fatalf("writeTree(collection) error = %v", err)
	}

	rootEntries = upsertCheckpointDTreeEntry(rootEntries, gitTreeEntry{
		Name: collection,
		Mode: gitModeTree,
		Type: "tree",
		Hash: collectionTreeHash,
	})

	updatedRootTreeHash, err := backend.writeTree(rootEntries)
	if err != nil {
		t.Fatalf("writeTree(root) error = %v", err)
	}

	nextTip, err := backend.writeCommit(gitCommitWriteRequest{
		TreeHash:     updatedRootTreeHash,
		ParentHashes: []gitHash{tipHash},
		Message:      "opax: checkpoint d malformed walk fixture",
	})
	if err != nil {
		t.Fatalf("writeCommit() error = %v", err)
	}

	if err := backend.updateRefCAS(opaxBranchRef, nextTip, &tipHash); err != nil {
		t.Fatalf("updateRefCAS() error = %v", err)
	}
}

func upsertCheckpointDTreeEntry(entries []gitTreeEntry, replacement gitTreeEntry) []gitTreeEntry {
	out := make([]gitTreeEntry, 0, len(entries)+1)
	replaced := false
	for _, entry := range entries {
		if entry.Name == replacement.Name {
			out = append(out, replacement)
			replaced = true
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}
