package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/opax-sh/opax/internal/types"
)

const saveTrailerKey = "Opax-Save"

type trailerEntry struct {
	Token string
	Lines []string
}

// AllocateSaveID returns a freshly generated save ID for trailer insertion.
func AllocateSaveID() types.SaveID {
	return types.NewSaveID()
}

// UpsertSaveTrailer rewrites a commit message using native Git trailer semantics.
// This helper is intended for hook-time message mutation, where Git's own config
// and trailer rules should be authoritative.
func UpsertSaveTrailer(ctx *RepoContext, message []byte) ([]byte, types.SaveID, error) {
	if ctx == nil {
		return nil, "", fmt.Errorf("git: upsert save trailer: repo context is nil")
	}
	if strings.TrimSpace(ctx.GitDir) == "" {
		return nil, "", fmt.Errorf("git: upsert save trailer: git dir is empty")
	}
	if strings.TrimSpace(ctx.WorkTreeRoot) == "" {
		return nil, "", fmt.Errorf("git: upsert save trailer: worktree root is empty")
	}

	saveID := AllocateSaveID()
	updated, err := rewriteTrailersWithGit(ctx, message, fmt.Sprintf("%s: %s", saveTrailerKey, saveID))
	if err != nil {
		return nil, "", err
	}
	return updated, saveID, nil
}

// ParseSaveTrailer extracts one valid save trailer from a commit message.
func ParseSaveTrailer(message []byte) (types.SaveID, bool, error) {
	lines, _ := splitMessageLines(message)
	lines = trimTrailingBlankLines(lines)

	_, trailers := splitTrailerBlock(lines)
	if len(trailers) == 0 {
		return "", false, nil
	}

	var saveID types.SaveID
	found := false
	for _, entry := range trailers {
		if !isSaveTrailerToken(entry.Token) {
			continue
		}
		if found {
			return "", false, fmt.Errorf("git: parse save trailer: multiple %s trailers", saveTrailerKey)
		}

		saveID = types.SaveID(strings.TrimSpace(entry.Value()))
		if err := saveID.Validate(); err != nil {
			return "", false, fmt.Errorf("git: parse save trailer: invalid %s value: %w", saveTrailerKey, err)
		}
		found = true
	}

	if !found {
		return "", false, nil
	}
	return saveID, true, nil
}

// ParseSaveTrailerFromCommit reads and parses one save trailer from a commit object.
func ParseSaveTrailerFromCommit(ctx *RepoContext, commitHash string) (types.SaveID, bool, error) {
	targetHash, err := normalizeCommitHash(commitHash)
	if err != nil {
		return "", false, err
	}

	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return "", false, err
	}

	commit, err := repo.CommitObject(targetHash)
	if err != nil {
		return "", false, fmt.Errorf("git: parse save trailer from commit %s: resolve commit: %w", targetHash, err)
	}

	saveID, ok, err := ParseSaveTrailer([]byte(commit.Message))
	if err != nil {
		return "", false, fmt.Errorf("git: parse save trailer from commit %s: %w", targetHash, err)
	}
	return saveID, ok, nil
}

func normalizeCommitHash(commitHash string) (plumbing.Hash, error) {
	trimmedHash := strings.TrimSpace(strings.ToLower(commitHash))
	if !plumbing.IsHash(trimmedHash) {
		return plumbing.ZeroHash, fmt.Errorf("git: commit hash %q is invalid", commitHash)
	}
	return plumbing.NewHash(trimmedHash), nil
}

func splitMessageLines(message []byte) ([]string, bool) {
	if len(message) == 0 {
		return nil, false
	}

	text := strings.ReplaceAll(string(message), "\r\n", "\n")
	hasTrailingNewline := strings.HasSuffix(text, "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil, hasTrailingNewline
	}

	return strings.Split(text, "\n"), hasTrailingNewline
}

func rewriteTrailersWithGit(ctx *RepoContext, message []byte, trailer string) ([]byte, error) {
	cmd := exec.Command(
		"git",
		"--git-dir", ctx.GitDir,
		"--work-tree", ctx.WorkTreeRoot,
		"interpret-trailers",
		"--if-exists", "replace",
		"--if-missing", "add",
		"--trailer", trailer,
	)
	cmd.Stdin = bytes.NewReader(message)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("git: rewrite save trailer: %s: %w", strings.TrimSpace(stderr.String()), err)
		}
		return nil, fmt.Errorf("git: rewrite save trailer: %w", err)
	}
	return stdout.Bytes(), nil
}

func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return append([]string(nil), lines[:end]...)
}

func splitTrailerBlock(lines []string) ([]string, []trailerEntry) {
	if len(lines) == 0 {
		return nil, nil
	}

	start := len(lines) - 1
	for start >= 0 && strings.TrimSpace(lines[start]) != "" {
		start--
	}
	if start < 0 {
		return append([]string(nil), lines...), nil
	}

	candidate := lines[start+1:]
	entries, ok := parseTrailerEntries(candidate)
	if !ok {
		return append([]string(nil), lines...), nil
	}

	body := trimTrailingBlankLines(lines[:start+1])
	return body, entries
}

func parseTrailerEntries(lines []string) ([]trailerEntry, bool) {
	if len(lines) == 0 {
		return nil, false
	}

	entries := make([]trailerEntry, 0, len(lines))
	for i := 0; i < len(lines); {
		token, ok := parseTrailerToken(lines[i])
		if !ok {
			return nil, false
		}

		entry := trailerEntry{
			Token: token,
			Lines: []string{lines[i]},
		}
		i++
		for i < len(lines) && isTrailerContinuationLine(lines[i]) {
			entry.Lines = append(entry.Lines, lines[i])
			i++
		}
		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return nil, false
	}
	return entries, true
}

func parseTrailerToken(line string) (string, bool) {
	if line == "" || isTrailerContinuationLine(line) {
		return "", false
	}

	separator := strings.IndexByte(line, ':')
	if separator <= 0 {
		return "", false
	}

	token := strings.TrimSpace(line[:separator])
	if token == "" {
		return "", false
	}
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return "", false
		}
	}

	return token, true
}

func isTrailerContinuationLine(line string) bool {
	return strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
}

func isSaveTrailerToken(token string) bool {
	return strings.EqualFold(token, saveTrailerKey)
}

func (t trailerEntry) Value() string {
	if len(t.Lines) == 0 {
		return ""
	}

	first := t.Lines[0]
	separator := strings.IndexByte(first, ':')
	if separator < 0 {
		return ""
	}

	valueLines := []string{strings.TrimSpace(first[separator+1:])}
	for _, line := range t.Lines[1:] {
		valueLines = append(valueLines, strings.TrimSpace(line))
	}
	return strings.Join(valueLines, "\n")
}
