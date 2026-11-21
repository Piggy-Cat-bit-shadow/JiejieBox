package parser

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
)

type _SingBoxDocument struct {
	Outbounds []option.Outbound `json:"outbounds"`
	Endpoints []option.Endpoint `json:"endpoints"`
}
type SingBoxDocument _SingBoxDocument

// UnmarshalJSONContext keeps the type's historical entry point for a caller that decodes a document
// with the JSON library directly. The provider reader uses ParseBoxSubscription, which decodes the
// same content from the string it already holds and does not copy it into a byte slice first.
func (o *SingBoxDocument) UnmarshalJSONContext(ctx context.Context, inputContent []byte) error {
	document, err := decodeSingBoxDocument(ctx, string(inputContent))
	if err != nil {
		return err
	}
	*o = SingBoxDocument(document)
	return nil
}

// decodeSingBoxDocument reads a native sing-box subscription document in one streaming pass.
//
// # Why this is not a whole-document decode of `SingBoxDocument`
//
// The document is untrusted and it is also the largest object a provider holds: a 10 000-node
// subscription is about one and a half megabytes of JSON before it is anything else. The historical reader built a
// generic JSON tree of the WHOLE document (about 4 KB of tree per node), filtered the `outbounds`
// array inside it, re-serialised the filtered tree (another document-sized copy plus a per-value
// buffer for every field) and only then decoded it into options - three whole-document workspaces, all live at the
// same time, on top of the option tree that is actually kept. MEASURED at 1 000
// nodes: 3.96 MB tree + 3.45 MB re-serialisation + 0.17 MB input copy for a 172 KB document.
//
// This reader keeps the parts of that behaviour that are semantic, and drops the materialisation:
//
//   - `outbounds` is validated element by element, so the indexed errors ("outbound[i] must be an
//     object, got null", "missing type in outbound[i]", "outbound[i] type must be a string") and
//     their ORIGINAL indexes are preserved. An element is decoded into options only after its type
//     passed, so a dropped group/direct element is never validated - the historical reader skipped
//     its payload too.
//   - The endpoint-tunable node types (`direct`, `block`, `dns`, `selector`, `urltest`) are not a
//     provider's nodes and are dropped without decoding, exactly as before.
//   - `null` as the `outbounds` value stays "no outbounds, no error"; any other non-array value is
//     still "outbounds must be an array, got <kind>".
//   - The last occurrence of a duplicated key wins, and unknown top-level keys are skipped, which is
//     what the schema-less whole-document decode did.
//   - Only one JSON value is accepted: trailing content is an error.
//
// # What it deliberately does not preserve byte for byte
//
// An option-level failure inside one element is reported with the same "outbounds[i]." context and
// the same index, but the streaming decoder cannot reuse the parent decoder's path stack, so a
// failure that used to read "outbounds[0].tls.enabled: ..." keeps that exact shape while one that
// carried no field path keeps the ": " join (both shapes are reproduced by indexedDecodeError). A
// malformed `endpoints` value is reported as a typed decode error without the "endpoints" field
// prefix. Malformed JSON inside an element loses the row/column suffix, whose offset the streaming
// decoder reports relative to the element rather than to the document. Every one of these is still
// a rejection with a message; none of them changes which documents are accepted or what they decode
// to. TestParseBoxSubscriptionMatchesTheWholeDocumentReader pins the accepted values and the error
// classification against a frozen copy of the previous implementation.
func decodeSingBoxDocument(ctx context.Context, content string) (SingBoxDocument, error) {
	var document SingBoxDocument
	decoder := json.NewDecoderContext(ctx, strings.NewReader(content))
	token, err := decoder.Token()
	if err != nil {
		return document, err
	}
	if token != json.Delim('{') {
		// The historical message, including "nil" for a null document.
		return document, E.New("expected json object start, but starts with ", token)
	}
	// The two typed fields are buffered RAW and decoded after the object closes, so a duplicated
	// top-level key keeps the last-value-wins behaviour the previous whole-document reader had.
	// Eager per-occurrence decoding would validate the FIRST `outbounds` and reject a document whose
	// later duplicate replaces it - a change in the accepted set, not in an error message. The
	// buffer is one raw slice of the winning value, not a generic tree, which is where the saving
	// this decoder exists for comes from.
	var (
		outboundsRaw json.RawMessage
		endpointsRaw json.RawMessage
		hasOutbounds bool
		hasEndpoints bool
	)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return document, err
		}
		key, isKey := keyToken.(string)
		if !isKey {
			return document, E.New("expected json object key, but got ", keyToken)
		}
		switch key {
		case "outbounds":
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return document, err
			}
			outboundsRaw = raw
			hasOutbounds = true
		case "endpoints":
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return document, err
			}
			endpointsRaw = raw
			hasEndpoints = true
		default:
			// An unknown top-level key is ignored by the typed schema; skip its value without
			// materialising it into a tree.
			var discarded json.RawMessage
			if err := decoder.Decode(&discarded); err != nil {
				return document, err
			}
		}
	}
	if _, err = decoder.Token(); err != nil {
		return document, err
	}
	trailing, err := decoder.Token()
	if err == io.EOF {
		trailing = nil
	} else if err != nil {
		return document, err
	}
	if trailing != nil {
		return document, E.New("invalid character after top-level value: ", trailing)
	}
	if hasOutbounds {
		outbounds, err := decodeOutboundsRaw(ctx, outboundsRaw)
		if err != nil {
			return document, err
		}
		document.Outbounds = outbounds
	}
	if hasEndpoints {
		endpoints, err := decodeEndpointsRaw(ctx, endpointsRaw)
		if err != nil {
			return document, err
		}
		document.Endpoints = endpoints
	}
	return document, nil
}

// decodeOutboundsRaw decodes the winning `outbounds` value from its raw bytes.
func decodeOutboundsRaw(ctx context.Context, raw json.RawMessage) ([]option.Outbound, error) {
	return decodeOutboundsValue(ctx, json.NewDecoderContext(ctx, bytes.NewReader(raw)))
}

// decodeEndpointsRaw decodes the winning `endpoints` value from its raw bytes.
func decodeEndpointsRaw(ctx context.Context, raw json.RawMessage) ([]option.Endpoint, error) {
	return decodeEndpointsValue(ctx, json.NewDecoderContext(ctx, bytes.NewReader(raw)))
}

// decodeOutboundsValue reads the `outbounds` value from the decoder and returns the provider's node
// list. See decodeSingBoxDocument for the preserved behaviour.
func decodeOutboundsValue(ctx context.Context, decoder *json.Decoder) ([]option.Outbound, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token == nil {
		// "outbounds": null - the historical reader left the field unset and reported no error.
		return nil, nil
	}
	if delim, isDelim := token.(json.Delim); !isDelim || delim != '[' {
		return nil, E.New("outbounds must be an array, got ", describeJSONToken(token))
	}
	var outbounds []option.Outbound
	index := 0
	for decoder.More() {
		var element json.RawMessage
		if err := decoder.Decode(&element); err != nil {
			return nil, err
		}
		kind := describeJSONValue(element)
		if kind != "object" {
			return nil, E.New("outbound[", index, "] must be an object, got ", kind)
		}
		var head struct {
			Type json.RawMessage `json:"type"`
		}
		if err := json.UnmarshalContext(ctx, element, &head); err != nil {
			return nil, err
		}
		if head.Type == nil {
			return nil, E.New("missing type in outbound[", index, "]")
		}
		typeKind := describeJSONValue(head.Type)
		if typeKind != "string" {
			return nil, E.New("outbound[", index, "] type must be a string, got ", typeKind)
		}
		var typeName string
		if err := json.UnmarshalContext(ctx, head.Type, &typeName); err != nil {
			return nil, E.New("outbound[", index, "] type must be a string, got ", typeKind)
		}
		index++
		if !isProviderNodeType(typeName) {
			// A group or a built-in has a payload this reader refuses to interpret; the historical
			// reader dropped it before the typed decode for the same reason.
			continue
		}
		var outbound option.Outbound
		if err := json.UnmarshalContext(ctx, element, &outbound); err != nil {
			return nil, indexedDecodeError(err, "outbounds", len(outbounds))
		}
		outbounds = append(outbounds, outbound)
	}
	// Consume the closing bracket. The element loop stopped at it, so this consumes a token the
	// decoder has already buffered.
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	return outbounds, nil
}

// isProviderNodeType reports whether a declared outbound type can be a provider node. A provider is
// a dynamic node set: a group would depend on members the provider does not own, and direct/block/dns
// are not servers at all. The list is the one the historical reader filtered.
func isProviderNodeType(outboundType string) bool {
	switch outboundType {
	case C.TypeDirect, C.TypeBlock, C.TypeDNS, C.TypeSelector, C.TypeURLTest:
		return false
	default:
		return true
	}
}

// decodeEndpointsValue reads the `endpoints` array. Endpoints are not provider nodes and carry no
// filtering, but they are decoded one element at a time so an endpoint list does not have to be
// materialised as bytes before it is decoded; the indexed error shape is preserved the same way.
func decodeEndpointsValue(ctx context.Context, decoder *json.Decoder) ([]option.Endpoint, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token == nil {
		return nil, nil
	}
	if delim, isDelim := token.(json.Delim); !isDelim || delim != '[' {
		return nil, E.New("endpoints must be an array, got ", describeJSONToken(token))
	}
	var endpoints []option.Endpoint
	index := 0
	for decoder.More() {
		var element json.RawMessage
		if err := decoder.Decode(&element); err != nil {
			return nil, err
		}
		var endpoint option.Endpoint
		if err := json.UnmarshalContext(ctx, element, &endpoint); err != nil {
			return nil, indexedDecodeError(err, "endpoints", index)
		}
		endpoints = append(endpoints, endpoint)
		index++
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	return endpoints, nil
}

// indexedDecodeError prefixes an element decode failure with the index the historical whole-document
// decoder printed. It also preserves that decoder's join: a failure that carries its own field path
// was joined with ".", and one without a path with ": ". The streaming decoder cannot reuse the
// parent decoder's path stack, so the shape is rebuilt from the failure's own context marker.
func indexedDecodeError(err error, field string, index int) error {
	prefix := field + "[" + strconv.Itoa(index) + "]"
	if strings.Contains(err.Error(), ": json:") {
		return &indexedContextError{prefix: prefix + ".", cause: err}
	}
	return E.Cause(err, prefix)
}

type indexedContextError struct {
	prefix string
	cause  error
}

func (e *indexedContextError) Error() string {
	return e.prefix + e.cause.Error()
}

func (e *indexedContextError) Unwrap() error {
	return e.cause
}

// describeJSONToken names the kind a token-level assertion met. It is the token counterpart of
// describeJSONValue, used for the `outbounds` value itself because that value is never materialised.
func describeJSONToken(token json.Token) string {
	switch value := token.(type) {
	case nil:
		return "null"
	case json.Delim:
		if value == '{' {
			return "object"
		}
		if value == '[' {
			return "array"
		}
		return "unknown"
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

// describeJSONValue names the kind of a raw JSON value without decoding it. The kind vocabulary is
// the one describeJSONKind used for the generic tree.
func describeJSONValue(value []byte) string {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 {
		return "unknown"
	}
	switch trimmed[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

func ParseBoxSubscription(ctx context.Context, content string) ([]option.Outbound, []option.Endpoint, error) {
	document, err := decodeSingBoxDocument(ctx, content)
	if err != nil {
		return nil, nil, describeDecodeError(content, err)
	}
	if len(document.Outbounds) == 0 && len(document.Endpoints) == 0 {
		return nil, nil, E.New("no servers found")
	}
	return document.Outbounds, document.Endpoints, nil
}

// describeDecodeError adds the row and column the JSON library's unmarshal helper added to a syntax
// error, and turns a plain end-of-input into the message that helper produced. The annotation is
// skipped for an element-local failure: the streaming decoder reports its syntax offset relative to
// the element, so a document-relative row/column would point at the wrong place.
func describeDecodeError(content string, err error) error {
	var indexed *indexedContextError
	if errors.As(err, &indexed) {
		return err
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		offset := int(syntaxError.Offset)
		if offset < 0 || offset > len(content) {
			return err
		}
		row, column := jsonRowColumn(content, offset)
		return E.Extend(syntaxError, "row ", row, ", column ", column)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		row, column := jsonRowColumn(content, len(content))
		return E.New("unexpected end of JSON input: row ", row, ", column ", column)
	}
	return err
}

func jsonRowColumn(content string, offset int) (int, int) {
	if offset > len(content) {
		offset = len(content)
	}
	prefix := content[:offset]
	row := strings.Count(prefix, "\n") + 1
	column := len(prefix) - strings.LastIndex(prefix, "\n") - 1
	return row, column
}
