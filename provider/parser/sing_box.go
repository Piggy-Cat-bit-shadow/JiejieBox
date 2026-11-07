package parser

import (
	"context"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
)

type _SingBoxDocument struct {
	Outbounds []option.Outbound `json:"outbounds"`
	Endpoints []option.Endpoint `json:"endpoints"`
}
type SingBoxDocument _SingBoxDocument

func (o *SingBoxDocument) UnmarshalJSONContext(ctx context.Context, inputContent []byte) error {
	var content badjson.JSONObject
	err := content.UnmarshalJSONContext(ctx, inputContent)
	if err != nil {
		return err
	}
	outbounds, hasOutbounds := content.Get("outbounds")
	if !hasOutbounds || outbounds == nil {
		o.Outbounds = nil
		return nil
	}
	// Every assertion below is checked rather than assumed: a subscription is remote input, and a
	// document whose outbounds is an object, or whose element is null, must be reported as an error
	// naming the index - not panic the daemon and not be silently accepted.
	array, isArray := outbounds.(badjson.JSONArray)
	if !isArray {
		return E.New("outbounds must be an array, got ", describeJSONKind(outbounds))
	}
	var outs badjson.JSONArray
	for i, element := range array {
		object, isObject := element.(*badjson.JSONObject)
		if !isObject {
			return E.New("outbound[", i, "] must be an object, got ", describeJSONKind(element))
		}
		typeValue, loaded := object.Get("type")
		if !loaded {
			return E.New("missing type in outbound[", i, "]")
		}
		typeName, isString := typeValue.(string)
		if !isString {
			return E.New("outbound[", i, "] type must be a string, got ", describeJSONKind(typeValue))
		}
		switch typeName {
		case C.TypeDirect, C.TypeBlock, C.TypeDNS, C.TypeSelector, C.TypeURLTest:
			continue
		default:
			outs = append(outs, object)
		}
	}
	content.Put("outbounds", outs)
	inputContent, err = content.MarshalJSONContext(ctx)
	if err != nil {
		return err
	}
	return json.UnmarshalContext(ctx, inputContent, (*_SingBoxDocument)(o))
}

// describeJSONKind names the JSON kind an assertion met, so an error explains what arrived instead
// of only what was expected.
func describeJSONKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case *badjson.JSONObject:
		return "object"
	case badjson.JSONArray:
		return "array"
	case string:
		return "string"
	case float64, int64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}

func ParseBoxSubscription(ctx context.Context, content string) ([]option.Outbound, []option.Endpoint, error) {
	options, err := json.UnmarshalExtendedContext[SingBoxDocument](ctx, []byte(content))
	if err != nil {
		return nil, nil, err
	}
	if len(options.Outbounds) == 0 && len(options.Endpoints) == 0 {
		return nil, nil, E.New("no servers found")
	}
	return options.Outbounds, options.Endpoints, nil
}
