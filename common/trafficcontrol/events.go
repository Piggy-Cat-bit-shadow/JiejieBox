package trafficcontrol

// The event fanout: every subscription gets its own bounded queue, and every drop is counted.
//
// # Why this is not observable.Observer anymore
//
// The upstream Observer fans out through observable.Subscriber.Emit, which drops silently when a
// listener's buffer is full. A slow subscription therefore lost events and could not count them: the
// daemon could only notice a loss when a LATER event arrived, because that event's sequence revealed
// the gap. After a silent tail loss - the reviewed case is 100 connections created and closed while
// the stream was blocked - no later event may come for minutes or hours, and a missing CLOSED row stays
// wrong forever.
//
// This fanout keeps the same contract the data plane needs (never block the publisher: a full queue
// drops) and adds the one fact that was missing: a per-subscription drop counter. The daemon can then
// publish "you lost N events" during silence, which is what makes recovery independent of business
// traffic.
//
// # One loss point, exactly
//
// The old path had two: the source Subscriber (256) fed an Observer goroutine which fanned out into
// per-listener Subscribers (64). An event could be dropped before fan-out (attributed to nobody) or
// after (attributed to one listener). Here publish() hands the event to every listener directly, under
// the manager's publish lock; each listener either queues it or increments its own counter. There is no
// intermediate buffer, so "source sequence advanced past what this subscription delivered" and "this
// subscription dropped" are the same number, and one subscriber's loss is never counted for another.

import (
	"os"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing/common/observable"
)

// eventListenerBuffer is the per-subscription queue size. It is the only buffer in the path.
const eventListenerBuffer = 64

type eventListener struct {
	channel   chan ConnectionEvent
	done      chan struct{}
	dropped   atomic.Uint64
	closeOnce sync.Once
}

// emit queues one event, or counts it as dropped. It never blocks.
func (l *eventListener) emit(event ConnectionEvent) {
	select {
	case <-l.done:
		return
	default:
	}
	select {
	case l.channel <- event:
	default:
		l.dropped.Add(1)
	}
}

func (l *eventListener) close() {
	l.closeOnce.Do(func() {
		close(l.done)
	})
}

// Dropped returns how many events this subscription never received.
func (l *eventListener) Dropped() uint64 {
	return l.dropped.Load()
}

type eventFanout struct {
	access    sync.Mutex
	listeners map[*eventListener]struct{}
	byChannel map[observable.Subscription[ConnectionEvent]]*eventListener
	closed    bool
}

func newEventFanout() *eventFanout {
	return &eventFanout{
		listeners: make(map[*eventListener]struct{}),
		byChannel: make(map[observable.Subscription[ConnectionEvent]]*eventListener),
	}
}

// publish hands one event to every subscription. Caller must have assigned the sequence already; the
// fanout preserves that order because publish is called under the manager's publish lock.
func (f *eventFanout) publish(event ConnectionEvent) {
	f.access.Lock()
	if f.closed {
		f.access.Unlock()
		return
	}
	for listener := range f.listeners {
		listener.emit(event)
	}
	f.access.Unlock()
}

func (f *eventFanout) subscribe() (*eventListener, error) {
	f.access.Lock()
	defer f.access.Unlock()
	if f.closed {
		return nil, os.ErrClosed
	}
	listener := &eventListener{
		channel: make(chan ConnectionEvent, eventListenerBuffer),
		done:    make(chan struct{}),
	}
	f.listeners[listener] = struct{}{}
	f.byChannel[observable.Subscription[ConnectionEvent](listener.channel)] = listener
	return listener, nil
}

func (f *eventFanout) unsubscribe(subscription observable.Subscription[ConnectionEvent]) {
	f.access.Lock()
	listener, loaded := f.byChannel[subscription]
	if loaded {
		delete(f.byChannel, subscription)
		delete(f.listeners, listener)
	}
	f.access.Unlock()
	if loaded {
		listener.close()
	}
}

func (f *eventFanout) close() error {
	f.access.Lock()
	if f.closed {
		f.access.Unlock()
		return os.ErrClosed
	}
	f.closed = true
	listeners := make([]*eventListener, 0, len(f.listeners))
	for listener := range f.listeners {
		listeners = append(listeners, listener)
	}
	f.listeners = nil
	f.byChannel = nil
	f.access.Unlock()
	for _, listener := range listeners {
		listener.close()
	}
	return nil
}
