package git

import (
	"errors"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opax-sh/opax/internal/types"
)

const (
	checkpointCReadRecordCallCeiling = 15
	checkpointCReadFileCallCeiling   = 13
)

type checkpointCGitHarness struct {
	logPath string
}

func TestCheckpointCReadRecordUsesBatchAndRespectsCallCeiling(t *testing.T) {
	harness := newCheckpointCGitHarness(t)
	ctx := seedCheckpointCRecordFixture(t)

	warmVersionGateThenClearLog(t, harness, ctx)

	recordID := types.NewSessionID().String()
	if _, err := WriteRecord(ctx, WriteRequest{
		Collection: "sessions",
		RecordID:   recordID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-c"}`)},
		},
	}); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}

	warmVersionGateThenClearLog(t, harness, ctx)

	if _, err := ReadRecord(ctx, "sessions", recordID); err != nil {
		t.Fatalf("ReadRecord() error = %v", err)
	}

	lines := harness.readLogLines(t)
	batchCount := countGitCallsWithSequence(lines, "cat-file", "--batch")
	if batchCount != 1 {
		t.Fatalf("ReadRecord() cat-file --batch calls = %d, want 1\ncalls:\n%s", batchCount, strings.Join(lines, "\n"))
	}

	hashBlobCount := countGitHashBlobCalls(lines)
	if hashBlobCount != 0 {
		t.Fatalf("ReadRecord() hash-form cat-file blob calls = %d, want 0\ncalls:\n%s", hashBlobCount, strings.Join(lines, "\n"))
	}

	if len(lines) > checkpointCReadRecordCallCeiling {
		t.Fatalf("ReadRecord() git call count = %d, want <= %d\ncalls:\n%s", len(lines), checkpointCReadRecordCallCeiling, strings.Join(lines, "\n"))
	}
}

func TestCheckpointCReadFileAtPathRespectsCallCeiling(t *testing.T) {
	harness := newCheckpointCGitHarness(t)
	ctx := seedCheckpointCRecordFixture(t)

	recordID := types.NewSessionID().String()
	writeResult, err := WriteRecord(ctx, WriteRequest{
		Collection: "sessions",
		RecordID:   recordID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-c"}`)},
		},
	})
	if err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}

	warmVersionGateThenClearLog(t, harness, ctx)

	if _, err := ReadFileAtPath(ctx, pathpkg.Join(writeResult.RecordRoot, "metadata.json")); err != nil {
		t.Fatalf("ReadFileAtPath() error = %v", err)
	}

	lines := harness.readLogLines(t)
	if len(lines) > checkpointCReadFileCallCeiling {
		t.Fatalf("ReadFileAtPath() git call count = %d, want <= %d\ncalls:\n%s", len(lines), checkpointCReadFileCallCeiling, strings.Join(lines, "\n"))
	}
}

func TestCheckpointCMalformedBatchMapsToMalformedTree(t *testing.T) {
	harness := newCheckpointCGitHarness(t)
	ctx := seedCheckpointCRecordFixture(t)

	recordID := types.NewSessionID().String()
	if _, err := WriteRecord(ctx, WriteRequest{
		Collection: "sessions",
		RecordID:   recordID,
		Files: []RecordFile{
			{Path: "metadata.json", Content: []byte(`{"source":"checkpoint-c"}`)},
		},
	}); err != nil {
		t.Fatalf("WriteRecord() error = %v", err)
	}

	warmVersionGateThenClearLog(t, harness, ctx)
	t.Setenv("OPAX_GIT_MALFORMED_BATCH", "1")

	_, err := ReadRecord(ctx, "sessions", recordID)
	if !errors.Is(err, ErrMalformedTree) {
		t.Fatalf("ReadRecord() malformed batch error = %v, want ErrMalformedTree", err)
	}
}

func newCheckpointCGitHarness(t *testing.T) checkpointCGitHarness {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("checkpoint C git-wrapper harness is unix-only")
	}

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary not available")
	}

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "git-calls.log")
	wrapperPath := filepath.Join(tmp, "git-wrapper.sh")

	script := "#!/bin/sh\n" +
		"set -eu\n" +
		": \"${OPAX_GIT_REAL_BIN:?}\"\n" +
		": \"${OPAX_GIT_CALL_LOG:?}\"\n" +
		"printf '%s\\n' \"$*\" >> \"$OPAX_GIT_CALL_LOG\"\n" +
		"is_batch=0\n" +
		"prev=''\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"cat-file\" ] && [ \"$arg\" = \"--batch\" ]; then\n" +
		"    is_batch=1\n" +
		"    break\n" +
		"  fi\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"if [ \"${OPAX_GIT_MALFORMED_BATCH:-0}\" = \"1\" ] && [ \"$is_batch\" = \"1\" ]; then\n" +
		"  printf 'malformed-header\\n'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exec \"$OPAX_GIT_REAL_BIN\" \"$@\"\n"

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
	t.Setenv("OPAX_GIT_MALFORMED_BATCH", "0")
	t.Setenv(gitBinaryOverrideEnv, wrapperPath)

	return checkpointCGitHarness{logPath: logPath}
}

func seedCheckpointCRecordFixture(t *testing.T) *RepoContext {
	t.Helper()

	repoRoot := initGitRepoCompat(t)
	writeFileCompat(t, repoRoot, "README.md", "checkpoint c\n")
	runGitCompat(t, repoRoot, "add", "README.md")
	runGitCompat(t, repoRoot, "commit", "-m", "initial")

	ctx := mustDiscoverRepoCompat(t, repoRoot)
	if _, err := EnsureOpaxBranch(ctx); err != nil {
		t.Fatalf("EnsureOpaxBranch() error = %v", err)
	}

	return ctx
}

func warmVersionGateThenClearLog(t *testing.T, harness checkpointCGitHarness, ctx *RepoContext) {
	t.Helper()

	if _, err := openRepoFromContext(ctx); err != nil {
		t.Fatalf("openRepoFromContext() error = %v", err)
	}
	harness.clearLog(t)
}

func (h checkpointCGitHarness) clearLog(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(h.logPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", h.logPath, err)
	}
}

func (h checkpointCGitHarness) readLogLines(t *testing.T) []string {
	t.Helper()

	data, err := os.ReadFile(h.logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("ReadFile(%q) error = %v", h.logPath, err)
	}
	return splitNonEmptyLines(data)
}

func countGitCallsWithSequence(lines []string, first, second string) int {
	count := 0
	for _, line := range lines {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == first && fields[i+1] == second {
				count++
				break
			}
		}
	}
	return count
}

func countGitHashBlobCalls(lines []string) int {
	count := 0
	for _, line := range lines {
		fields := strings.Fields(line)
		for i := 0; i+2 < len(fields); i++ {
			if fields[i] == "cat-file" && fields[i+1] == "blob" && isCanonicalHash(fields[i+2]) {
				count++
				break
			}
		}
	}
	return count
}
