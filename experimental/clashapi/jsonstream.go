package clashapi

import (
	"encoding/json"
	"io"
)

// jsonStream writes a single JSON document incrementally to an io.Writer.
//
// # Why this exists
//
// The list endpoints used to build the complete document before writing any of it:
//
//   - /rules built a []Rule and passed it to render.JSON, which encodes the whole document into a
//     bytes.Buffer and only then writes it;
//   - /proxies built a badjson object graph (one linked-hash-map entry per node, one JSONObject per
//     node) and then marshalled that graph into a single []byte.
//
// Both therefore materialise the entire response before the first byte reaches the client, so a
// response with N entries costs the object graph AND the encoded document at the same time, and a
// client that reads slowly keeps the whole document resident. jsonStream keeps the output bytes the
// same while bounding the encoder's own state to one value: each value is encoded on its own and
// written through to the ResponseWriter, whose existing buffer does the batching.
//
// The writer is deliberately not a global pool: the only per-request allocations are the encoded
// values themselves, and holding a large buffer in a package-level sync.Pool would pin the largest
// response the process has ever produced.
//
// # Errors
//
// The first error is sticky. Every later call becomes a no-op, so a handler can write its fields
// naturally and check once, and a disconnected client stops the loop instead of being fed the rest
// of a response nobody will read.
type jsonStream struct {
	writer io.Writer
	err    error
}

func newJSONStream(writer io.Writer) *jsonStream {
	return &jsonStream{writer: writer}
}

// raw writes literal JSON syntax (braces, colons, commas, field names). It is not for user data.
func (s *jsonStream) raw(literal string) {
	if s.err != nil {
		return
	}
	_, s.err = io.WriteString(s.writer, literal)
}

// rawBytes writes already-encoded JSON (for example the inside of an array produced by one
// json.Marshal pass).
func (s *jsonStream) rawBytes(encoded []byte) {
	if s.err != nil {
		return
	}
	_, s.err = s.writer.Write(encoded)
}

// value encodes one value with encoding/json's default escaping (HTML escaping ON, exactly as the
// encoders this replaces did) and writes it.
func (s *jsonStream) value(value any) {
	if s.err != nil {
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		s.err = err
		return
	}
	_, s.err = s.writer.Write(encoded)
}

// stringArray writes a []string the way encoding/json would: a nil slice is null, an empty slice is
// [] and every element is separately escaped.
func (s *jsonStream) stringArray(values []string) {
	if s.err != nil {
		return
	}
	if values == nil {
		s.raw("null")
		return
	}
	s.raw("[")
	for index, value := range values {
		if s.err != nil {
			return
		}
		if index > 0 {
			s.raw(",")
		}
		s.value(value)
	}
	s.raw("]")
}
