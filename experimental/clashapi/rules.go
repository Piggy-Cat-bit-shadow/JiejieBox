package clashapi

import (
	"encoding/json"
	"net/http"

	"github.com/sagernet/sing-box/adapter"

	"github.com/go-chi/chi/v5"
)

// rulesEncodeBatch is how many rules are encoded in one encoding/json pass.
//
// One pass per rule was measured to be ~20% slower than the old single pass for ten thousand rules:
// every json.Marshal pays the encoder's setup and copies its result. One pass per 64 rules amortises
// that while keeping the temporary buffer at a few KiB - roughly 1000x smaller than encoding the
// whole document, and bounded independently of the rule count.
const rulesEncodeBatch = 64

func ruleRouter(router adapter.Router) http.Handler {
	r := chi.NewRouter()
	r.Get("/", getRules(router))
	return r
}

type Rule struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
}

func getRules(router adapter.Router) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		rawRules := router.Rules()

		// The old implementation built []Rule and handed it to render.JSON. With no rules the
		// accumulator stayed nil, and encoding/json writes a nil slice as null - so the historical
		// empty body is {"rules":null}, not {"rules":[]}. That byte sequence is kept.
		//
		// Streaming is safe here for a specific reason, not by assumption: every field written below
		// is a string (Rule.Type/Payload/Proxy), and encoding/json cannot fail on a string. The old
		// code's only possible Encode error was therefore unreachable too - render.JSON's
		// http.Error(500) could not happen for this endpoint. /proxies, whose history field contains
		// a time.Time, does have a reachable (if pathological) MarshalJSON failure and pre-validates
		// it before writing; see getProxies.
		w.Header().Set("Content-Type", "application/json")
		stream := newJSONStream(w)
		stream.raw(`{"rules":`)
		if len(rawRules) == 0 {
			stream.raw("null")
		} else {
			stream.raw("[")
			batch := make([]Rule, 0, rulesEncodeBatch)
			for start := 0; start < len(rawRules); start += rulesEncodeBatch {
				// Stop as soon as the client is gone or a write failed: continuing would keep
				// calling String() on every remaining rule for a response nobody will receive.
				if stream.err != nil || r.Context().Err() != nil {
					return
				}
				end := start + rulesEncodeBatch
				if end > len(rawRules) {
					end = len(rawRules)
				}
				batch = batch[:0]
				for _, rule := range rawRules[start:end] {
					batch = append(batch, Rule{
						Type:    rule.Type(),
						Payload: rule.String(),
						Proxy:   rule.Action().String(),
					})
				}
				encoded, err := json.Marshal(batch)
				if err != nil {
					// Unreachable for string fields, but do not emit a half-written value if it
					// ever becomes reachable.
					stream.err = err
					return
				}
				// encoded is the JSON array "[{...},{...}]"; write its elements, with the comma
				// between batches restoring the single-array layout.
				if start > 0 {
					stream.raw(",")
				}
				stream.rawBytes(encoded[1 : len(encoded)-1])
			}
			stream.raw("]")
		}
		// render.JSON ended the document with the encoder's newline; keep it.
		stream.raw("}\n")
	}
}
