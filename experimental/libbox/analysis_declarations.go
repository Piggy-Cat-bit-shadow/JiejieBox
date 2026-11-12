package libbox

// C01C02 - what AnalyzeConfig can declare without building a box.
//
// AnalyzeConfig parses the document and checks DECLARATIONS: tags that must be unique, and the
// references between objects. It deliberately does not build a Box, construct a DNS client, fetch a
// provider or probe anything, so its answers are available before any of that could run - and it
// says so: Checks separates parsed, declarations-checked and runtime-checked, and RuntimeChecked is
// always false here.
//
// # Who checks what
//
// This file answers the question that is not about references: are two objects fighting over one
// tag, and which providers can only exist after a fetch. The reference graph itself lives in
// analysis_objects.go, which walks the option schema's own `reference` tags over the DECODED
// document; that walk is what produces the dangling-reference findings, so the report's reference
// list and its findings are the same projection read twice instead of two lists that can disagree.
//
// Two facts are not errors and must not be reported as such:
//
//   - a remote provider's members do not exist at parse time. A group that names such a
//     provider is resolved at start, so its provider-rendered members ("<provider>/<node>")
//     are reported as PENDING rather than as dangling references;
//   - a value can be syntactically valid and still fail construction (an invalid certificate
//     path, an unstartable service). That is the "parsable but construction fails" category,
//     and AnalyzeConfig's answer for it is Parsed=true with DeclarationsChecked still true -
//     the failure belongs to the build, not to the document's references.

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// ConfigChecks says how far the analysis got. runtimeChecked is never true for AnalyzeConfig.
type ConfigChecks struct {
	Parsed              bool `json:"parsed"`
	DeclarationsChecked bool `json:"declarationsChecked"`
	RuntimeChecked      bool `json:"runtimeChecked"`
}

// ConfigFinding is one declaration problem, identified by a stable code.
type ConfigFinding struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	// Reason is the consequence the core would produce: the failure a client would otherwise only
	// discover by starting a Box.
	Reason string `json:"reason,omitempty"`
}

// ConfigPendingProvider is a provider whose members cannot exist until it is fetched.
type ConfigPendingProvider struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`
}

const (
	findingDuplicateTag      = "duplicate_tag"
	findingDanglingReference = "dangling_reference"
	findingPendingReference  = "pending_reference"
)

// analysisBuildID identifies this binary's capability set, so a report can be tied to the
// exact build that produced it instead of to a version string that may be repackaged.
func analysisBuildID() string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(runtime.GOOS + "/" + runtime.GOARCH))
	_, _ = hasher.Write([]byte("/quic=" + boolText(C.WithQUIC)))
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}

func boolText(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

// analysisRevision identifies the document, not the parse: the same bytes analysed twice
// produce the same revision, and any edit produces another.
func analysisRevision(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}

// analyseDeclarations reports the duplicate tags and the providers that only exist after a fetch.
//
// References are NOT checked here: analysis_objects.go walks the schema's reference tags and
// produces both the reference list and the dangling findings from that one walk.
func analyseDeclarations(options *option.Options) (findings []ConfigFinding, pending []ConfigPendingProvider) {
	inboundTags := make(map[string]bool, len(options.Inbounds))
	for _, inbound := range options.Inbounds {
		if inbound.Tag == "" {
			continue
		}
		if inboundTags[inbound.Tag] {
			findings = append(findings, ConfigFinding{Code: findingDuplicateTag, Detail: "inbound " + inbound.Tag})
		}
		inboundTags[inbound.Tag] = true
	}
	outboundTags := make(map[string]bool, len(options.Outbounds))
	for _, outbound := range options.Outbounds {
		if outbound.Tag == "" {
			continue
		}
		if outboundTags[outbound.Tag] {
			findings = append(findings, ConfigFinding{Code: findingDuplicateTag, Detail: "outbound " + outbound.Tag})
		}
		outboundTags[outbound.Tag] = true
	}
	endpointTags := make(map[string]bool, len(options.Endpoints))
	for _, endpoint := range options.Endpoints {
		if endpoint.Tag == "" {
			continue
		}
		if endpointTags[endpoint.Tag] {
			findings = append(findings, ConfigFinding{Code: findingDuplicateTag, Detail: "endpoint " + endpoint.Tag})
		}
		endpointTags[endpoint.Tag] = true
	}
	providerTags := make(map[string]bool, len(options.Providers))
	for _, provider := range options.Providers {
		if provider.Tag == "" {
			continue
		}
		if providerTags[provider.Tag] {
			findings = append(findings, ConfigFinding{Code: findingDuplicateTag, Detail: "provider " + provider.Tag})
		}
		providerTags[provider.Tag] = true
		if provider.Type == "remote" {
			// A remote provider is the dynamic case: its nodes are rendered at start from a
			// document that does not exist yet.
			pending = append(pending, ConfigPendingProvider{Tag: provider.Tag, Type: provider.Type})
		}
	}
	// A rule that names a rule-set the document never declares is not a reference the Box
	// tolerates: the router refuses to start. That finding now comes from the reference walk
	// (analysis_objects.go), which reports the same sentence for the same rule-set tag.
	return findings, pending
}
