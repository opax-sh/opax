package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ggit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gitstorage "github.com/go-git/go-git/v5/storage"
	fsstorage "github.com/go-git/go-git/v5/storage/filesystem"
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
		repo, err := openRepoFromContext(ctx)
		if err != nil {
			return nil, err
		}

		currentRef, err := repo.Reference(refName, true)
		if err != nil {
			if errors.Is(err, plumbing.ErrReferenceNotFound) {
				currentRef = nil
			} else {
				return nil, fmt.Errorf("git: read ref %s: %w", refName, err)
			}
		}

		nextRef, err := build(repo, currentRef)
		if err != nil {
			return nil, err
		}
		if nextRef == nil {
			return nil, fmt.Errorf("git: publish ref %s: builder returned nil reference", refName)
		}
		if nextRef.Name() != refName {
			return nil, fmt.Errorf("git: publish ref %s: builder returned %s", refName, nextRef.Name())
		}

		if err := publishReference(repo, nextRef, currentRef); err == nil {
			return nextRef, nil
		} else if errors.Is(err, gitstorage.ErrReferenceHasChanged) {
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

func publishReference(repo *ggit.Repository, nextRef, currentRef *plumbing.Reference) error {
	if currentRef != nil {
		return repo.Storer.CheckAndSetReference(nextRef, currentRef)
	}
	return createReferenceIfAbsent(repo, nextRef)
}

func createReferenceIfAbsent(repo *ggit.Repository, ref *plumbing.Reference) error {
	storage, ok := repo.Storer.(*fsstorage.Storage)
	if !ok {
		return fmt.Errorf("git: publish ref %s: unexpected repository storage type %T", ref.Name(), repo.Storer)
	}

	content, err := refContent(ref)
	if err != nil {
		return err
	}

	refPath := filepath.Join(storage.Filesystem().Root(), filepath.FromSlash(ref.Name().String()))
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		return fmt.Errorf("git: publish ref %s: create parent directory: %w", ref.Name(), err)
	}

	refFile, err := os.OpenFile(refPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return gitstorage.ErrReferenceHasChanged
		}
		return fmt.Errorf("git: publish ref %s: create ref file: %w", ref.Name(), err)
	}

	if _, err := refFile.WriteString(content); err != nil {
		_ = refFile.Close()
		_ = os.Remove(refPath)
		return fmt.Errorf("git: publish ref %s: write ref file: %w", ref.Name(), err)
	}
	if err := refFile.Close(); err != nil {
		return fmt.Errorf("git: publish ref %s: close ref file: %w", ref.Name(), err)
	}

	return nil
}

func refContent(ref *plumbing.Reference) (string, error) {
	switch ref.Type() {
	case plumbing.HashReference:
		return fmt.Sprintf("%s\n", ref.Hash()), nil
	case plumbing.SymbolicReference:
		return fmt.Sprintf("ref: %s\n", ref.Target()), nil
	default:
		return "", fmt.Errorf("git: publish ref %s: unsupported reference type %s", ref.Name(), ref.Type())
	}
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
