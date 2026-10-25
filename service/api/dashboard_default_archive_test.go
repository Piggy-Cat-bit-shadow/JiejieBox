package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dashboard served when `download_url` is empty is whatever this constant names, and the
// constant named the upstream `SagerNet` repository while this fork's own dashboard - carrying
// the Jiejiebox branding and the subscription-quota card - was published to its own `gh-pages`.
// Nothing else in the tree reads the constant, so a revert or a careless merge would reinstate
// the upstream UI silently: the daemon would keep starting, the panel would keep loading, and
// the only difference would be somebody else's interface. This is the one assertion that makes
// that a test failure instead.
func TestTheDefaultDashboardIsTheForksBuiltUI(t *testing.T) {
	const expected = "https://github.com/Piggy-Cat-bit-shadow/sing-box-dashboard/archive/refs/heads/gh-pages.zip"
	if defaultDashboardURL != expected {
		t.Fatalf("default dashboard url = %q, want the fork's built UI at %q", defaultDashboardURL, expected)
	}
}

// TestTheDefaultDashboardArchiveServes drives the PRODUCTION extraction path against the
// archive the default URL actually serves, then serves the result through the same
// `http.FileServer` the API mounts at `/dashboard/`. It is opt-in because it needs the archive
// on disk - the daemon downloads it at run time and a unit test must not require GitHub:
//
//	curl -L -o gh-pages.zip https://github.com/Piggy-Cat-bit-shadow/sing-box-dashboard/archive/refs/heads/gh-pages.zip
//	DASHBOARD_SMOKE_ZIP=gh-pages.zip go test ./service/api/ -run TestTheDefaultDashboardArchiveServes -v
//
// What it proves, and why each part is needed: the archive is a GitHub branch archive, so every
// entry is under one `sing-box-dashboard-gh-pages/` directory that has to be trimmed or the
// served root is empty; `index.html` and the hashed `assets/` must survive extraction, because a
// page that loads without its bundle is a blank screen; and the page must carry the fork's
// branding, because "200 OK" is equally true of the upstream build this constant used to name.
func TestTheDefaultDashboardArchiveServes(t *testing.T) {
	archive := os.Getenv("DASHBOARD_SMOKE_ZIP")
	if archive == "" {
		t.Skip("set DASHBOARD_SMOKE_ZIP to the archive the default dashboard url serves")
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	dir := t.TempDir()
	d := &dashboard{ctx: t.Context(), path: dir, url: defaultDashboardURL}
	if err = d.extract(file, `W/"smoke"`); err != nil {
		t.Fatalf("extract: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	t.Logf("extracted root: %v", names)
	for _, required := range []string{"index.html", "assets", ".etag"} {
		if _, err = os.Stat(filepath.Join(dir, required)); err != nil {
			t.Fatalf("missing %s: %v", required, err)
		}
	}
	if _, err = os.Stat(filepath.Join(dir, "sing-box-dashboard-gh-pages")); err == nil {
		t.Fatal("archive top-level directory was not trimmed")
	}
	etag, err := os.ReadFile(filepath.Join(dir, ".etag"))
	if err != nil {
		t.Fatal(err)
	}
	if string(etag) != `W/"smoke"` {
		t.Fatalf(".etag = %q", etag)
	}

	server := httptest.NewServer(http.StripPrefix(dashboardRoutePrefix, http.FileServer(dashboardDir(dir))))
	defer server.Close()

	response, err := http.Get(server.URL + dashboardRoutePrefix)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", dashboardRoutePrefix, response.StatusCode)
	}
	page, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "<!doctype html>") {
		t.Fatalf("GET %s did not return index.html: %q", dashboardRoutePrefix, string(page[:80]))
	}
	if !strings.Contains(string(page), "Jiejiebox") {
		t.Fatal("served page is not the fork's branded build")
	}

	assets, err := os.ReadDir(filepath.Join(dir, "assets"))
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, asset := range assets {
		if strings.HasSuffix(asset.Name(), ".js") {
			script = asset.Name()
			break
		}
	}
	if script == "" {
		t.Fatal("no javascript asset extracted")
	}
	response2, err := http.Get(server.URL + dashboardRoutePrefix + "assets/" + script)
	if err != nil {
		t.Fatal(err)
	}
	defer response2.Body.Close()
	if response2.StatusCode != http.StatusOK {
		t.Fatalf("GET %sassets/%s = %d", dashboardRoutePrefix, script, response2.StatusCode)
	}
	t.Logf("GET %s = 200 index.html (branded), GET %sassets/%s = 200", dashboardRoutePrefix, dashboardRoutePrefix, script)
}
