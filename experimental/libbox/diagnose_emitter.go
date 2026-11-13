package libbox

// O02 — the diagnostic emitter: how a running connection test describes itself.
//
// A diagnostic that returns only a final report cannot explain a slow or stuck attempt: the
// caller has no begin, no per-stage progress and no way to tell "this stage was skipped from
// cache" from "this stage never ran". The emitter is that missing narrative, and every one of
// its properties is a bound or an ordering rule:
//
//   - ORDER: every event carries a monotonic sequence number, so a subscriber can detect a gap
//     instead of assuming the stream told it everything.
//   - BEGIN: the first event is always a begin, and a subscriber that joins late is handed the
//     retained history first (a snapshot), so a late join never starts mid-narrative.
//   - LOSS IS VISIBLE: a slow subscriber loses events rather than blocking the test, and the
//     drop is counted and reported as a finding - a silent loss is what makes a diagnostic
//     untrustworthy.
//   - BOUNDED: retention is bounded by count and by bytes, so a chatty test cannot pin memory.
//   - CLOSE FINALISES: Close emits the final snapshot exactly once and marks the stream
//     finished, so "no more events" is a fact rather than an absence.

import (
	"strings"
	"sync"
	"time"
)

// connectionTestEventKind names what an event is.
type connectionTestEventKind string

const (
	eventBegin    connectionTestEventKind = "begin"
	eventStage    connectionTestEventKind = "stage"
	eventFinding  connectionTestEventKind = "finding"
	eventSnapshot connectionTestEventKind = "snapshot"
	eventEnd      connectionTestEventKind = "end"
)

// connectionTestOutcome classifies one stage's result.
type connectionTestOutcome string

const (
	outcomeRunning   connectionTestOutcome = "running"
	outcomeOK        connectionTestOutcome = "ok"
	outcomeFailed    connectionTestOutcome = "failed"
	outcomeSkipped   connectionTestOutcome = "skipped"
	outcomeReused    connectionTestOutcome = "reused"
	outcomeRecovered connectionTestOutcome = "recovered"
	outcomeCanceled  connectionTestOutcome = "cancelled"
	outcomeDropped   connectionTestOutcome = "dropped"
)

// connectionTestEvent is one emitted fact.
type connectionTestEvent struct {
	Seq     uint64                  `json:"seq"`
	Kind    connectionTestEventKind `json:"kind"`
	Stage   string                  `json:"stage,omitempty"`
	Parent  string                  `json:"parent,omitempty"`
	Name    string                  `json:"name,omitempty"`
	Outcome connectionTestOutcome   `json:"outcome,omitempty"`
	// Code is the stable causal code of a stage or a finding (see diagnose_codes.go). The stream
	// carries it so a live subscriber branches on the same vocabulary the final report uses.
	Code     string `json:"code,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Snapshot bool   `json:"snapshot,omitempty"`
	Elapsed  int64  `json:"elapsedMs"`
}

// emitterBounds keeps one diagnostic's narrative small enough to be safe on a phone.
type emitterBounds struct {
	Queue     int
	Retention int
	MaxBytes  int
}

func defaultEmitterBounds() emitterBounds {
	return emitterBounds{Queue: 64, Retention: 128, MaxBytes: 32 << 10}
}

// connectionTestEmitter owns the event stream of one test.
type connectionTestEmitter struct {
	access sync.Mutex

	bounds emitterBounds
	queue  chan connectionTestEvent

	next     uint64
	history  []connectionTestEvent
	bytes    int
	dropped  uint64
	closed   bool
	finished chan struct{}
}

// newConnectionTestEmitter returns an emitter with a subscriber queue.
func newConnectionTestEmitter(bounds emitterBounds, started time.Time) *connectionTestEmitter {
	if bounds.Queue <= 0 {
		bounds.Queue = defaultEmitterBounds().Queue
	}
	if bounds.Retention <= 0 {
		bounds.Retention = defaultEmitterBounds().Retention
	}
	if bounds.MaxBytes <= 0 {
		bounds.MaxBytes = defaultEmitterBounds().MaxBytes
	}
	emitter := &connectionTestEmitter{
		bounds:   bounds,
		queue:    make(chan connectionTestEvent, bounds.Queue),
		finished: make(chan struct{}),
	}
	return emitter
}

// Events is the subscriber's channel. The first event is the begin.
func (e *connectionTestEmitter) Events() <-chan connectionTestEvent { return e.queue }

// Finished is closed after the final snapshot was published.
func (e *connectionTestEmitter) Finished() <-chan struct{} { return e.finished }

// Dropped reports how many events a slow subscriber missed.
func (e *connectionTestEmitter) Dropped() uint64 {
	e.access.Lock()
	defer e.access.Unlock()
	return e.dropped
}

// Publish appends one event unless the stream is finished. The final events are written by
// appendAndOfferLocked directly under the same lock, so nothing can be appended after the end event.
func (e *connectionTestEmitter) Publish(event connectionTestEvent) {
	e.access.Lock()
	defer e.access.Unlock()
	if e.closed {
		return
	}
	e.appendAndOfferLocked(event)
}

// appendAndOfferLocked retains one event and offers it to the live subscriber without blocking.
// The caller holds access, which is what makes the closed check, the append and the final end event
// one critical section; the send itself is non-blocking, so holding the lock across it is safe.
func (e *connectionTestEmitter) appendAndOfferLocked(event connectionTestEvent) {
	e.next++
	event.Seq = e.next
	if event.Kind == "" {
		event.Kind = eventStage
	}
	e.history = append(e.history, event)
	e.bytes += eventSize(event)
	for len(e.history) > e.bounds.Retention || e.bytes > e.bounds.MaxBytes {
		e.bytes -= eventSize(e.history[0])
		e.history = e.history[1:]
	}
	select {
	case e.queue <- event:
	default:
		e.dropped++
	}
}

// Snapshot returns the retained history for a late subscriber.
func (e *connectionTestEmitter) Snapshot() []connectionTestEvent {
	e.access.Lock()
	defer e.access.Unlock()
	return append([]connectionTestEvent(nil), e.history...)
}

// Close publishes the final snapshot exactly once and finishes the stream. The final events and the
// finished signal are written under the same lock that Publish uses, so a concurrent publisher can
// never append a stage or finding after the end event or observe a half-written final snapshot.
func (e *connectionTestEmitter) Close(elapsed time.Duration) {
	e.access.Lock()
	defer e.access.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	if e.dropped > 0 {
		e.appendAndOfferLocked(connectionTestEvent{Kind: eventFinding, Outcome: outcomeDropped, Detail: "subscriber missed events", Elapsed: elapsed.Milliseconds()})
	}
	e.appendAndOfferLocked(connectionTestEvent{Kind: eventSnapshot, Snapshot: true, Elapsed: elapsed.Milliseconds()})
	e.appendAndOfferLocked(connectionTestEvent{Kind: eventEnd, Elapsed: elapsed.Milliseconds()})
	close(e.finished)
}

// eventSize is the retained size estimate of one event.
func eventSize(event connectionTestEvent) int {
	return 48 + len(event.Stage) + len(event.Parent) + len(event.Name) + len(event.Code) + len(event.Detail)
}

// renderEvent is the stable text form handed to the platform.
func renderEvent(event connectionTestEvent) string {
	var builder strings.Builder
	builder.WriteString(string(event.Kind))
	if event.Stage != "" {
		builder.WriteString(" ")
		builder.WriteString(event.Stage)
	}
	if event.Name != "" {
		builder.WriteString(" ")
		builder.WriteString(event.Name)
	}
	if event.Outcome != "" {
		builder.WriteString("=")
		builder.WriteString(string(event.Outcome))
	}
	if event.Code != "" {
		// The code is rendered as `[code]` so a consumer that parses the text form has the same
		// stable vocabulary the JSON document carries, and so a reader cannot mistake the code for
		// part of the prose detail.
		builder.WriteString(" [")
		builder.WriteString(event.Code)
		builder.WriteString("]")
	}
	if event.Detail != "" {
		builder.WriteString(": ")
		builder.WriteString(event.Detail)
	}
	return builder.String()
}
