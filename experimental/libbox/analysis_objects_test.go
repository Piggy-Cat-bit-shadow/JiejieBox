package libbox

// Q21 — the object/reference projection and the capability list, pinned as tests.
//
// The reviewed gap was not "the report needs more fields". It was that the report described how
// LARGE a document is (counts) and a few hand-checked references, while a client that wants to
// explain a profile needs the graph: which objects exist, of what type, and what each one points
// at. These tests hold the three properties that make the projection trustworthy as the schema
// grows:
//
//   - the objects are the document's own objects, with their declared type and tag;
//   - the references are the ones the OPTION SCHEMA declares (its `reference` struct tags), not a
//     list maintained in the analysis - so a reference the old account never knew about (a
//     detour, a resolver server, a rule action's server) appears without this file being edited;
//   - a reference's state and the report's findings cannot disagree, and the two kinds of
//     "cannot be satisfied" (the core fails / a condition simply never matches) stay distinct.

import (
	"strings"
	"testing"

	"github.com/sagernet/sing-box/include"
	"github.com/stretchr/testify/require"
)

// projectedObjects reduces the object list to "kind/tag/type" strings for comparison.
func projectedObjects(analysis ConfigAnalysis) []string {
	objects := make([]string, 0, len(analysis.Objects))
	for _, object := range analysis.Objects {
		objects = append(objects, object.Kind+"/"+object.Tag+"/"+object.Type)
	}
	return objects
}

// referenceAt returns the projection of one document path.
func referenceAt(t *testing.T, analysis ConfigAnalysis, from string) ConfigReference {
	t.Helper()
	for _, reference := range analysis.References {
		if reference.From == from {
			return reference
		}
	}
	t.Fatalf("no reference projected at %q; projected: %v", from, projectedReferences(analysis))
	return ConfigReference{}
}

// projectedReferences lists the paths the report projected, for a failure message.
func projectedReferences(analysis ConfigAnalysis) []string {
	paths := make([]string, 0, len(analysis.References))
	for _, reference := range analysis.References {
		paths = append(paths, reference.From+"->"+reference.Kind+":"+reference.Tag+"("+reference.State+")")
	}
	return paths
}

// objectGraphConfig declares one object of every kind a reference can point at.
const objectGraphConfig = `{
  "inbounds": [{"type": "socks", "tag": "in", "listen": "127.0.0.1", "listen_port": 1080}],
  "outbounds": [
    {"type": "direct", "tag": "a"},
    {"type": "direct", "tag": "with-refs", "detour": "a", "domain_resolver": {"server": "dns-local"}},
    {"type": "selector", "tag": "group", "outbounds": ["a", "with-refs"], "default": "a"}
  ],
  "providers": [{"type": "remote", "tag": "airport", "url": "https://example.org/sub"}],
  "dns": {"servers": [{"type": "local", "tag": "dns-local"}]},
  "route": {
    "final": "group",
    "rule_set": [{"type": "remote", "tag": "rs", "url": "https://example.org/rs.json"}],
    "rules": [
      {"domain": ["example.com"], "rule_set": ["rs"], "action": "resolve", "server": "dns-local"},
      {"inbound": ["in"], "rule_set": ["rs"], "action": "route", "outbound": "a"}
    ]
  }
}`

// TestTheReportNamesTheObjectsTheDocumentDeclares is the object half of the projection.
func TestTheReportNamesTheObjectsTheDocumentDeclares(t *testing.T) {
	analysis := analyse(t, objectGraphConfig)
	require.Equal(t, []string{
		"inbound/in/socks",
		"outbound/a/direct",
		"outbound/with-refs/direct",
		"outbound/group/selector",
		"provider/airport/remote",
		"dns_server/dns-local/local",
		"rule_set/rs/remote",
	}, projectedObjects(analysis))
}

// TestTheReportProjectsEveryReferenceTheSchemaDeclares is the reference half.
//
// The four references below are the ones the previous account could not describe: a dialer's
// detour, a dialer's resolver server, a rule action's server, and a rule's inbound condition. They
// are asserted through the document path, so a client can point at the field in the document it
// was given.
func TestTheReportProjectsEveryReferenceTheSchemaDeclares(t *testing.T) {
	analysis := analyse(t, objectGraphConfig)

	detour := referenceAt(t, analysis, "outbounds[1].detour")
	require.Equal(t, objectKindOutbound, detour.Kind)
	require.Equal(t, "a", detour.Tag)
	require.Equal(t, referenceStateResolved, detour.State)

	resolver := referenceAt(t, analysis, "outbounds[1].domain_resolver.server")
	require.Equal(t, objectKindDNSServer, resolver.Kind)
	require.Equal(t, "dns-local", resolver.Tag)
	require.Equal(t, referenceStateResolved, resolver.State)

	actionServer := referenceAt(t, analysis, "route.rules[0].server")
	require.Equal(t, objectKindDNSServer, actionServer.Kind)
	require.Equal(t, "dns-local", actionServer.Tag)

	ruleSet := referenceAt(t, analysis, "route.rules[0].rule_set[0]")
	require.Equal(t, objectKindRuleSet, ruleSet.Kind)
	require.Equal(t, "rs", ruleSet.Tag)
	require.Equal(t, referenceStateResolved, ruleSet.State)

	member := referenceAt(t, analysis, "outbounds[2].outbounds[1]")
	require.Equal(t, objectKindOutbound, member.Kind)
	require.Equal(t, "with-refs", member.Tag)
	require.Equal(t, referenceStateResolved, member.State)

	final := referenceAt(t, analysis, "route.final")
	require.Equal(t, "group", final.Tag)
	require.Equal(t, referenceStateResolved, final.State)

	inboundCondition := referenceAt(t, analysis, "route.rules[1].inbound[0]")
	require.Equal(t, objectKindInbound, inboundCondition.Kind)
	require.Equal(t, "in", inboundCondition.Tag)
	require.Equal(t, referenceStateResolved, inboundCondition.State)
}

// TestAReferenceTheDocumentCannotSatisfyBecomesAFinding: the projection is not a display list. A
// reference nothing can satisfy is reported as the declaration problem it is, and the finding names
// the document field so a client can show it.
func TestAReferenceTheDocumentCannotSatisfyBecomesAFinding(t *testing.T) {
	analysis := analyse(t, `{
	  "outbounds": [{"type": "direct", "tag": "a", "detour": "ghost"}],
	  "dns": {"servers": [{"type": "local", "tag": "dns-local"}], "final": "ghost-dns"},
	  "route": {"rules": [{"domain": ["example.com"], "action": "resolve", "server": "ghost-dns"}]}
	}`)

	dangling := map[string]string{}
	for _, finding := range analysis.Findings {
		dangling[finding.Detail] = finding.Code
	}
	require.Equal(t, findingDanglingReference, dangling["outbounds[0].detour outbound ghost"],
		"a detour that names no declared outbound cannot be resolved, and this was never checked before")
	require.Equal(t, findingDanglingReference, dangling["dns.final ghost-dns"])
	require.Equal(t, findingDanglingReference, dangling["route.rules[0].server dns_server ghost-dns"])

	require.Equal(t, referenceStateDangling, referenceAt(t, analysis, "outbounds[0].detour").State)
	require.Equal(t, referenceStateDangling, referenceAt(t, analysis, "dns.final").State)
	require.Equal(t, referenceStateDangling, referenceAt(t, analysis, "route.rules[0].server").State)
}

// TestAProviderRenderedMemberIsPendingNotDangling keeps the rule that a fetch will provide the
// object: it is neither resolved (the document cannot name it) nor missing.
func TestAProviderRenderedMemberIsPendingNotDangling(t *testing.T) {
	analysis := analyse(t, `{
	  "providers": [{"type": "remote", "tag": "airport", "url": "https://example.org/sub"}],
	  "outbounds": [{"type": "selector", "tag": "group", "outbounds": ["airport/node-a"], "providers": ["airport"]}]
	}`)

	member := referenceAt(t, analysis, "outbounds[0].outbounds[0]")
	require.Equal(t, referenceStatePending, member.State,
		"a provider renders this member at start, so the document cannot declare it")
	require.NotEmpty(t, member.Reason)

	providerReference := referenceAt(t, analysis, "outbounds[0].providers[0]")
	require.Equal(t, objectKindProvider, providerReference.Kind)
	require.Equal(t, referenceStateResolved, providerReference.State)

	require.Empty(t, analysis.Findings, "a pending reference is not a finding")
}

// TestAConditionWithoutItsObjectIsUnresolvedNotAFinding is the line the previous account did not
// draw: a rule's `inbound` condition is a MATCHER. Naming an undeclared inbound does not stop the
// box from starting, so reporting it as dangling would be a false failure report - but silently
// dropping it would hide a rule that can never match.
func TestAConditionWithoutItsObjectIsUnresolvedNotAFinding(t *testing.T) {
	analysis := analyse(t, `{"route": {"rules": [{"inbound": ["ghost"], "action": "reject"}]}}`)

	condition := referenceAt(t, analysis, "route.rules[0].inbound[0]")
	require.Equal(t, referenceStateUnresolved, condition.State)
	require.Contains(t, condition.Reason, "never applies")
	require.Empty(t, analysis.Findings,
		"the core starts with this document: a matcher that names nothing matches nothing")
}

// TestANamespaceThePlatformMayOwnIsUnresolvedNotAFinding: `netns` names something the operating
// system may provide without the document declaring it, so the analysis says it cannot tell.
func TestANamespaceThePlatformMayOwnIsUnresolvedNotAFinding(t *testing.T) {
	analysis := analyse(t, `{"outbounds": [{"type": "direct", "tag": "a", "netns": "ns-elsewhere"}]}`)

	namespace := referenceAt(t, analysis, "outbounds[0].netns")
	require.Equal(t, objectKindNetworkNamespace, namespace.Kind)
	require.Equal(t, referenceStateUnresolved, namespace.State)
	require.Empty(t, analysis.Findings)
}

// TestEveryDanglingReferenceHasExactlyOneFinding is the invariant that keeps the two views from
// drifting: the reference list is what the findings are built from, so a dangling entry without a
// finding (or a finding without an entry) is a defect in this file, not a client's problem.
func TestEveryDanglingReferenceHasExactlyOneFinding(t *testing.T) {
	analysis := analyse(t, `{
	  "outbounds": [
	    {"type": "direct", "tag": "a"},
	    {"type": "selector", "tag": "group", "outbounds": ["a", "ghost-member"], "default": "ghost-default"},
	    {"type": "direct", "tag": "detoured", "detour": "ghost-detour"}
	  ],
	  "route": {"final": "ghost-final", "rules": [{"rule_set": ["ghost-set"], "action": "route", "outbound": "ghost-out"}]}
	}`)

	var danglingPaths []string
	for _, reference := range analysis.References {
		if reference.State == referenceStateDangling {
			danglingPaths = append(danglingPaths, reference.From)
		}
	}
	require.Len(t, danglingPaths, 6, "five dangling references plus the group default: %v", danglingPaths)
	require.Len(t, analysis.Findings, len(danglingPaths))
	for _, finding := range analysis.Findings {
		require.Equal(t, findingDanglingReference, finding.Code)
	}
}

// TestTheCapabilityListIsTheRegistrysOwnAnswer: the list is read from the same registries the
// decoder used, so it cannot describe a build that is not this one.
func TestTheCapabilityListIsTheRegistrysOwnAnswer(t *testing.T) {
	analysis := analyse(t, `{"outbounds": [{"type": "direct", "tag": "a"}]}`)
	require.NotEmpty(t, analysis.Capabilities)

	capabilities := map[string][]string{}
	for _, capability := range analysis.Capabilities {
		capabilities[capability.Kind] = capability.Types
	}
	require.Equal(t, include.OutboundRegistry().OptionTypes(), capabilities[objectKindOutbound])
	require.Equal(t, include.InboundRegistry().OptionTypes(), capabilities[objectKindInbound])
	require.Equal(t, include.DNSTransportRegistry().OptionTypes(), capabilities[objectKindDNSServer])
	require.Contains(t, capabilities[objectKindOutbound], "selector",
		"the outbound list is the one the decoder resolves types against")
}

// TestTooManyReferencesAreTruncatedAndSaid: the list is bounded and the bound is STATED. A silently
// truncated graph would read as a document with no other references.
func TestTooManyReferencesAreTruncatedAndSaid(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`{"outbounds": [{"type": "direct", "tag": "a"}], "route": {"rules": [`)
	const ruleCount = maxAnalysisReferences + 100
	for index := 0; index < ruleCount; index++ {
		if index > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(`{"domain": ["example.com"], "action": "route", "outbound": "a"}`)
	}
	builder.WriteString(`]}}`)

	analysis := analyse(t, builder.String())
	require.True(t, analysis.ReferencesTruncated, "the document holds more references than one report carries")
	require.Len(t, analysis.References, maxAnalysisReferences)
	require.Empty(t, analysis.Findings, "every projected reference resolves; truncation is not a finding")
}
