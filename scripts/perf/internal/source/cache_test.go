package source

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
)

// TestCachedSourceRefreshesChangedGoMod pins the boundary the cache must not
// cross: the commit can be identical while go.mod (or test/go.mod) has moved,
// so the cached module view must be recomputed, not trusted.
func TestCachedSourceRefreshesChangedGoMod(t *testing.T) {
	repo := t.TempDir()
	goMod := "module example.com/tree\n\ngo 1.25\n\nrequire (\n\tgithub.com/sagernet/sing v1.2.3\n)\n\nreplace github.com/sagernet/sing => github.com/fork/b v1.2.4\n"
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	cached := schema.Source{
		Repo:    repo,
		Commit:  "deadbeef",
		Modules: map[string]string{"github.com/sagernet/sing": "v1.0.0"},
	}
	raw, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERF_SOURCE_JSON", string(raw))

	got := Collect(repo, "")
	if got.Modules["github.com/sagernet/sing"] != "v1.2.4" {
		t.Fatalf("cached modules were not refreshed to the replacement: %#v", got.Modules)
	}
	if !hasWarning(got.Warnings, "source_cache_modules_stale") {
		t.Fatalf("missing modules-stale warning: %v", got.Warnings)
	}
}

// TestCachedSourceKeepsMatchingModules is the negative control: an equal view
// must not fabricate a warning.
func TestCachedSourceKeepsMatchingModules(t *testing.T) {
	repo := t.TempDir()
	goMod := "module example.com/tree\n\ngo 1.25\n\nrequire github.com/sagernet/sing v1.2.3\n"
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	cached := schema.Source{
		Repo:    repo,
		Commit:  "deadbeef",
		Modules: map[string]string{"github.com/sagernet/sing": "v1.2.3"},
	}
	raw, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERF_SOURCE_JSON", string(raw))

	got := Collect(repo, "")
	if hasWarning(got.Warnings, "source_cache_modules_stale") {
		t.Fatalf("unexpected modules-stale warning: %v", got.Warnings)
	}
}

func TestCachedSourceMarksDirtyStateAsCached(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/tree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty := false
	cached := schema.Source{Repo: repo, Commit: "deadbeef", Dirty: &dirty}
	raw, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERF_SOURCE_JSON", string(raw))
	got := Collect(repo, "")
	if !hasWarning(got.Warnings, "source_cache_dirty_from_cache") {
		t.Fatalf("cached dirty state was not disclosed: %v", got.Warnings)
	}
}

func hasWarning(warnings []string, prefix string) bool {
	for _, warning := range warnings {
		if strings.HasPrefix(warning, prefix) {
			return true
		}
	}
	return false
}
