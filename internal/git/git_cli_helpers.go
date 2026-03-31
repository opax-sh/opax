package git

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

type nativeGitBackend struct {
	ctx *RepoContext
}

type gitCommit struct {
	Hash         plumbing.Hash
	TreeHash     plumbing.Hash
	ParentHashes []plumbing.Hash
	Message      string
}

type gitTreeEntry struct {
	Name string
	Mode string
	Type string
	Hash plumbing.Hash
}

type gitTreePathEntry struct {
	gitTreeEntry
	Path string
}

type gitCommitWriteRequest struct {
	TreeHash       plumbing.Hash
	ParentHashes   []plumbing.Hash
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
	return &nativeGitBackend{ctx: ctx}, nil
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
	stdout, stderr, err := runStandaloneGitCapture(nil, "version")
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return fmt.Errorf("git: check git version: %s: %w", strings.TrimSpace(string(stderr)), err)
		}
		return fmt.Errorf("git: check git version: %w", err)
	}

	version, parseErr := parseGitVersion(string(stdout))
	if parseErr != nil {
		return fmt.Errorf("git: check git version: %w", parseErr)
	}
	minimum, parseErr := parseGitVersion("git version " + gitMinSupportedVersion)
	if parseErr != nil {
		return fmt.Errorf("git: check git version minimum: %w", parseErr)
	}
	if versionLessThan(version, minimum) {
		return fmt.Errorf("git: installed git %s is below minimum supported %s", version, minimum)
	}

	return nil
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
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = b.ctx.WorkTreeRoot
	return cmd
}

func (b *nativeGitBackend) runCapture(stdin []byte, args ...string) ([]byte, []byte, error) {
	cmd := b.command(args...)
	return runCommandCapture(cmd, stdin)
}

func (b *nativeGitBackend) readRef(refName plumbing.ReferenceName) (*plumbing.Reference, error) {
	stdout, stderr, err := b.runCapture(nil, "for-each-ref", "--format=%(objectname)", "--", refName.String())
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return nil, fmt.Errorf("git: read ref %s: %s: %w", refName, strings.TrimSpace(string(stderr)), err)
		}
		return nil, fmt.Errorf("git: read ref %s: %w", refName, err)
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
	return plumbing.NewHashReference(refName, hash), nil
}

func (b *nativeGitBackend) isSymbolicRef(refName plumbing.ReferenceName) (bool, string, error) {
	stdout, stderr, err := b.runCapture(nil, "symbolic-ref", "-q", refName.String())
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

	if strings.TrimSpace(string(stderr)) != "" {
		return false, "", fmt.Errorf("git: resolve symbolic ref %s: %s: %w", refName, strings.TrimSpace(string(stderr)), err)
	}
	return false, "", fmt.Errorf("git: resolve symbolic ref %s: %w", refName, err)
}

func (b *nativeGitBackend) updateRefCAS(refName plumbing.ReferenceName, newHash plumbing.Hash, oldHash *plumbing.Hash) error {
	expectedOld := plumbing.ZeroHash
	if oldHash != nil {
		expectedOld = *oldHash
	}

	_, stderr, err := b.runCapture(
		nil,
		"update-ref",
		refName.String(),
		newHash.String(),
		expectedOld.String(),
	)
	if err == nil {
		return nil
	}

	stderrText := strings.TrimSpace(string(stderr))
	if isGitUpdateRefConflict(stderrText) {
		return errReferenceChanged
	}
	if stderrText != "" {
		return fmt.Errorf("git: update ref %s: %s: %w", refName, stderrText, err)
	}
	return fmt.Errorf("git: update ref %s: %w", refName, err)
}

func (b *nativeGitBackend) ensureCommitExists(hash plumbing.Hash) error {
	typ, err := b.objectType(hash.String())
	if err != nil {
		return err
	}
	if typ != "commit" {
		return fmt.Errorf("git: object %s is %s, want commit", hash, typ)
	}
	return nil
}

func (b *nativeGitBackend) objectType(spec string) (string, error) {
	stdout, stderr, err := b.runCapture(nil, "cat-file", "-t", spec)
	if err != nil {
		stderrText := strings.TrimSpace(string(stderr))
		if isGitObjectNotFound(stderrText) {
			return "", os.ErrNotExist
		}
		if stderrText != "" {
			return "", fmt.Errorf("git: object type %s: %s: %w", spec, stderrText, err)
		}
		return "", fmt.Errorf("git: object type %s: %w", spec, err)
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
		strings.Contains(lower, "path does not exist") ||
		strings.Contains(lower, "does not exist")
}

func (b *nativeGitBackend) readCommit(hash plumbing.Hash) (*gitCommit, error) {
	typ, err := b.objectType(hash.String())
	if err != nil {
		if err == os.ErrNotExist {
			return nil, fmt.Errorf("git: commit %s not found", hash)
		}
		return nil, fmt.Errorf("git: read commit %s type: %w", hash, err)
	}
	if typ != "commit" {
		return nil, fmt.Errorf("git: object %s is %s, want commit", hash, typ)
	}

	stdout, stderr, err := b.runCapture(nil, "show", "-s", "--format=%T%x00%P%x00%B", "--no-patch", hash.String())
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return nil, fmt.Errorf("git: read commit %s: %s: %w", hash, strings.TrimSpace(string(stderr)), err)
		}
		return nil, fmt.Errorf("git: read commit %s: %w", hash, err)
	}

	parts := bytes.SplitN(stdout, []byte{0}, 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("git: read commit %s: unexpected show format", hash)
	}

	treeHash, err := parseHash(strings.TrimSpace(string(parts[0])))
	if err != nil {
		return nil, fmt.Errorf("git: read commit %s tree hash: %w", hash, err)
	}

	var parents []plumbing.Hash
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

func (b *nativeGitBackend) readBlob(hash plumbing.Hash) ([]byte, error) {
	stdout, stderr, err := b.runCapture(nil, "cat-file", "blob", hash.String())
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return nil, fmt.Errorf("git: read blob %s: %s: %w", hash, strings.TrimSpace(string(stderr)), err)
		}
		return nil, fmt.Errorf("git: read blob %s: %w", hash, err)
	}
	return stdout, nil
}

func (b *nativeGitBackend) readBlobAtPath(commitHash plumbing.Hash, path string) ([]byte, error) {
	spec := fmt.Sprintf("%s:%s", commitHash, path)
	stdout, stderr, err := b.runCapture(nil, "cat-file", "blob", spec)
	if err != nil {
		stderrText := strings.TrimSpace(string(stderr))
		if isGitObjectNotFound(stderrText) {
			return nil, os.ErrNotExist
		}
		if stderrText != "" {
			return nil, fmt.Errorf("git: read blob %s: %s: %w", spec, stderrText, err)
		}
		return nil, fmt.Errorf("git: read blob %s: %w", spec, err)
	}
	return stdout, nil
}

func (b *nativeGitBackend) readTree(hash plumbing.Hash) ([]gitTreeEntry, error) {
	stdout, stderr, err := b.runCapture(nil, "ls-tree", "-z", hash.String())
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return nil, fmt.Errorf("git: read tree %s: %s: %w", hash, strings.TrimSpace(string(stderr)), err)
		}
		return nil, fmt.Errorf("git: read tree %s: %w", hash, err)
	}
	entries, parseErr := parseLsTreeEntries(stdout)
	if parseErr != nil {
		return nil, fmt.Errorf("git: read tree %s: %w", hash, parseErr)
	}
	return entries, nil
}

func (b *nativeGitBackend) readTreeRecursive(hash plumbing.Hash) ([]gitTreePathEntry, error) {
	stdout, stderr, err := b.runCapture(nil, "ls-tree", "-r", "-t", "-z", hash.String())
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return nil, fmt.Errorf("git: read tree recursive %s: %s: %w", hash, strings.TrimSpace(string(stderr)), err)
		}
		return nil, fmt.Errorf("git: read tree recursive %s: %w", hash, err)
	}
	entries, parseErr := parseLsTreePathEntries(stdout)
	if parseErr != nil {
		return nil, fmt.Errorf("git: read tree recursive %s: %w", hash, parseErr)
	}
	return entries, nil
}

func (b *nativeGitBackend) readBlobsBatch(hashes []plumbing.Hash) (map[plumbing.Hash][]byte, error) {
	if len(hashes) == 0 {
		return map[plumbing.Hash][]byte{}, nil
	}

	unique := make([]plumbing.Hash, 0, len(hashes))
	seen := make(map[plumbing.Hash]struct{}, len(hashes))
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
		if strings.TrimSpace(string(stderr)) != "" {
			return nil, fmt.Errorf("git: batch read blobs: %s: %w", strings.TrimSpace(string(stderr)), err)
		}
		return nil, fmt.Errorf("git: batch read blobs: %w", err)
	}

	result := make(map[plumbing.Hash][]byte, len(unique))
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

func (b *nativeGitBackend) writeBlob(data []byte) (plumbing.Hash, error) {
	stdout, stderr, err := b.runCapture(data, "hash-object", "-w", "--stdin")
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return plumbing.ZeroHash, fmt.Errorf("git: write blob: %s: %w", strings.TrimSpace(string(stderr)), err)
		}
		return plumbing.ZeroHash, fmt.Errorf("git: write blob: %w", err)
	}
	return parseHash(strings.TrimSpace(string(stdout)))
}

func (b *nativeGitBackend) writeTree(entries []gitTreeEntry) (plumbing.Hash, error) {
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
			return plumbing.ZeroHash, fmt.Errorf("git: write tree entry %q must not contain slash", entry.Name)
		}
		if entry.Name == "" {
			return plumbing.ZeroHash, fmt.Errorf("git: write tree entry name is empty")
		}
		if !plumbing.IsHash(entry.Hash.String()) {
			return plumbing.ZeroHash, fmt.Errorf("git: write tree entry %q has invalid hash %q", entry.Name, entry.Hash)
		}
		if entry.Type != "tree" && entry.Type != "blob" {
			return plumbing.ZeroHash, fmt.Errorf("git: write tree entry %q has unsupported type %q", entry.Name, entry.Type)
		}
		if entry.Mode == "" {
			return plumbing.ZeroHash, fmt.Errorf("git: write tree entry %q mode is empty", entry.Name)
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
		if strings.TrimSpace(string(stderr)) != "" {
			return plumbing.ZeroHash, fmt.Errorf("git: write tree: %s: %w", strings.TrimSpace(string(stderr)), err)
		}
		return plumbing.ZeroHash, fmt.Errorf("git: write tree: %w", err)
	}
	return parseHash(strings.TrimSpace(string(stdout)))
}

func (b *nativeGitBackend) writeCommit(req gitCommitWriteRequest) (plumbing.Hash, error) {
	if req.TreeHash == plumbing.ZeroHash {
		return plumbing.ZeroHash, fmt.Errorf("git: write commit tree hash is zero")
	}
	if strings.TrimSpace(req.Message) == "" {
		return plumbing.ZeroHash, fmt.Errorf("git: write commit message is empty")
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
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+req.AuthorName,
		"GIT_AUTHOR_EMAIL="+req.AuthorEmail,
		"GIT_AUTHOR_DATE="+when.Format(time.RFC3339),
		"GIT_COMMITTER_NAME="+req.CommitterName,
		"GIT_COMMITTER_EMAIL="+req.CommitterEmail,
		"GIT_COMMITTER_DATE="+when.Format(time.RFC3339),
	)

	stdout, stderr, err := runCommandCapture(cmd, nil)
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return plumbing.ZeroHash, fmt.Errorf("git: write commit: %s: %w", strings.TrimSpace(string(stderr)), err)
		}
		return plumbing.ZeroHash, fmt.Errorf("git: write commit: %w", err)
	}
	return parseHash(strings.TrimSpace(string(stdout)))
}

func (b *nativeGitBackend) listRefsByPrefix(prefix string) ([]string, error) {
	stdout, stderr, err := b.runCapture(nil, "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		if strings.TrimSpace(string(stderr)) != "" {
			return nil, fmt.Errorf("git: list refs %s: %s: %w", prefix, strings.TrimSpace(string(stderr)), err)
		}
		return nil, fmt.Errorf("git: list refs %s: %w", prefix, err)
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

func parseHash(raw string) (plumbing.Hash, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if !plumbing.IsHash(trimmed) {
		return plumbing.ZeroHash, fmt.Errorf("invalid hash %q", raw)
	}
	return plumbing.NewHash(trimmed), nil
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
	cmd := exec.Command("git", args...)
	return runCommandCapture(cmd, stdin)
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
