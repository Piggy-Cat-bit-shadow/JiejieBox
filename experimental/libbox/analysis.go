package libbox

import (
	stdjson "encoding/json"
	"runtime"

	C "github.com/sagernet/sing-box/constant"
)

// ConfigAnalysis is the lightweight projection of a configuration document.
//
// # What it is
//
// Parse-only: it reuses the SAME extended-JSON parser and registries as CheckConfig, then reports
// the declared shape (counts, platform, build capabilities). It does NOT construct a Box, start any
// component, download a rule set, resolve DNS, dial, or touch the running selection.
//
// It is deliberately a separate, additive entry point: CheckConfig keeps its full "construct a
// throwaway Box" semantics, and a client that only needs to classify/display a profile can call this
// without paying for the runtime path.
//
// The values are DECLARED facts plus the two things that explain them without a Box: the defaults
// the core applies (ConfigValue, source "default") and the platform facts a condition needs before
// it can match (ConfigRequirement). What is NEVER claimed here is the runtime side: no component is
// constructed, no DNS client is built, no provider is fetched, and Checks.RuntimeChecked stays
// false.
//
// The document's own graph is projected too: Objects names every declared object (kind, tag, type),
// References lists every reference the option schema declares between them with the document path it
// sits at, and Capabilities lists the types this build's registries can construct. Those three
// answer "what does this profile consist of and point at" without constructing any of it; see
// analysis_objects.go for the states a reference can have and for why capabilities come from the
// registry rather than from a table here.
type ConfigAnalysis struct {
	Parsed    bool   `json:"parsed"`
	Platform  string `json:"platform"`
	BuildQUIC bool   `json:"buildQuic"`
	// BuildID identifies the capability set this analysis was produced by, so a report can be
	// tied to the exact binary instead of to a repackaged version string.
	BuildID string `json:"buildId"`
	// ConfigRevision identifies the DOCUMENT, not the parse: the same bytes analysed twice
	// produce the same revision, and any edit produces another.
	ConfigRevision string `json:"configRevision"`
	Schema         string `json:"schema,omitempty"`
	// Checks says how far the analysis actually got. AnalyzeConfig never sets RuntimeChecked:
	// it does not build a box, construct a DNS client, fetch a provider or probe anything.
	Checks ConfigChecks `json:"checks"`
	// Findings are declaration problems (duplicate tags, dangling references).
	Findings []ConfigFinding `json:"findings,omitempty"`
	// Defaults are the values the core will use for fields the document left unset, with the
	// declaration each one came from.
	Defaults []ConfigValue `json:"defaults,omitempty"`
	// Requirements are the platform facts the document's conditions need before they can match.
	// They are pending on the PLATFORM side: this analysis cannot observe whether it supplies them.
	Requirements []ConfigRequirement `json:"requirements,omitempty"`
	// PendingProviders are remote providers whose nodes do not exist until they are fetched.
	PendingProviders []ConfigPendingProvider `json:"pendingProviders,omitempty"`
	// Objects are the objects the document DECLARES: kind, tag and type. They are the names a
	// reference can use, and the identity a client needs to render the graph.
	Objects []ConfigObject `json:"objects,omitempty"`
	// References are the references the option schema declares between those objects, with the
	// document path each one sits at and whether the document resolves it. See
	// analysis_objects.go for what the four states mean.
	References []ConfigReference `json:"references,omitempty"`
	// ReferencesTruncated reports that the document holds more references than one analysis
	// carries. It is stated rather than silent: a partial list read as complete would describe a
	// broken graph.
	ReferencesTruncated bool `json:"referencesTruncated,omitempty"`
	// Capabilities are the types THIS BUILD's registries can construct, per namespace. They are
	// build facts, not document facts, and they are what makes "this profile needs a type this
	// binary does not have" answerable without starting a Box.
	Capabilities []ConfigCapability `json:"capabilities,omitempty"`
	Inbounds     int                `json:"inbounds"`
	Outbounds    int                `json:"outbounds"`
	Endpoints    int                `json:"endpoints"`
	Services     int                `json:"services"`
	Providers    int                `json:"providers"`
	RouteRules   int                `json:"routeRules"`
	RuleSets     int                `json:"ruleSets"`
	DNSRules     int                `json:"dnsRules"`
	DNSServers   int                `json:"dnsServers"`
	// FindProcess and AutoDetectInterface are configuration intents, not a claim that the platform
	// granted the capability.
	FindProcess         bool `json:"findProcess"`
	AutoDetectInterface bool `json:"autoDetectInterface"`
}

// AnalyzeConfig parses configContent and returns the ConfigAnalysis as JSON.
//
// It never builds a Box. A malformed document returns the parser's error, exactly as CheckConfig
// would report it.
//
// # Why the result is a *StringBox
//
// AnalyzeConfig is a package-level function bound by gomobile, so its result crosses cmd/cgo's
// packed result frame. A bare string puts the string's pointer in that frame, which is the
// `bulkBarrierPreWrite: unaligned arguments` shape gomobile_surface_test.go exists to catch (the
// pinned toolchain does not align the frame; Go 1.26 does). StringBox keeps the pointer in a heap
// object and leaves only a refnum in the frame, the same workaround GetDNSMode and FormatConfig
// already use. Clients read `analyzeConfig(...)!.value`.
func AnalyzeConfig(configContent string) (*StringBox, error) {
	ctx := baseContext(nil)
	options, err := parseConfig(ctx, configContent)
	if err != nil {
		return nil, err
	}
	analysis := ConfigAnalysis{
		Parsed:         true,
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		BuildQUIC:      C.WithQUIC,
		BuildID:        analysisBuildID(),
		ConfigRevision: analysisRevision(configContent),
		Schema:         options.Schema,
		Inbounds:       len(options.Inbounds),
		Outbounds:      len(options.Outbounds),
		Endpoints:      len(options.Endpoints),
		Services:       len(options.Services),
		Providers:      len(options.Providers),
	}
	analysis.Findings, analysis.PendingProviders = analyseDeclarations(&options)
	graph := analyseObjectGraph(ctx, &options)
	analysis.Findings = append(analysis.Findings, graph.Findings...)
	analysis.Objects = graph.Objects
	analysis.References = graph.References
	analysis.ReferencesTruncated = graph.Truncated
	analysis.Capabilities = graph.Capabilities
	analysis.Defaults, analysis.Requirements = analyseEffective(&options)
	analysis.Checks = ConfigChecks{Parsed: true, DeclarationsChecked: true}
	if options.Route != nil {
		analysis.RouteRules = len(options.Route.Rules)
		analysis.RuleSets = len(options.Route.RuleSet)
		analysis.FindProcess = options.Route.FindProcess
		analysis.AutoDetectInterface = options.Route.AutoDetectInterface
	}
	if options.DNS != nil {
		analysis.DNSRules = len(options.DNS.Rules)
		analysis.DNSServers = len(options.DNS.Servers)
	}
	encoded, err := stdjson.Marshal(analysis)
	if err != nil {
		return nil, err
	}
	return wrapString(string(encoded)), nil
}
