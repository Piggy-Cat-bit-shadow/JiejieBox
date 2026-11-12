package dns

// O03 — attributable query events.
//
// The DNS client had no per-query observation surface: a caller could see an answer or an
// error, but not which transport produced it, whether it came from cache, or what the
// outcome was when the answer was empty. Those facts are what makes a diagnosis possible.
//
// Three properties are deliberate:
//
//   - NOTHING is assembled when nobody is subscribed. The gate is an atomic check before the
//     event value is built, so a process with no subscriber pays one branch per query.
//   - ANSWERS ARE OPT-IN. A subscriber states whether it wants answer records; when it does,
//     they are rendered and capped, because a diagnostic must not grow without bound or
//     disclose records nobody asked for.
//   - LOSS IS OBSERVABLE. A slow subscriber loses events instead of blocking a query, and
//     the bus counts what it dropped, so a missing event is a fact rather than a mystery.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	R "github.com/sagernet/sing-box/route/rule"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/miekg/dns"
)

// QuerySource names where an answer came from. It is the attribution the event exists for:
// "error" alone cannot tell a transport failure from a cache rejection or a local answer.
type QuerySource string

const (
	QuerySourceUpstream      QuerySource = "upstream"
	QuerySourceCache         QuerySource = "cache"
	QuerySourceCacheStale    QuerySource = "cache-stale"
	QuerySourceCacheNXDomain QuerySource = "cache-nxdomain"
	QuerySourceLocal         QuerySource = "local"
	QuerySourceBlocked       QuerySource = "blocked"
	// QuerySourceRejected is a rule action that rejected the query: a REFUSED answer or a drop.
	// It is distinct from "blocked" (adblock), because the operator wrote a different decision and
	// one answers with a code while the other produces no response at all.
	QuerySourceRejected QuerySource = "rejected"
	// QuerySourceCanceled is the caller going away or its deadline expiring before any verdict.
	// It is not an upstream failure: nothing was learned about the server.
	QuerySourceCanceled QuerySource = "canceled"
)

const (
	defaultEventQueue = 16
	// maxAnswerRecords bounds how many records one subscriber may ask for, so "opt in to
	// answers" cannot become "opt in to unbounded memory".
	maxAnswerRecords = 8
	// maxAnswerRecordLength bounds one rendered record.
	maxAnswerRecordLength = 256
)

// QueryEvent is one DNS query's attributable facts.
type QueryEvent struct {
	Time         time.Time   `json:"time"`
	TransportTag string      `json:"transportTag"`
	Name         string      `json:"name"`
	QType        uint16      `json:"qType"`
	Source       QuerySource `json:"source"`
	// RCode is the response code, or -1 when no response was produced.
	RCode int `json:"rcode"`
	// Shared reports that this caller did not ask upstream itself: its answer came from an exchange
	// another caller was already performing for the same question.
	//
	// It is separate from Source on purpose. A waiting caller usually reports Source=cache, because
	// what it finally read is the entry the other caller stored - but "I joined a query that was
	// already in flight" and "this answer was in the cache before I asked" are different facts, and
	// only the second one means the resolver had already answered this question.
	Shared bool `json:"shared,omitempty"`
	// Err is the failure text when the query failed, and empty otherwise.
	Err        string   `json:"err,omitempty"`
	DurationMs int64    `json:"durationMs"`
	Answers    []string `json:"answers,omitempty"`
	// Final marks the query's TERMINAL verdict, reached without any upstream exchange: a rule
	// rejected or answered it locally, the entry policy suppressed it, or the caller cancelled.
	//
	// These are the terminations the client cannot report, because they never reach it. An event
	// without Final is a CANDIDATE ATTEMPT reported by the client (an upstream query, a cache hit,
	// a joined exchange); a query may have several of those, and the router emits no final event
	// for it, so one termination is never counted twice.
	Final bool `json:"final,omitempty"`
	// Rule describes the rule that produced a Final verdict, empty when no rule was involved
	// (an entry policy decision, a structural early answer, a cancellation).
	Rule string `json:"rule,omitempty"`
	// DialPath is the execution-time projection of WHERE this exchange actually went.
	//
	// TransportTag names the transport that ran the exchange, which is not the same fact as the
	// outbound that carried its connection: a transport dials through a detour, the detour may be a
	// group, and the group picks its member at the instant of the dial. This field records what the
	// dial did, so a later selector move cannot re-describe an answer that has already been given.
	//
	// It is nil only when no projection was attempted (a Final verdict reached without any
	// exchange, which the router reports without opening a record).
	DialPath *DialPathView `json:"dialPath,omitempty"`
}

// DialPathView is the bounded, serializable projection of one exchange's dial record.
//
// # What each field is, and what it is NOT
//
//   - Steps is the chain of objects the dial was handed to, outermost first: the transport's
//     detour, then each outbound group's chosen member. A failover retry appears as a further step,
//     because the connection WAS handed to that member; Leaf is the one that carried it.
//   - Leaf is the last step: the outbound that actually carried the connection.
//   - Revision is the selection revision of the group that chose Leaf, and RevisionKnown says
//     whether that group reports a revision at all. Zero with RevisionKnown false is "unknown", not
//     "revision zero".
//   - TransportMember is the member transport of a DNS transport group that ran the exchange. It is
//     a separate dimension from the outbound chain: the group chooses a TRANSPORT, which then dials
//     through its own outbound path.
//   - Reason explains why the projection is incomplete, using a fixed vocabulary (adapter's
//     DialPathReason). It is empty when everything the event can carry was observed.
//
// # Why the leaf cannot be read out of current state instead
//
// Reading the selector when the event is consumed would report the member of the moment the reader
// looked, not of the moment the query ran. That is the exact mis-attribution (an answer credited to
// a node that never carried it) this projection exists to prevent.
type DialPathView struct {
	Steps           []string `json:"steps,omitempty"`
	Leaf            string   `json:"leaf,omitempty"`
	Revision        uint64   `json:"revision,omitempty"`
	RevisionKnown   bool     `json:"revisionKnown,omitempty"`
	TransportMember string   `json:"transportMember,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	Truncated       bool     `json:"truncated,omitempty"`
}

// eventSubscriber is one subscriber's delivery state.
type eventSubscriber struct {
	includeAnswers bool
	maxAnswers     int
	channel        chan QueryEvent
}

// EventBus delivers QueryEvents to subscribers without being able to block a query.
//
// It owns no goroutine: delivery is a non-blocking send from the querying goroutine, and a
// full subscriber queue drops the event and increments Dropped.
type EventBus struct {
	access  sync.Mutex
	nextID  uint64
	subs    map[uint64]*eventSubscriber
	queue   int
	active  atomic.Int32
	dropped atomic.Uint64
}

// NewEventBus returns a bus whose subscriber queues hold queueSize events. A non-positive
// size falls back to a small default: a zero queue would drop everything.
func NewEventBus(queueSize int) *EventBus {
	if queueSize <= 0 {
		queueSize = defaultEventQueue
	}
	return &EventBus{
		subs:  make(map[uint64]*eventSubscriber),
		queue: queueSize,
	}
}

// Subscribe registers a subscriber and returns its channel plus the function that removes it.
//
// The channel is NOT closed on unsubscribe: a racing emit may still hold the channel, and
// closing it would turn that send into a panic. A reader stops by cancelling its own context.
func (b *EventBus) Subscribe(includeAnswers bool, maxAnswers int) (<-chan QueryEvent, func()) {
	if b == nil {
		return nil, func() {}
	}
	if maxAnswers <= 0 || maxAnswers > maxAnswerRecords {
		maxAnswers = 1
	}
	subscriber := &eventSubscriber{
		includeAnswers: includeAnswers,
		maxAnswers:     maxAnswers,
		channel:        make(chan QueryEvent, b.queue),
	}
	b.access.Lock()
	b.nextID++
	id := b.nextID
	b.subs[id] = subscriber
	b.access.Unlock()
	b.active.Add(1)
	return subscriber.channel, func() {
		b.access.Lock()
		if _, loaded := b.subs[id]; loaded {
			delete(b.subs, id)
			b.active.Add(-1)
		}
		b.access.Unlock()
	}
}

// Active reports whether any subscriber is registered. It is the gate the query path checks
// before building an event.
func (b *EventBus) Active() bool {
	return b != nil && b.active.Load() > 0
}

// AnswerBudget reports the largest answer count any active subscriber asked for, or false
// when none of them wants answers at all.
func (b *EventBus) AnswerBudget() (int, bool) {
	if b == nil {
		return 0, false
	}
	b.access.Lock()
	defer b.access.Unlock()
	budget := 0
	for _, subscriber := range b.subs {
		if subscriber.includeAnswers && subscriber.maxAnswers > budget {
			budget = subscriber.maxAnswers
		}
	}
	return budget, budget > 0
}

// Dropped reports how many deliveries were dropped because a subscriber's queue was full.
func (b *EventBus) Dropped() uint64 {
	if b == nil {
		return 0
	}
	return b.dropped.Load()
}

// emit delivers one event to every subscriber, non-blockingly.
func (b *EventBus) emit(event QueryEvent) {
	if b == nil {
		return
	}
	b.access.Lock()
	if len(b.subs) == 0 {
		b.access.Unlock()
		return
	}
	subscribers := make([]*eventSubscriber, 0, len(b.subs))
	for _, subscriber := range b.subs {
		subscribers = append(subscribers, subscriber)
	}
	b.access.Unlock()
	for _, subscriber := range subscribers {
		delivered := event
		if !subscriber.includeAnswers {
			// The event was assembled with answers because ANOTHER subscriber asked for
			// them; this one did not, so it must not receive them.
			delivered.Answers = nil
		} else if len(delivered.Answers) > subscriber.maxAnswers {
			delivered.Answers = delivered.Answers[:subscriber.maxAnswers]
		}
		select {
		case subscriber.channel <- delivered:
		default:
			b.dropped.Add(1)
		}
	}
}

// emitQueryEvent assembles and delivers one event. It returns immediately when no
// subscriber is registered, which is the gate that keeps the query path allocation-free for
// a process nobody is diagnosing.
func (c *Client) emitQueryEvent(started time.Time, transport adapter.DNSTransport, message *dns.Msg, source QuerySource, shared bool, response *dns.Msg, err error, dialPath *adapter.DialPath) {
	bus := c.eventBus
	if bus == nil || !bus.Active() {
		return
	}
	event := QueryEvent{
		Time:       started,
		Source:     source,
		Shared:     shared,
		RCode:      -1,
		DurationMs: time.Since(started).Milliseconds(),
	}
	if transport != nil {
		event.TransportTag = transport.Tag()
	}
	event.DialPath = buildDialPathView(dialPath)
	if message != nil && len(message.Question) > 0 {
		event.Name = message.Question[0].Name
		event.QType = message.Question[0].Qtype
	}
	if response != nil {
		event.RCode = response.Rcode
		if limit, wanted := bus.AnswerBudget(); wanted {
			event.Answers = renderAnswers(response, limit)
		}
	}
	if err != nil {
		event.Err = E.New(err).Error()
	}
	bus.emit(event)
}

// buildDialPathView projects one exchange's dial record onto the event document.
//
// # Why a fan-out suppresses the steps
//
// A DNS transport group that split the query across members ran several dials CONCURRENTLY into the
// same record. The steps would then be whichever member's dial wrote last, which is not the path
// that answered - it is an accident of scheduling. Naming one of them would be worse than naming
// none, so the record reports why it cannot name a single member instead.
func buildDialPathView(dialPath *adapter.DialPath) *DialPathView {
	if dialPath == nil {
		return nil
	}
	result := dialPath.Result()
	view := &DialPathView{
		TransportMember: result.TransportMember,
		Reason:          string(result.Reason()),
		Truncated:       result.Truncated,
	}
	if result.TransportFanOut {
		view.Reason = string(adapter.DialPathReasonTransportFanOut)
		return view
	}
	if len(result.Steps) > 0 {
		view.Steps = make([]string, 0, len(result.Steps))
		for _, step := range result.Steps {
			view.Steps = append(view.Steps, step.Tag)
		}
		leaf, _ := result.Leaf()
		view.Leaf = leaf.Tag
		view.Revision = leaf.Revision
		view.RevisionKnown = leaf.HasRevision
	}
	return view
}

// emitFinalQueryEvent records the query's terminal verdict when it was reached WITHOUT an upstream
// exchange.
//
// # What it exists for
//
// The client emits an event per candidate attempt: an upstream query, a cache hit, a joined
// exchange. A rule that rejects or answers a query locally never reaches the client, so those
// terminations used to be invisible in both the event stream and the bounded history - the query
// came back REFUSED and the history was empty. This is the missing half of the contract, emitted at
// the point the exchange actually terminates.
//
// # Why it cannot double count
//
// It is only called where transport == nil, which is exactly "no client exchange ran for this
// query". A query that did reach a transport has its attempt events and no final event, so one
// termination is recorded once.
func (r *Router) emitFinalQueryEvent(started time.Time, message *dns.Msg, source QuerySource, rule adapter.DNSRule, response *dns.Msg, err error) {
	bus := r.events
	if bus == nil || !bus.Active() {
		return
	}
	if message == nil || len(message.Question) == 0 {
		return
	}
	event := QueryEvent{
		Time:       started,
		Source:     source,
		RCode:      -1,
		Final:      true,
		DurationMs: time.Since(started).Milliseconds(),
	}
	if rule != nil {
		event.Rule = rule.String()
	}
	event.Name = message.Question[0].Name
	event.QType = message.Question[0].Qtype
	if response != nil {
		event.RCode = response.Rcode
		if limit, wanted := bus.AnswerBudget(); wanted {
			event.Answers = renderAnswers(response, limit)
		}
	}
	if err != nil {
		event.Err = E.New(err).Error()
	}
	bus.emit(event)
}

// localVerdictSource classifies a terminal verdict reached without a client exchange.
//
// The order matters: a rejected query is reported as rejected even though a rule produced it, and
// an adblock answer keeps its own source because "the operator blocked this name" and "the rules
// rejected this query" are different decisions with different responses.
func localVerdictSource(result exchangeWithRulesResult) QuerySource {
	if result.rejectAction != nil {
		return QuerySourceRejected
	}
	if result.err != nil {
		if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
			return QuerySourceCanceled
		}
	}
	if result.rule != nil {
		switch result.rule.Action().(type) {
		case *R.RuleActionAdblock:
			return QuerySourceBlocked
		}
	}
	return QuerySourceLocal
}

// legacyLocalVerdictSource classifies a local verdict from the legacy exchange.
//
// The legacy path does not carry the deciding rule, so the classification uses what the termination
// looks like: a drop or a REFUSED answer is a reject action, a cancellation is the caller leaving,
// and anything else that terminated without a transport was answered locally.
func legacyLocalVerdictSource(response *dns.Msg, err error) QuerySource {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return QuerySourceCanceled
		}
		return QuerySourceRejected
	}
	if response != nil && response.Rcode == dns.RcodeRefused {
		return QuerySourceRejected
	}
	return QuerySourceLocal
}

// renderAnswers renders up to limit answer records in a stable, bounded form.
func renderAnswers(response *dns.Msg, limit int) []string {
	if limit <= 0 || len(response.Answer) == 0 {
		return nil
	}
	limit = min(limit, len(response.Answer), maxAnswerRecords)
	rendered := make([]string, 0, limit)
	for _, record := range response.Answer[:limit] {
		header := record.Header()
		var value string
		switch typed := record.(type) {
		case *dns.A:
			value = typed.A.String()
		case *dns.AAAA:
			value = typed.AAAA.String()
		case *dns.CNAME:
			value = typed.Target
		case *dns.NS:
			value = typed.Ns
		case *dns.PTR:
			value = typed.Ptr
		case *dns.MX:
			value = typed.Mx
		case *dns.TXT:
			value = strings.Join(typed.Txt, " ")
		default:
			value = record.String()
		}
		rendered = append(rendered, renderAnswerRecord(header.Name, header.Rrtype, header.Ttl, value))
	}
	return rendered
}

// renderAnswerRecord renders one answer in a stable, bounded form for a diagnostic.
func renderAnswerRecord(owner string, recordType uint16, ttl uint32, value string) string {
	var builder strings.Builder
	builder.WriteString(owner)
	builder.WriteByte(' ')
	builder.WriteString(strconv.Itoa(int(recordType)))
	builder.WriteByte(' ')
	builder.WriteString(strconv.FormatUint(uint64(ttl), 10))
	builder.WriteByte(' ')
	builder.WriteString(value)
	return builder.String()
}
