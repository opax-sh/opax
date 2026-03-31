package git

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

func publishRefWithRetry(
	ctx *RepoContext,
	refName plumbing.ReferenceName,
	build refPublishBuilder,
) (*plumbing.Reference, error) {
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
		if nextRef.Name() != refName {
			return nil, fmt.Errorf("git: publish ref %s: builder returned %s", refName, nextRef.Name())
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

func publishReference(backend *nativeGitBackend, nextRef, currentRef *plumbing.Reference) error {
	if nextRef == nil {
		return fmt.Errorf("git: publish ref: reference is nil")
	}
	if nextRef.Type() != plumbing.HashReference {
		return fmt.Errorf("git: publish ref %s: unsupported reference type %s", nextRef.Name(), nextRef.Type())
	}

	var oldHash *plumbing.Hash
	if currentRef != nil {
		hash := currentRef.Hash()
		oldHash = &hash
	}
	return backend.updateRefCAS(nextRef.Name(), nextRef.Hash(), oldHash)
}

func createReferenceIfAbsent(ctx *RepoContext, ref *plumbing.Reference) error {
	if ref == nil {
		return fmt.Errorf("git: publish ref: reference is nil")
	}
	if ref.Type() != plumbing.HashReference {
		return fmt.Errorf("git: publish ref %s: unsupported reference type %s", ref.Name(), ref.Type())
	}

	backend, err := openRepoFromContext(ctx)
	if err != nil {
		return err
	}
	zero := plumbing.ZeroHash
	return backend.updateRefCAS(ref.Name(), ref.Hash(), &zero)
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
