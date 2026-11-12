package v2raygrpclite

// The transport's stream-identity ledger (audit Q15). Same boundary as v2rayhttp: the library's
// pooled HTTP/2 connections are its owner; this wrapper counts the gRPC streams it opens and hooks
// the conn's own onClose instead of wrapping it, so optional interfaces stay visible.

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
	gunConn, isGunConn := conn.(*GunConn)
	if !isGunConn {
		return
	}
	var released atomic.Bool
	previous := gunConn.onClose
	c.ledger.active.Add(1)
	gunConn.onClose = func() {
		if previous != nil {
			previous()
		}
		if released.CompareAndSwap(false, true) {
			c.ledger.active.Add(-1)
			c.ledger.terminated.Add(1)
		}
	}
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
