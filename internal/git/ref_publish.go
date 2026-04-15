package git

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func publishRefWithRetry(
	ctx *RepoContext,
	refName string,
	build refPublishBuilder,
) (*gitRef, error) {
	if build == nil {
		return nil, fmt.Errorf("git: publish ref %s: builder is nil", refName)
	}

	var lastErr error
	for attempt := 1; attempt <= maxRefPublishAttempts; attempt++ {
		backend, err := openRepoFromContext(ctx)
		if err != nil {
			return nil, err
		}

		currentRef, err := backend.readRef(refName)
		if err != nil {
			return nil, err
		}

		nextRef, err := build(backend, currentRef)
		if err != nil {
			return nil, err
		}
		if nextRef == nil {
			return nil, fmt.Errorf("git: publish ref %s: builder returned nil reference", refName)
		}
		if nextRef.name != refName {
			return nil, fmt.Errorf("git: publish ref %s: builder returned %s", refName, nextRef.name)
		}

		err = publishReference(backend, nextRef, currentRef)
		if err == nil {
			return nextRef, nil
		}
		if errors.Is(err, errReferenceChanged) {
			lastErr = err
		} else {
			return nil, fmt.Errorf("git: publish ref %s: %w", refName, err)
		}

		if attempt == maxRefPublishAttempts {
			break
		}
		time.Sleep(refPublishBackoff(attempt))
	}

	return nil, fmt.Errorf(
		"git: publish ref %s: retries exhausted after %d attempts: %w",
		refName,
		maxRefPublishAttempts,
		lastErr,
	)
}

func publishReference(backend *nativeGitBackend, nextRef, currentRef *gitRef) error {
	if nextRef == nil {
		return fmt.Errorf("git: publish ref: reference is nil")
	}

	var oldHash *gitHash
	if currentRef != nil {
		hash := currentRef.hash
		oldHash = &hash
	}
	return backend.updateRefCAS(nextRef.name, nextRef.hash, oldHash)
}

func isGitUpdateRefConflict(stderr string) bool {
	lower := strings.ToLower(strings.TrimSpace(stderr))
	return strings.Contains(lower, "cannot lock ref") &&
		(strings.Contains(lower, "reference already exists") ||
			strings.Contains(lower, "expected") ||
			strings.Contains(lower, "is at"))
}

func refPublishBackoff(attempt int) time.Duration {
	if attempt < 1 {
		return refPublishBackoffBase
	}

	delay := refPublishBackoffBase << (attempt - 1)
	if delay > refPublishBackoffCap {
		return refPublishBackoffCap
	}
	return delay
}
