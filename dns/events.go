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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
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
	// Err is the failure text when the query failed, and empty otherwise.
	Err        string   `json:"err,omitempty"`
	DurationMs int64    `json:"durationMs"`
	Answers    []string `json:"answers,omitempty"`
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
func (c *Client) emitQueryEvent(started time.Time, transport adapter.DNSTransport, message *dns.Msg, source QuerySource, response *dns.Msg, err error) {
	bus := c.eventBus
	if bus == nil || !bus.Active() {
		return
	}
	event := QueryEvent{
		Time:       started,
		Source:     source,
		RCode:      -1,
		DurationMs: time.Since(started).Milliseconds(),
	}
	if transport != nil {
		event.TransportTag = transport.Tag()
	}
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
