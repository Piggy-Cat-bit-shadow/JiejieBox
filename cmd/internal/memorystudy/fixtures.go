package main

import (
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

// Fixture identities that the measurement stages address by name.
const (
	fixtureOutboundTag    = "out"
	fixtureInboundTag     = "in"
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
}

// nodeServer is TEST-NET-2 (RFC 5737). A never-dialled node must not reach a real peer, and a test
// address makes an accidental dial fail immediately instead of opening a connection to somebody
// else's host.
const nodeServerPrefix = "198.51.100."

// generateFixture renders the configuration document for a spec.
func generateFixture(spec runSpec, inboundPort uint16, probeURL string) fixtureConfig {
	fixture := fixtureConfig{Nodes: spec.Nodes, Groups: spec.Groups, ProbeURL: probeURL}
	builder := &strings.Builder{}
	// A 10 000-node document is roughly 1.6 MB; pre-sizing keeps the builder's own growth out of the
	// config_build measurement.
	builder.Grow(256 + spec.Nodes*180 + spec.Groups*64)

	builder.WriteString(`{"log":{"disabled":true,"level":"error"},"inbounds":[{"type":"socks","tag":"`)
	builder.WriteString(fixtureInboundTag)
	builder.WriteString(`","listen":"127.0.0.1","listen_port":`)
	builder.WriteString(strconv.Itoa(int(inboundPort)))
	builder.WriteString(`}],"outbounds":[{"type":"direct","tag":"`)
	builder.WriteString(fixtureOutboundTag)
	builder.WriteString(`"}`)

	fixture.NodeTags = make([]string, 0, spec.Nodes)
	for index := 0; index < spec.Nodes; index++ {
		tag := fmt.Sprintf("node-%05d", index)
		fixture.NodeTags = append(fixture.NodeTags, tag)
		builder.WriteString(`,`)
		writeNode(builder, spec.NodeProto, tag, index)
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
		writeGroup(builder, groupType, tag, fixture.NodeTags, probeURL)
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

func writeNode(builder *strings.Builder, proto string, tag string, index int) {
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
		builder.WriteString(strconv.Itoa(20000 + index%1000))
		builder.WriteString(`,"password":"memory-study","multiplex":{"enabled":true,`)
		builder.WriteString(`"protocol":"smux","max_streams":4},"tls":{"enabled":true,"server_name":"node.example"}}`)
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
		builder.WriteString(strconv.Itoa(20000 + index%1000))
		builder.WriteString(`,"password":"memory-study","tls":{"enabled":true,"insecure":true,`)
		builder.WriteString(`"server_name":"cold.example"}}`)
	}
}

func writeGroup(builder *strings.Builder, groupType string, tag string, members []string, probeURL string) {
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
