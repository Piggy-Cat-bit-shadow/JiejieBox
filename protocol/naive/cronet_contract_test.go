//go:build with_naive_outbound

package naive

// The SOURCE / GENERATED / NATIVE version contract (audit Q43).
//
// # What these tests are for
//
// The cronet dependency is 31 Go modules (root, `all`, and 29 `lib/<os>_<arch>`), and a `replace`
// written for one of them is not inherited by the others. A release can therefore pin the fork in
// the root, resolve a nested module to upstream, and link a native archive nobody tested - while
// every individual file in the tree looks correct. The release gate for that is
// scripts/ci/verify-cronet-provenance.sh, which needs a module graph and the archives; these tests
// are the part that can run anywhere, offline, in the ordinary test suite:
//
//   - go.mod: every cronet module resolves to the fork, and all 29 native modules carry the SAME
//     generation (no split pin);
//   - release/source-lock.json: the recorded generation, module count and contract policy agree
//     with go.mod, so a pin bump that forgets the lock fails here instead of at release time;
//   - release/cronet-provenance*.txt: every platform's manifest names the modules that platform
//     links, and each recorded line binds one module to one archive's bytes;
//   - the runtime contract (native_contract.go) reports the same binding generation the module
//     graph resolved.
//
// They deliberately do NOT claim the archives were rebuilt from that source, and they do not
// replace the script's byte comparison: those need the toolchain and the artifacts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// repoPath resolves a path relative to the repository root, from this package's directory.
func repoPath(t *testing.T, relative string) string {
	t.Helper()
	return filepath.Join("..", "..", relative)
}

type sourceLock struct {
	CronetNative struct {
		Modules int      `json:"modules"`
		Policy  string   `json:"policy"`
		SHAs    []string `json:"shas"`
	} `json:"cronet_native"`
	GoModules []struct {
		Module  string `json:"module"`
		Pinned  string `json:"pinned"`
		Target  string `json:"target"`
		Version string `json:"version"`
	} `json:"go_modules"`
}

func readSourceLock(t *testing.T) sourceLock {
	t.Helper()
	content, err := os.ReadFile(repoPath(t, filepath.Join("release", "source-lock.json")))
	require.NoError(t, err)
	var lock sourceLock
	require.NoError(t, json.Unmarshal(content, &lock))
	return lock
}

// commitSuffix is the 12-character commit every pseudo-version ends with.
func commitSuffix(version string) string {
	index := strings.LastIndex(version, "-")
	if index < 0 {
		return ""
	}
	return version[index+1:]
}

var replaceLine = regexp.MustCompile(`^replace\s+(github\.com/sagernet/cronet-go[^\s]*)\s+=>\s+(\S+)\s+(\S+)\s*$`)

// TestCronetModuleGraphHasNoSplitPin is the go.mod half of the contract.
func TestCronetModuleGraphHasNoSplitPin(t *testing.T) {
	content, err := os.ReadFile(repoPath(t, "go.mod"))
	require.NoError(t, err)

	nativeVersions := make(map[string]string)
	rootVersion := ""
	allVersion := ""
	for _, line := range strings.Split(string(content), "\n") {
		match := replaceLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		module := match[1]
		target := match[2]
		version := match[3]
		require.True(t, strings.HasPrefix(target, "github.com/Piggy-Cat-bit-shadow/cronet-go"),
			"%s must replace to the fork, got %s", module, target)
		switch {
		case module == cronetGoModulePath:
			rootVersion = version
		case module == cronetGoModulePath+"/all":
			allVersion = version
		case strings.HasPrefix(module, cronetGoModulePath+"/lib/"):
			nativeVersions[module] = version
		}
	}

	require.Equal(t, 29, len(nativeVersions),
		"the lock records 29 native modules; go.mod has %d, so a module was dropped or added "+
			"without the lock and the provenance manifest being updated", len(nativeVersions))
	require.NotEmpty(t, rootVersion, "the cronet-go root module must have a replace")
	require.NotEmpty(t, allVersion, "the cronet-go/all module must have a replace")

	distinct := make(map[string]struct{})
	for module, version := range nativeVersions {
		distinct[version] = struct{}{}
		require.Equal(t, commitSuffix(rootVersion), commitSuffix(version),
			"%s resolves to %s while the root resolves to %s: a native module inherits nothing "+
				"from the root's replace, so this is a split pin", module, version, rootVersion)
	}
	require.Len(t, distinct, 1, "every native module must resolve to the same generation, got %v", distinct)
}

// TestSourceLockAgreesWithTheModuleGraph ties the recorded lock to go.mod.
func TestSourceLockAgreesWithTheModuleGraph(t *testing.T) {
	lock := readSourceLock(t)
	require.Equal(t, "controlled", lock.CronetNative.Policy,
		"the native archives are controlled: changing their pin requires a rebuild and a re-record")
	require.Len(t, lock.CronetNative.SHAs, 1,
		"the lock records one native generation; more than one means a split pin was recorded")
	recordedCommit := lock.CronetNative.SHAs[0]

	content, err := os.ReadFile(repoPath(t, "go.mod"))
	require.NoError(t, err)
	var rootVersion, allVersion string
	nativeModules := 0
	for _, line := range strings.Split(string(content), "\n") {
		match := replaceLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		switch {
		case match[1] == cronetGoModulePath:
			rootVersion = match[3]
		case match[1] == cronetGoModulePath+"/all":
			allVersion = match[3]
		case strings.HasPrefix(match[1], cronetGoModulePath+"/lib/"):
			nativeModules++
		}
	}
	require.Equal(t, recordedCommit, commitSuffix(rootVersion),
		"the lock's native generation and go.mod's root replace disagree")
	require.Equal(t, lock.CronetNative.Modules, nativeModules,
		"the lock records %d native modules, go.mod has %d", lock.CronetNative.Modules, nativeModules)

	lockedVersions := make(map[string]string)
	for _, module := range lock.GoModules {
		lockedVersions[module.Module] = module.Version
	}
	require.Equal(t, rootVersion, lockedVersions[cronetGoModulePath],
		"the root go module entry in release/source-lock.json does not match go.mod")
	require.Equal(t, allVersion, lockedVersions[cronetGoModulePath+"/all"],
		"the all go module entry in release/source-lock.json does not match go.mod")
}

// TestProvenanceManifestsDescribeTheirPlatform checks every recorded manifest against the modules
// the corresponding platform links.
func TestProvenanceManifestsDescribeTheirPlatform(t *testing.T) {
	testCases := []struct {
		manifest    string
		moduleGlobs []string
		// slices is how many archives the platform links at this pin.
		slices int
	}{
		{manifest: "release/cronet-provenance.txt", moduleGlobs: []string{"lib/darwin_", "lib/ios_"}, slices: 5},
		{manifest: "release/cronet-provenance-windows.txt", moduleGlobs: []string{"lib/windows_"}, slices: 2},
	}
	for _, testCase := range testCases {
		t.Run(filepath.Base(testCase.manifest), func(t *testing.T) {
			content, err := os.ReadFile(repoPath(t, testCase.manifest))
			require.NoError(t, err, "a platform that links native archives must record them")
			lines := 0
			for _, line := range strings.Split(string(content), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				fields := strings.Fields(line)
				require.Len(t, fields, 4, "manifest line must be <module> <archive> <size> <sha256>: %q", line)
				module, archive, sizeText, digest := fields[0], fields[1], fields[2], fields[3]
				require.True(t, strings.HasPrefix(module, cronetGoModulePath+"/lib/"),
					"%s records a module outside the native set: %s", testCase.manifest, module)
				matched := false
				for _, glob := range testCase.moduleGlobs {
					if strings.HasPrefix(module, cronetGoModulePath+"/"+glob) {
						matched = true
					}
				}
				require.True(t, matched, "%s records %s, which this platform does not link",
					testCase.manifest, module)
				require.True(t, strings.HasSuffix(archive, ".a") || strings.HasSuffix(archive, ".dll"),
					"unexpected archive name %q", archive)
				size, sizeErr := strconv.ParseUint(sizeText, 10, 64)
				require.NoError(t, sizeErr)
				require.NotZero(t, size, "a recorded archive cannot be empty")
				require.Len(t, digest, 64, "the digest must be a full sha256: %q", digest)
				require.Regexp(t, `^[0-9a-f]{64}$`, digest)
				lines++
			}
			require.Equal(t, testCase.slices, lines,
				"%s records %d archive(s), but this platform links %d", testCase.manifest, lines, testCase.slices)
		})
	}
}

// TestNativeContractReportsTheResolvedBindingGeneration ties the runtime record to the module
// graph: a build must be able to say which binding generation it is running.
//
// A `go test` binary carries no dependency metadata (the tool strips it), so on this host the
// positive half of the assertion is covered by TestModuleVersionFromBuildInfo with synthetic
// metadata, and this test states the boundary instead of pretending the gap is a pass.
func TestNativeContractReportsTheResolvedBindingGeneration(t *testing.T) {
	contract := moduleContract()
	require.True(t, contract.CreationCallback,
		"this build compiles against the native creation callback; a build without it cannot exist")
	if contract.Unknown {
		t.Skip("this binary carries no module dependency metadata (go test strips it); " +
			"the parser is covered by TestModuleVersionFromBuildInfo, and a real build records " +
			"the version at Start")
	}
	lock := readSourceLock(t)
	expected := ""
	for _, module := range lock.GoModules {
		if module.Module == cronetGoModulePath {
			expected = module.Version
		}
	}
	require.NotEmpty(t, expected, "release/source-lock.json must pin the cronet-go module")
	require.True(t, contract.ModuleReplaced,
		"the build must resolve cronet-go through the replace directive, not upstream")
	require.Equal(t, expected, contract.ModuleVersion,
		"the running binding generation must be the one release/source-lock.json pins")
}

// TestModuleVersionFromBuildInfo pins the parser's three cases: replaced (the fork), unreplaced
// (upstream resolved), and absent (no metadata).
func TestModuleVersionFromBuildInfo(t *testing.T) {
	replaced := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: "github.com/sagernet/sing", Version: "v0.9.6"},
		{Path: cronetGoModulePath, Version: "v0.0.1-upstream", Replace: &debug.Module{
			Path:    "github.com/Piggy-Cat-bit-shadow/cronet-go",
			Version: "v0.0.1-143.0.7499.109-2.0.20261105062326-362e63b14ce2",
		}},
	}}
	version, isReplaced, found := moduleVersionFromBuildInfo(replaced)
	require.True(t, found)
	require.True(t, isReplaced)
	require.Equal(t, "v0.0.1-143.0.7499.109-2.0.20261105062326-362e63b14ce2", version)

	upstream := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: cronetGoModulePath, Version: "v0.0.1-143.0.7499.109-2.0.20260929202119-8c68ce89873c"},
	}}
	version, isReplaced, found = moduleVersionFromBuildInfo(upstream)
	require.True(t, found)
	require.False(t, isReplaced, "a build that resolved upstream must say so, not claim the fork")
	require.NotEmpty(t, version)

	_, _, found = moduleVersionFromBuildInfo(&debug.BuildInfo{})
	require.False(t, found)
	_, _, found = moduleVersionFromBuildInfo(nil)
	require.False(t, found)
}
