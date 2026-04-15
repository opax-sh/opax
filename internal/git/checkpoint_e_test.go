package git

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/opax-sh/opax/internal/types"
)

func TestCheckpointEWriteRecordRetryOnCASConflictSucceeds(t *testing.T) {
	ctx := seedCheckpointCRecordFixture(t)
	conflictHash := checkpointECreateSiblingRefCommit(t, ctx, opaxBranchRef, "opax: checkpoint e competing branch write")
	harness := newCheckpointEGitHarness(t, checkpointEConflictRefPlan{
		refName:            opaxBranchRef,
		conflictFailures:   1,
		conflictTargetHash: conflictHash.String(),
	})

	warmVersionGateThenClearLog(t, harness.checkpointCGitHarness, ctx)

	recordID := types.NewSessionID().String()
	result, err := WriteRecord(ctx, WriteRequest{
		Collection: "sessions",
		RecordID:   recordID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-e"}`)},
			{Path: "notes/summary.md", Content: []byte("checkpoint e\n")},
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}

	record, err := ReadRecord(ctx, "sessions", recordID)
	if err != nil {
		t.Fatalf("ReadRecord() error = %v", err)
	}
	if record.RecordRoot != result.RecordRoot {
		t.Fatalf("ReadRecord().RecordRoot = %q, want %q", record.RecordRoot, result.RecordRoot)
	}
	if got := string(record.Files["metadata.json"]); got != `{"source":"checkpoint-e"}` {
		t.Fatalf("ReadRecord().Files[metadata.json] = %q", got)
	}

	lines := harness.readLogLines(t)
	if countGitCallsWithSequence(lines, "update-ref", opaxBranchRef) < 2 {
		t.Fatalf("WriteRecord() update-ref calls = %d, want at least 2\ncalls:\n%s", countGitCallsWithSequence(lines, "update-ref", opaxBranchRef), strings.Join(lines, "\n"))
	}
}

func TestCheckpointEWriteRecordExpectedTipMismatchPreservesErrTipChanged(t *testing.T) {
	ctx := seedCheckpointCRecordFixture(t)

	warmupID := types.NewSessionID().String()
	firstWrite, err := WriteRecord(ctx, WriteRequest{
		Collection: "sessions",
		RecordID:   warmupID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-e"}`)},
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord() warm-up error = %v", err)
	}

	staleTip := firstWrite.BranchTip
	secondID := types.NewSessionID().String()
	if _, err := WriteRecord(ctx, WriteRequest{
		Collection: "sessions",
		RecordID:   secondID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-e"}`)},
		},
	}); err != nil {
		t.Fatalf("WriteRecord() second write error = %v", err)
	}

	thirdID := types.NewSessionID().String()
	_, err = WriteRecord(ctx, WriteRequest{
		Collection:  "sessions",
		RecordID:    thirdID,
		ExpectedTip: &staleTip,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-e"}`)},
		},
	})
	if !errors.Is(err, ErrTipChanged) {
		t.Fatalf("WriteRecord() error = %v, want ErrTipChanged", err)
	}
}

func TestCheckpointEWriteRecordDuplicateRacePreservesErrRecordExists(t *testing.T) {
	harness := newCheckpointEGitHarness(t, checkpointEConflictRefPlan{
		refName:          opaxBranchRef,
		preExecSleep:     "0.1",
		sleepFirstCalls:  1,
		conflictFailures: 0,
	})
	ctx := seedCheckpointCRecordFixture(t)

	recordID := types.NewSessionID().String()
	errCh := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := WriteRecord(ctx, WriteRequest{
				Collection: "sessions",
				RecordID:   recordID,
				Files: []RecordFile{
					{Path: "metadata.json", Content: []byte(fmt.Sprintf(`{"source":"checkpoint-e","writer":%d}`, i))},
				},
			})
			errCh <- err
		}(i)
	}
	close(start)
	wg.Wait()

	var nilCount int
	var duplicateCount int
	for i := 0; i < 2; i++ {
		err := <-errCh
		switch {
		case err == nil:
			nilCount++
		case errors.Is(err, ErrRecordExists):
			duplicateCount++
		default:
			t.Fatalf("WriteRecord() race error = %v, want nil or ErrRecordExists", err)
		}
	}

	if nilCount != 1 || duplicateCount != 1 {
		lines := harness.readLogLines(t)
		t.Fatalf("WriteRecord() race results = success:%d duplicate:%d, want 1/1\ncalls:\n%s", nilCount, duplicateCount, strings.Join(lines, "\n"))
	}
}

func TestCheckpointEWriteRecordLinkedWorktreePreservesWorkingTree(t *testing.T) {
	fixture := setupWorktreeConfigFixture(t)

	if _, err := EnsureOpaxBranch(fixture.worktreeCtx); err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}

	before := strings.TrimSpace(runGitCompat(t, fixture.worktreeRoot, "status", "--short"))

	recordID := types.NewSessionID().String()
	result, err := WriteRecord(fixture.worktreeCtx, WriteRequest{
		Collection: "sessions",
		RecordID:   recordID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-e-worktree"}`)},
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}

	after := strings.TrimSpace(runGitCompat(t, fixture.worktreeRoot, "status", "--short"))
	if after != before {
		t.Fatalf("worktree status changed: before=%q after=%q", before, after)
	}

	content, err := ReadFileAtPath(fixture.mainCtx, pathpkg.Join(result.RecordRoot, "metadata.json"))
	if err != nil {
		t.Fatalf("ReadFileAtPath() error = %v", err)
	}
	if string(content) != `{"source":"checkpoint-e-worktree"}` {
		t.Fatalf("ReadFileAtPath() content = %q", content)
	}
}

func TestCheckpointEWriteNoteRetryOnCASConflictSucceeds(t *testing.T) {
	fixture := setupWorktreeConfigFixture(t)

	if err := WriteNote(fixture.worktreeCtx, types.Note{
		CommitHash: fixture.baseCommit,
		Namespace:  "ext-reviews",
		Version:    1,
		Content:    mustJSONRawMessageCompat(t, `{"reviewer":"bootstrap"}`),
	}); err != nil {
		t.Fatalf("WriteNote() bootstrap error = %v", err)
	}
	if err := WriteNote(fixture.worktreeCtx, types.Note{
		CommitHash: fixture.baseCommit,
		Namespace:  "ext-reviews",
		Version:    1,
		Content:    mustJSONRawMessageCompat(t, `{"reviewer":"bootstrap","stage":"steady"}`),
	}); err != nil {
		t.Fatalf("WriteNote() steady-state bootstrap error = %v", err)
	}
	conflictHash := checkpointECreateSiblingRefCommit(t, fixture.worktreeCtx, noteRefName("ext-reviews"), "opax: checkpoint e competing note write")

	harness := newCheckpointEGitHarness(t, checkpointEConflictRefPlan{
		refName:            opaxNotesRefPrefix + "ext-reviews",
		conflictFailures:   1,
		conflictTargetHash: conflictHash.String(),
	})

	warmVersionGateThenClearLog(t, harness.checkpointCGitHarness, fixture.worktreeCtx)

	if err := WriteNote(fixture.worktreeCtx, types.Note{
		CommitHash: fixture.baseCommit,
		Namespace:  "ext-reviews",
		Version:    2,
		Content:    mustJSONRawMessageCompat(t, `{"reviewer":"checkpoint-e","verdict":"pass"}`),
	}); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}

	note, err := ReadNote(fixture.mainCtx, "ext-reviews", fixture.baseCommit)
	if err != nil {
		t.Fatalf("ReadNote() error = %v", err)
	}
	if note.Version != 2 {
		t.Fatalf("ReadNote().Version = %d, want 2", note.Version)
	}
	if string(note.Content) != `{"reviewer":"checkpoint-e","verdict":"pass"}` {
		t.Fatalf("ReadNote().Content = %q", note.Content)
	}

	lines := harness.readLogLines(t)
	if countGitCallsWithSequence(lines, "update-ref", opaxNotesRefPrefix+"ext-reviews") < 2 {
		t.Fatalf("WriteNote() update-ref calls = %d, want at least 2\ncalls:\n%s", countGitCallsWithSequence(lines, "update-ref", opaxNotesRefPrefix+"ext-reviews"), strings.Join(lines, "\n"))
	}
}

func TestCheckpointEWriteNoteAndListParityOnLinkedWorktree(t *testing.T) {
	fixture := setupWorktreeConfigFixture(t)

	if err := WriteNote(fixture.worktreeCtx, types.Note{
		CommitHash: fixture.baseCommit,
		Namespace:  "ext-reviews",
		Version:    1,
		Content:    mustJSONRawMessageCompat(t, `{"reviewer":"checkpoint-e"}`),
	}); err != nil {
		t.Fatalf("WriteNote() first error = %v", err)
	}
	if err := WriteNote(fixture.worktreeCtx, types.Note{
		CommitHash: fixture.baseCommit,
		Namespace:  "ext-reviews",
		Version:    3,
		Content:    mustJSONRawMessageCompat(t, `{"reviewer":"checkpoint-e","status":"updated"}`),
	}); err != nil {
		t.Fatalf("WriteNote() overwrite error = %v", err)
	}

	note, err := ReadNote(fixture.mainCtx, "ext-reviews", fixture.baseCommit)
	if err != nil {
		t.Fatalf("ReadNote() error = %v", err)
	}
	if note.Version != 3 {
		t.Fatalf("ReadNote().Version = %d, want 3", note.Version)
	}
	if string(note.Content) != `{"reviewer":"checkpoint-e","status":"updated"}` {
		t.Fatalf("ReadNote().Content = %q", note.Content)
	}

	notes, err := ListNotes(fixture.mainCtx, "ext-reviews")
	if err != nil {
		t.Fatalf("ListNotes() error = %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("len(ListNotes()) = %d, want 1", len(notes))
	}
	if notes[0].CommitHash != fixture.baseCommit {
		t.Fatalf("ListNotes()[0].CommitHash = %q, want %q", notes[0].CommitHash, fixture.baseCommit)
	}

	namespaces, err := ListNoteNamespaces(fixture.mainCtx)
	if err != nil {
		t.Fatalf("ListNoteNamespaces() error = %v", err)
	}
	if len(namespaces) != 1 || namespaces[0] != "ext-reviews" {
		t.Fatalf("ListNoteNamespaces() = %#v, want [ext-reviews]", namespaces)
	}
}

func TestCheckpointENoteNamespaceValidationParity(t *testing.T) {
	ctx := seedCheckpointCRecordFixture(t)

	err := WriteNote(ctx, types.Note{
		CommitHash: strings.Repeat("a", 40),
		Namespace:  "ext/reviews",
		Version:    1,
		Content:    mustCheckpointEJSONRawMessage(t, `{"ok":true}`),
	})
	if err == nil {
		t.Fatal("WriteNote() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "must not contain slash") {
		t.Fatalf("WriteNote() error = %v, want slash validation", err)
	}
}

func TestCheckpointEListNoteNamespacesRejectsNestedRef(t *testing.T) {
	ctx := seedCheckpointCRecordFixture(t)

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	tipHash, _, err := resolveValidatedOpaxBranchTip(backend)
	if err != nil {
		t.Fatalf("resolveValidatedOpaxBranchTip() error = %v", err)
	}

	if err := backend.updateRefCAS(opaxNotesRefPrefix+"ext/reviews", tipHash, nil); err != nil {
		t.Fatalf("updateRefCAS(nested note ref) error = %v", err)
	}

	_, err = ListNoteNamespaces(ctx)
	if !errors.Is(err, ErrMalformedNote) {
		t.Fatalf("ListNoteNamespaces() error = %v, want ErrMalformedNote", err)
	}
}

func TestCheckpointEReadNoteMalformedPayloadMapsToErrMalformedNote(t *testing.T) {
	ctx := seedCheckpointCRecordFixture(t)

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	tipHash, _, err := resolveValidatedOpaxBranchTip(backend)
	if err != nil {
		t.Fatalf("resolveValidatedOpaxBranchTip() error = %v", err)
	}

	blobHash, err := backend.writeBlob([]byte("not-json"))
	if err != nil {
		t.Fatalf("writeBlob() error = %v", err)
	}

	noteCommitHash, err := checkpointEInstallMalformedNoteRef(backend, "ext-reviews", tipHash, blobHash)
	if err != nil {
		t.Fatalf("checkpointEInstallMalformedNoteRef() error = %v", err)
	}

	_, err = ReadNote(ctx, "ext-reviews", tipHash.String())
	if !errors.Is(err, ErrMalformedNote) {
		t.Fatalf("ReadNote() error = %v, want ErrMalformedNote (note commit %s)", err, noteCommitHash)
	}
}

func TestCheckpointEReadNoteMalformedRefCommitMapsToErrMalformedNote(t *testing.T) {
	ctx := seedCheckpointCRecordFixture(t)

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	tipHash, _, err := resolveValidatedOpaxBranchTip(backend)
	if err != nil {
		t.Fatalf("resolveValidatedOpaxBranchTip() error = %v", err)
	}

	blobHash, err := backend.writeBlob([]byte(`{"version":1,"reviewer":"checkpoint-e"}`))
	if err != nil {
		t.Fatalf("writeBlob() error = %v", err)
	}

	if err := backend.updateRefCAS(noteRefName("ext-reviews"), blobHash, nil); err != nil {
		t.Fatalf("updateRefCAS(note ref -> blob) error = %v", err)
	}

	_, err = ReadNote(ctx, "ext-reviews", tipHash.String())
	if !errors.Is(err, ErrMalformedNote) {
		t.Fatalf("ReadNote() error = %v, want ErrMalformedNote", err)
	}
}

type checkpointEConflictRefPlan struct {
	refName            string
	conflictFailures   int
	preExecSleep       string
	sleepFirstCalls    int
	conflictTargetHash string
}

type checkpointEGitHarness struct {
	checkpointCGitHarness
}

func newCheckpointEGitHarness(t *testing.T, plan checkpointEConflictRefPlan) checkpointEGitHarness {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("checkpoint E git-wrapper harness is unix-only")
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary not available")
	}

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "git-calls.log")
	wrapperPath := filepath.Join(tmp, "git-wrapper.sh")
	statePath := filepath.Join(tmp, "update-ref-conflicts")

	script := fmt.Sprintf(`#!/bin/sh
set -eu
: "${OPAX_GIT_REAL_BIN:?}"
: "${OPAX_GIT_CALL_LOG:?}"
: "${OPAX_GIT_CONFLICT_STATE:?}"
printf '%%s\n' "$*" >> "$OPAX_GIT_CALL_LOG"

while [ "$#" -gt 0 ]; do
	case "$1" in
		--git-dir|--work-tree)
			shift 2
			;;
		*)
			break
			;;
	esac
done

cmd="${1:-}"
if [ -n "$cmd" ]; then
	shift
fi

if [ "$cmd" = "update-ref" ] && [ "${1:-}" = "%s" ]; then
	count=0
	if [ -f "$OPAX_GIT_CONFLICT_STATE" ]; then
		count=$(cat "$OPAX_GIT_CONFLICT_STATE")
	fi
	if [ "$count" -lt %d ] && [ -n "%s" ]; then
		sleep "%s"
	fi
	if [ "$count" -lt %d ]; then
		if [ -n "%s" ]; then
			"$OPAX_GIT_REAL_BIN" "$cmd" "${1:-}" "%s" "${3:-}" >/dev/null 2>/dev/null || true
		fi
		count=$((count + 1))
		printf '%%s' "$count" > "$OPAX_GIT_CONFLICT_STATE"
		echo "fatal: cannot lock ref '%s': is at 1111111111111111111111111111111111111111 but expected 0000000000000000000000000000000000000000" >&2
		exit 1
	fi
fi

exec "$OPAX_GIT_REAL_BIN" "$cmd" "$@"
`, plan.refName, plan.sleepFirstCalls, plan.preExecSleep, plan.preExecSleep, plan.conflictFailures, plan.conflictTargetHash, plan.conflictTargetHash, plan.refName)

	if err := os.WriteFile(wrapperPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", wrapperPath, err)
	}

	if err := resetGitVersionGateCacheForTests(); err != nil {
		t.Fatalf("resetGitVersionGateCacheForTests() error = %v", err)
	}
	t.Cleanup(func() {
		_ = resetGitVersionGateCacheForTests()
	})

	t.Setenv("OPAX_GIT_REAL_BIN", realGit)
	t.Setenv("OPAX_GIT_CALL_LOG", logPath)
	t.Setenv("OPAX_GIT_CONFLICT_STATE", statePath)
	t.Setenv(gitBinaryOverrideEnv, wrapperPath)

	return checkpointEGitHarness{
		checkpointCGitHarness: checkpointCGitHarness{logPath: logPath},
	}
}

func checkpointEInstallMalformedNoteRef(
	backend *nativeGitBackend,
	namespace string,
	targetCommitHash gitHash,
	payloadBlobHash gitHash,
) (gitHash, error) {
	shard, leaf := notePathComponents(targetCommitHash)

	shardTreeHash, err := backend.writeTree([]gitTreeEntry{{
		Name: leaf,
		Mode: gitModeBlob,
		Type: "blob",
		Hash: payloadBlobHash,
	}})
	if err != nil {
		return "", err
	}

	rootTreeHash, err := backend.writeTree([]gitTreeEntry{{
		Name: shard,
		Mode: gitModeTree,
		Type: "tree",
		Hash: shardTreeHash,
	}})
	if err != nil {
		return "", err
	}

	noteCommitHash, err := backend.writeCommit(gitCommitWriteRequest{
		TreeHash:       rootTreeHash,
		ParentHashes:   []gitHash{targetCommitHash},
		Message:        "opax: checkpoint e malformed note",
		AuthorName:     opaxAuthorName,
		AuthorEmail:    opaxAuthorEmail,
		CommitterName:  opaxAuthorName,
		CommitterEmail: opaxAuthorEmail,
	})
	if err != nil {
		return "", err
	}

	if err := backend.updateRefCAS(noteRefName(namespace), noteCommitHash, nil); err != nil {
		return "", err
	}
	return noteCommitHash, nil
}

func checkpointECreateSiblingRefCommit(
	t *testing.T,
	ctx *RepoContext,
	refName string,
	message string,
) gitHash {
	t.Helper()

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}

	ref, err := backend.readRef(refName)
	if err != nil {
		t.Fatalf("readRef(%s) error = %v", refName, err)
	}
	if ref == nil {
		t.Fatalf("readRef(%s) = nil, want existing ref", refName)
	}

	currentCommit, err := backend.readCommit(ref.hash)
	if err != nil {
		t.Fatalf("readCommit(%s) error = %v", ref.hash, err)
	}

	siblingHash, err := backend.writeCommit(gitCommitWriteRequest{
		TreeHash:       currentCommit.TreeHash,
		ParentHashes:   []gitHash{ref.hash},
		Message:        message,
		AuthorName:     opaxAuthorName,
		AuthorEmail:    opaxAuthorEmail,
		CommitterName:  opaxAuthorName,
		CommitterEmail: opaxAuthorEmail,
	})
	if err != nil {
		t.Fatalf("writeCommit() error = %v", err)
	}
	return siblingHash
}

func mustCheckpointEJSONRawMessage(t *testing.T, raw string) json.RawMessage {
	t.Helper()

	var parsed json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", raw, err)
	}
	return parsed
}
