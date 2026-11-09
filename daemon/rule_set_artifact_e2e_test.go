package daemon

// B3 — a real routing request across a remote rule-set version switch.
//
// The Box is the product composition (HTTP inbound, direct outbound, one remote binary rule set).
// The rule-set server changes its document; the periodic refresh validates it, publishes a new
// versioned artifact and swaps the live rules. The request through the HTTP inbound flips from
// rejected to served, which is the switch observed by traffic rather than by a report field. A
// corrupt document afterwards changes neither the traffic nor the stored versions.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

func binaryRuleSetForTest(t *testing.T, prefix string) []byte {
	t.Helper()
	var payload bytes.Buffer
	err := srs.Write(&payload, option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				IPCIDR: badoption.Listable[*badoption.Prefixable]{
					common.Ptr(badoption.Prefixable(netip.MustParsePrefix(prefix))),
				},
			},
		}},
	}, C.RuleSetVersionCurrent)
	require.NoError(t, err)
	return payload.Bytes()
}

func ruleSetDigest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// storedArtifactDigests lists the digest recorded by every version in the store.
func storedArtifactDigests(t *testing.T, root string) []string {
	t.Helper()
	var digests []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.IsDir() || !strings.HasPrefix(entry.Name(), "v-") {
			return nil
		}
		encoded, readErr := os.ReadFile(filepath.Join(path, "manifest.json"))
		if readErr != nil {
			return nil
		}
		var manifest struct {
			Digest string `json:"digest"`
		}
		if json.Unmarshal(encoded, &manifest) == nil && manifest.Digest != "" {
			digests = append(digests, manifest.Digest)
		}
		return nil
	})
	return digests
}

func TestARemoteRuleSetVersionSwitchChangesRealRouting(t *testing.T) {
	ctx := recoveryBusinessContext(t)
	storeRoot := filemanager.BasePath(ctx, "rule-set-artifacts")

	rejectLoopback := binaryRuleSetForTest(t, "127.0.0.1/32")
	allowLoopback := binaryRuleSetForTest(t, "10.0.0.0/8")
	var documentAccess sync.RWMutex
	document := rejectLoopback
	var requests int
	var requestsAccess sync.Mutex
	ruleSetServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		documentAccess.RLock()
		content := document
		documentAccess.RUnlock()
		requestsAccess.Lock()
		requests++
		requestsAccess.Unlock()
		_, _ = writer.Write(content)
	}))
	t.Cleanup(ruleSetServer.Close)
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	proxyPort := freeLoopbackPort(t)
	profile := fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {
    "rule_set": [{"type": "remote", "tag": "gate", "format": "binary", "url": %q, "update_interval": "50ms"}],
    "rules": [{"rule_set": "gate", "action": "reject"}],
    "final": "direct"
  }
}`, proxyPort, ruleSetServer.URL)
	service := NewStartedService(ServiceOptions{Context: ctx})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), profile, nil))
	client := newProxyClient(t, proxyPort)
	defer client.CloseIdleConnections()

	served := func() bool {
		status, err := proxyStatus(t, client, target.URL)
		return err == nil && status == http.StatusOK
	}
	require.False(t, served(), "the first rule-set version rejects the loopback target")
	require.Len(t, storedArtifactDigests(t, storeRoot), 1)

	documentAccess.Lock()
	document = allowLoopback
	documentAccess.Unlock()
	require.Eventually(t, served, 10*time.Second, 25*time.Millisecond,
		"the refreshed rule-set version allows the loopback target")

	requestsAccess.Lock()
	requestCount := requests
	requestsAccess.Unlock()
	require.Greater(t, requestCount, 1, "the switch came from a real refresh")

	digests := storedArtifactDigests(t, storeRoot)
	require.Contains(t, digests, ruleSetDigest(allowLoopback),
		"the new document is stored as a version named by its digest")
	require.NotContains(t, digests, ruleSetDigest(rejectLoopback),
		"the superseded version is collected once the live rules moved on")

	// A corrupt update must change neither the traffic nor the stored versions.
	documentAccess.Lock()
	document = []byte("this is not a binary rule set")
	documentAccess.Unlock()
	time.Sleep(300 * time.Millisecond)
	require.True(t, served(), "a bad document must keep the published rules serving")
	require.Len(t, storedArtifactDigests(t, storeRoot), len(digests), "a bad document must not become a version")
}
