package checks_test

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const goGitImportPrefix = "github.com/go-git/go-git/v5"

func TestInternalGitProductionFilesDoNotImportGoGit(t *testing.T) {
	root := repoRoot(t)
	internalGitDir := filepath.Join(root, "internal", "git")
	fset := token.NewFileSet()

	var violations []string
	err := filepath.WalkDir(internalGitDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s imports: %w", path, err)
		}

		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return fmt.Errorf("unquote import path %s in %s: %w", imp.Path.Value, path, err)
			}
			if !strings.HasPrefix(importPath, goGitImportPrefix) {
				continue
			}

			relPath, err := filepath.Rel(root, path)
			if err != nil {
				return fmt.Errorf("relative path for %s: %w", path, err)
			}
			violations = append(violations, fmt.Sprintf("%s imports disallowed go-git package %q", filepath.ToSlash(relPath), importPath))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/git imports: %v", err)
	}

	if len(violations) > 0 {
		slices.Sort(violations)
		t.Fatalf("production internal/git imports must not use go-git:\n%s", strings.Join(violations, "\n"))
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller path")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
