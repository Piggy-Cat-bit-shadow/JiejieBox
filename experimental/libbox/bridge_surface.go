package libbox

// P3 — the two pure functions the import flow needs and the host previously had to reimplement.
//
// # Why these exist at all
//
// The flow a user actually takes is: paste something -> decide what it IS -> either validate it as a
// complete configuration or preview it as nodes -> write it into a profile keyed by a revision.
// `CheckConfig`/`AnalyzeConfig` answer the first branch and `ParsePreview` answers the second, but
// two decisions fell on the host:
//
//   - WHICH branch. A sing-box document and a node list are both "some JSON", and a wrong guess
//     writes a subscription where a profile belongs (or vice versa). The classification is a fact
//     about the core's own decoders, not about the UI.
//   - The revision of a document that does not parse. A profile can be broken and still need an
//     identity: the host must be able to tell "the file changed" from "the file is invalid".
//     AnalyzeConfig returns configRevision only for documents it could parse.
//
// Both functions are pure: no Box, no network, no files, no provider. They are named for what they
// answer rather than for a screen, so they stay target-agnostic.
//
// # Why the results are *StringBox
//
// Same reason as the rest of this package: a bare string result puts a pointer into cmd/cgo's
// packed result frame (see gomobile_surface_test.go).

import (
	stdjson "encoding/json"

	"github.com/sagernet/sing-box/provider/parser"
	E "github.com/sagernet/sing/common/exceptions"
)

// Import kinds. They are the values a client switches on before it offers an action.
const (
	// ImportKindConfig is a document the core decodes as a complete configuration: it is
	// validated with CheckConfig/AnalyzeConfig and applied as a profile.
	ImportKindConfig = "config"
	// ImportKindNodes is a document a subscription reader recognises: it is previewed with
	// ParsePreview and written into a profile as nodes.
	ImportKindNodes = "nodes"
	// ImportKindUnknown is text neither reader accepted.
	ImportKindUnknown = "unknown"
)

// importReport is the JSON DetectImport returns.
type importReport struct {
	// Kind is one of the ImportKind constants.
	Kind string `json:"kind"`
	// Format is the subscription format when Kind is nodes (links/clash/sip008/sing-box), and
	// empty otherwise. A "sing-box" format appears here only when the document is a NODE
	// document the config decoder refused, which is the useful distinction.
	Format string `json:"format,omitempty"`
	// Revision always identifies the CONTENT, even when nothing parsed. It uses the same
	// sha256[:16] the core uses elsewhere (configRevision/analysisRevision), so a host can key a
	// profile by it before the document is valid.
	Revision string `json:"revision"`
	// NodeCount is the number of outbounds the preview found (0 for a config).
	NodeCount int `json:"nodeCount,omitempty"`
	// EndpointCount is separate: a provider carries outbounds only.
	EndpointCount int `json:"endpointCount,omitempty"`
	// ConfigShape reports what a config document declares, so the UI can say "this is a full
	// profile with routing" instead of just "config".
	ConfigShape *importConfigShape `json:"configShape,omitempty"`
	// Skipped and Diagnostics are the preview's, when Kind is nodes.
	Skipped     []parsePreviewSkip `json:"skipped,omitempty"`
	Diagnostics map[string]string  `json:"diagnostics,omitempty"`
	Message     string             `json:"message,omitempty"`
}

// importConfigShape is the coarse shape of a document the config decoder accepted.
type importConfigShape struct {
	Inbounds  int  `json:"inbounds"`
	Outbounds int  `json:"outbounds"`
	Endpoints int  `json:"endpoints"`
	Providers int  `json:"providers"`
	HasRoute  bool `json:"hasRoute"`
	HasDNS    bool `json:"hasDNS"`
	// NodesOnly is true when the document declares outbounds and nothing that makes it a usable
	// profile (no inbounds, no route, no dns, no providers, no services): it is a node list that
	// happens to be valid sing-box JSON, and the UI should offer "import nodes" rather than
	// "replace profile".
	NodesOnly bool `json:"nodesOnly"`
}

// DetectImport classifies pasted text and reports the content revision.
//
// It decodes the document with the core's own decoders and builds no Box, so it is cheap enough to
// call on every keystroke of a paste dialog. The classification precedence is deliberate: a
// document that the CONFIG decoder accepts is a config, because that is the stronger statement, and
// a nodes-only config (outbounds and nothing else) is reported as such rather than as a profile.
func DetectImport(content string) (*StringBox, error) {
	report := importReport{Revision: analysisRevision(content)}

	if options, err := parseConfig(baseContext(nil), content); err == nil {
		shape := &importConfigShape{
			Inbounds:  len(options.Inbounds),
			Outbounds: len(options.Outbounds),
			Endpoints: len(options.Endpoints),
			Providers: len(options.Providers),
			HasRoute:  options.Route != nil,
			HasDNS:    options.DNS != nil,
		}
		shape.NodesOnly = shape.Outbounds > 0 && shape.Inbounds == 0 && shape.Endpoints == 0 &&
			shape.Providers == 0 && !shape.HasRoute && !shape.HasDNS && len(options.Services) == 0
		report.Kind = ImportKindConfig
		report.ConfigShape = shape
		return wrapImportReport(report)
	}

	preview := parser.ParseSubscriptionPreview(baseContext(nil), content)
	if preview.Recognised() {
		report.Kind = ImportKindNodes
		report.Format = preview.Format
		report.NodeCount = len(preview.Outbounds)
		report.EndpointCount = len(preview.Endpoints)
		for _, skip := range preview.Skipped {
			report.Skipped = append(report.Skipped, parsePreviewSkip{Line: skip.Line, Reason: skip.Reason})
		}
		report.Diagnostics = preview.Diagnostics
		return wrapImportReport(report)
	}

	report.Kind = ImportKindUnknown
	report.Message = "the text is neither a configuration the core accepts nor a subscription it recognises"
	report.Diagnostics = preview.Diagnostics
	return wrapImportReport(report)
}

// ConfigRevision returns the content identity the core uses, for any input.
//
// `AnalyzeConfig` reports configRevision only for documents it could parse, but a profile that is
// currently broken still needs an identity: the host must be able to detect that the file changed
// underneath it. The algorithm is the core's own (sha256, first 16 hex characters), so the value is
// directly comparable with `ServiceChangeReport.activeRevision`.
func ConfigRevision(content string) (*StringBox, error) {
	return wrapString(`{"revision":"` + analysisRevision(content) + `"}`), nil
}

// wrapImportReport encodes a report for the binding.
func wrapImportReport(report importReport) (*StringBox, error) {
	encoded, err := stdjson.Marshal(report)
	if err != nil {
		return nil, E.Cause(err, "encode import report")
	}
	return wrapString(string(encoded)), nil
}
