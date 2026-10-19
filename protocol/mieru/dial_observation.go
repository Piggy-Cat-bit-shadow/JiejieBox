package mieru

import (
	"context"
	"sync"
)

// dialObservation is the typed evidence the fork's dialer produced during ONE SDK dial.
//
// # Why this has to be captured here and not read from the returned error
//
// The official SDK wraps every underlay failure in a plain fmt error before this package sees it:
//
//	pkg/protocol/mux.go  fmt.Errorf("NewTCPUnderlay() failed: %v", err)
//	pkg/protocol/mux.go  fmt.Errorf("NewUDPUnderlay() failed: %v", err)
//
// `%v`, not `%w`. NewStreamUnderlay itself wraps correctly ("DialContext() failed: %w"), so the
// chain survives until the mux rebuilds it as a string - and then it is gone. What is left is text,
// and this fork has already paid for trusting text: Windows formats a socket error in the system
// language, so "connect: connection refused" is not a stable string there. The SDK's own English
// prefix still says "an underlay failed", which is why the fallback itself kept working; what the
// text can never say is *which kind* of socket failure it was, and therefore whether it is proof.
// The tree-wide answer for that question is common/dialer's errno sets (ErroredWith), which match
// both the POSIX and the raw Winsock spelling; the only way to use them after the SDK has flattened
// the error is to record the error while it still has its type.
//
// # Cost and ownership
//
// One observation per SDK dial, carried by a context wrapper (one allocation; no goroutine, no
// timer, no lock on the success path beyond the record of the successful dial). It is scoped to the
// dial: transportRuntime.dial installs it, the SDK passes the same context down to this package's
// dialer, and the ledger is read once when the dial has returned. Nothing else ever sees it, so it
// cannot become a second health state.
type dialObservation struct {
	access sync.Mutex
	// socketErr is the LAST error the underlying dialer produced, and it is cleared by a later
	// success. That ordering matters: a failed IPv6 attempt followed by a successful IPv4 attempt
	// and then a handshake timeout must not be reported as a definite IPv6 socket failure.
	socketErr error
}

type dialObservationKey struct{}

// dialObservationContext carries the ledger without a second allocation or a value chain.
type dialObservationContext struct {
	context.Context
	observation dialObservation
}

func (c *dialObservationContext) Value(key any) any {
	if key == (dialObservationKey{}) {
		return &c.observation
	}
	return c.Context.Value(key)
}

// withDialObservation installs the ledger for one SDK dial. A nil context is tolerated because the
// helper is also called from tests that pass one.
func withDialObservation(ctx context.Context) (context.Context, *dialObservation) {
	if ctx == nil {
		ctx = context.Background()
	}
	observationContext := &dialObservationContext{Context: ctx}
	return observationContext, &observationContext.observation
}

func dialObservationFrom(ctx context.Context) *dialObservation {
	if ctx == nil {
		return nil
	}
	observation, _ := ctx.Value(dialObservationKey{}).(*dialObservation)
	return observation
}

// observeSocketDial is called by the serverDialer for every socket it creates. A nil error records
// a success; the SDK may dial several endpoints (IPv4 then IPv6, or two underlays) and only the
// last event describes the state the final error belongs to.
func observeSocketDial(ctx context.Context, err error) {
	observation := dialObservationFrom(ctx)
	if observation == nil {
		return
	}
	observation.access.Lock()
	if err == nil {
		observation.socketErr = nil
	} else {
		observation.socketErr = err
	}
	observation.access.Unlock()
}

// socketFailure returns the last socket-level error of this attempt, or nil when the last socket
// event was a success.
func (o *dialObservation) socketFailure() error {
	if o == nil {
		return nil
	}
	o.access.Lock()
	defer o.access.Unlock()
	return o.socketErr
}
