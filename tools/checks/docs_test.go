package checks_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var markdownLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

var stableReferenceDocs = []string{
	"product/roadmap.md",
	"product/overview.md",
	"architecture/repo-structure.md",
	"runbooks/doc-authoring-quickstart.md",
	"runbooks/spec-driven-delivery-workflow.md",
}

func TestIndexLinksEveryDoc(t *testing.T) {
	root := docsRoot(t)
	indexPath := filepath.Join(root, "index.md")
	linkedDocs := linkedMarkdownFiles(t, indexPath)

	var docs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "index.md" {
			return nil
		}
		docs = append(docs, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}

	slices.Sort(docs)
	for _, doc := range docs {
		if _, ok := linkedDocs[doc]; !ok {
			t.Errorf("docs/index.md is missing link to %s", doc)
		}
	}
}

func TestStableReferenceLinksResolve(t *testing.T) {
	root := docsRoot(t)

	for _, relDoc := range stableReferenceDocs {
		docPath := filepath.Join(root, filepath.FromSlash(relDoc))
		links := linkedMarkdownTargets(t, docPath)
		for _, target := range links {
			resolved := resolveMarkdownTarget(t, docPath, target)
			info, err := os.Stat(resolved)
			if err != nil {
				t.Errorf("%s links to missing file %s", relDoc, target)
				continue
			}
			if info.IsDir() {
				t.Errorf("%s links to directory %s", relDoc, target)
			}
		}
	}
}

func TestRoadmapAvoidsCurrentStateDuplication(t *testing.T) {
	root := docsRoot(t)
	data := mustReadFile(t, filepath.Join(root, "product", "roadmap.md"))

	banned := []string{
		"Current implementation snapshot",
		"Task Tracking",
		"| Feature ID |",
		"**Status:**",
		"`Backlog`",
		"`Completed`",
		"`In Progress`",
	}
	for _, needle := range banned {
		if strings.Contains(data, needle) {
			t.Errorf("docs/product/roadmap.md must not contain %q", needle)
		}
	}

	if !strings.Contains(data, "../index.md") {
		t.Error("docs/product/roadmap.md should point readers to docs/index.md for current state")
	}
}

func TestEpicAndFeatureDocsOmitMutableStatusFields(t *testing.T) {
	root := docsRoot(t)
	for _, dir := range []string{"epics", "features"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}

			path := filepath.Join(root, dir, entry.Name())
			data := mustReadFile(t, path)
			if strings.Contains(data, "**Status:**") {
				t.Errorf("%s contains mutable status metadata", filepath.ToSlash(filepath.Join(dir, entry.Name())))
			}
			if strings.Contains(data, "| **Status** |") {
				t.Errorf("%s contains mutable status table metadata", filepath.ToSlash(filepath.Join(dir, entry.Name())))
			}
		}
	}
}

func docsRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve caller path")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "docs"))
}

func linkedMarkdownFiles(t *testing.T, docPath string) map[string]struct{} {
	t.Helper()

	root := docsRoot(t)
	targets := linkedMarkdownTargets(t, docPath)
	result := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		resolved := resolveMarkdownTarget(t, docPath, target)
		rel, err := filepath.Rel(root, resolved)
		if err != nil {
			t.Fatalf("relative path for %s: %v", target, err)
		}
		result[filepath.ToSlash(rel)] = struct{}{}
	}
	return result
}

func linkedMarkdownTargets(t *testing.T, docPath string) []string {
	t.Helper()

	data := mustReadFile(t, docPath)
	matches := markdownLinkPattern.FindAllStringSubmatch(data, -1)
	var targets []string
	for _, match := range matches {
		target := strings.TrimSpace(match[1])
		if target == "" {
			continue
		}
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
			continue
		}
		if strings.HasPrefix(target, "#") {
			continue
		}

		target = strings.Split(target, "#")[0]
		target = strings.Split(target, "?")[0]
		if filepath.Ext(target) != ".md" {
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

func resolveMarkdownTarget(t *testing.T, docPath, target string) string {
	t.Helper()

	docDir := filepath.Dir(docPath)
	resolved := filepath.Clean(filepath.Join(docDir, filepath.FromSlash(target)))
	root := docsRoot(t)
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		t.Fatalf("relative target path for %s: %v", target, err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Fatalf("%s resolves outside docs/: %s", target, resolved)
	}
	return resolved
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
