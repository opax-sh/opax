package git

import (
	"fmt"
	"strings"
)

const hashHexLength = 40

const zeroGitHash gitHash = "0000000000000000000000000000000000000000"

type gitHash string

func (h gitHash) String() string {
	return string(h)
}

func (h gitHash) IsZero() bool {
	return h == zeroGitHash
}

type gitRef struct {
	name string
	hash gitHash
}

func normalizeHash(raw string) (gitHash, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if len(trimmed) != hashHexLength {
		return "", fmt.Errorf("git: invalid hash %q: %w", raw, ErrInvalidHash)
	}
	for i := 0; i < len(trimmed); i++ {
		if !isLowerHex(trimmed[i]) {
			return "", fmt.Errorf("git: invalid hash %q: %w", raw, ErrInvalidHash)
		}
	}
	return gitHash(trimmed), nil
}

func isCanonicalHash(s string) bool {
	if len(s) != hashHexLength {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isLowerHex(s[i]) {
			return false
		}
	}
	return true
}
