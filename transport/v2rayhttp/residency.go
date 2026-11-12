package v2rayhttp

// The transport's stream-identity ledger (audit Q15).
//
// The HTTP/2 library owns its connection pool and does not expose it; what this wrapper CAN count is
// its own streams: one object per DialContext call that returned a conn. Creating is the window
// before the call returns, Active is an open stream, Terminated is a stream whose Close body ran.
// The ledger hooks the conn's own onClose; it never wraps the conn, so ReaderReplaceable,
// WriterReplaceable, NeedHandshake, Upstream and syscall.Conn stay visible to the caller.
//
// Exact is false because the object is a stream, not a pool connection, and the Note names the
// library's pool as a different owner.

import (
	"net"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
)

type clientLedger struct {
	creating   atomic.Int64
	active     atomic.Int64
	terminated atomic.Uint64
}

func (c *Client) attachLedger(conn net.Conn) {
	if c == nil || conn == nil {
		return
	}
	var released atomic.Bool
	release := func() {
		if released.CompareAndSwap(false, true) {
			c.ledger.active.Add(-1)
			c.ledger.terminated.Add(1)
		}
	}
	c.ledger.active.Add(1)
	switch value := conn.(type) {
	case *HTTP2Conn:
		previous := value.onClose
		value.onClose = func() {
			if previous != nil {
				previous()
			}
			release()
		}
	case *HTTPConn:
		previous := value.onClose
		value.onClose = func() {
			if previous != nil {
				previous()
			}
			release()
		}
	}
}

// ResidencyLedger implements adapter.ResidencyLedgerReporter.
func (c *Client) ResidencyLedger() adapter.ResidencyLedger {
	if c == nil {
		return adapter.ResidencyLedger{}
	}
	return adapter.ResidencyLedger{
		Active:     uint64(max(c.ledger.active.Load(), 0)),
		Creating:   uint64(max(c.ledger.creating.Load(), 0)),
		Terminated: c.ledger.terminated.Load(),
		Exact:      false,
		Answered:   true,
		Note: "HTTP streams opened through this transport; the library's pooled HTTP/2 " +
			"connections are a different owner and are not counted here",
	}
}
