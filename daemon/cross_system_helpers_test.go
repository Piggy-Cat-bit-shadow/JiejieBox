package daemon

// Shared fixtures for agent E's host-executable cross-system scenarios.
//
// These tests drive the real product composition (`NewStartedService` → a real `box.Box`), because
// the questions they answer are about ownership between components that a unit test would have to
// fake: a provider's build reservation, a Box's scope drain, two Boxes sharing one artifact
// directory, and the URL-test services a Box creates for itself.
//
// `recoveryBusinessContext` in config_recovery_business_test.go is the closest existing fixture, but
// it creates its own `t.TempDir()` per call, which cannot express "two Boxes on one artifact store
// directory". These helpers take the directory as an argument for that reason and are otherwise the
// same registry set, plus one recording outbound type so a node CONSTRUCTION is a countable fact
// rather than an inference from the published list.

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxprovider "github.com/sagernet/sing-box/adapter/provider"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	protocolblock "github.com/sagernet/sing-box/protocol/block"
	protocoldirect "github.com/sagernet/sing-box/protocol/direct"
	protocolgroup "github.com/sagernet/sing-box/protocol/group"
	protocolhttp "github.com/sagernet/sing-box/protocol/http"
	protocolsocks "github.com/sagernet/sing-box/protocol/socks"
	providerlocal "github.com/sagernet/sing-box/provider/local"
	providerremote "github.com/sagernet/sing-box/provider/remote"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// crossSystemNodeType is a node type whose constructor is observed. A provider publish is a
// construction of the document's outbounds; counting constructions is therefore a direct witness of
// "the document reached the published graph", and a cancelled/closed build must produce none.
const crossSystemNodeType = "cross-system-node"

type crossSystemNodeOptions struct{}

type crossSystemNode struct {
	outbound.Adapter
}

func (n *crossSystemNode) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, net.ErrClosed
}

func (n *crossSystemNode) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, net.ErrClosed
}

// crossSystemRecorder records every construction the test's outbound registry performs.
type crossSystemRecorder struct {
	access sync.Mutex
	tags   []string
}

func (r *crossSystemRecorder) record(tag string) {
	if r == nil {
		return
	}
	r.access.Lock()
	r.tags = append(r.tags, tag)
	r.access.Unlock()
}

func (r *crossSystemRecorder) snapshot() []string {
	if r == nil {
		return nil
	}
	r.access.Lock()
	defer r.access.Unlock()
	return append([]string(nil), r.tags...)
}

// countMatching counts constructions whose tag contains substr.
func (r *crossSystemRecorder) countMatching(substr string) int {
	count := 0
	for _, tag := range r.snapshot() {
		if strings.Contains(tag, substr) {
			count++
		}
	}
	return count
}

// crossSystemContext builds the product registry set over a CALLER-SUPPLIED directory.
//
// `directory` is the file manager's base path, which is where every artifact store resolves
// (`<directory>/provider-artifacts`, `<directory>/rule-set-artifacts`). Passing it in - instead of
// creating one - is what lets two Boxes in one process share a store directory, the scenario this
// fixture exists for.
//
// `recorder` may be nil; when set, every construction of `crossSystemNodeType` is recorded.
func crossSystemContext(t *testing.T, directory string, recorder *crossSystemRecorder) context.Context {
	t.Helper()
	inboundRegistry := inbound.NewRegistry()
	protocolhttp.RegisterInbound(inboundRegistry)
	outboundRegistry := outbound.NewRegistry()
	protocoldirect.RegisterOutbound(outboundRegistry)
	protocolblock.RegisterOutbound(outboundRegistry)
	protocolsocks.RegisterOutbound(outboundRegistry)
	protocolgroup.RegisterSelector(outboundRegistry)
	protocolgroup.RegisterURLTest(outboundRegistry)
	if recorder != nil {
		outbound.Register[crossSystemNodeOptions](outboundRegistry, crossSystemNodeType,
			func(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ crossSystemNodeOptions) (adapter.Outbound, error) {
				recorder.record(tag)
				return &crossSystemNode{Adapter: outbound.NewAdapter(crossSystemNodeType, tag, []string{"tcp"}, nil)}, nil
			})
	}
	providerRegistry := boxprovider.NewRegistry()
	providerlocal.RegisterProviderInline(providerRegistry)
	providerlocal.RegisterProviderLocal(providerRegistry)
	providerremote.RegisterProvider(providerRegistry)
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	ctx := box.Context(context.Background(),
		inboundRegistry,
		outboundRegistry,
		providerRegistry,
		endpoint.NewRegistry(),
		dnsRegistry,
		boxservice.NewRegistry(),
		certificate.NewRegistry(),
	)
	return filemanager.WithDefault(ctx, directory, directory, -1, -1)
}

// crossSystemVersion is one version directory found in an artifact store.
type crossSystemVersion struct {
	Key     string
	Dir     string
	Digest  string
	Schema  string
	Policy  string
	Created int64
}

// crossSystemArtifactVersions walks a store root and returns every complete version it holds.
//
// The walk is deliberately by the documented on-disk layout (`<key>/<version>/manifest.json`) and not
// by opening the store: the test has to observe what is ON DISK after a Box closed, and `Store.Load`
// takes a lease, which would itself protect the version from collection.
func crossSystemArtifactVersions(t *testing.T, storeRoot string) []crossSystemVersion {
	t.Helper()
	var versions []crossSystemVersion
	keyEntries, err := os.ReadDir(storeRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		require.NoError(t, err)
	}
	for _, keyEntry := range keyEntries {
		if !keyEntry.IsDir() || strings.HasPrefix(keyEntry.Name(), ".") {
			continue
		}
		keyDir := filepath.Join(storeRoot, keyEntry.Name())
		versionEntries, readErr := os.ReadDir(keyDir)
		if readErr != nil {
			continue
		}
		for _, versionEntry := range versionEntries {
			if !versionEntry.IsDir() || strings.HasPrefix(versionEntry.Name(), ".tmp-") {
				continue
			}
			versionDir := filepath.Join(keyDir, versionEntry.Name())
			encoded, readErr := os.ReadFile(filepath.Join(versionDir, "manifest.json"))
			if readErr != nil {
				continue
			}
			var manifest struct {
				Schema  string `json:"schema"`
				Policy  string `json:"policy"`
				Digest  string `json:"digest"`
				Created int64  `json:"created"`
			}
			require.NoError(t, json.Unmarshal(encoded, &manifest), "manifest at %s", versionDir)
			versions = append(versions, crossSystemVersion{
				Key:     keyEntry.Name(),
				Dir:     versionDir,
				Digest:  manifest.Digest,
				Schema:  manifest.Schema,
				Policy:  manifest.Policy,
				Created: manifest.Created,
			})
		}
	}
	sort.Slice(versions, func(left, right int) bool {
		if versions[left].Key != versions[right].Key {
			return versions[left].Key < versions[right].Key
		}
		return versions[left].Digest < versions[right].Digest
	})
	return versions
}

// crossSystemKeyForTag returns the artifact-store key whose manifests carry `tag=<tag>`.
func crossSystemKeyForTag(t *testing.T, versions []crossSystemVersion, tag string) string {
	t.Helper()
	for _, version := range versions {
		if version.Policy == "tag="+tag {
			return version.Key
		}
	}
	return ""
}

// crossSystemVersionPresent reports whether the exact version directory is still on disk.
func crossSystemVersionsFor(t *testing.T, storeRoot string, key string) []crossSystemVersion {
	t.Helper()
	var matched []crossSystemVersion
	for _, version := range crossSystemArtifactVersions(t, storeRoot) {
		if version.Key == key {
			matched = append(matched, version)
		}
	}
	return matched
}

// providerNodesOf returns the node tags a provider publishes, e.g. "nodes/hk".
func providerNodesOf(t *testing.T, provider adapter.Provider) []string {
	t.Helper()
	tags := make([]string, 0, len(provider.Outbounds()))
	for _, node := range provider.Outbounds() {
		tags = append(tags, node.Tag())
	}
	sort.Strings(tags)
	return tags
}

// crossSystemGroupMembers returns a group's live member list, read through the outbound manager's
// public group interface. A provider publish reaches the published graph through the group's
// callback registration, so this is the callback outcome rather than a provider-internal view.
func crossSystemGroupMembers(t *testing.T, instance *Instance, tag string) []string {
	t.Helper()
	outbound, loaded := instance.Box().Outbound().Outbound(tag)
	require.True(t, loaded, "group %q is not loaded", tag)
	outboundGroup, isGroup := outbound.(adapter.OutboundGroup)
	require.True(t, isGroup, "outbound %q is %T, not a group", tag, outbound)
	members := outboundGroup.All()
	sort.Strings(members)
	return members
}

// crossSystemBinaryRuleSet builds one binary rule-set document whose only condition is the given
// domain suffixes. Two rules with disjoint domains are therefore distinguishable by the public
// `Match` API, which is how the isolation tests observe whose document a Box is serving.
func crossSystemBinaryRuleSet(t *testing.T, domains ...string) []byte {
	t.Helper()
	var payload bytes.Buffer
	require.NoError(t, srs.Write(&payload, option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{
				DomainSuffix: badoption.Listable[string](domains),
			},
		}},
	}, C.RuleSetVersionCurrent))
	return payload.Bytes()
}
