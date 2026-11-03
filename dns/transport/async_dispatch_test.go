package transport

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	mDNS "github.com/miekg/dns"
)

// TestExchangeAsyncDoesNotBlockOnConnectionSetup is the regression for upstream
// 04b42a81231 "Fix DNS connection setup blocking TUN packet processing".
//
// The asynchronous entry point is reached from the packet-tunnel DNS hijack path, and
// exchangeSingle ACQUIRES a connection from the serial pool - acquiring may dial. Running that
// inline means the first query to a cold DNS server blocks packet processing for as long as the
// TCP (and possibly TLS) setup takes.
//
// This fork has no separate async transport path to fix (the pipelining probe was removed), so
// the property is asserted at the funnel instead: ExchangeAsync must return while the dial is
// still blocked, and the dial must be running on another goroutine.
//
// The caller's context is deliberately NOT cancelled by the caller: the transport's async
// contract has always been "the callback owns the result, the context outlives the call", which
// is how dns.Client passes its operation context.
func TestExchangeAsyncDoesNotBlockOnConnectionSetup(t *testing.T) {
	t.Parallel()

	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	multiplexer := newQueryMultiplexer(queryMultiplexerOptions{
		dial: func(ctx context.Context) (net.Conn, error) {
			close(dialStarted)
			<-releaseDial
			// No connection: the callback must still run with the error.
			return nil, io.ErrClosedPipe
		},
		write: func(conn net.Conn, message *mDNS.Msg, queryId uint16) error {
			return nil
		},
		readNext: func(conn net.Conn) (*mDNS.Msg, error) {
			return nil, io.EOF
		},
	})
	defer multiplexer.Close()

	message := new(mDNS.Msg)
	message.SetQuestion("blocking.example.com.", mDNS.TypeA)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	callbackErr := make(chan error, 1)
	returned := make(chan struct{})
	go func() {
		multiplexer.ExchangeAsync(ctx, message, func(response *mDNS.Msg, err error) {
			callbackErr <- err
		})
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("ExchangeAsync did not return while a connection setup was in progress; " +
			"the caller's goroutine is being used for the dial")
	}

	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		t.Fatal("the dial never started")
	}

	close(releaseDial)
	select {
	case err := <-callbackErr:
		if err == nil {
			t.Fatal("expected the dial failure to reach the callback")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the callback never ran after the dial failed")
	}
}
