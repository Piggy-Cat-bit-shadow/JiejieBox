package daemon

// W7 / X11 (ORIGINAL oracle, task book 30 section 7.2): "configtx prepare of several resource
// classes -> the new configuration fails -> recover last-good -> the NEXT round succeeds | the exact
// old combination is recoverable, without referencing a derived ID or half a snapshot".
//
// The two existing per-resource recovery tests (daemon/config_recovery_provider_test.go and
// daemon/config_recovery_references_test.go) each pin ONE resource class. This test is the combined
// shape the oracle asks for, with a real Provider and a real rule source in the SAME saved
// combination, and it adds the third step the oracle requires: after a successful recovery, the next
// configuration round must succeed and become the new saved combination.
//
// Witnesses are business-visible:
//   - the provider snapshot decides WHICH node tag is published and whether a real request is carried
//     ("nodes/alpha" vs "nodes/beta" vs "nodes/gamma" through a local SOCKS server);
//   - the rule source decides whether the same request is REJECTED or routed (binary rule-set A
//     rejects the loopback target; B/D allow it).
// A recovery that restored A's document with B's snapshot would publish "nodes/beta"; one that
// restored A's provider but B's rules would serve the request. Both are distinguished here.
//
// Platform note: the derived SRSM cache is inactive in this product build (`mmapRuleSetPlatform` is
// false outside iOS), so the reference set names exactly the two SOURCE artifacts and no derived ID.
// That absence is asserted (len==2) and recorded in COVERAGE.csv as a platform constraint, not
// silently treated as covered.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/artifact"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// w7X11ProviderDocument is a provider document whose node tag names the generation, so the published
// provider tag is the witness.
func w7X11ProviderDocument(tag string, socksPort int) string {
	return fmt.Sprintf(`{"outbounds":[{"type":"socks","tag":%q,"server":"127.0.0.1","server_port":%d,"version":"5"}]}`, tag, socksPort)
}

// w7X11Profile is the product composition with one remote provider AND one remote binary rule-set.
func w7X11Profile(proxyPort int, providerURL string, ruleSetURL string, logLevel string) string {
	return fmt.Sprintf(`{
  "log": {"level": %q},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "providers": [{"type": "remote", "tag": "nodes", "url": %q, "update_interval": "1h"}],
  "outbounds": [
    {"type": "selector", "tag": "selected", "providers": ["nodes"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {
    "rule_set": [{"type": "remote", "tag": "gate", "format": "binary", "url": %q, "update_interval": "50ms"}],
    "rules": [
      {"rule_set": "gate", "action": "reject"},
      {"inbound": ["proxy"], "action": "route", "outbound": "selected"}
    ],
    "final": "direct"
  }
}`, logLevel, proxyPort, providerURL, ruleSetURL)
}

func w7X11ProviderTags(t *testing.T, service *StartedService) []string {
	t.Helper()
	instance := service.Instance()
	require.NotNil(t, instance)
	tags := make([]string, 0, 4)
	for _, object := range instance.Box().Outbound().Outbounds() {
		if len(object.Tag()) > len("nodes/") && object.Tag()[:len("nodes/")] == "nodes/" {
			tags = append(tags, object.Tag())
		}
	}
	return tags
}

func TestX11ConfigtxMultiResourceFailureRecoversExactCombinationAndNextRoundSucceeds(t *testing.T) {
	socksPort := startProviderSocksServer(t)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	// The provider subscription is a real HTTP document; its content is what the running Box sees.
	var providerAccess sync.RWMutex
	providerDocument := w7X11ProviderDocument("alpha", socksPort)
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		providerAccess.RLock()
		content := providerDocument
		providerAccess.RUnlock()
		_, _ = writer.Write([]byte(content))
	}))
	t.Cleanup(providerServer.Close)
	setProviderDocument := func(document string) {
		providerAccess.Lock()
		providerDocument = document
		providerAccess.Unlock()
	}

	// The rule source is a real binary rule-set document.
	rejectLoopback := binaryRuleSetForTest(t, "127.0.0.1/32")
	allowLoopback := binaryRuleSetForTest(t, "10.0.0.0/8")
	var ruleSetAccess sync.RWMutex
	ruleSetDocument := rejectLoopback
	var ruleSetFailing atomic.Bool
	ruleSetServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if ruleSetFailing.Load() {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		ruleSetAccess.RLock()
		content := ruleSetDocument
		ruleSetAccess.RUnlock()
		_, _ = writer.Write([]byte(content))
	}))
	t.Cleanup(ruleSetServer.Close)
	setRuleSetDocument := func(document []byte) {
		ruleSetAccess.Lock()
		ruleSetDocument = document
		ruleSetAccess.Unlock()
	}

	directory := t.TempDir()
	proxyPort := freeLoopbackPort(t)
	configA := w7X11Profile(proxyPort, providerServer.URL, ruleSetServer.URL, "error")
	configB := w7X11Profile(proxyPort, providerServer.URL, ruleSetServer.URL, "warn")
	configD := w7X11Profile(proxyPort, providerServer.URL, ruleSetServer.URL, "info")
	revisionA := configRevisionOf(configA)
	revisionD := configRevisionOf(configD)

	// B runs but the disk refuses to make it the active document: the store keeps A as last-good.
	ctx := recoveryBusinessContext(t)
	store := recoveryStoreWithWriteFault(t, directory, []byte(configB))
	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	service.ConfigureConfigRecovery(directory, true)
	installStore(service, store)

	served := func() bool {
		client := newProxyClient(t, proxyPort)
		defer client.CloseIdleConnections()
		status, err := proxyStatus(t, client, target.URL)
		t.Logf("X11 served probe: status=%d err=%v", status, err)
		return err == nil && status == http.StatusOK
	}

	// ---- A: the saved combination (provider snapshot alpha + rule source reject). ----
	require.NoError(t, service.StartOrReloadService(context.Background(), configA, nil))
	// The provider's initial fetch is asynchronous; the production manual refresh is what publishes
	// the first nodes, and an identical reload persists their exact snapshot as a reference.
	_, err := service.RefreshProviders(context.Background(), []string{"nodes"}, 5*time.Second)
	require.NoError(t, err)
	require.NoError(t, service.StartOrReloadService(context.Background(), configA, nil))
	require.Equal(t, persistenceSaved, service.LastServiceChange().Persistence)
	referencesA, err := store.References(revisionA)
	require.NoError(t, err)
	require.Len(t, referencesA, 2,
		"the saved combination names exactly the provider snapshot and the rule-set source: %v", referencesA)
	require.False(t, served(), "A's rule source rejects the loopback target")
	require.Equal(t, []string{"nodes/alpha"}, w7X11ProviderTags(t, service),
		"A's provider snapshot is what is published")
	providerReferences, ruleSetReferences := 0, 0
	for key := range referencesA {
		switch {
		case strings.HasPrefix(key, "nodes-"):
			providerReferences++
		case strings.HasPrefix(key, "gate-"):
			ruleSetReferences++
		}
	}
	require.Equal(t, 1, providerReferences, "one reference is the provider snapshot: %v", referencesA)
	require.Equal(t, 1, ruleSetReferences, "one reference is the rule-set source: %v", referencesA)
	t.Logf("X11 A: references=%v", referencesA)

	// ---- B: both sources change, B runs, but the disk refuses to save B. ----
	setProviderDocument(w7X11ProviderDocument("beta", socksPort))
	setRuleSetDocument(allowLoopback)
	require.NoError(t, service.StartOrReloadService(context.Background(), configB, nil))
	_, err = service.RefreshProviders(context.Background(), []string{"nodes"}, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, persistenceDegraded, service.LastServiceChange().Persistence,
		"B runs but the disk refused it, so only A is recoverable")
	t.Logf("X11 B report: %+v tags=%v", service.LastServiceChange(), w7X11ProviderTags(t, service))
	// The rule-set updater is asynchronous (the production timer), so the new allow document becomes
	// live within one update interval; the provider republication is driven by the manual refresh.
	require.Eventually(t, served, 15*time.Second, 25*time.Millisecond,
		"B's rule source allows the loopback target once its update lands")
	require.Equal(t, []string{"nodes/beta"}, w7X11ProviderTags(t, service),
		"B published the changed subscription, not A's snapshot")
	referencesB, err := store.References(configRevisionOf(configB))
	require.NoError(t, err)
	require.Empty(t, referencesB, "an unsaved configuration must not become a recovery target")
	t.Log("X11 B: both resource classes changed and are serving; B is unsaved")

	// ---- C: the new configuration fails; recovery must restore the EXACT old combination. ----
	// The rule-set SOURCE is made unreachable for the recovery window on purpose: the recovered Box
	// must serve A's PINNED rule version, not the newest stored one (B's allow). If the recovery used
	// the newest version, the live rules would stay "allow" and nothing could correct them because the
	// source is failing - so the assertion below has no race with the periodic updater.
	ruleSetFailing.Store(true)
	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))
	report := service.LastServiceChange()
	require.Equal(t, ServiceChangeStatusRecovered, report.Status)
	require.Equal(t, revisionA, report.ActiveRevision)
	require.Eventually(t, func() bool { return !served() }, 10*time.Second, 25*time.Millisecond,
		"recovery must restore A's rejecting rule source, not B's allow rule")
	require.Equal(t, []string{"nodes/alpha"}, w7X11ProviderTags(t, service),
		"recovery must restore A's provider snapshot, not the current subscription (beta)")

	recoveredReferences, err := store.References(revisionA)
	require.NoError(t, err)
	require.Equal(t, referencesA, recoveredReferences,
		"the recovered combination is the SAME pair of references, not a re-derived one")

	// Both pinned source versions are still loadable and are exactly A's bytes: the recovery did not
	// need (and did not use) a derived ID or half a snapshot.
	providerKey, ruleSetKey := "", ""
	for key := range referencesA {
		switch {
		case strings.HasPrefix(key, "nodes-"):
			providerKey = key
		case strings.HasPrefix(key, "gate-"):
			ruleSetKey = key
		}
	}
	require.NotEmpty(t, providerKey)
	require.NotEmpty(t, ruleSetKey)
	require.Equal(t, ruleSetDigest(rejectLoopback), referencesA[ruleSetKey],
		"the pinned rule source is A's rejecting document")
	require.NotEqual(t, ruleSetDigest(allowLoopback), referencesA[ruleSetKey])
	require.Equal(t, referencesA[providerKey], recoveredReferences[providerKey])
	t.Logf("X11 C: recovered exact pair provider=%s rule=%s", referencesA[providerKey], referencesA[ruleSetKey])

	// Both pinned artifact versions are loadable from the real store: the recovery restored exactly
	// these bytes, and the platform has no derived ID to reference (see the file header).
	storeRoot := filemanager.BasePath(ctx, "")
	require.NotEmpty(t, storeRoot)
	providerStore, err := artifact.Open(filepath.Join(storeRoot, "provider-artifacts"))
	require.NoError(t, err)
	pinnedProvider, err := providerStore.LoadExact(providerKey, referencesA[providerKey])
	require.NoError(t, err, "A's provider snapshot bytes are retained for the recovery")
	pinnedProvider.Release()
	ruleStore, err := artifact.Open(filepath.Join(storeRoot, "rule-set-artifacts"))
	require.NoError(t, err)
	pinnedRule, err := ruleStore.LoadExact(ruleSetKey, referencesA[ruleSetKey])
	require.NoError(t, err, "A's rule-source bytes are retained for the recovery")
	pinnedRule.Release()

	// ---- D: the NEXT round must succeed and become the new combination. ----
	ruleSetFailing.Store(false)
	setProviderDocument(w7X11ProviderDocument("gamma", socksPort))
	setRuleSetDocument(allowLoopback)
	require.NoError(t, service.StartOrReloadService(context.Background(), configD, nil))
	_, err = service.RefreshProviders(context.Background(), []string{"nodes"}, 5*time.Second)
	require.NoError(t, err)
	require.NoError(t, service.StartOrReloadService(context.Background(), configD, nil))
	reportD := service.LastServiceChange()
	require.Equal(t, persistenceSaved, reportD.Persistence, "the next round is saved again, not degraded")
	require.Equal(t, revisionD, reportD.ActiveRevision)
	require.NotEqual(t, ServiceChangeStatusRecovered, reportD.Status)
	require.Eventually(t, served, 15*time.Second, 25*time.Millisecond, "D's rule source allows the target")
	require.Equal(t, []string{"nodes/gamma"}, w7X11ProviderTags(t, service),
		"the next round publishes its own provider snapshot")
	referencesD, err := store.References(revisionD)
	require.NoError(t, err)
	require.Len(t, referencesD, 2, "the new combination names its own provider and rule-source versions")
	require.NotEqual(t, referencesA[ruleSetKey], referencesD[ruleSetKey],
		"the new round references its own rule-source version, not A's")
	require.Equal(t, ruleSetDigest(allowLoopback), referencesD[ruleSetKey])
	t.Logf("X11 D: next round saved references=%v", referencesD)

	// No stray artifact is required to still exist for A: the new commit moved the live references.
	require.NoError(t, service.CloseService())
}
