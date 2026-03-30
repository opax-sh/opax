package checks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var markdownLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
var statusPattern = regexp.MustCompile(`(?m)^\*\*Status:\*\*\s*([^\n]+)\s*$`)
var epicRollupPattern = regexp.MustCompile(`^\|\s*(EPIC-\d{4})\s*\|\s*(Backlog|In Progress|Completed|Cancelled)\s*\|\s*\[[^\]]+\]\((epics/[^)]+)\)\s*\|\s*$`)
var featureRollupPattern = regexp.MustCompile(`^\|\s*(FEAT-\d{4})\s*\|\s*(EPIC-\d{4})\s*\|\s*(Backlog|In Progress|Completed|Cancelled)\s*\|\s*\[[^\]]+\]\((features/[^)]+)\)\s*\|\s*$`)

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

func TestNotesDocsUseCanonicalGitNotesNamespace(t *testing.T) {
	root := docsRoot(t)

	featureDoc := mustReadFile(t, filepath.Join(root, "features", "FEAT-0009-git-notes-operations.md"))
	for _, needle := range []string{
		"refs/notes/opax/{namespace}",
		"`git notes --ref=opax/sessions show <commit>`",
		"`git notes --ref=opax/ext-reviews add -m '<json>' <commit>`",
		"`ErrNoteNotFound`",
		"`ErrMalformedNote`",
		"`ErrNoteConflict`",
	} {
		if !strings.Contains(featureDoc, needle) {
			t.Errorf("FEAT-0009 doc is missing %q", needle)
		}
	}
	if strings.Contains(featureDoc, "refs/opax/notes/") {
		t.Error("FEAT-0009 doc should not reference refs/opax/notes/")
	}

	dataSpec := mustReadFile(t, filepath.Join(root, "product", "data-spec.md"))
	for _, needle := range []string{
		"refs/notes/opax/{namespace}",
		"refs/notes/opax/sessions",
		"refs/notes/opax/ext-reviews",
		"namespace = 'ext-reviews'",
	} {
		if !strings.Contains(dataSpec, needle) {
			t.Errorf("data-spec doc is missing %q", needle)
		}
	}
	if strings.Contains(dataSpec, "refs/opax/notes/") {
		t.Error("data-spec doc should not reference refs/opax/notes/")
	}

	refspecDoc := mustReadFile(t, filepath.Join(root, "features", "FEAT-0011-refspec-configuration.md"))
	if !strings.Contains(refspecDoc, "+refs/notes/opax/*:refs/notes/opax/*") {
		t.Error("FEAT-0011 doc should include explicit notes refspecs")
	}
}

func TestEpicAndFeatureDocsHaveValidStatus(t *testing.T) {
	root := docsRoot(t)
	for _, dir := range []string{"epics", "features"} {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" || entry.Name() == "_template.md" {
				continue
			}

			path := filepath.Join(root, dir, entry.Name())
			data := mustReadFile(t, path)
			status, ok := extractStatus(data)
			if !ok {
				t.Errorf("%s is missing required **Status:** field", filepath.ToSlash(filepath.Join(dir, entry.Name())))
				continue
			}
			if !isValidStatus(status) {
				t.Errorf("%s has invalid status %q", filepath.ToSlash(filepath.Join(dir, entry.Name())), status)
			}
		}
	}
}

func TestIndexRollupMatchesScopedDocStatuses(t *testing.T) {
	root := docsRoot(t)
	indexData := mustReadFile(t, filepath.Join(root, "index.md"))

	epicRollup, featureRollup := parseIndexRollups(t, indexData)
	if len(epicRollup) == 0 {
		t.Fatal("docs/index.md epic rollup table not found or empty")
	}
	if len(featureRollup) == 0 {
		t.Fatal("docs/index.md feature rollup table not found or empty")
	}

	epicDocs := statusByDocID(t, root, "epics", "EPIC")
	featureDocs := statusByDocID(t, root, "features", "FEAT")

	for id, status := range epicDocs {
		indexStatus, ok := epicRollup[id]
		if !ok {
			t.Errorf("docs/index.md epic rollup missing %s", id)
			continue
		}
		if indexStatus != status {
			t.Errorf("docs/index.md epic rollup status mismatch for %s: got %q, want %q", id, indexStatus, status)
		}
	}
	for id := range epicRollup {
		if _, ok := epicDocs[id]; !ok {
			t.Errorf("docs/index.md epic rollup contains unknown entry %s", id)
		}
	}

	for id, status := range featureDocs {
		indexStatus, ok := featureRollup[id]
		if !ok {
			t.Errorf("docs/index.md feature rollup missing %s", id)
			continue
		}
		if indexStatus != status {
			t.Errorf("docs/index.md feature rollup status mismatch for %s: got %q, want %q", id, indexStatus, status)
		}
	}
	for id := range featureRollup {
		if _, ok := featureDocs[id]; !ok {
			t.Errorf("docs/index.md feature rollup contains unknown entry %s", id)
		}
	}
}

func statusByDocID(t *testing.T, root, dir, prefix string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(root, dir))
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	statuses := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" || entry.Name() == "_template.md" {
			continue
		}
		path := filepath.Join(root, dir, entry.Name())
		data := mustReadFile(t, path)
		status, ok := extractStatus(data)
		if !ok {
			t.Fatalf("%s is missing required **Status:** field", filepath.ToSlash(filepath.Join(dir, entry.Name())))
		}
		if !isValidStatus(status) {
			t.Fatalf("%s has invalid status %q", filepath.ToSlash(filepath.Join(dir, entry.Name())), status)
		}
		id, err := docIDFromFilename(entry.Name())
		if err != nil {
			t.Fatalf("%s", err)
		}
		if !strings.HasPrefix(id, prefix+"-") {
			t.Fatalf("unexpected %s doc name format: %s", dir, entry.Name())
		}
		statuses[id] = status
	}
	return statuses
}

func parseIndexRollups(t *testing.T, data string) (map[string]string, map[string]string) {
	t.Helper()

	epics := map[string]string{}
	features := map[string]string{}

	for _, line := range strings.Split(data, "\n") {
		if match := epicRollupPattern.FindStringSubmatch(strings.TrimSpace(line)); len(match) == 4 {
			epics[match[1]] = match[2]
			continue
		}
		if match := featureRollupPattern.FindStringSubmatch(strings.TrimSpace(line)); len(match) == 5 {
			features[match[1]] = match[3]
		}
	}

	return epics, features
}

func extractStatus(data string) (string, bool) {
	match := statusPattern.FindStringSubmatch(data)
	if len(match) != 2 {
		return "", false
	}
	return strings.TrimSpace(match[1]), true
}

func isValidStatus(status string) bool {
	switch status {
	case "Backlog", "In Progress", "Completed", "Cancelled":
		return true
	default:
		return false
	}
}

func docIDFromFilename(name string) (string, error) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	parts := strings.Split(base, "-")
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected doc name format: %s", name)
	}
	return parts[0] + "-" + parts[1], nil
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
