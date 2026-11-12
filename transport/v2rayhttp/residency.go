package v2rayhttp

// The transport's stream-identity ledger (audit Q15).
//
// The HTTP/2 library owns its connection pool and does not expose it; what this wrapper CAN count
// exactly is its own HTTP streams: one object per DialContext call that returned a conn. Creating is
// the window before the call returns, Active is an open stream, Terminated is a stream whose Close
// body ran. The pooled connections are a different owner and are named in the Note rather than
// guessed at, and Exact is false because the object is a stream, not a pool connection.

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

type ledgerConn struct {
	net.Conn
	ledger *clientLedger
	closed atomic.Bool
}

func (c *ledgerConn) Close() error {
	err := c.Conn.Close()
	if c.closed.CompareAndSwap(false, true) {
		c.ledger.active.Add(-1)
		c.ledger.terminated.Add(1)
	}
	return err
}

// ResidencyLedger implements adapter.ResidencyLedgerReporter.
func (c *Client) ResidencyLedger() adapter.ResidencyLedger {
	if c == nil {
		return adapter.ResidencyLedger{}
	}
	ledger := adapter.ResidencyLedger{
		Active:     uint64(max(c.ledger.active.Load(), 0)),
		Creating:   uint64(max(c.ledger.creating.Load(), 0)),
		Terminated: c.ledger.terminated.Load(),
		Exact:      false,
		Answered:   true,
		Note: "HTTP streams opened through this transport; the library's pooled HTTP/2 " +
			"connections are a different owner and are not counted here",
	}
	return ledger
}
