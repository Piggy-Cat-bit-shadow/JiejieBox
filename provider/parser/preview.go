package parser

// P0 — the read-only parse preview.
//
// # Why this exists
//
// The four readers in this package answer "give me the outbounds" so that a provider can apply
// them. A CLIENT that is about to import a subscription needs a different question answered
// BEFORE anything is applied: which document shape is this, which entries became nodes, and which
// entries were refused and why. Today the only way to ask is either to apply the document to a
// provider inside a running box, or to reimplement these readers in the client - and a second
// implementation drifts from this one.
//
// # What it deliberately does NOT do
//
// It does not build a Box, download anything, write a file, register a provider, or change the
// running selection. It also does NOT rewrite `detour`: provider-scoped rewriting belongs to the
// provider, which knows its own tag (see ParseSubscription). A preview shows the document as
// written, so what the user reviews is what the document says.
//
// # Why the format order is repeated here
//
// It is the same order as subscriptionParsers, and preview_test.go pins the two against each
// other on the same fixtures: a document that ParseSubscription would accept with a given reader
// is reported by this function with that reader's format name. The duplication is checked, not
// assumed.

import (
	"context"
	"strings"

	"github.com/sagernet/sing-box/option"
)

// Preview format names. They are the values a client switches on to explain an import.
const (
	PreviewFormatLinks   = "links"
	PreviewFormatClash   = "clash"
	PreviewFormatSIP008  = "sip008"
	PreviewFormatSingBox = "sing-box"
)

// PreviewSkip is one entry a reader refused, with the reason that entry produced.
type PreviewSkip struct {
	// Line is the 1-based line number for the link-list reader. The document readers do not
	// attribute a refusal to a line, and leave it zero rather than guessing one.
	Line int `json:"line,omitempty"`
	// Reason is the reader's own error text for this entry. It is described by the reader, not
	// rewritten here, so a caller can match on it - and so a fix in the reader reaches the user.
	Reason string `json:"reason"`
}

// SubscriptionPreview is one document's parse outcome, before anything is applied.
type SubscriptionPreview struct {
	// Format is one of the PreviewFormat names, or empty when no reader recognised the
	// document.
	Format string
	// Base64 reports that the winning reader had to decode the body first.
	Base64 bool
	// Outbounds are the nodes the document declares, in document order.
	Outbounds []option.Outbound
	// Endpoints are separate: a provider carries outbounds only (see ParseSubscription), and a
	// client that shows both in one list would promise something the provider cannot deliver.
	Endpoints []option.Endpoint
	// Skipped are the entries the winning reader refused.
	Skipped []PreviewSkip
	// Declared counts the entries the document declared, when the winning reader can count
	// them. Zero means "this reader does not report a declaration", NOT "the document declared
	// nothing" - the parsed count is the fact; this is the context for it.
	Declared int
	// Cancelled reports that ctx ended while the link list was still being read. The nodes in
	// Outbounds are what was parsed UP TO that point: a caller that needs a complete answer must
	// treat this as a refusal, not as a short document.
	Cancelled bool
	// Diagnostics carries every reader's error, keyed by format name. Nothing is recognised by
	// reading this; it is what lets a person understand a refusal, and it must not be shown as
	// the primary message.
	Diagnostics map[string]string
}

// Recognised reports whether any reader produced a node or an endpoint.
func (p *SubscriptionPreview) Recognised() bool {
	return len(p.Outbounds) > 0 || len(p.Endpoints) > 0
}

// ParseSubscriptionPreview decodes a subscription document the same way ParseSubscription does and
// reports what the winning reader found, including the entries it refused.
//
// A document that no reader recognises is NOT an error here: it is a preview with an empty Format
// and a populated Diagnostics map. Turning "I do not recognise this" into an error is the caller's
// decision, because for an import form that answer is a normal outcome, not a failure.
func ParseSubscriptionPreview(ctx context.Context, content string) *SubscriptionPreview {
	preview := &SubscriptionPreview{Diagnostics: make(map[string]string)}
	if strings.TrimSpace(content) == "" {
		return preview
	}

	if outbounds, endpoints, err := ParseBoxSubscription(ctx, content); len(outbounds) > 0 || len(endpoints) > 0 {
		return preview.adopt(PreviewFormatSingBox, outbounds, endpoints, nil, 0)
	} else if err != nil {
		preview.Diagnostics[PreviewFormatSingBox] = err.Error()
	}

	if outbounds, endpoints, err := ParseClashSubscription(ctx, content); len(outbounds) > 0 || len(endpoints) > 0 {
		return preview.adopt(PreviewFormatClash, outbounds, endpoints, nil, 0)
	} else if err != nil {
		preview.Diagnostics[PreviewFormatClash] = err.Error()
	}

	if outbounds, endpoints, err := ParseSIP008Subscription(ctx, content); len(outbounds) > 0 || len(endpoints) > 0 {
		return preview.adopt(PreviewFormatSIP008, outbounds, endpoints, nil, 0)
	} else if err != nil {
		preview.Diagnostics[PreviewFormatSIP008] = err.Error()
	}

	// The link-list reader is also the only one whose body may be base64-wrapped, and the only
	// one that can succeed PARTIALLY. Both facts are reported rather than smoothed over.
	base64Encoded := false
	linkContent := content
	if decoded, err := DecodeBase64URLSafe(content); err == nil && decoded != content {
		if decodedOutbounds, _, _, _ := parseRawPreview(ctx, decoded); len(decodedOutbounds) > 0 {
			linkContent = decoded
			base64Encoded = true
		}
	}
	outbounds, skipped, declared, cancelled := parseRawPreview(ctx, linkContent)
	preview.Cancelled = cancelled
	if len(outbounds) > 0 {
		preview.adopt(PreviewFormatLinks, outbounds, nil, skipped, declared)
		preview.Base64 = base64Encoded
		return preview
	}
	if len(skipped) > 0 {
		preview.Diagnostics[PreviewFormatLinks] = formatLinkDiagnostics(skipped)
	}
	return preview
}

// adopt fills the preview from the winning reader and returns it.
func (p *SubscriptionPreview) adopt(format string, outbounds []option.Outbound, endpoints []option.Endpoint, skipped []PreviewSkip, declared int) *SubscriptionPreview {
	p.Format = format
	p.Outbounds = outbounds
	p.Endpoints = endpoints
	if skipped == nil {
		skipped = []PreviewSkip{}
	}
	p.Skipped = skipped
	p.Declared = declared
	return p
}

// parseRawPreview mirrors parseRawSubscription line by line and keeps what that loop discards: a
// line ParseSubscriptionLink refused becomes a skip instead of disappearing.
//
// Empty and whitespace-only lines are not skips: they declare nothing, and reporting them would
// bury the one line that did fail. Every other line is handed to ParseSubscriptionLink UNCHANGED,
// so this function cannot become more permissive than the reader it previews.
//
// # Cancellation
//
// A subscription can be arbitrarily large (the core puts no cap on the body it accepts), so the
// loop honours ctx: once it is done, the lines parsed so far are returned together with
// cancelled=true. The check runs every previewCancelCheckInterval lines rather than per line: a
// context lookup per line would cost more than parsing a short link, and the interval bounds the
// overshoot to a few hundred cheap calls.
//
// A caller that wants "all or nothing" must treat cancelled=true as a refusal; the contract says
// the partial list is what the document declared UP TO that point, not a complete result.
func parseRawPreview(ctx context.Context, content string) (outbounds []option.Outbound, skipped []PreviewSkip, declared int, cancelled bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lineNumber := 0
	for lineContent := range strings.SplitSeq(content, "\n") {
		lineNumber++
		if ctx != nil && lineNumber%previewCancelCheckInterval == 0 && ctx.Err() != nil {
			return outbounds, skipped, declared, true
		}
		if strings.TrimSpace(lineContent) == "" {
			continue
		}
		declared++
		outbound, err := ParseSubscriptionLink(lineContent)
		if err != nil {
			skipped = append(skipped, PreviewSkip{Line: lineNumber, Reason: err.Error()})
			continue
		}
		outbounds = append(outbounds, outbound)
	}
	return outbounds, skipped, declared, false
}

// previewCancelCheckInterval is how many lines pass between two context checks.
const previewCancelCheckInterval = 256

// formatLinkDiagnostics renders the skipped lines as the reader's summary, so a preview that
// produced nothing still carries the same kind of explanation ParseSubscription would return.
func formatLinkDiagnostics(skipped []PreviewSkip) string {
	var builder strings.Builder
	builder.WriteString("no servers found in the link list")
	for _, skip := range skipped {
		builder.WriteString("; line ")
		builder.WriteString(itoa(skip.Line))
		builder.WriteString(": ")
		builder.WriteString(skip.Reason)
	}
	return builder.String()
}

// itoa is strconv.Itoa without the import churn; line numbers are small and non-negative.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}
