package git

import (
	"fmt"
	"strings"

	"github.com/opax-sh/opax/internal/types"
)

const saveTrailerKey = "Opax-Save"

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
	return parseSaveTrailerWithGit(nil, message)
}

// ParseSaveTrailerFromCommit reads and parses one save trailer from a commit object.
func ParseSaveTrailerFromCommit(ctx *RepoContext, commitHash string) (types.SaveID, bool, error) {
	targetHash, err := normalizeCommitHash(commitHash)
	if err != nil {
		return "", false, err
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return "", false, err
	}

	commit, err := backend.readCommitForLookup(targetHash)
	if err != nil {
		return "", false, fmt.Errorf("git: parse save trailer from commit %s: resolve commit: %w", targetHash, err)
	}

	saveID, ok, err := parseSaveTrailerWithGit(ctx, []byte(commit.Message))
	if err != nil {
		return "", false, fmt.Errorf("git: parse save trailer from commit %s: %w", targetHash, err)
	}
	return saveID, ok, nil
}

func normalizeCommitHash(commitHash string) (gitHash, error) {
	hash, err := normalizeHash(commitHash)
	if err != nil {
		return "", fmt.Errorf("git: commit hash %q is invalid: %w", commitHash, err)
	}
	return hash, nil
}

func parseSaveTrailerWithGit(ctx *RepoContext, message []byte) (types.SaveID, bool, error) {
	trailers, err := parseTrailersWithGit(ctx, message)
	if err != nil {
		return "", false, err
	}
	if len(trailers) == 0 {
		return "", false, nil
	}

	var saveID types.SaveID
	found := false
	for _, trailerLine := range trailers {
		token, value, ok := parseGitTrailerLine(trailerLine)
		if !ok {
			return "", false, fmt.Errorf("git: parse save trailer: malformed trailer output line %q", trailerLine)
		}
		if !isSaveTrailerToken(token) {
			continue
		}
		if found {
			return "", false, fmt.Errorf("git: parse save trailer: multiple %s trailers", saveTrailerKey)
		}

		saveID = types.SaveID(value)
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

func rewriteTrailersWithGit(ctx *RepoContext, message []byte, trailer string) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("git: rewrite save trailer: repo context is nil")
	}

	stdout, stderr, err := runGitWithContextCapture(
		ctx,
		message,
		"interpret-trailers",
		"--if-exists", "replace",
		"--if-missing", "add",
		"--trailer", trailer,
	)
	if err != nil {
		return nil, wrapGitStderrError("git: rewrite save trailer", stderr, err)
	}
	return stdout, nil
}

func parseTrailersWithGit(ctx *RepoContext, message []byte) ([]string, error) {
	args := []string{"interpret-trailers", "--parse"}
	if ctx != nil {
		if strings.TrimSpace(ctx.GitDir) == "" {
			return nil, fmt.Errorf("git: parse trailers: git dir is empty")
		}
		if strings.TrimSpace(ctx.WorkTreeRoot) == "" {
			return nil, fmt.Errorf("git: parse trailers: worktree root is empty")
		}
	}

	stdout, stderr, err := runGitWithContextCapture(ctx, message, args...)
	if err != nil {
		return nil, wrapGitStderrError("git: parse trailers", stderr, err)
	}

	output := strings.ReplaceAll(string(stdout), "\r\n", "\n")
	output = strings.TrimSuffix(output, "\n")
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}

func parseGitTrailerLine(line string) (token, value string, ok bool) {
	separator := strings.IndexByte(line, ':')
	if separator <= 0 {
		return "", "", false
	}
	token = strings.TrimSpace(line[:separator])
	if token == "" {
		return "", "", false
	}
	value = strings.TrimSpace(line[separator+1:])
	return token, value, true
}

func isSaveTrailerToken(token string) bool {
	return strings.EqualFold(token, saveTrailerKey)
}
