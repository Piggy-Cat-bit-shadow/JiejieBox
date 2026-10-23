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
//	"fast_path" absent  -> nil   -> fall back to automatic tag detection
//	"fast_path": true   -> true  -> authorise this group
//	"fast_path": false  -> false -> refuse, even if the tag looks like AI
//
// The third is the one a plain bool cannot express, and it is load-bearing: an operator whose AI
// selector is used for flows that must stay in userspace writes `false`, and automatic detection
// must not override a decision that explicit. The same applies to the intrinsic grant a transparent
// chain over `direct` receives when nobody states a policy: an explicit false is a decision, so
// resolveFastPathRequest reads it BEFORE the intrinsic rule and the chain refuses the attempt. See
// FastPathRequest.
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
