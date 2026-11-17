package libbox

// P0 - the import preview, as the platform binding sees it.
//
// # What it answers
//
// "The user pasted something. Is it a subscription, which shape is it, and what exactly will be
// imported?" Answering it BEFORE anything is applied is the whole point: the alternative is to
// write the document into a provider and start a box, which the user cannot review first.
//
// # Where the parsing lives
//
// In provider/parser, next to the readers themselves (ParseSubscriptionPreview). This file only
// shapes the result for the binding: it collects the full node documents, which the client needs
// in order to write them into its own profile, and it never re-implements a reader.
//
// # Why the result is a *StringBox
//
// Same reason as AnalyzeConfig and FormatConfig: a bare `string` result puts a pointer into
// cmd/cgo's packed result frame, which is the alignment shape gomobile_surface_test.go exists to
// refuse (see that file's header). Clients read `parsePreview(...)!.value`.
//
// # Why a structural failure is a RESULT and not an error
//
// "This text is not a subscription" is a normal outcome of an import form, not a fault: the user
// may have pasted a complete configuration instead. Returning a Go error for it forces every
// caller to match on error text to tell "nothing recognised" from "the bridge broke". The report
// therefore always carries `ok`, `count` and a stable `error.code`; the Go error return stays for
// a genuine failure of this function (a node that cannot be serialised), which a caller can treat
// as a bug rather than a user mistake.

import (
	stdjson "encoding/json"

	"github.com/sagernet/sing-box/provider/parser"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
)

// ParsePreviewErrorCode is the stable code a client switches on. It is deliberately a small,
// closed set: a client can localise three strings, it cannot localise arbitrary reader text.
const (
	// ParsePreviewCodeEmpty means the argument was blank.
	ParsePreviewCodeEmpty = "empty_input"
	// ParsePreviewCodeNotRecognized means no reader recognised the document. The per-reader
	// reasons are in `diagnostics` and must not be shown as the primary message.
	ParsePreviewCodeNotRecognized = "not_recognized"
)

// parsePreviewItem is one node, as the client will write it.
type parsePreviewItem struct {
	// Index is the position in the document, so a client can keep the author's order.
	Index int    `json:"index"`
	Tag   string `json:"tag"`
	Type  string `json:"type"`
	// Server and ServerPort are lifted out of the node document for list rendering. They are
	// empty for an endpoint, whose address sits inside its peers.
	Server     string `json:"server,omitempty"`
	ServerPort uint16 `json:"serverPort,omitempty"`
	// Endpoint marks a node that must be written into `endpoints`, not `outbounds`. A provider
	// carries outbounds only, so a client that shows both must say which list it lands in.
	Endpoint bool `json:"endpoint,omitempty"`
	// Content is the complete node document, in the core's own encoding. This is what the
	// client stores; it holds credentials, so it must never be logged or shown in diagnostics.
	Content string `json:"content"`
}

// parsePreviewSkip is one entry the winning reader refused.
type parsePreviewSkip struct {
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason"`
}

// parsePreviewError is the stable failure shape.
type parsePreviewError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// parsePreviewReport is the JSON document ParsePreview returns.
type parsePreviewReport struct {
	OK            bool               `json:"ok"`
	Format        string             `json:"format,omitempty"`
	Base64        bool               `json:"base64,omitempty"`
	Count         int                `json:"count"`
	EndpointCount int                `json:"endpointCount,omitempty"`
	Declared      int                `json:"declared,omitempty"`
	Items         []parsePreviewItem `json:"items"`
	Skipped       []parsePreviewSkip `json:"skipped"`
	Error         *parsePreviewError `json:"error,omitempty"`
	Diagnostics   map[string]string  `json:"diagnostics,omitempty"`
}

// ParsePreview parses a share link list, a subscription document or a complete configuration's
// outbound section, and returns the preview as JSON.
//
// It constructs no Box, downloads nothing, writes nothing and registers no provider.
//
// The returned JSON always parses; `ok` says whether anything was recognised. Skipped entries are
// reported per line for a link list and are absent for the document readers, which refuse a
// document as a whole rather than an entry (see provider/parser/preview.go).
func ParsePreview(content string) (*StringBox, error) {
	ctx := baseContext(nil)
	preview := parser.ParseSubscriptionPreview(ctx, content)
	report := parsePreviewReport{
		Format:        preview.Format,
		Base64:        preview.Base64,
		Declared:      preview.Declared,
		Count:         len(preview.Outbounds),
		EndpointCount: len(preview.Endpoints),
		Items:         make([]parsePreviewItem, 0, len(preview.Outbounds)+len(preview.Endpoints)),
		Skipped:       make([]parsePreviewSkip, 0, len(preview.Skipped)),
		Diagnostics:   preview.Diagnostics,
	}
	for index, outbound := range preview.Outbounds {
		item, err := newParsePreviewItem(index, outbound.Tag, outbound.Type, &preview.Outbounds[index])
		if err != nil {
			return nil, err
		}
		report.Items = append(report.Items, item)
	}
	for index, endpoint := range preview.Endpoints {
		item, err := newParsePreviewItem(len(preview.Outbounds)+index, endpoint.Tag, endpoint.Type, &preview.Endpoints[index])
		if err != nil {
			return nil, err
		}
		item.Endpoint = true
		report.Items = append(report.Items, item)
	}
	for _, skip := range preview.Skipped {
		report.Skipped = append(report.Skipped, parsePreviewSkip{Line: skip.Line, Reason: skip.Reason})
	}
	report.OK = preview.Recognised()
	if !report.OK {
		code := ParsePreviewCodeNotRecognized
		if len(preview.Diagnostics) == 0 {
			code = ParsePreviewCodeEmpty
		}
		report.Error = &parsePreviewError{Code: code, Message: previewDiagnosticSummary(preview)}
	}
	encoded, err := stdjson.Marshal(report)
	if err != nil {
		return nil, E.Cause(err, "encode parse preview")
	}
	return wrapString(string(encoded)), nil
}

// newParsePreviewItem serialises one node with the core's own encoder and lifts the address out
// of the result.
func newParsePreviewItem(index int, tag string, nodeType string, value any) (parsePreviewItem, error) {
	encoded, err := json.MarshalContext(baseContext(nil), value)
	if err != nil {
		return parsePreviewItem{}, E.Cause(err, "encode node ", tag)
	}
	item := parsePreviewItem{Index: index, Tag: tag, Type: nodeType, Content: string(encoded)}
	// The address is read back from the encoded document rather than from a type assertion:
	// every outbound protocol declares ServerOptions, an endpoint does not, and a per-type
	// switch here would need a second list that could drift from the registries.
	var address struct {
		Server     string `json:"server"`
		ServerPort uint16 `json:"server_port"`
	}
	if err = stdjson.Unmarshal(encoded, &address); err == nil {
		item.Server = address.Server
		item.ServerPort = address.ServerPort
	}
	return item, nil
}

// previewDiagnosticSummary renders the readers' refusals into one line for the report message.
// The per-reader detail stays in `diagnostics`, where it belongs.
func previewDiagnosticSummary(preview *parser.SubscriptionPreview) string {
	if len(preview.Diagnostics) == 0 {
		return "the text is empty"
	}
	message := "the text is not a recognised subscription format"
	for _, format := range []string{parser.PreviewFormatSingBox, parser.PreviewFormatClash, parser.PreviewFormatSIP008, parser.PreviewFormatLinks} {
		if reason, loaded := preview.Diagnostics[format]; loaded && reason != "" {
			return message + ": " + format + " reader: " + reason
		}
	}
	return message
}
