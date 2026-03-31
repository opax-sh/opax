package git

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/opax-sh/opax/internal/types"
)

const (
	saveTrailerKey          = "Opax-Save"
	defaultGitCommentPrefix = "#"
)

type trailerEntry struct {
	Token string
	Lines []string
}

// AllocateSaveID returns a freshly generated save ID for trailer insertion.
func AllocateSaveID() types.SaveID {
	return types.NewSaveID()
}

// UpsertSaveTrailer inserts one canonical save trailer into a commit message.
func UpsertSaveTrailer(ctx *RepoContext, message []byte) ([]byte, types.SaveID, error) {
	lines, hasTrailingNewline := splitMessageLines(message)
	commentPrefix, err := resolveGitCommentPrefix(ctx, lines)
	if err != nil {
		return nil, "", err
	}

	contentLines, commentBlock := splitTrailingCommentBlock(lines, commentPrefix)
	contentLines = trimTrailingBlankLines(contentLines)

	bodyLines, existingTrailers := splitTrailerBlock(contentLines)
	filteredTrailers := make([]trailerEntry, 0, len(existingTrailers)+1)
	for _, entry := range existingTrailers {
		if isSaveTrailerToken(entry.Token) {
			continue
		}
		filteredTrailers = append(filteredTrailers, entry)
	}

	saveID := AllocateSaveID()
	filteredTrailers = append(filteredTrailers, trailerEntry{
		Token: saveTrailerKey,
		Lines: []string{fmt.Sprintf("%s: %s", saveTrailerKey, saveID)},
	})

	var out []string
	out = append(out, bodyLines...)
	if len(bodyLines) > 0 && len(filteredTrailers) > 0 {
		out = append(out, "")
	}
	for _, entry := range filteredTrailers {
		out = append(out, entry.Lines...)
	}
	if len(commentBlock) > 0 {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, commentBlock...)
	}

	return joinMessageLines(out, hasTrailingNewline), saveID, nil
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

func resolveGitCommentPrefix(ctx *RepoContext, lines []string) (string, error) {
	repo, err := openRepoFromContext(ctx)
	if err != nil {
		return "", err
	}

	cfg, err := repo.Config()
	if err != nil {
		return "", fmt.Errorf("git: read repository config: %w", err)
	}

	commentPrefix := strings.TrimSpace(cfg.Core.CommentChar)
	switch {
	case commentPrefix == "":
		return defaultGitCommentPrefix, nil
	case strings.EqualFold(commentPrefix, "auto"):
		if inferredPrefix := inferAutoCommentPrefix(lines); inferredPrefix != "" {
			return inferredPrefix, nil
		}
		return defaultGitCommentPrefix, nil
	default:
		return commentPrefix, nil
	}
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

func joinMessageLines(lines []string, trailingNewline bool) []byte {
	if len(lines) == 0 {
		if trailingNewline {
			return []byte("\n")
		}
		return nil
	}

	text := strings.Join(lines, "\n")
	if trailingNewline {
		text += "\n"
	}
	return []byte(text)
}

func splitTrailingCommentBlock(lines []string, commentPrefix string) ([]string, []string) {
	if len(lines) == 0 {
		return nil, nil
	}

	end := len(lines) - 1
	for end >= 0 && strings.TrimSpace(lines[end]) == "" {
		end--
	}
	if end < 0 {
		return nil, nil
	}

	start := end
	for start >= 0 && isCommentLine(lines[start], commentPrefix) {
		start--
	}
	if start == end {
		return lines[:end+1], nil
	}

	content := trimTrailingBlankLines(lines[:start+1])
	commentBlock := append([]string(nil), lines[start+1:]...)
	return content, commentBlock
}

func isCommentLine(line, commentPrefix string) bool {
	return commentPrefix != "" && strings.HasPrefix(line, commentPrefix)
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

func inferAutoCommentPrefix(lines []string) string {
	lines = trimTrailingBlankLines(lines)
	if len(lines) == 0 {
		return ""
	}

	start := len(lines) - 1
	for start >= 0 && strings.TrimSpace(lines[start]) != "" {
		start--
	}

	commentBlock := lines[start+1:]
	prefix := sharedCommentPrefix(commentBlock)
	if prefix == "" {
		return ""
	}
	// Fail closed for core.commentChar=auto. Only preserve a trailing block when
	// it clearly matches Git's template/scissor text to avoid moving user body
	// paragraphs below the trailer.
	if looksLikeGitTemplateBlock(commentBlock, prefix) {
		return prefix
	}
	return ""
}

func sharedCommentPrefix(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	prefix := commentPrefixToken(lines[0])
	if prefix == "" {
		return ""
	}

	for _, line := range lines[1:] {
		if commentPrefixToken(line) != prefix {
			return ""
		}
	}
	return prefix
}

func commentPrefixToken(line string) string {
	token := line
	if whitespaceIndex := strings.IndexFunc(line, unicode.IsSpace); whitespaceIndex >= 0 {
		token = line[:whitespaceIndex]
	}
	if token == "" {
		return ""
	}
	for _, r := range token {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return ""
		}
	}
	return token
}

func looksLikeGitTemplateBlock(lines []string, prefix string) bool {
	for _, line := range lines {
		body := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		switch {
		case strings.Contains(body, ">8"):
			return true
		case strings.HasPrefix(body, "Please enter the commit message"):
			return true
		case strings.HasPrefix(body, "Do not modify or remove the line above."):
			return true
		case strings.HasPrefix(body, "Everything below it will be ignored."):
			return true
		case strings.HasPrefix(body, "On branch "):
			return true
		case strings.HasPrefix(body, "Changes to be committed:"):
			return true
		case strings.HasPrefix(body, "Changes not staged for commit:"):
			return true
		case strings.HasPrefix(body, "Untracked files:"):
			return true
		case strings.HasPrefix(body, "diff --git "):
			return true
		}
	}
	return false
}
