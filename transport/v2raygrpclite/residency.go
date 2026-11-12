package v2raygrpclite

// The transport's stream-identity ledger (audit Q15). See transport/v2rayhttp/residency.go for the
// same boundary: the HTTP/2 pool is the library's, the streams this wrapper opens are ours to count.

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
		Note: "gRPC streams opened through this transport; the library's pooled HTTP/2 " +
			"connections are a different owner and are not counted here",
	}
}
