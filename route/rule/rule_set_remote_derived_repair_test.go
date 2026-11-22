//go:build unix

package rule

// C07 at the remote identity: a loader snapshots a never-usable (pre-identity-generation) manifest,
// a concurrent publisher repairs the SAME artifact bytes, and the loader's late discard must re-read
// the record instead of deleting the repaired conversion. The repair and the discard are deterministic
// (the test blocks inside open, repairs, then releases); production only sees a nil hook.

import (
	"bytes"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/common/artifact"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"

	"github.com/stretchr/testify/require"
)

func TestRemoteDerivedC07LateDiscardKeepsTheRepairedConversion(t *testing.T) {
	enableDerivedRuleSetPlatform(t)
	ctx, base := derivedTestContext(t)
	store, err := artifact.Open(filepath.Join(base, derivedStoreDirectory))
	require.NoError(t, err)
	cache := &derivedRuleSetCache{ctx: ctx, store: store, logger: log.NewNOPFactory().Logger()}

	source := derivedJSONDomainSource("repair.example")
	digest, size := derivedContentDigest(source)
	identity := remoteDerivedSource("repair", "artifact-key-repair", C.RuleSetFormatSource, digest, size)
	compiled := encodeDerivedMmapRuleSet(t, parseDerivedJSONSource(t, source))

	// The pre-fix account of these exact bytes: no identity generation, so this build can never use it.
	stale, err := store.Commit(identity.key,
		oldStyleDerivedManifest(t, cache, identity, C.RuleSetVersionCurrent), bytes.NewReader(compiled))
	require.NoError(t, err)
	stale.Release()

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	derivedOpenTestHook = func(point string) {
		if point != "manifest-snapshot" {
			return
		}
		once.Do(func() { close(entered) })
		<-release
	}
	t.Cleanup(func() { derivedOpenTestHook = nil })

	result := make(chan bool, 1)
	go func() {
		_, hit := cache.open(identity)
		result <- hit
	}()
	<-entered

	// Repair the same physical version while the loader still holds the stale snapshot.
	corrected, err := store.Commit(identity.key,
		cache.derivedManifest(identity, C.RuleSetVersionCurrent, 0), bytes.NewReader(compiled))
	require.NoError(t, err)
	require.Equal(t, stale.Path(), corrected.Path(), "the repair is one physical version")
	corrected.Release()
	close(release)

	require.False(t, <-result, "the stale snapshot is a miss, not a hit")
	require.Len(t, derivedVersions(t, base, identity.key), 1,
		"the late discard must not delete the repaired account")
	loaded, err := store.Load(identity.key)
	require.NoError(t, err)
	require.Contains(t, loaded.Manifest().Dependencies, "identity-generation="+strconv.Itoa(derivedIdentityGeneration))
	loaded.Release()

	// The repaired conversion is now loadable; the repair did not leave the cache permanently cold.
	derivedOpenTestHook = nil
	_, hit := cache.open(identity)
	require.True(t, hit, "the repaired conversion must be serveable")
}
