package source

import (
	"os"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/scripts/perf/internal/schema"
)

func TestDecodeBuildInfoCapturesWhatRan(t *testing.T) {
	sha := strings.Repeat("a", 64)
	binary := schema.Binary{Path: "/bin/sing-box", SHA256: sha}
	info := &debug.BuildInfo{
		GoVersion: "go1.25.5",
		Path:      "github.com/sagernet/sing-box/cmd/sing-box",
		Main:      debug.Module{Path: "github.com/sagernet/sing-box", Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "-tags", Value: "with_quic,with_wireguard,with_purego"},
			{Key: "CGO_ENABLED", Value: "0"},
			{Key: "GOOS", Value: "linux"},
			{Key: "GOARCH", Value: "amd64"},
		},
		Deps: []*debug.Module{
			{Path: "github.com/sagernet/sing-tun", Version: "v0.9.7", Replace: &debug.Module{Path: "github.com/Piggy-Cat-bit-shadow/sing-tun", Version: "v0.9.7-fork"}},
		},
	}
	decodeBuildInfo(info, &binary)
	if binary.GoVersion != "go1.25.5" || binary.GOOS != "linux" || binary.GOARCH != "amd64" {
		t.Fatalf("go/os/arch = %q/%q/%q", binary.GoVersion, binary.GOOS, binary.GOARCH)
	}
	if binary.CGOEnabled == nil || *binary.CGOEnabled {
		t.Fatal("CGO_ENABLED=0 was not recorded as false")
	}
	if len(binary.BuildTags) != 3 || binary.BuildTags[2] != "with_purego" || binary.TagsSource != "buildinfo" {
		t.Fatalf("tags = %v source=%q", binary.BuildTags, binary.TagsSource)
	}
	if binary.MainPath != "github.com/sagernet/sing-box" || binary.MainVersion != "(devel)" {
		t.Fatalf("main = %q %q", binary.MainPath, binary.MainVersion)
	}
	if len(binary.Deps) != 1 || binary.Deps[0].ReplaceVersion != "v0.9.7-fork" {
		t.Fatalf("deps = %+v", binary.Deps)
	}
	if binary.VCSRevision != "" || binary.VCSModified != nil {
		t.Fatal("a binary without vcs settings must not invent a revision")
	}
}

func TestTagsSourcePrefersBuildInfoOverSidecar(t *testing.T) {
	binary := schema.Binary{BuildTags: []string{"a", "b"}}
	tags, from := TagsSource(binary, []string{"a", "c"})
	if from != "buildinfo" || len(tags) != 2 || tags[0] != "a" {
		t.Fatalf("tags=%v from=%q", tags, from)
	}
	tags, from = TagsSource(schema.Binary{}, []string{"a", "c"})
	if from != "sidecar" || len(tags) != 2 {
		t.Fatalf("sidecar fallback: tags=%v from=%q", tags, from)
	}
	tags, from = TagsSource(schema.Binary{}, nil)
	if from != "unavailable" || tags != nil {
		t.Fatalf("no source: tags=%v from=%q", tags, from)
	}
}

func TestInspectBinaryReadsTheRunningTestBinary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := InspectBinary(executable)
	if binary.ReadError != "" {
		t.Fatalf("ReadError = %q", binary.ReadError)
	}
	if len(binary.SHA256) != 64 {
		t.Fatalf("sha256 = %q", binary.SHA256)
	}
	if binary.GoVersion == "" || binary.GOOS == "" {
		t.Fatalf("missing build info: %+v", binary)
	}
}

func TestInspectBinaryReportsMissingAndNonGoFiles(t *testing.T) {
	missing := InspectBinary("/nonexistent/sing-box")
	if missing.ReadError == "" {
		t.Fatal("a missing binary must carry a read error")
	}
	path := t.TempDir() + "/not-go"
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	notGo := InspectBinary(path)
	if notGo.ReadError == "" {
		t.Fatal("a non-Go file must carry a read error")
	}
	if len(notGo.SHA256) != 64 {
		t.Fatalf("sha256 of an existing file was not recorded: %q", notGo.SHA256)
	}
}

func TestCollectEnrichesFromTheBinary(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	collected := Collect("", executable)
	if collected.Binary == nil {
		t.Fatal("Collect did not read the binary")
	}
	if collected.BinarySHA256 == "" || collected.BinarySHA256 != collected.Binary.SHA256 {
		t.Fatalf("binary_sha256 = %q, binary.sha256 = %q", collected.BinarySHA256, collected.Binary.SHA256)
	}
	if collected.Binary.TagsSource == "" {
		t.Fatal("tags_source must be explicit (buildinfo, sidecar or unavailable)")
	}
}

func TestCollectWarnsAboutAStaleSourceCache(t *testing.T) {
	t.Setenv("PERF_SOURCE_JSON", `{"commit":"deadbeef","branch":"stale"}`)
	collected := Collect("/mnt/c/Deepseek/_perf/kernel", "")
	if len(collected.Warnings) == 0 {
		t.Fatal("a cache naming a different commit produced no warning")
	}
	found := false
	for _, warning := range collected.Warnings {
		if strings.Contains(warning, "source_cache_stale") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %v", collected.Warnings)
	}
}
