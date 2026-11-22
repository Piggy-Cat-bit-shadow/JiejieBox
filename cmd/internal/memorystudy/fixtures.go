package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// The fixtures are the product's own configuration shape, not a benchmark harness's: a SOCKS
// inbound that carries a real relay, a direct outbound for the traffic path, the ordinary nodes the
// subscription would have produced, and the selection groups a client builds over them. Everything
// is generated from the run spec so that two runs differ only in the parameters under test.
//
// # Why the document is generated as text
//
// The configuration-processing cost this ledger attributes is the cost of turning a DOCUMENT into a
// runtime. Building the option tree in Go and then measuring the decode would measure a different
// pipeline than the one the product runs on every launch. Generating the bytes also makes the
// document-size term visible: a 10 000-node subscription is a megabyte of JSON before it is anything
// else.

// fixtureVersion names the generated-document contract.
//
// # Bump this when the generated document changes
//
// A budget's thresholds are only comparable across runs of the SAME document shape. The version is
// recorded in the ledger, in the reference manifest and in the budget, and the gate refuses a
// comparison across versions. The per-run document digest next to it is what catches an edit that
// changed the document without bumping this string; a version bump alone would be a promise, and the
// gate is not allowed to take promises.
const fixtureVersion = "memorystudy-fixture/1"

// Fixture identities that the measurement stages address by name.
const (
	fixtureOutboundTag    = "out"
	fixtureInboundTag     = "in"
	fixtureProviderTag    = "nodes"
	fixtureSelectorPrefix = "group-selector"
	fixtureURLTestPrefix  = "group-urltest"
)

// fixtureConfig is a generated document plus the tags the stages need to address.
type fixtureConfig struct {
	Document  []byte
	NodeTags  []string
	GroupTags []string
	// ProbeTag is the URL-test group the probe stage drives. It is empty when the fixture has no
	// URL-test group, and the probe stage then reports itself unsupported rather than silently
	// measuring nothing.
	ProbeTag string
	ProbeURL string
	Nodes    int
	Groups   int
	// ProviderDocument is the node document a local provider reads, and ProviderTag the tag the
	// configuration declares it under. Both are empty for a static configuration.
	ProviderDocument []byte
	ProviderTag      string
	// MemberEdges is the number of group-member references the document declares: one per static
	// node named by a group, or one provider edge per group plus the provider's own node list. It
	// is reported so "node cost" and "member-reference cost" cannot be confused.
	MemberEdges int
	// DocumentBytes and ProviderBytes are the exact sizes of the two documents; the node term is a
	// function of both when the nodes live in a provider file.
	ProviderBytes int
}

// fixtureFingerprint hashes the document's SHAPE.
//
// # Why the raw bytes are not the identity
//
// Each child reserves a free local port for its inbound and, for the probe case, starts its fixture
// server on another one. Both appear in the generated document, so two runs of the SAME spec produce
// different bytes by construction - measured: the first version of this digest refused its own
// repeated runs as "the fixture is not deterministic". The ephemeral values are redacted to a single
// token and everything the spec controls (node count, groups, protocols, addresses, provider edges)
// stays in the hash, which is what "the same document" has to mean across sessions.
func fixtureFingerprint(document []byte, ephemeral ...string) string {
	normalized := string(document)
	for _, token := range ephemeral {
		if token == "" {
			continue
		}
		normalized = strings.ReplaceAll(normalized, token, "{{ephemeral}}")
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// nodeServer is TEST-NET-2 (RFC 5737). A never-dialled node must not reach a real peer, and a test
// address makes an accidental dial fail immediately instead of opening a connection to somebody
// else's host.
const nodeServerPrefix = "198.51.100."

// generateFixture renders the configuration document for a spec.
func generateFixture(spec runSpec, inboundPort uint16, probeURL string) fixtureConfig {
	return generateFixtureWithProvider(spec, inboundPort, probeURL, "")
}

// generateFixtureWithProvider renders the configuration document for a spec, and the separate node
// document a local provider reads when spec.ConfigSource is "provider".
//
// # Why the provider document is a second file
//
// A provider subscription is not the configuration: the product keeps the configuration small and
// reads a node document from the filesystem (or a URL). Measuring a provider by inlining its nodes
// would measure the static shape with extra steps. The provider path is passed in because the child
// owns the temporary directory; an empty path is only used by the static tests, which never reach
// the provider branch.
func generateFixtureWithProvider(spec runSpec, inboundPort uint16, probeURL string, providerPath string) fixtureConfig {
	fixture := fixtureConfig{Nodes: spec.Nodes, Groups: spec.Groups, ProbeURL: probeURL}
	builder := &strings.Builder{}
	// A 10 000-node document is roughly 1.6 MB; pre-sizing keeps the builder's own growth out of the
	// config_build measurement.
	builder.Grow(256 + spec.Nodes*180 + spec.Groups*64)

	builder.WriteString(`{"log":{"disabled":true,"level":"error"},"inbounds":[{"type":"socks","tag":"`)
	builder.WriteString(fixtureInboundTag)
	builder.WriteString(`","listen":"127.0.0.1","listen_port":`)
	builder.WriteString(strconv.Itoa(int(inboundPort)))
	builder.WriteString(`}]`)

	providerSource := spec.ConfigSource == "provider"
	if providerSource {
		fixture.ProviderTag = fixtureProviderTag
		fixture.ProviderDocument = generateProviderDocument(spec, 0)
		fixture.ProviderBytes = len(fixture.ProviderDocument)
		builder.WriteString(`,"providers":[{"type":"local","tag":"`)
		builder.WriteString(fixtureProviderTag)
		builder.WriteString(`","path":"`)
		// The child hands in a path it has already normalised to forward slashes, so the string can
		// be embedded without a JSON escaper: a Windows path in JSON with raw backslashes would be a
		// decode error, and quietly rewriting it here would hide that from the caller.
		builder.WriteString(providerPath)
		builder.WriteString(`"}]`)
	}

	builder.WriteString(`,"outbounds":[{"type":"direct","tag":"`)
	builder.WriteString(fixtureOutboundTag)
	builder.WriteString(`"}`)

	fixture.NodeTags = make([]string, 0, spec.Nodes)
	if !providerSource {
		for index := 0; index < spec.Nodes; index++ {
			tag := fmt.Sprintf("node-%05d", index)
			fixture.NodeTags = append(fixture.NodeTags, tag)
			builder.WriteString(`,`)
			writeNode(builder, spec.NodeProto, tag, index, 0)
		}
	} else {
		// The rendered tag is what the product publishes and what a client addresses: "<provider
		// tag>/<node tag>". The fixture records the rendered form so a later stage that looks a node
		// up by tag addresses the same object the product does.
		for index := 0; index < spec.Nodes; index++ {
			fixture.NodeTags = append(fixture.NodeTags,
				fixtureProviderTag+"/"+fmt.Sprintf("node-%05d", index))
		}
	}

	fixture.GroupTags = make([]string, 0, spec.Groups)
	for index := 0; index < spec.Groups; index++ {
		// # Why the group kind depends on the activity
		//
		// A URL-test group starts a probing round in PostStart, so a node inside one is DIALED at
		// startup. That is the right shape when the case is about probing, and the wrong shape when
		// the case is about what a configured-but-never-used node costs: the ledger would report the
		// cost of a hundred failed dials as if it were the cost of a hundred cold objects.
		//
		// So: selector groups (which hold member references and dial nothing) by default, and a
		// URL-test group only when the run asks for a probe.
		probeGroup := hasActivity(spec, "probe") && index == 0
		groupType := "selector"
		prefix := fixtureSelectorPrefix
		if probeGroup {
			groupType = "urltest"
			prefix = fixtureURLTestPrefix
		}
		tag := prefix + "-" + strconv.Itoa(index)
		fixture.GroupTags = append(fixture.GroupTags, tag)
		if groupType == "urltest" && fixture.ProbeTag == "" {
			fixture.ProbeTag = tag
		}
		builder.WriteString(`,`)
		if providerSource {
			// One provider edge per group. The provider's own node list is the other edge set; the
			// two are counted apart so the ledger cannot present a thousand node references as a
			// thousand extra nodes.
			writeProviderGroup(builder, groupType, tag, probeURL)
		} else {
			fixture.MemberEdges += len(fixture.NodeTags)
			writeGroup(builder, groupType, tag, fixture.NodeTags, probeURL)
		}
	}
	if providerSource {
		fixture.MemberEdges = spec.Groups + spec.Nodes
	}

	builder.WriteString(`],"route":{"final":"`)
	builder.WriteString(fixtureOutboundTag)
	// One fixed rule keeps the rule set non-empty and identical across runs, so a difference between
	// two runs cannot be a difference in routing policy.
	builder.WriteString(`","rules":[{"domain_suffix":[".memory-study.invalid"],"outbound":"`)
	builder.WriteString(fixtureOutboundTag)
	builder.WriteString(`"}]}}`)

	fixture.Document = []byte(builder.String())
	return fixture
}

// generateProviderDocument renders the node document a local provider reads.
//
// portOffset shifts every node's port; a refresh that re-reads the same bytes is the "unchanged"
// case and a refresh with a different offset rebuilds every node, which is what makes the two
// readings a controlled A/B rather than two names for the same work.
func generateProviderDocument(spec runSpec, portOffset int) []byte {
	builder := &strings.Builder{}
	builder.Grow(32 + spec.Nodes*180)
	builder.WriteString(`{"outbounds":[`)
	for index := 0; index < spec.Nodes; index++ {
		if index > 0 {
			builder.WriteString(`,`)
		}
		writeNode(builder, spec.NodeProto, fmt.Sprintf("node-%05d", index), index, portOffset)
	}
	builder.WriteString(`]}`)
	return []byte(builder.String())
}

func writeNode(builder *strings.Builder, proto string, tag string, index int, portOffset int) {
	switch proto {
	case "direct":
		// A direct node has no dialer and no TLS: it exists so that grouping and URL-test cost can be
		// measured without a peer, not because a subscription contains direct nodes.
		builder.WriteString(`{"type":"direct","tag":"`)
		builder.WriteString(tag)
		builder.WriteString(`"}`)
	case "heavy":
		// The conditional case: the same light protocol with the two options that make a node hold
		// more than its configuration - multiplex, and a non-insecure TLS block.
		builder.WriteString(`{"type":"trojan","tag":"`)
		builder.WriteString(tag)
		builder.WriteString(`","server":"`)
		builder.WriteString(nodeServerPrefix)
		builder.WriteString(strconv.Itoa(7 + index%200))
		builder.WriteString(`","server_port":`)
		builder.WriteString(strconv.Itoa(20000 + index%1000 + portOffset))
		builder.WriteString(`,"password":"memory-study","multiplex":{"enabled":true,`)
		builder.WriteString(`"protocol":"smux","max_streams":4},"tls":{"enabled":true,"server_name":"node.example"}}`)
	case "mixed":
		writeMixedNode(builder, tag, index, portOffset)
	default:
		// Trojan is the representative light kind: it has the full shape of a node - a dialer, a TLS
		// configuration and a transport - without any of them being eager. This is the same choice
		// the existing cold-cost test makes, for the same reason.
		builder.WriteString(`{"type":"trojan","tag":"`)
		builder.WriteString(tag)
		builder.WriteString(`","server":"`)
		builder.WriteString(nodeServerPrefix)
		builder.WriteString(strconv.Itoa(7 + index%200))
		builder.WriteString(`","server_port":`)
		builder.WriteString(strconv.Itoa(20000 + index%1000 + portOffset))
		builder.WriteString(`,"password":"memory-study","tls":{"enabled":true,"insecure":true,`)
		builder.WriteString(`"server_name":"cold.example"}}`)
	}
}

// mixedNodeKinds are the five protocols this build really compiles in, in the round-robin order the
// mixed fixture uses. Each is registered unconditionally by include.OutboundRegistry except the two
// QUIC protocols, which need the with_quic build tag; the run that produced the ledger was built
// with release/DEFAULT_BUILD_TAGS_OTHERS (which contains with_quic) plus with_purego.
var mixedNodeKinds = []string{"trojan", "vless", "anytls", "hysteria2", "tuic"}

// mixedNodeKind reports the protocol of the index-th mixed node and whether it carries the V2Ray
// WebSocket transport. The transport is the one variant that makes a "plain" protocol carry an
// extra nested option object, so it is present in a fixed one-in-fifteen share rather than spread
// over every node.
func mixedNodeKind(index int) (string, bool) {
	kind := mixedNodeKinds[index%len(mixedNodeKinds)]
	websocket := kind == "vless" && (index/len(mixedNodeKinds))%3 == 2
	return kind, websocket
}

// writeMixedNode writes one node of the mixed subscription: Trojan, VLESS, AnyTLS, Hysteria2 and
// TUIC in equal shares, with a TLS block on every node and a WebSocket transport on one VLESS in
// three.
//
// # Why the TLS blocks are "insecure"
//
// The address is TEST-NET-2, so no node is ever dialled in the cases this fixture is for. `insecure`
// keeps a node that IS accidentally dialled from reaching a real peer through a certificate
// failure path: the connection attempt must fail on the address, not on trust.
func writeMixedNode(builder *strings.Builder, tag string, index int, portOffset int) {
	kind, websocket := mixedNodeKind(index)
	server := nodeServerPrefix + strconv.Itoa(7+index%200)
	port := strconv.Itoa(20000 + index%1000 + portOffset)

	builder.WriteString(`{"type":"`)
	builder.WriteString(kind)
	builder.WriteString(`","tag":"`)
	builder.WriteString(tag)
	builder.WriteString(`","server":"`)
	builder.WriteString(server)
	builder.WriteString(`","server_port":`)
	builder.WriteString(port)
	switch kind {
	case "trojan":
		builder.WriteString(`,"password":"memory-study"`)
	case "vless":
		builder.WriteString(`,"uuid":"b831381d-6324-4d53-ad4f-8cda48b30811"`)
		if websocket {
			builder.WriteString(`,"transport":{"type":"ws","path":"/memory-study"}`)
		}
	case "anytls":
		builder.WriteString(`,"password":"memory-study"`)
	case "hysteria2":
		builder.WriteString(`,"password":"memory-study","up_mbps":10,"down_mbps":50`)
	case "tuic":
		builder.WriteString(`,"uuid":"b831381d-6324-4d53-ad4f-8cda48b30811",` +
			`"password":"memory-study","congestion_control":"bbr"`)
	}
	builder.WriteString(`,"tls":{"enabled":true,"insecure":true,"server_name":"cold.example"}}`)
}

func writeProviderGroup(builder *strings.Builder, groupType string, tag string, probeURL string) {
	writeGroupWithProviders(builder, groupType, tag, nil, []string{fixtureProviderTag}, probeURL)
}

func writeGroup(builder *strings.Builder, groupType string, tag string, members []string, probeURL string) {
	writeGroupWithProviders(builder, groupType, tag, members, nil, probeURL)
}

// writeGroupWithProviders writes a group whose members are static tags, provider tags, or both.
// The `providers` array is omitted when empty so a static document keeps exactly the shape the
// existing tests pin.
func writeGroupWithProviders(builder *strings.Builder, groupType string, tag string, members []string, providers []string, probeURL string) {
	builder.WriteString(`{"type":"`)
	builder.WriteString(groupType)
	builder.WriteString(`","tag":"`)
	builder.WriteString(tag)
	builder.WriteString(`","outbounds":[`)
	for index, member := range members {
		if index > 0 {
			builder.WriteString(`,`)
		}
		builder.WriteString(`"`)
		builder.WriteString(member)
		builder.WriteString(`"`)
	}
	builder.WriteString(`]`)
	if len(providers) > 0 {
		builder.WriteString(`,"providers":[`)
		for index, provider := range providers {
			if index > 0 {
				builder.WriteString(`,`)
			}
			builder.WriteString(`"`)
			builder.WriteString(provider)
			builder.WriteString(`"`)
		}
		builder.WriteString(`]`)
	}
	writeGroupTail(builder, groupType, probeURL)
}

func writeGroupTail(builder *strings.Builder, groupType string, probeURL string) {
	if groupType == "urltest" {
		builder.WriteString(`,"url":"`)
		builder.WriteString(probeURL)
		// The background loop must stay out of the ledger: the probe stage drives a round
		// explicitly, which is the only way the measurement has a synchronous boundary. The
		// validation requires interval <= idle_timeout, so both are set well beyond any run.
		builder.WriteString(`","interval":"30m","idle_timeout":"1h","tolerance":50`)
	}
	builder.WriteString(`}`)
}

// normalizeSpec fills in the defaults a case implies and rejects combinations that would make the
// ledger say something meaningless.
//
// The rejections are part of the contract: an "empty" case with nodes is not an empty case, and a
// probe against nodes whose server is TEST-NET-2 measures a connect timeout, not a URL test.
func normalizeSpec(spec runSpec) (runSpec, error) {
	switch spec.Case {
	case "empty":
		if spec.Nodes != 0 {
			return spec, fmt.Errorf("case empty takes no nodes, got -nodes %d", spec.Nodes)
		}
		if spec.Groups != 0 {
			return spec, fmt.Errorf("case empty takes no groups, got -groups %d", spec.Groups)
		}
		spec.NodeProto = "direct"
	case "ordinary":
		if spec.NodeProto == "" || spec.NodeProto == "heavy" {
			spec.NodeProto = "trojan"
		}
		if spec.Nodes < 0 {
			return spec, fmt.Errorf("case ordinary takes a non-negative -nodes, got %d", spec.Nodes)
		}
	case "heavy":
		spec.NodeProto = "heavy"
		if spec.Nodes < 1 {
			return spec, fmt.Errorf("case heavy needs at least one node, got -nodes %d", spec.Nodes)
		}
	default:
		return spec, fmt.Errorf("unknown -case %q (want empty, ordinary or heavy)", spec.Case)
	}
	if spec.Groups < 0 {
		return spec, fmt.Errorf("case %s takes a non-negative -groups, got %d", spec.Case, spec.Groups)
	}
	switch spec.ConfigSource {
	case "":
		spec.ConfigSource = "static"
	case "static":
	case "provider":
		if spec.Nodes == 0 {
			// A provider with no nodes cannot be published: the product refuses an empty node set
			// precisely so a garbage document cannot wipe the last known good one. A zero-node
			// origin is a static configuration.
			return spec, fmt.Errorf("-config-source provider needs at least one node, got %d", spec.Nodes)
		}
	default:
		return spec, fmt.Errorf("unknown -config-source %q (want static or provider)", spec.ConfigSource)
	}
	switch spec.NodeProto {
	case "direct", "trojan", "heavy", "mixed":
	default:
		return spec, fmt.Errorf("unknown -node-proto %q (want direct, trojan, heavy or mixed)", spec.NodeProto)
	}
	if spec.Nodes == 0 && spec.Groups > 0 {
		// A selection group over zero members is not a legal configuration: box.New rejects it with
		// "missing outbound and provider tags". The zero-node case exists to put the origin on the
		// per-node slope, and a group cannot be part of that origin.
		spec.Groups = 0
	}
	if spec.GCMode != "natural" && spec.GCMode != "forced" {
		return spec, fmt.Errorf("unknown -gc %q (want natural or forced)", spec.GCMode)
	}
	for _, activity := range spec.Activity {
		switch activity {
		case "none", "probe", "traffic":
		default:
			return spec, fmt.Errorf("unknown -activity %q (want none, probe, traffic)", activity)
		}
	}
	if hasActivity(spec, "probe") && spec.Groups < 2 {
		// The probe stage needs a URL-test group to drive; the fixture alternates selector and URL
		// test, so two groups is the minimum that contains one.
		spec.Groups = 2
	}
	if hasActivity(spec, "traffic") && spec.HeldFlows < 1 {
		return spec, fmt.Errorf("-activity traffic needs at least one held flow, got %d", spec.HeldFlows)
	}
	if spec.TrafficBytes < 1 {
		return spec, fmt.Errorf("-traffic-bytes must be positive, got %d", spec.TrafficBytes)
	}
	if spec.PostIdle == "" {
		spec.PostIdle = "0s"
	}
	if len(spec.Idle) == 0 {
		spec.Idle = []string{"0s"}
	}
	return spec, nil
}

func hasActivity(spec runSpec, wanted string) bool {
	for _, activity := range spec.Activity {
		if activity == wanted {
			return true
		}
	}
	return false
}
