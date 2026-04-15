package git

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type nativeGitBackend struct {
	ctx     *RepoContext
	runtime *gitCommandRuntime
}

type gitCommit struct {
	Hash         gitHash
	TreeHash     gitHash
	ParentHashes []gitHash
	Message      string
}

type gitTreeEntry struct {
	Name string
	Mode string
	Type string
	Hash gitHash
}

type gitTreePathEntry struct {
	gitTreeEntry
	Path string
}

type gitCommitWriteRequest struct {
	TreeHash       gitHash
	ParentHashes   []gitHash
	Message        string
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	When           time.Time
}

func newNativeGitBackend(ctx *RepoContext) (*nativeGitBackend, error) {
	if ctx == nil {
		return nil, fmt.Errorf("git: repo context is nil")
	}
	if strings.TrimSpace(ctx.GitDir) == "" {
		return nil, fmt.Errorf("git: git dir is empty")
	}
	if strings.TrimSpace(ctx.WorkTreeRoot) == "" {
		return nil, fmt.Errorf("git: worktree root is empty")
	}
	if err := ensureExistingDir(ctx.GitDir, "git dir"); err != nil {
		return nil, err
	}
	if err := ensureExistingDir(ctx.WorkTreeRoot, "worktree root"); err != nil {
		return nil, err
	}
	runtime, err := newGitCommandRuntime()
	if err != nil {
		return nil, err
	}
	return &nativeGitBackend{
		ctx:     ctx,
		runtime: runtime,
	}, nil
}

func ensureExistingDir(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("git: %s does not exist: %s", label, path)
		}
		return fmt.Errorf("git: stat %s %s: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("git: %s is not a directory: %s", label, path)
	}
	return nil
}

func (b *nativeGitBackend) ensureSupportedGitVersion() error {
	if b == nil || b.runtime == nil {
		return fmt.Errorf("git: runtime is nil")
	}
	return ensureSupportedGitVersion(b.runtime.binaryPath)
}

func parseGitVersion(raw string) (gitVersion, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return gitVersion{}, fmt.Errorf("empty git version output")
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return gitVersion{}, fmt.Errorf("unexpected git version output %q", trimmed)
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return gitVersion{}, fmt.Errorf("unexpected git version token %q", fields[2])
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return gitVersion{}, fmt.Errorf("parse git major version %q: %w", parts[0], err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return gitVersion{}, fmt.Errorf("parse git minor version %q: %w", parts[1], err)
	}
	patch := 0
	if len(parts) > 2 {
		patchToken := parts[2]
		for i := 0; i < len(patchToken); i++ {
			if patchToken[i] < '0' || patchToken[i] > '9' {
				patchToken = patchToken[:i]
				break
			}
		}
		if patchToken != "" {
			parsedPatch, err := strconv.Atoi(patchToken)
			if err != nil {
				return gitVersion{}, fmt.Errorf("parse git patch version %q: %w", patchToken, err)
			}
			patch = parsedPatch
		}
	}
	return gitVersion{major: major, minor: minor, patch: patch}, nil
}

type gitVersion struct {
	major int
	minor int
	patch int
}

func (v gitVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func versionLessThan(left, right gitVersion) bool {
	if left.major != right.major {
		return left.major < right.major
	}
	if left.minor != right.minor {
		return left.minor < right.minor
	}
	return left.patch < right.patch
}

func (b *nativeGitBackend) command(args ...string) *exec.Cmd {
	gitArgs := append([]string{"--git-dir", b.ctx.GitDir, "--work-tree", b.ctx.WorkTreeRoot}, args...)
	return b.runtime.command(b.ctx.WorkTreeRoot, nil, gitArgs...)
}

func (b *nativeGitBackend) runCapture(stdin []byte, args ...string) ([]byte, []byte, error) {
	cmd := b.command(args...)
	return runCommandCapture(cmd, stdin)
}

func (b *nativeGitBackend) readRef(refName string) (*gitRef, error) {
	stdout, stderr, err := b.runCapture(nil, "for-each-ref", "--format=%(objectname)", "--", refName)
	if err != nil {
		return nil, wrapGitStderrError(fmt.Sprintf("git: read ref %s", refName), stderr, err)
	}

	lines := splitNonEmptyLines(stdout)
	if len(lines) == 0 {
		return nil, nil
	}
	if len(lines) != 1 {
		return nil, fmt.Errorf("git: read ref %s: unexpected result count %d", refName, len(lines))
	}

	hash, err := parseHash(lines[0])
	if err != nil {
		return nil, fmt.Errorf("git: read ref %s: %w", refName, err)
	}
	return &gitRef{name: refName, hash: hash}, nil
}

func (b *nativeGitBackend) isSymbolicRef(refName string) (bool, string, error) {
	stdout, stderr, err := b.runCapture(nil, "symbolic-ref", "-q", refName)
	if err == nil {
		target := strings.TrimSpace(string(stdout))
		if target == "" {
			return false, "", nil
		}
		return true, target, nil
	}

	exitErr, ok := err.(*exec.ExitError)
	if ok && exitErr.ExitCode() == 1 {
		return false, "", nil
	}

	return false, "", wrapGitStderrError(fmt.Sprintf("git: resolve symbolic ref %s", refName), stderr, err)
}

type refCASOutcome uint8

const (
	refCASOutcomeUnknown refCASOutcome = iota
	refCASOutcomeApplied
	refCASOutcomeConflict
)

func (b *nativeGitBackend) updateRefCAS(refName string, newHash gitHash, oldHash *gitHash) error {
	expectedOld := zeroGitHash
	if oldHash != nil {
		expectedOld = *oldHash
	}

	_, stderr, err := b.runCapture(
		nil,
		"update-ref",
		refName,
		newHash.String(),
		expectedOld.String(),
	)
	if err == nil {
		return nil
	}

	outcome, probeErr := b.probeUpdateRefCASOutcome(refName, newHash, expectedOld)
	if probeErr == nil {
		switch outcome {
		case refCASOutcomeApplied:
			return nil
		case refCASOutcomeConflict:
			return errReferenceChanged
		case refCASOutcomeUnknown:
			return wrapGitStderrError(
				fmt.Sprintf("git: update ref %s", refName),
				stderr,
				fmt.Errorf("%w: post-condition probe inconclusive", errReferenceCASUnknown),
			)
		}
	}

	stderrText := normalizeGitStderr(stderr)
	if isGitUpdateRefConflict(stderrText) {
		return errReferenceChanged
	}
	if probeErr != nil {
		return wrapGitStderrError(
			fmt.Sprintf("git: update ref %s", refName),
			stderr,
			fmt.Errorf("%w: post-condition probe failed: %v", errReferenceCASUnknown, probeErr),
		)
	}
	return wrapGitStderrError(
		fmt.Sprintf("git: update ref %s", refName),
		stderr,
		fmt.Errorf("%w", errReferenceCASUnknown),
	)
}

func (b *nativeGitBackend) probeUpdateRefCASOutcome(
	refName string,
	newHash gitHash,
	expectedOld gitHash,
) (refCASOutcome, error) {
	currentRef, err := b.readRef(refName)
	if err != nil {
		return refCASOutcomeUnknown, err
	}
	return classifyRefCASOutcome(currentRef, newHash, expectedOld), nil
}

func classifyRefCASOutcome(currentRef *gitRef, newHash, expectedOld gitHash) refCASOutcome {
	if currentRef != nil && currentRef.hash == newHash {
		return refCASOutcomeApplied
	}

	if expectedOld == zeroGitHash {
		if currentRef != nil {
			return refCASOutcomeConflict
		}
		return refCASOutcomeUnknown
	}

	if currentRef == nil || currentRef.hash != expectedOld {
		return refCASOutcomeConflict
	}
	return refCASOutcomeUnknown
}

func (b *nativeGitBackend) ensureCommitExists(hash gitHash) error {
	typ, err := b.objectType(hash.String())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("git: commit %s not found: %w", hash, ErrCommitNotFound)
		}
		return err
	}
	if typ != "commit" {
		return fmt.Errorf("git: object %s is %s, want commit", hash, typ)
	}
	return nil
}

func (b *nativeGitBackend) readCommitForLookup(hash gitHash) (*gitCommit, error) {
	commit, err := b.readCommit(hash)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("git: commit %s not found: %w", hash, ErrCommitNotFound)
		}
		return nil, err
	}
	return commit, nil
}

func (b *nativeGitBackend) objectType(spec string) (string, error) {
	stdout, stderr, err := b.runCapture(nil, "cat-file", "-t", spec)
	if err != nil {
		stderrText := normalizeGitStderr(stderr)
		if isGitObjectNotFound(stderrText) {
			return "", os.ErrNotExist
		}
		return "", wrapGitStderrError(fmt.Sprintf("git: object type %s", spec), stderr, err)
	}

	typ := strings.TrimSpace(string(stdout))
	if typ == "" {
		return "", fmt.Errorf("git: object type %s: empty output", spec)
	}
	return typ, nil
}

func isGitObjectNotFound(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "not a valid object name") ||
		strings.Contains(lower, "invalid object name") ||
		strings.Contains(lower, "unknown revision") ||
		strings.Contains(lower, "could not get object info") ||
		strings.Contains(lower, "path does not exist") ||
		strings.Contains(lower, "does not exist")
}

func (b *nativeGitBackend) readCommit(hash gitHash) (*gitCommit, error) {
	typ, err := b.objectType(hash.String())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("git: commit %s not found: %w", hash, os.ErrNotExist)
		}
		return nil, fmt.Errorf("git: read commit %s type: %w", hash, err)
	}
	if typ != "commit" {
		return nil, fmt.Errorf("git: object %s is %s, want commit", hash, typ)
	}

	stdout, stderr, err := b.runCapture(nil, "show", "-s", "--format=%T%x00%P%x00%B", "--no-patch", hash.String())
	if err != nil {
		return nil, wrapGitStderrError(fmt.Sprintf("git: read commit %s", hash), stderr, err)
	}

	parts := bytes.SplitN(stdout, []byte{0}, 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("git: read commit %s: unexpected show format", hash)
	}

	treeHash, err := parseHash(strings.TrimSpace(string(parts[0])))
	if err != nil {
		return nil, fmt.Errorf("git: read commit %s tree hash: %w", hash, err)
	}

	var parents []gitHash
	parentRaw := strings.TrimSpace(string(parts[1]))
	if parentRaw != "" {
		for _, token := range strings.Fields(parentRaw) {
			parentHash, err := parseHash(token)
			if err != nil {
				return nil, fmt.Errorf("git: read commit %s parent hash: %w", hash, err)
			}
			parents = append(parents, parentHash)
		}
	}

	message := strings.TrimSuffix(string(parts[2]), "\n")

	return &gitCommit{
		Hash:         hash,
		TreeHash:     treeHash,
		ParentHashes: parents,
		Message:      message,
	}, nil
}

func (b *nativeGitBackend) readBlob(hash gitHash) ([]byte, error) {
	stdout, stderr, err := b.runCapture(nil, "cat-file", "blob", hash.String())
	if err != nil {
		return nil, wrapGitStderrError(fmt.Sprintf("git: read blob %s", hash), stderr, err)
	}
	return stdout, nil
}

func (b *nativeGitBackend) readBlobAtPath(commitHash gitHash, path string) ([]byte, error) {
	spec := fmt.Sprintf("%s:%s", commitHash, path)
	stdout, stderr, err := b.runCapture(nil, "cat-file", "blob", spec)
	if err != nil {
		stderrText := normalizeGitStderr(stderr)
		if isGitObjectNotFound(stderrText) {
			return nil, os.ErrNotExist
		}
		return nil, wrapGitStderrError(fmt.Sprintf("git: read blob %s", spec), stderr, err)
	}
	return stdout, nil
}

func (b *nativeGitBackend) readTree(hash gitHash) ([]gitTreeEntry, error) {
	stdout, stderr, err := b.runCapture(nil, "ls-tree", "-z", hash.String())
	if err != nil {
		return nil, wrapGitStderrError(fmt.Sprintf("git: read tree %s", hash), stderr, err)
	}
	entries, parseErr := parseLsTreeEntries(stdout)
	if parseErr != nil {
		return nil, fmt.Errorf("git: read tree %s: %w", hash, parseErr)
	}
	return entries, nil
}

func (b *nativeGitBackend) readTreeRecursive(hash gitHash) ([]gitTreePathEntry, error) {
	stdout, stderr, err := b.runCapture(nil, "ls-tree", "-r", "-t", "-z", hash.String())
	if err != nil {
		return nil, wrapGitStderrError(fmt.Sprintf("git: read tree recursive %s", hash), stderr, err)
	}
	entries, parseErr := parseLsTreePathEntries(stdout)
	if parseErr != nil {
		return nil, fmt.Errorf("git: read tree recursive %s: %w", hash, parseErr)
	}
	return entries, nil
}

func (b *nativeGitBackend) readBlobsBatch(hashes []gitHash) (map[gitHash][]byte, error) {
	if len(hashes) == 0 {
		return map[gitHash][]byte{}, nil
	}

	unique := make([]gitHash, 0, len(hashes))
	seen := make(map[gitHash]struct{}, len(hashes))
	for _, hash := range hashes {
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		unique = append(unique, hash)
	}

	var stdin bytes.Buffer
	for _, hash := range unique {
		stdin.WriteString(hash.String())
		stdin.WriteByte('\n')
	}

	stdout, stderr, err := b.runCapture(stdin.Bytes(), "cat-file", "--batch")
	if err != nil {
		return nil, wrapGitStderrError("git: batch read blobs", stderr, err)
	}

	result := make(map[gitHash][]byte, len(unique))
	reader := bufio.NewReader(bytes.NewReader(stdout))
	for _, wantHash := range unique {
		header, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil, fmt.Errorf("git: batch read blobs %s header: %w", wantHash, readErr)
		}

		header = strings.TrimSuffix(header, "\n")
		parts := strings.Split(header, " ")
		if len(parts) < 3 {
			return nil, fmt.Errorf("git: batch read blobs %s malformed header %q", wantHash, header)
		}

		actualHash, err := parseHash(parts[0])
		if err != nil {
			return nil, fmt.Errorf("git: batch read blobs %s parse header hash: %w", wantHash, err)
		}
		if actualHash != wantHash {
			return nil, fmt.Errorf("git: batch read blobs expected %s, got %s", wantHash, actualHash)
		}
		if parts[1] != "blob" {
			return nil, fmt.Errorf("git: batch read blobs %s type = %s, want blob", wantHash, parts[1])
		}

		size, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("git: batch read blobs %s parse size %q: %w", wantHash, parts[2], err)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(reader, content); err != nil {
			return nil, fmt.Errorf("git: batch read blobs %s content: %w", wantHash, err)
		}
		if term, err := reader.ReadByte(); err != nil {
			return nil, fmt.Errorf("git: batch read blobs %s trailing newline: %w", wantHash, err)
		} else if term != '\n' {
			return nil, fmt.Errorf("git: batch read blobs %s malformed trailing delimiter", wantHash)
		}

		result[wantHash] = content
	}

	return result, nil
}

func (b *nativeGitBackend) writeBlob(data []byte) (gitHash, error) {
	stdout, stderr, err := b.runCapture(data, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", wrapGitStderrError("git: write blob", stderr, err)
	}
	return parseHash(strings.TrimSpace(string(stdout)))
}

func (b *nativeGitBackend) writeTree(entries []gitTreeEntry) (gitHash, error) {
	normalized := append([]gitTreeEntry(nil), entries...)
	sort.Slice(normalized, func(i, j int) bool {
		left := normalized[i].Name
		right := normalized[j].Name
		if normalized[i].Type == "tree" {
			left += "/"
		}
		if normalized[j].Type == "tree" {
			right += "/"
		}
		return left < right
	})

	var input bytes.Buffer
	for _, entry := range normalized {
		if strings.Contains(entry.Name, "/") {
			return "", fmt.Errorf("git: write tree entry %q must not contain slash", entry.Name)
		}
		if entry.Name == "" {
			return "", fmt.Errorf("git: write tree entry name is empty")
		}
		if !isCanonicalHash(entry.Hash.String()) {
			return "", fmt.Errorf("git: write tree entry %q has invalid hash %q", entry.Name, entry.Hash)
		}
		if entry.Type != "tree" && entry.Type != "blob" {
			return "", fmt.Errorf("git: write tree entry %q has unsupported type %q", entry.Name, entry.Type)
		}
		if entry.Mode == "" {
			return "", fmt.Errorf("git: write tree entry %q mode is empty", entry.Name)
		}
		input.WriteString(entry.Mode)
		input.WriteByte(' ')
		input.WriteString(entry.Type)
		input.WriteByte(' ')
		input.WriteString(entry.Hash.String())
		input.WriteByte('\t')
		input.WriteString(entry.Name)
		input.WriteByte(0)
	}

	stdout, stderr, err := b.runCapture(input.Bytes(), "mktree", "-z")
	if err != nil {
		return "", wrapGitStderrError("git: write tree", stderr, err)
	}
	return parseHash(strings.TrimSpace(string(stdout)))
}

func (b *nativeGitBackend) writeCommit(req gitCommitWriteRequest) (gitHash, error) {
	if req.TreeHash.IsZero() {
		return "", fmt.Errorf("git: write commit tree hash is zero")
	}
	if strings.TrimSpace(req.Message) == "" {
		return "", fmt.Errorf("git: write commit message is empty")
	}

	when := req.When.UTC()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	if strings.TrimSpace(req.AuthorName) == "" {
		req.AuthorName = opaxAuthorName
	}
	if strings.TrimSpace(req.AuthorEmail) == "" {
		req.AuthorEmail = opaxAuthorEmail
	}
	if strings.TrimSpace(req.CommitterName) == "" {
		req.CommitterName = opaxAuthorName
	}
	if strings.TrimSpace(req.CommitterEmail) == "" {
		req.CommitterEmail = opaxAuthorEmail
	}

	args := []string{"commit-tree", req.TreeHash.String()}
	for _, parent := range req.ParentHashes {
		args = append(args, "-p", parent.String())
	}
	args = append(args, "-m", req.Message)

	cmd := b.command(args...)
	cmd.Env = gitCommandEnv([]string{
		"GIT_AUTHOR_NAME=" + req.AuthorName,
		"GIT_AUTHOR_EMAIL=" + req.AuthorEmail,
		"GIT_AUTHOR_DATE=" + when.Format(time.RFC3339),
		"GIT_COMMITTER_NAME=" + req.CommitterName,
		"GIT_COMMITTER_EMAIL=" + req.CommitterEmail,
		"GIT_COMMITTER_DATE=" + when.Format(time.RFC3339),
	})

	stdout, stderr, err := runCommandCapture(cmd, nil)
	if err != nil {
		return "", wrapGitStderrError("git: write commit", stderr, err)
	}
	return parseHash(strings.TrimSpace(string(stdout)))
}

func (b *nativeGitBackend) listRefsByPrefix(prefix string) ([]string, error) {
	stdout, stderr, err := b.runCapture(nil, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return nil, wrapGitStderrError(fmt.Sprintf("git: list refs %s", prefix), stderr, err)
	}
	return splitNonEmptyLines(stdout), nil
}

func parseLsTreeEntries(raw []byte) ([]gitTreeEntry, error) {
	records := bytes.Split(raw, []byte{0})
	entries := make([]gitTreeEntry, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		entry, _, err := parseLsTreeRecord(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseLsTreePathEntries(raw []byte) ([]gitTreePathEntry, error) {
	records := bytes.Split(raw, []byte{0})
	entries := make([]gitTreePathEntry, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		entry, path, err := parseLsTreeRecord(record)
		if err != nil {
			return nil, err
		}
		entries = append(entries, gitTreePathEntry{gitTreeEntry: entry, Path: path})
	}
	return entries, nil
}

func parseLsTreeRecord(record []byte) (gitTreeEntry, string, error) {
	header, pathRaw, ok := bytes.Cut(record, []byte{'\t'})
	if !ok {
		return gitTreeEntry{}, "", fmt.Errorf("malformed ls-tree record %q", string(record))
	}
	parts := strings.Fields(string(header))
	if len(parts) != 3 {
		return gitTreeEntry{}, "", fmt.Errorf("malformed ls-tree header %q", string(header))
	}
	hash, err := parseHash(parts[2])
	if err != nil {
		return gitTreeEntry{}, "", fmt.Errorf("parse ls-tree hash %q: %w", parts[2], err)
	}
	path := string(pathRaw)
	entry := gitTreeEntry{
		Mode: parts[0],
		Type: parts[1],
		Hash: hash,
	}
	if strings.Contains(path, "/") {
		entry.Name = filepath.Base(path)
	} else {
		entry.Name = path
	}
	return entry, path, nil
}

func parseHash(raw string) (gitHash, error) {
	return normalizeHash(raw)
}

func runCommandCapture(cmd *exec.Cmd, stdin []byte) ([]byte, []byte, error) {
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func runStandaloneGitCapture(stdin []byte, args ...string) ([]byte, []byte, error) {
	runtime, err := newGitCommandRuntime()
	if err != nil {
		return nil, nil, err
	}
	return runtime.runCapture("", stdin, nil, args...)
}

func runStandaloneGitCaptureWithBinary(binaryPath string, stdin []byte, args ...string) ([]byte, []byte, error) {
	runtime := &gitCommandRuntime{binaryPath: binaryPath}
	return runtime.runCapture("", stdin, nil, args...)
}

func runGitWithContextCapture(ctx *RepoContext, stdin []byte, args ...string) ([]byte, []byte, error) {
	if ctx == nil {
		return runStandaloneGitCapture(stdin, args...)
	}
	backend, err := newNativeGitBackend(ctx)
	if err != nil {
		return nil, nil, err
	}
	return backend.runCapture(stdin, args...)
}
