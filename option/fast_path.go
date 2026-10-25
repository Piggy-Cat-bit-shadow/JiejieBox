package option

import (
	"context"
	"strconv"

	"github.com/sagernet/sing-box/schema"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
)

// FastPathPolicy is the CONFIGURATION-side fast_path intent of a logical group.
//
// # What fast_path means, and what it does not
//
// It means: the flows that enter through THIS group and are resolved by it are authorised to
// *attempt* the existing Direct Fast Path. It does not mean the group is itself a bypassable
// outbound, it does not make the group's selected leaf into a fast-path node, and it does not skip
// any condition of the existing eligibility gate - the router still decides, per flow, through
// canFastBypass and BypassVerdict.
//
// # Why the envelope carries a pointer
//
// Three states have to survive decoding:
//
//	"fast_path" absent  -> nil   -> state nothing: a transparent chain over `direct` is authorised
//	                                intrinsically, and no other shape is authorised at all
//	"fast_path": true   -> true  -> authorise this group
//	"fast_path": false  -> false -> refuse it, including the intrinsic shape
//
// The third is the one a plain bool cannot express, and it is load-bearing: an operator whose
// transparent selector is used for flows that must stay in userspace writes `false`, and the
// intrinsic grant must not override a decision that explicit. It is the only value that changes a
// verdict in this tree - see route.resolveFastPathRequest, which reads it BEFORE the intrinsic rule
// and refuses the attempt.
//
// The states are NOT a tag heuristic. An earlier version fell back to the AI keyword matcher when
// nothing was stated; that fallback could not change a verdict for any shape this build can produce
// and has been removed, so the name of a group has no bearing on what this field does.
//
// # Why the shared envelope rather than SelectorOutboundOptions
//
// This is the same placement decision as TrafficClass, and it is deliberate rather than copied. The
// field has to exist for every logical group type that may carry it (currently selector and urltest,
// see adapter.FastPathAuthorizedGroup), and putting it on one options type would make it
// unavailable on the other and tie a flow policy to a single protocol. It also has to be
// REJECTED on every other outbound, which the reader does at configuration load: a leaf naming
// fast_path is a configuration error, never a node property.
//
// # Accepted, and inert where it cannot be applied
//
// The key is accepted everywhere and keeps its configuration meaning - it is still group-only, and a
// leaf that names it is still refused - but it only has an effect on an entry whose transport can
// act on an implicit bypass verdict: sing-tun's nfqueue consumer, which is what auto_redirect uses.
// Every other transport turns that verdict into the same accept entry as an ordinary accept
// (flow_dispatch.go install/installAccept, and the forwarders branch only on reject, drop and
// hijack), so no flow through it can change its data path because of this field. The router
// therefore does not run the authorisation-and-eligibility decision at all for those entries,
// rather than evaluating it per flow and discarding the answer; see
// adapter.InboundContext.ConsumesBypassVerdict.
//
// Writing it is not an error and does not require a migration: a configuration shared between iOS
// and the desktop platforms states the same intent everywhere, and the platforms that can honour it
// still do. What it must not be read as - on any platform - is a speed setting: it never selects a
// node, never reorders a group, and never authorises a splice.
//
// # Compatibility
//
// The field is omitted when unset, so a configuration that does not use it serialises exactly as
// before. A configuration that DOES use it is fork-only: official sing-box does not know this key.
type FastPathPolicy struct {
	// Enabled is the explicit intent. It is only ever read through the pointer on the envelope, so
	// its own zero value is never interpreted as a decision.
	Enabled bool
}

func (p FastPathPolicy) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatBool(p.Enabled)), nil
}

func (p *FastPathPolicy) UnmarshalJSON(content []byte) error {
	var value bool
	if err := json.Unmarshal(content, &value); err != nil {
		return E.Cause(err, "fast_path must be a boolean")
	}
	p.Enabled = value
	return nil
}

func (p FastPathPolicy) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	return schema.BooleanNode(), nil
}

// MarshalJSONContext and UnmarshalJSONContext are the context-aware forms the configuration loader
// uses, for the same reason TrafficClassPolicy has them: the option package's other types are
// decoded through the context-aware codec, which would otherwise bypass the plain methods above.
func (p FastPathPolicy) MarshalJSONContext(ctx context.Context) ([]byte, error) {
	return p.MarshalJSON()
}

func (p *FastPathPolicy) UnmarshalJSONContext(ctx context.Context, content []byte) error {
	return p.UnmarshalJSON(content)
}
