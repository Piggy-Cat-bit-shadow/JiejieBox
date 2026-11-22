//go:build unix

package rule

// The in-place identity tests for the derived rule-set cache.
//
// The defect these tests pin: reloadFile hashed the source descriptor, looked the derived cache up
// by that digest, and then read/parsed the SAME descriptor. An in-place edit between the two reads
// (saving a file does not require a rename) made the parser see B while the manifest was told A, so
// B's conversion was published under A's identity and every checksum stayed valid. Restoring A then
// hit that artifact and served B's rules.
//
// The window is reproduced through localReloadWindowTestHook - the production test seam that runs
// exactly between the hashing pass and the parse read - rather than through a patched copy of the
// production file. Every test asserts the one rule the fix adds: the identity that is published is
// the identity of the bytes that were actually parsed.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

func derivedContentDigest(content []byte) (string, int64) {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), int64(len(content))
}

// derivedJSONDomainSource is the smallest source-format rule set with one exact-domain rule.
func derivedJSONDomainSource(domain string) []byte {
	return []byte(`{"version":1,"rules":[{"domain":["` + domain + `"]}]}`)
}

// installReloadWindowAction runs action once, from the reload window of the next load of path:
// after the first hashing pass and before the parse read. The action keeps the inode (or replaces
// the name) exactly as an operator's editor would.
func installReloadWindowAction(t *testing.T, path string, action func()) {
	t.Helper()
	ran := false
	localReloadWindowTestHook = func(hookPath string) {
		if ran || hookPath != path {
			return
		}
		ran = true
		action()
	}
	t.Cleanup(func() { localReloadWindowTestHook = nil })
}

// derivedManifests reads the manifest of every physical version of one store key.
func derivedManifests(t *testing.T, base string, key string) []artifact.Manifest {
	t.Helper()
	var manifests []artifact.Manifest
	for _, version := range derivedVersions(t, base, key) {
		encoded, err := os.ReadFile(filepath.Join(base, derivedStoreDirectory, key, version, "manifest.json"))
		require.NoError(t, err, "read manifest of %s", version)
		var manifest artifact.Manifest
		require.NoError(t, json.Unmarshal(encoded, &manifest), "decode manifest of %s", version)
		manifests = append(manifests, manifest)
	}
	return manifests
}

// requireEveryDerivedManifestNames asserts that EVERY version in the store attributes its artifact
// to exactly these source bytes, and carries the current identity generation. This is the
// manifest-level form of the defect: a version compiled from B may not be named A, even when the
// store's own byte digest still verifies.
func requireEveryDerivedManifestNames(t *testing.T, base string, key string, digest string, size int64) {
	t.Helper()
	manifests := derivedManifests(t, base, key)
	require.NotEmpty(t, manifests, "no derived version was published for %s", key)
	for _, manifest := range manifests {
		require.Contains(t, manifest.Dependencies, "source-sha256="+digest,
			"a derived version is attributed to the wrong source bytes")
		require.Contains(t, manifest.Dependencies, "source-size="+strconv.FormatInt(size, 10),
			"a derived version names the wrong source size")
		require.Contains(t, manifest.Dependencies, "identity-generation="+strconv.Itoa(derivedIdentityGeneration),
			"every published manifest must record the identity generation")
	}
}

// oldStyleDerivedManifest is the manifest a pre-fix build wrote: the current one minus the
// identity-generation line, published earlier than anything this test loads.
func oldStyleDerivedManifest(t *testing.T, cache *derivedRuleSetCache, source *derivedSource, ruleSetVersion uint8) artifact.Manifest {
	t.Helper()
	manifest := cache.derivedManifest(source, ruleSetVersion)
	dependencies := make([]string, 0, len(manifest.Dependencies))
	for _, dependency := range manifest.Dependencies {
		if strings.HasPrefix(dependency, "identity-generation=") {
			continue
		}
		dependencies = append(dependencies, dependency)
	}
	manifest.Dependencies = dependencies
	manifest.Created = time.Now().Add(-time.Hour).UnixNano()
	return manifest
}

func parseDerivedJSONSource(t *testing.T, content []byte) option.PlainRuleSetCompat {
	t.Helper()
	compat, err := singjson.UnmarshalExtended[option.PlainRuleSetCompat](content)
	require.NoError(t, err)
	return compat
}

// encodeDerivedMmapRuleSet writes the persisted SRSM layout of a rule set and returns the bytes.
// It is the same writer the cold path uses before it publishes an artifact.
func encodeDerivedMmapRuleSet(t *testing.T, ruleSet option.PlainRuleSetCompat) []byte {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "derived-mmap-*.srsm")
	require.NoError(t, err)
	require.NoError(t, srs.WriteMmap(file, ruleSet))
	require.NoError(t, file.Close())
	encoded, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	return encoded
}

func derivedBinaryDomainRuleSet(domain string) option.PlainRuleSet {
	return option.PlainRuleSet{Rules: []option.HeadlessRule{{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultHeadlessRule{
			Domain: badoption.Listable[string]{domain},
		},
	}}}
}

func encodeDerivedBinaryRuleSet(t *testing.T, ruleSet option.PlainRuleSet) []byte {
	t.Helper()
	var buffer bytes.Buffer
	require.NoError(t, srs.Write(&buffer, ruleSet, C.RuleSetVersionCurrent))
	return buffer.Bytes()
}

// equalLengthDerivedBinarySources finds two DIFFERENT binary rule sets whose encoded files have the
// SAME length, so the in-place overwrite in the reload window neither grows nor shrinks the file.
// The search is deterministic: candidates are visited in index order and the first length collision
// wins. Candidates share one shape and differ only in four digits, so compressed lengths repeat
// almost immediately; the bound exists so a changed compressor cannot hang the search.
func equalLengthDerivedBinarySources(t *testing.T) (first []byte, second []byte, firstDomain string, secondDomain string) {
	t.Helper()
	seen := make(map[int]string)
	for index := 0; index < 1<<12; index++ {
		domain := fmt.Sprintf("binary-%04d.example", index)
		encoded := encodeDerivedBinaryRuleSet(t, derivedBinaryDomainRuleSet(domain))
		if previousDomain, loaded := seen[len(encoded)]; loaded {
			return encodeDerivedBinaryRuleSet(t, derivedBinaryDomainRuleSet(previousDomain)),
				encoded, previousDomain, domain
		}
		seen[len(encoded)] = domain
	}
	t.Fatal("no equal-length pair of binary rule-set fixtures was found")
	return nil, nil, "", ""
}

// TestDerivedRuleSetInPlaceOverwriteNamesTheParsedBytes is defect case (a): a same-length overwrite
// through the pathname, while the source descriptor stays the same inode.
func TestDerivedRuleSetInPlaceOverwriteNamesTheParsedBytes(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "same-length.json")
	original := derivedJSONDomainSource("first.example")
	replacement := derivedJSONDomainSource("other.example")
	require.Equal(t, len(original), len(replacement), "the overwrite must not change the file length")

	require.NoError(t, os.WriteFile(path, original, 0o600))
	installReloadWindowAction(t, path, func() {
		// Same inode, same length: the descriptor that was hashed now reads the replacement.
		require.NoError(t, os.WriteFile(path, replacement, 0o600))
	})
	ruleSet := newDerivedRuleSet(t, ctx, "in-place", path, C.RuleSetFormatSource)

	// The cold load parsed the replacement bytes, so it must serve and name THEM.
	requireDomainMatch(t, ruleSet, "other.example", true)
	requireDomainMatch(t, ruleSet, "first.example", false)
	replacementDigest, replacementSize := derivedContentDigest(replacement)
	key := localDerivedSource("in-place", path, C.RuleSetFormatSource, replacementDigest, replacementSize).key
	requireEveryDerivedManifestNames(t, base, key, replacementDigest, replacementSize)

	hits, misses, publishes, failures := derivedCacheDiagnostics()
	require.Zero(t, hits, "the first load cannot hit")
	require.EqualValues(t, 2, misses,
		"the first-pass identity and the corrected identity are two lookups, then the parse is chosen")
	require.EqualValues(t, 1, publishes)
	require.Zero(t, failures)

	// Restoring the original bytes must serve the ORIGINAL rule set. Under the defect the artifact
	// published above was named with the original digest, so this reload served `other.example`.
	require.NoError(t, os.WriteFile(path, original, 0o600))
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "first.example", true)
	requireDomainMatch(t, ruleSet, "other.example", false)
	originalDigest, originalSize := derivedContentDigest(original)
	requireEveryDerivedManifestNames(t, base, key, originalDigest, originalSize)

	// The restored conversion is cached and attributed to the restored bytes.
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "first.example", true)
	hits, _, _, failures = derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits, "the restored source must be a warm hit")
	require.Zero(t, failures)
	require.Len(t, derivedVersions(t, base, key), 1)
}

// TestDerivedRuleSetTruncatingOverwriteNamesTheParsedBytes is defect case (b): the edit shortens
// the file, so size alone would already reveal it - and must not be ignored.
func TestDerivedRuleSetTruncatingOverwriteNamesTheParsedBytes(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "truncated.json")
	original := derivedJSONDomainSource("first.example")
	replacement := derivedJSONDomainSource("b.example")
	require.Less(t, len(replacement), len(original), "the rewrite must actually truncate the file")

	require.NoError(t, os.WriteFile(path, original, 0o600))
	installReloadWindowAction(t, path, func() {
		require.NoError(t, os.WriteFile(path, replacement, 0o600))
	})
	ruleSet := newDerivedRuleSet(t, ctx, "truncated", path, C.RuleSetFormatSource)

	requireDomainMatch(t, ruleSet, "b.example", true)
	requireDomainMatch(t, ruleSet, "first.example", false)
	replacementDigest, replacementSize := derivedContentDigest(replacement)
	key := localDerivedSource("truncated", path, C.RuleSetFormatSource, replacementDigest, replacementSize).key
	requireEveryDerivedManifestNames(t, base, key, replacementDigest, replacementSize)

	require.NoError(t, os.WriteFile(path, original, 0o600))
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "first.example", true)
	requireDomainMatch(t, ruleSet, "b.example", false)
	originalDigest, originalSize := derivedContentDigest(original)
	requireEveryDerivedManifestNames(t, base, key, originalDigest, originalSize)

	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "first.example", true)
	hits, _, _, failures := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits)
	require.Zero(t, failures)
}

// TestDerivedRuleSetRenameReplacementKeepsTheOpenedFileConsistent is defect case (c): the pathname
// is atomically replaced in the window. The descriptor opened before the rename still yields the
// original bytes, so the load must parse AND name the original - and the next reload must see the
// renamed file.
func TestDerivedRuleSetRenameReplacementKeepsTheOpenedFileConsistent(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "renamed.json")
	replacementPath := filepath.Join(directory, "replacement.json")
	original := derivedJSONDomainSource("first.example")
	replacement := derivedJSONDomainSource("other.example")
	require.NoError(t, os.WriteFile(path, original, 0o600))
	require.NoError(t, os.WriteFile(replacementPath, replacement, 0o600))
	installReloadWindowAction(t, path, func() {
		require.NoError(t, os.Rename(replacementPath, path))
	})
	originalDigest, originalSize := derivedContentDigest(original)
	key := localDerivedSource("rename", path, C.RuleSetFormatSource, originalDigest, originalSize).key

	ruleSet := newDerivedRuleSet(t, ctx, "rename", path, C.RuleSetFormatSource)
	requireDomainMatch(t, ruleSet, "first.example", true)
	requireDomainMatch(t, ruleSet, "other.example", false)
	requireEveryDerivedManifestNames(t, base, key, originalDigest, originalSize)

	// The name now points at the renamed file, and a fresh reload must see it.
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "other.example", true)
	requireDomainMatch(t, ruleSet, "first.example", false)
	replacementDigest, replacementSize := derivedContentDigest(replacement)
	requireEveryDerivedManifestNames(t, base, key, replacementDigest, replacementSize)

	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "other.example", true)
	hits, _, _, failures := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits)
	require.Zero(t, failures)
}

// TestDerivedRuleSetBinaryInPlaceOverwriteNamesTheParsedBytes is defect case (d): the binary source
// is overwritten with another binary of the SAME length, so a size check cannot catch it and the
// parser sees the replacement while the first pass named the original.
func TestDerivedRuleSetBinaryInPlaceOverwriteNamesTheParsedBytes(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "same-length.srs")
	original, replacement, originalDomain, replacementDomain := equalLengthDerivedBinarySources(t)
	require.Len(t, original, len(replacement), "the binary overwrite must keep the file length")
	require.NotEqual(t, originalDomain, replacementDomain)

	require.NoError(t, os.WriteFile(path, original, 0o600))
	installReloadWindowAction(t, path, func() {
		require.NoError(t, os.WriteFile(path, replacement, 0o600))
	})
	ruleSet := newDerivedRuleSet(t, ctx, "binary-in-place", path, C.RuleSetFormatBinary)

	requireDomainMatch(t, ruleSet, replacementDomain, true)
	requireDomainMatch(t, ruleSet, originalDomain, false)
	replacementDigest, replacementSize := derivedContentDigest(replacement)
	key := localDerivedSource("binary-in-place", path, C.RuleSetFormatBinary, replacementDigest, replacementSize).key
	requireEveryDerivedManifestNames(t, base, key, replacementDigest, replacementSize)

	// Restoring the original binary must not be served the replacement's conversion.
	require.NoError(t, os.WriteFile(path, original, 0o600))
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, originalDomain, true)
	requireDomainMatch(t, ruleSet, replacementDomain, false)
	originalDigest, originalSize := derivedContentDigest(original)
	requireEveryDerivedManifestNames(t, base, key, originalDigest, originalSize)

	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, originalDomain, true)
	hits, _, publishes, failures := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits)
	require.EqualValues(t, 2, publishes)
	require.Zero(t, failures)
	require.Len(t, derivedVersions(t, base, key), 1)
}

// TestDerivedRuleSetLegacyPoisonedManifestIsNotServed is defect case (e): a pre-fix artifact in the
// store, compiled from B but attributed to A with an old-style manifest. It must be refused, A must
// be parsed and republished correctly, and the FOLLOWING reload must hit the corrected version -
// proving the migration completes instead of merely refusing the poisoned hit forever.
func TestDerivedRuleSetLegacyPoisonedManifestIsNotServed(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "legacy-poisoned.json")
	sourceA := derivedJSONDomainSource("a.example")
	sourceB := derivedJSONDomainSource("b.example")
	require.NoError(t, os.WriteFile(path, sourceA, 0o600))

	store, err := artifact.Open(filepath.Join(base, derivedStoreDirectory))
	require.NoError(t, err)
	cache := &derivedRuleSetCache{ctx: ctx, store: store, logger: logger.NOP()}
	digestA, sizeA := derivedContentDigest(sourceA)
	sourceIdentity := localDerivedSource("legacy-poisoned", path, C.RuleSetFormatSource, digestA, sizeA)
	compiledFromB := encodeDerivedMmapRuleSet(t, parseDerivedJSONSource(t, sourceB))
	manifest := oldStyleDerivedManifest(t, cache, sourceIdentity, C.RuleSetVersionCurrent)
	poisoned, err := store.Commit(sourceIdentity.key, manifest, bytes.NewReader(compiledFromB))
	require.NoError(t, err)
	poisoned.Release()

	ruleSet := newDerivedRuleSet(t, ctx, "legacy-poisoned", path, C.RuleSetFormatSource)
	requireDomainMatch(t, ruleSet, "a.example", true)
	require.False(t, ruleSet.Match(&adapter.InboundContext{Domain: "b.example"}),
		"a pre-fix artifact must never be served")

	requireEveryDerivedManifestNames(t, base, sourceIdentity.key, digestA, sizeA)
	require.Len(t, derivedVersions(t, base, sourceIdentity.key), 1,
		"the unleased poisoned version is removed once the corrected one exists")
	hits, _, publishes, failures := derivedCacheDiagnostics()
	require.Zero(t, hits, "the poisoned version must be a miss")
	require.EqualValues(t, 1, publishes)
	require.Zero(t, failures)

	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "a.example", true)
	requireDomainMatch(t, ruleSet, "b.example", false)
	hits, _, publishes, failures = derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits, "migration must complete: the corrected version is a hit")
	require.EqualValues(t, 1, publishes)
	require.Zero(t, failures)
}

// TestDerivedRuleSetLegacyManifestForIdenticalBytesIsMigrated covers the other half of the
// migration: the pre-fix artifact compiled the SAME source, so its SRSM bytes are identical and the
// version directory already exists. The corrected publication must REPLACE that manifest instead of
// being shadowed by it forever, and the next reload must hit.
func TestDerivedRuleSetLegacyManifestForIdenticalBytesIsMigrated(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "legacy-identical.json")
	source := derivedJSONDomainSource("a.example")
	require.NoError(t, os.WriteFile(path, source, 0o600))

	store, err := artifact.Open(filepath.Join(base, derivedStoreDirectory))
	require.NoError(t, err)
	cache := &derivedRuleSetCache{ctx: ctx, store: store, logger: logger.NOP()}
	digest, size := derivedContentDigest(source)
	sourceIdentity := localDerivedSource("legacy-identical", path, C.RuleSetFormatSource, digest, size)
	compiled := encodeDerivedMmapRuleSet(t, parseDerivedJSONSource(t, source))
	manifest := oldStyleDerivedManifest(t, cache, sourceIdentity, C.RuleSetVersionCurrent)
	stale, err := store.Commit(sourceIdentity.key, manifest, bytes.NewReader(compiled))
	require.NoError(t, err)
	stale.Release()
	versionsBefore := derivedVersions(t, base, sourceIdentity.key)
	require.Len(t, versionsBefore, 1)

	ruleSet := newDerivedRuleSet(t, ctx, "legacy-identical", path, C.RuleSetFormatSource)
	requireDomainMatch(t, ruleSet, "a.example", true)
	hits, _, publishes, failures := derivedCacheDiagnostics()
	require.Zero(t, hits, "a manifest without identity-generation must be refused even when its source-sha256 matches")
	require.EqualValues(t, 1, publishes)
	require.Zero(t, failures)
	require.Equal(t, versionsBefore, derivedVersions(t, base, sourceIdentity.key),
		"identical artifact bytes must stay one physical version; only the manifest is replaced")
	requireEveryDerivedManifestNames(t, base, sourceIdentity.key, digest, size)

	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "a.example", true)
	hits, _, _, failures = derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits, "the migrated manifest must be served on the next load")
	require.Zero(t, failures)
}

// TestDerivedRuleSetUnparseableEditPublishesNothing is defect case (f), first half: an edit that
// makes the document unparseable must return that parse error on two sequential loads and publish
// no artifact, while the previous generation keeps serving.
func TestDerivedRuleSetUnparseableEditPublishesNothing(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "broken.json")
	initial := derivedJSONDomainSource("initial.example")
	require.NoError(t, os.WriteFile(path, initial, 0o600))
	ruleSet := newDerivedRuleSet(t, ctx, "broken", path, C.RuleSetFormatSource)
	initialDigest, initialSize := derivedContentDigest(initial)
	key := localDerivedSource("broken", path, C.RuleSetFormatSource, initialDigest, initialSize).key

	// A valid but different file makes the first lookup miss before the window edit.
	require.NoError(t, os.WriteFile(path, derivedJSONDomainSource("intermediate.example"), 0o600))
	broken := []byte(`{"version":1,`)
	installReloadWindowAction(t, path, func() {
		require.NoError(t, os.WriteFile(path, broken, 0o600))
	})
	_, expectedErr := singjson.UnmarshalExtended[option.PlainRuleSetCompat](broken)
	require.Error(t, expectedErr)

	require.EqualError(t, ruleSet.reloadFile(path), expectedErr.Error())
	require.EqualError(t, ruleSet.reloadFile(path), expectedErr.Error(),
		"the second load reads the same unparseable bytes and fails the same way")
	require.True(t, ruleSet.Match(&adapter.InboundContext{Domain: "initial.example"}),
		"the last good generation keeps serving")

	hits, _, publishes, failures := derivedCacheDiagnostics()
	require.Zero(t, hits)
	require.EqualValues(t, 1, publishes, "only the initial load published")
	require.Zero(t, failures)
	requireEveryDerivedManifestNames(t, base, key, initialDigest, initialSize)
}

// TestDerivedRuleSetRenamedAwayAfterOpenStillUsesTheOpenedFile is defect case (f), second half: a
// reload whose target is renamed away after it was opened must still read the opened descriptor
// instead of failing or re-opening the missing path.
func TestDerivedRuleSetRenamedAwayAfterOpenStillUsesTheOpenedFile(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "moving.json")
	movedPath := filepath.Join(directory, "moved.json")
	loaded := derivedJSONDomainSource("loaded.example")
	require.NoError(t, os.WriteFile(path, loaded, 0o600))
	ruleSet := newDerivedRuleSet(t, ctx, "moving", path, C.RuleSetFormatSource)

	// Change the file so the reload cannot be a warm hit on the artifact just published.
	next := derivedJSONDomainSource("next.example")
	require.NoError(t, os.WriteFile(path, next, 0o600))
	installReloadWindowAction(t, path, func() {
		require.NoError(t, os.Rename(path, movedPath))
	})
	require.NoError(t, ruleSet.reloadFile(path),
		"the descriptor opened before the rename must still carry the bytes")
	requireDomainMatch(t, ruleSet, "next.example", true)
	requireDomainMatch(t, ruleSet, "loaded.example", false)
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	nextDigest, nextSize := derivedContentDigest(next)
	key := localDerivedSource("moving", path, C.RuleSetFormatSource, nextDigest, nextSize).key
	requireEveryDerivedManifestNames(t, base, key, nextDigest, nextSize)

	// Putting the file back makes the conversion nameable again; it is a warm hit.
	require.NoError(t, os.Rename(movedPath, path))
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "next.example", true)
	hits, _, _, failures := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits)
	require.Zero(t, failures)
}

// TestDerivedRuleSetNoManifestNamesBytesItDidNotParse is the manifest-level regression: drive the
// in-place window, then verify for every version in the store that its manifest names the parsed
// bytes AND that mapping its artifact really serves a rule set built from those bytes.
func TestDerivedRuleSetNoManifestNamesBytesItDidNotParse(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "manifest-level.json")
	preEdit := derivedJSONDomainSource("before01.example")
	parsed := derivedJSONDomainSource("parsed01.example")
	require.Equal(t, len(preEdit), len(parsed))

	require.NoError(t, os.WriteFile(path, preEdit, 0o600))
	installReloadWindowAction(t, path, func() {
		require.NoError(t, os.WriteFile(path, parsed, 0o600))
	})
	ruleSet := newDerivedRuleSet(t, ctx, "manifest-level", path, C.RuleSetFormatSource)
	requireDomainMatch(t, ruleSet, "parsed01.example", true)
	requireDomainMatch(t, ruleSet, "before01.example", false)

	preEditDigest, _ := derivedContentDigest(preEdit)
	parsedDigest, parsedSize := derivedContentDigest(parsed)
	require.NotEqual(t, preEditDigest, parsedDigest)
	key := localDerivedSource("manifest-level", path, C.RuleSetFormatSource, parsedDigest, parsedSize).key

	manifests := derivedManifests(t, base, key)
	require.Len(t, manifests, 1)
	for _, manifest := range manifests {
		require.NotContains(t, manifest.Dependencies, "source-sha256="+preEditDigest,
			"a version compiled from the parsed bytes may not be attributed to the pre-edit snapshot")
		require.Contains(t, manifest.Dependencies, "source-sha256="+parsedDigest)
		require.Contains(t, manifest.Dependencies, "source-size="+strconv.FormatInt(parsedSize, 10))
		require.Contains(t, manifest.Dependencies, "identity-generation="+strconv.Itoa(derivedIdentityGeneration))
	}

	// The artifact really serves the rule set the manifest names.
	version, err := ruleSet.derivedCache.store.Load(key)
	require.NoError(t, err)
	file, err := version.Open()
	require.NoError(t, err)
	mapped, err := srs.ReadMmapPersistent(file, version.Release)
	_ = file.Close()
	require.NoError(t, err)
	plain, err := mapped.Upgrade()
	require.NoError(t, err)
	require.NotEmpty(t, plain.Rules)
	headless, err := NewHeadlessRule(ctx, plain.Rules[0])
	require.NoError(t, err)
	require.True(t, matchAnyHeadlessRule([]adapter.HeadlessRule{headless},
		&adapter.InboundContext{Domain: "parsed01.example"}))
	require.False(t, matchAnyHeadlessRule([]adapter.HeadlessRule{headless},
		&adapter.InboundContext{Domain: "before01.example"}))
}

// TestDerivedRuleSetFutureLegacyManifestStillMigrates covers the clock-skew boundary the other
// migration tests do not: the pre-fix manifest's Created is AHEAD of this host's clock.
//
// The store ranks versions by Created and Load returns the newest one, so without dropping the
// refused version every load would refuse it, publish a correctly attributed version with a smaller
// stamp, and the next Collect would delete that correct version as the non-newest one - the cache
// would never warm again while the stale entry stayed "newest". Correctness (never serving the stale
// bytes) is asserted as everywhere; this test additionally requires the migration to COMPLETE.
func TestDerivedRuleSetFutureLegacyManifestStillMigrates(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	resetDerivedCacheDiagnostics()
	ctx, base := derivedTestContext(t)
	path := filepath.Join(t.TempDir(), "legacy-future.json")
	sourceA := derivedJSONDomainSource("future-a.example")
	sourceB := derivedJSONDomainSource("future-b.example")
	require.NoError(t, os.WriteFile(path, sourceA, 0o600))

	store, err := artifact.Open(filepath.Join(base, derivedStoreDirectory))
	require.NoError(t, err)
	cache := &derivedRuleSetCache{ctx: ctx, store: store, logger: logger.NOP()}
	digestA, sizeA := derivedContentDigest(sourceA)
	identityA := localDerivedSource("legacy-future", path, C.RuleSetFormatSource, digestA, sizeA)
	compiledFromB := encodeDerivedMmapRuleSet(t, parseDerivedJSONSource(t, sourceB))
	manifest := oldStyleDerivedManifest(t, cache, identityA, C.RuleSetVersionCurrent)
	// The stale entry claims to have been published an hour from now, e.g. after a clock correction.
	manifest.Created = time.Now().Add(time.Hour).UnixNano()
	poisoned, err := store.Commit(identityA.key, manifest, bytes.NewReader(compiledFromB))
	require.NoError(t, err)
	poisoned.Release()

	ruleSet := newDerivedRuleSet(t, ctx, "legacy-future", path, C.RuleSetFormatSource)
	requireDomainMatch(t, ruleSet, "future-a.example", true)
	requireDomainMatch(t, ruleSet, "future-b.example", false)

	// The refused version must not keep shadowing the corrected one: the next load is a hit.
	require.NoError(t, ruleSet.reloadFile(path))
	requireDomainMatch(t, ruleSet, "future-a.example", true)
	hits, _, publishes, failures := derivedCacheDiagnostics()
	require.EqualValues(t, 1, hits,
		"the migration must complete even when the stale manifest's timestamp is in the future")
	require.EqualValues(t, 1, publishes)
	require.Zero(t, failures)
	require.Len(t, derivedVersions(t, base, identityA.key), 1)
	requireEveryDerivedManifestNames(t, base, identityA.key, digestA, sizeA)
}
