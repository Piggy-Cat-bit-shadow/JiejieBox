package clashapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing/common"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/json/badjson"
	N "github.com/sagernet/sing/common/network"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
)

func proxyRouter(server *Server, router adapter.Router) http.Handler {
	r := chi.NewRouter()
	r.Get("/", getProxies(server))

	r.Route("/{name}", func(r chi.Router) {
		r.Use(parseProxyName, findProxyByName(server))
		r.Get("/", getProxy(server))
		r.Get("/delay", getProxyDelay(server))
		r.Put("/", updateProxy)
	})
	return r
}

func parseProxyName(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := getEscapeParam(r, "name")
		ctx := context.WithValue(r.Context(), CtxKeyProxyName, name)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func findProxyByName(server *Server) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := r.Context().Value(CtxKeyProxyName).(string)

			// Outbounds first, then endpoints.
			//
			// Endpoints are listed alongside outbounds in /proxies, so a client that sees one
			// there must be able to measure it. Looking only at the outbound manager made every
			// endpoint visible but unmeasurable: the delay request answered 404 for a node the
			// same API had just advertised.
			//
			// An endpoint satisfies adapter.Outbound, so it can be measured directly. An outbound
			// wins a tag collision, which keeps the existing resolution order for every name that
			// resolved before.
			proxy, exist := server.outbound.Outbound(name)
			if !exist {
				if server.endpoint != nil {
					if endpoint, endpointExists := server.endpoint.Get(name); endpointExists {
						proxy, exist = endpoint, true
					}
				}
			}
			if !exist {
				render.Status(r, http.StatusNotFound)
				render.JSON(w, r, ErrNotFound)
				return
			}
			ctx := context.WithValue(r.Context(), CtxKeyProxy, proxy)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// proxyView is the scalar state of one node as the list endpoint reports it.
//
// It exists so that building the response and writing it are separate phases: every value is read
// from the live manager/group while the snapshot is built, and the (possibly slow) network write
// afterwards touches only these fields. It is deliberately made of strings, a bool, one history
// pointer and the group's member tags - no outbound reference, no JSON object graph. Holding an
// outbound here would keep a node (and whatever it owns) alive for as long as a slow client reads,
// and the provider contract only allows Tag/Type/Network to be read from a replaced object.
type proxyView struct {
	clashType string
	history   *adapter.URLTestHistory
	all       []string
	now       string
	udp       bool
	isGroup   bool
	hasNow    bool
}

// newProxyView performs exactly the reads the old proxyInfo did, in the same order, and returns them
// as plain values.
func newProxyView(server *Server, detour adapter.Outbound) proxyView {
	var view proxyView
	switch detour.Type() {
	case C.TypeBlock:
		view.clashType = "Reject"
	default:
		view.clashType = C.ProxyDisplayName(detour.Type())
	}
	view.udp = common.Contains(detour.Network(), N.NetworkUDP)
	view.history = server.urlTestHistory.LoadURLTestHistory(group.RealTag(detour, N.NetworkTCP))
	if detourGroup, isGroup := detour.(adapter.OutboundGroup); isGroup {
		view.isGroup = true
		// A flow-aware group has no current member to report.
		//
		// Its member is a property of the flow, so Selected() answers "which member would
		// the next flow take" - a question whose answer changes with every flow and is not
		// the member any existing connection is using. Publishing that as `now` would tell
		// the user every flow goes there, which is the opposite of what the group does. The
		// field is omitted, as the reference implementation omits it for the same reason,
		// while `all` and `type` still describe the group so a dashboard can list it.
		if _, isFlowAware := detourGroup.(adapter.FlowAwareOutboundGroup); !isFlowAware {
			view.hasNow = true
			if selected := detourGroup.Selected(N.NetworkTCP); selected != nil {
				view.now = selected.Tag()
			}
		}
		view.all = detourGroup.All()
	}
	return view
}

func proxyInfo(server *Server, detour adapter.Outbound) *badjson.JSONObject {
	view := newProxyView(server, detour)
	var info badjson.JSONObject
	info.Put("type", view.clashType)
	info.Put("name", detour.Tag())
	info.Put("udp", view.udp)
	if view.history != nil {
		info.Put("history", []*adapter.URLTestHistory{view.history})
	} else {
		info.Put("history", []*adapter.URLTestHistory{})
	}
	if view.isGroup {
		if view.hasNow {
			info.Put("now", view.now)
		}
		info.Put("all", view.all)
	}
	return &info
}

// proxyEntry is one key/value pair of the `proxies` object, in output order.
type proxyEntry struct {
	key       string
	clashType string
	history   *adapter.URLTestHistory
	all       []string
	now       string
	global    bool
	isGroup   bool
	hasNow    bool
	udp       bool
}

// snapshotProxyEntries builds the ordered entry list once, before any byte is written.
//
// The ordering and duplicate-key semantics are the ones badjson.JSONObject.Put provided: the first
// occurrence of a key fixes its position, a later occurrence REPLACES the value in place. That is
// observable - when an outbound and an endpoint share a tag, the endpoint's card is reported but at
// the outbound's position - so it is reproduced rather than "cleaned up".
//
// The duplicate index is dropped when no duplicate occurred (the normal case) so a slow client does
// not pin a map it will never consult again.
func snapshotProxyEntries(server *Server) ([]proxyEntry, error) {
	outbounds := common.Filter(server.outbound.Outbounds(), func(detour adapter.Outbound) bool {
		return detour.Tag() != ""
	})
	outbounds = append(outbounds, common.Map(common.Filter(server.endpoint.Endpoints(), func(detour adapter.Endpoint) bool {
		return detour.Tag() != ""
	}), func(it adapter.Endpoint) adapter.Outbound {
		return it
	})...)

	allProxies := make([]string, 0, len(outbounds))

	for _, detour := range outbounds {
		switch detour.Type() {
		case C.TypeDirect, C.TypeBlock, C.TypeDNS:
			continue
		}
		allProxies = append(allProxies, detour.Tag())
	}

	defaultTag := server.outbound.Default().Tag()

	// Exactly the old stable sort: every entry equal to the default tag is moved to the front,
	// keeping the relative order of everything else.
	sort.SliceStable(allProxies, func(i, j int) bool {
		return allProxies[i] == defaultTag
	})

	entries := make([]proxyEntry, 0, len(outbounds)+1)
	positions := make(map[string]int, len(outbounds)+1)
	// fix clash dashboard
	entries = append(entries, proxyEntry{
		key:    "GLOBAL",
		global: true,
		all:    allProxies,
		now:    defaultTag,
	})
	positions["GLOBAL"] = 0
	duplicate := false
	put := func(entry proxyEntry) {
		if index, loaded := positions[entry.key]; loaded {
			// Put replaced the value in place, so a node tagged GLOBAL takes the pseudo entry's
			// position but not its Fallback card.
			entry.global = false
			entries[index] = entry
			duplicate = true
			return
		}
		positions[entry.key] = len(entries)
		entries = append(entries, entry)
	}

	for i, detour := range outbounds {
		var tag string
		if detour.Tag() == "" {
			tag = F.ToString(i)
		} else {
			tag = detour.Tag()
		}
		view := newProxyView(server, detour)
		put(proxyEntry{
			key:       tag,
			clashType: view.clashType,
			history:   view.history,
			all:       view.all,
			now:       view.now,
			isGroup:   view.isGroup,
			hasNow:    view.hasNow,
			udp:       view.udp,
		})
	}
	if !duplicate {
		// The index is only needed to reproduce "replace in place" duplicate handling. Dropping
		// it here makes the map unreachable before the response is written, so a slow client does
		// not pin it.
		positions = nil
	}

	// MarshalJSON of the old object graph could fail in exactly one place: a history entry whose
	// time is outside the RFC 3339 range encodable by time.Time.MarshalJSON. Everything else in the
	// document is a string, a bool or a []string. The condition is checked here, before the response
	// head and before the first byte, so the endpoint keeps the old 500-instead-of-body behaviour
	// instead of emitting a truncated document.
	if invalid := invalidProxyHistory(entries); invalid != nil {
		if _, err := json.Marshal(invalid); err != nil {
			return nil, err
		}
		// Unreachable: the year range above is the only failure mode of time.Time.MarshalJSON.
	}
	return entries, nil
}

// invalidProxyHistory returns the first history entry that encoding/json cannot encode.
func invalidProxyHistory(entries []proxyEntry) *adapter.URLTestHistory {
	for index := range entries {
		history := entries[index].history
		if history == nil {
			continue
		}
		// time.Time.MarshalJSON fails when the year is outside [0, 9999].
		if year := history.Time.Year(); year < 0 || year > 9999 {
			return history
		}
	}
	return nil
}

func getProxies(server *Server) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := snapshotProxyEntries(server)
		if err != nil {
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, newError(err.Error()))
			return
		}
		// No Content-Type is set on purpose: the old handler wrote the marshalled bytes without
		// one, so net/http sniffed the body (text/plain; charset=utf-8). Keeping the first chunk
		// text makes the sniffed type identical.
		stream := newJSONStream(w)
		stream.raw(`{"proxies": {`)
		for index := range entries {
			if stream.err != nil || r.Context().Err() != nil {
				// The client is gone or the write failed. Stop: a truncated document is not
				// reported as success because the connection is already unusable, and no error
				// JSON is appended to a body that has already started.
				return
			}
			if index > 0 {
				stream.raw(",")
			}
			writeProxyEntry(stream, &entries[index])
		}
		stream.raw("}}")
	}
}

// writeProxyEntry writes one node card in the exact byte layout the old object graph produced.
//
// The layout is compact (`{"type":"Direct",...}`) because encoding/json compacts the output of
// every MarshalJSON implementation it calls, and the nested badjson objects were all produced
// through such calls. Only the outermost `{"proxies": ` separator kept a space: it was written by
// the top-level badjson object, whose bytes were passed to w.Write without a further compaction.
// That single space is reproduced here and nowhere else.
func writeProxyEntry(stream *jsonStream, entry *proxyEntry) {
	stream.value(entry.key)
	if entry.global {
		// encoding/json sorts map keys, so the pseudo GLOBAL card is always in this order.
		stream.raw(`:{"all":`)
		stream.stringArray(entry.all)
		stream.raw(`,"history":[],"name":"GLOBAL","now":`)
		stream.value(entry.now)
		stream.raw(`,"type":"Fallback","udp":true}`)
		return
	}
	stream.raw(`:{"type":`)
	stream.value(entry.clashType)
	stream.raw(`,"name":`)
	stream.value(entry.key)
	stream.raw(`,"udp":`)
	if entry.udp {
		stream.raw("true")
	} else {
		stream.raw("false")
	}
	stream.raw(`,"history":`)
	if entry.history == nil {
		stream.raw("[]")
	} else {
		stream.raw("[")
		stream.value(entry.history)
		stream.raw("]")
	}
	if entry.isGroup {
		if entry.hasNow {
			stream.raw(`,"now":`)
			stream.value(entry.now)
		}
		stream.raw(`,"all":`)
		stream.stringArray(entry.all)
	}
	stream.raw("}")
}

func getProxy(server *Server) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		proxy := r.Context().Value(CtxKeyProxy).(adapter.Outbound)
		response, err := proxyInfo(server, proxy).MarshalJSON()
		if err != nil {
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, newError(err.Error()))
			return
		}
		w.Write(response)
	}
}

type UpdateProxyRequest struct {
	Name string `json:"name"`
}

func updateProxy(w http.ResponseWriter, r *http.Request) {
	req := UpdateProxyRequest{}
	if err := render.DecodeJSON(r.Body, &req); err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, ErrBadRequest)
		return
	}

	proxy := r.Context().Value(CtxKeyProxy).(adapter.Outbound)
	selector, ok := proxy.(*group.Selector)
	if !ok {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("Must be a Selector"))
		return
	}

	if !selector.SelectOutbound(req.Name) {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, newError("Selector update error: not found"))
		return
	}

	render.NoContent(w, r)
}

func groupContains(outboundManager adapter.OutboundManager, outboundGroup adapter.OutboundGroup, tag string, visited map[string]bool) bool {
	for _, memberTag := range outboundGroup.All() {
		if memberTag == tag {
			return true
		}
		member, loaded := outboundManager.Outbound(memberTag)
		if !loaded {
			continue
		}
		if group.RealTag(member, N.NetworkTCP) == tag {
			return true
		}
		memberGroup, isGroup := member.(adapter.OutboundGroup)
		if !isGroup || visited[memberTag] {
			continue
		}
		visited[memberTag] = true
		if groupContains(outboundManager, memberGroup, tag, visited) {
			return true
		}
	}
	return false
}

func getProxyDelay(server *Server) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		// The requested URL is used as given.
		//
		// This used to blank out any `http://` URL, which then fell through to the default
		// `https://www.gstatic.com/generate_204`. A client explicitly asking to measure against
		// a plain-HTTP endpoint therefore measured a TLS endpoint instead - a different
		// destination, an extra TLS handshake, and a number that cannot be compared with another
		// client's measurement of the same URL.
		//
		// An empty URL still means "use the default"; an explicit scheme is honoured.
		url := query.Get("url")
		// The timeout must be a POSITIVE number of milliseconds.
		//
		// ParseInt accepted a negative value, and a negative duration makes the context already
		// expired - so "timeout=-1" produced an instant, meaningless "measurement" that looked
		// like a failed probe rather than a bad request.
		timeout, err := strconv.ParseInt(query.Get("timeout"), 10, 16)
		if err != nil || timeout <= 0 {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, ErrBadRequest)
			return
		}

		// `expected` restricts which HTTP status counts as a successful measurement, using the
		// Mihomo syntax (204, 200-299, 200/204/301-399, *, or empty for no constraint).
		// A malformed expression is the caller's error, so it is refused here rather than being
		// silently ignored and producing a measurement against the wrong acceptance rule.
		expected, err := urltest.ParseExpectedStatus(query.Get("expected"))
		if err != nil {
			render.Status(r, http.StatusBadRequest)
			render.JSON(w, r, ErrBadRequest)
			return
		}

		proxy := r.Context().Value(CtxKeyProxy).(adapter.Outbound)

		// Resolve the leaf ONCE, before measuring.
		//
		// A group's selection can move while the measurement is in flight, so resolving afterwards
		// - which is what RealTag did at the end - could attribute the delay to a node that was
		// never measured. The attribution must be a fact about the connection that was tested, so
		// the leaf is fixed up front and the measurement runs against it.
		proxy, leafErr := group.ResolveURLTestLeaf(proxy, N.NetworkTCP)
		if leafErr != nil {
			render.Status(r, http.StatusInternalServerError)
			render.JSON(w, r, ErrBadRequest)
			return
		}

		// The measurement context is derived from the SERVER's context, not from Background.
		//
		// Measure reads the Box's own services out of the context - the certificate roots it must
		// trust and the NTP clock it timestamps with. Starting from Background discarded both, so
		// an HTTPS probe through a private root or against a skewed clock failed here while the
		// identical native or group measurement succeeded: same engine, same node, different
		// answer purely because of which context reached it.
		//
		// The request's own context is wired in as well, so a client that disconnects cancels the
		// measurement instead of leaving it to run on.
		ctx, cancel := context.WithCancel(server.ctx)
		stopRequestWatch := context.AfterFunc(r.Context(), cancel)
		defer stopRequestWatch()
		defer cancel()

		// The caller's requested timeout applies on top, inside the Box's lifetime.
		ctx, cancelTimeout := context.WithTimeout(ctx, time.Millisecond*time.Duration(timeout))
		defer cancelTimeout()

		measurement, measureErr := urltest.Measure(ctx, urltest.MeasureOptions{
			Link:           url,
			ExpectedStatus: expected,
		}, proxy)

		// This records the outcome of THIS request and nothing else.
		//
		// It previously walked every URLTest group containing this node and forced an immediate
		// re-selection. That coupled a manual probe of one node against one target to the
		// selection of unrelated groups: a measurement taken for a diagnostic could move live
		// traffic, and a group measuring a different target would re-select from a result that
		// was never about its target. A group maintains its own selection from its own periodic
		// checks, and when this probe happens to use the same target, the scoped result is simply
		// available to the group's next check.
		// The leaf was resolved before the measurement, so its tag is the node that was measured.
		realTag := proxy.Tag()
		if measureErr == nil {
			// DISPLAY ONLY.
			//
			// A manual probe asks "how fast is this node against the URL I typed". It is not
			// measured against any group's target, so it must not become selection evidence - and
			// writing it into the health map would let a user grow that map without bound by
			// testing arbitrary URLs.
			//
			// A failure records nothing at all. Failing this URL does not disprove a previous
			// success against another one, and the request already reports the failure.
			server.urlTestHistory.StoreDisplayHistory(realTag, measurement.Scope, &adapter.URLTestHistory{
				Time:  time.Now(),
				Delay: measurement.Delay,
			})
		}
		delay, err := measurement.Delay, measureErr

		if ctx.Err() != nil {
			render.Status(r, http.StatusGatewayTimeout)
			render.JSON(w, r, ErrRequestTimeout)
			return
		}

		if err != nil || delay == 0 {
			render.Status(r, http.StatusServiceUnavailable)
			render.JSON(w, r, newError("An error occurred in the delay test"))
			return
		}

		render.JSON(w, r, render.M{
			"delay": delay,
		})
	}
}
