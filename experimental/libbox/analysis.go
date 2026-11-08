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
// The values are DECLARED facts, not effective ones. Turning them into a declared/default/effective
// report with per-field reasons is the next slice (C02); this type must not pretend to have it.
type ConfigAnalysis struct {
	Parsed     bool   `json:"parsed"`
	Platform   string `json:"platform"`
	BuildQUIC  bool   `json:"buildQuic"`
	Inbounds   int    `json:"inbounds"`
	Outbounds  int    `json:"outbounds"`
	Endpoints  int    `json:"endpoints"`
	Services   int    `json:"services"`
	Providers  int    `json:"providers"`
	RouteRules int    `json:"routeRules"`
	RuleSets   int    `json:"ruleSets"`
	DNSRules   int    `json:"dnsRules"`
	DNSServers int    `json:"dnsServers"`
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
		Parsed:    true,
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		BuildQUIC: C.WithQUIC,
		Inbounds:  len(options.Inbounds),
		Outbounds: len(options.Outbounds),
		Endpoints: len(options.Endpoints),
		Services:  len(options.Services),
		Providers: len(options.Providers),
	}
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
