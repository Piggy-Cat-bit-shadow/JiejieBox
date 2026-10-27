// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux && go1.25 && badlinkname

package ktls

import (
	"github.com/sagernet/sing/common/buf"
	N "github.com/sagernet/sing/common/network"
)

func (c *Conn) InitializeReadWaiter(options N.ReadWaitOptions) (needCopy bool) {
	c.readWaitOptions = options
	return false
}

// deferReadErrorAfterData returns the data buffer the waiter has already filled and queues the
// record-layer error for the next call. The slow Read path returns (n, err) in this situation; the
// waiter contract cannot return a buffer with a non-nil error, so the data must go first and the
// error one call later. Releasing the buffer here (the old non-EOF branch) discarded n real bytes.
func (c *Conn) deferReadErrorAfterData(buffer *buf.Buffer, err error) (*buf.Buffer, error) {
	c.pendingReadErr = err
	c.readWaitOptions.PostReturn(buffer)
	return buffer, nil
}

func (c *Conn) WaitReadBuffer() (buffer *buf.Buffer, err error) {
	c.rawConn.In.Lock()
	defer c.rawConn.In.Unlock()
	if c.pendingReadErr != nil {
		err = c.pendingReadErr
		c.pendingReadErr = nil
		return nil, err
	}
	for c.rawConn.Input.Len() == 0 {
		err = c.readRecord()
		if err != nil {
			return
		}
		// Post-handshake messages are drained here, exactly as the reading path in Read does it:
		//
		//	for c.rawConn.Input.Len() == 0 {
		//		if err := c.readRecord(); err != nil { return 0, err }
		//		for c.rawConn.Hand.Len() > 0 {
		//			if err := c.handlePostHandshakeMessage(); err != nil { return 0, err }
		//		}
		//	}
		//
		// readRecord parses a record and, when it is a handshake record, APPENDS it to Hand rather than
		// consuming it (ktls_read.go:163). Something has to drain that buffer, and on this path nothing
		// did: the only other drains are the reading path above, which this function does not go
		// through, and the one inside NewConn, which runs once during setup and never again.
		//
		// What that costs is not limited to a stalled loop. `handlePostHandshakeMessage` is where a TLS
		// 1.3 KeyUpdate is applied, and applying it means calling `resetupRX`, which reinstalls the RX
		// key in the KERNEL. A ReadWaitBuffer caller that never drains Hand therefore never updates the
		// kernel's key, while the peer has already moved to the new one - so the connection stops being
		// able to decrypt, on the fast path only, which is the path a session using the splice takes.
		//
		// The loop is unconditional rather than guarded on Hand.Len() > 0: the condition IS the guard,
		// the same way it is in Read, so this stays a line-for-line mirror of the reference.
		for c.rawConn.Hand.Len() > 0 {
			err = c.handlePostHandshakeMessage()
			if err != nil {
				return
			}
		}
	}
	buffer = c.readWaitOptions.NewBuffer()
	n, err := c.rawConn.Input.Read(buffer.FreeBytes())
	if err != nil {
		buffer.Release()
		return
	}
	buffer.Truncate(n)

	// If a close-notify alert is waiting, read it so this read can return the EOF that follows, exactly
	// as the non-waiting path does in Read. The condition is the one from that path verbatim:
	//
	//	if n != 0 && c.rawConn.Input.Len() == 0 && c.rawConn.RawInput.Len() > 0 &&
	//		c.rawConn.RawInput.Bytes()[0] == recordTypeAlert {
	//
	// This site previously read `c.rawConn.Input.Len() == 0 && c.rawConn.Input.Len() > 0`, which is
	// unsatisfiable, so the branch never ran at all. That made the alert cleanup the slow path's
	// behaviour only: a session whose reads go through WaitReadBuffer - the fast path - would not observe
	// the close-notify with the data that preceded it, which is the race the comment on the slow path
	// describes (a reader that does not see EOF until its next read, by which time the connection may
	// already have been reused for a new request).
	//
	// The same typo also removed the only guard on `RawInput.Bytes()[0]`: with the condition corrected to
	// test RawInput at all, an unguarded index would panic whenever the buffer is empty, because
	// bytes.Buffer.Bytes() returns a zero-length slice. The `RawInput.Len() > 0` term is what makes the
	// index safe, and it is load-bearing rather than incidental.
	//
	// And the call is `c.readRecord`, not `c.rawConn.ReadRecord`: this type wraps the record layer so that
	// a record is decrypted into `rawConn.Input`, which is what the check above is about, whereas the
	// badtls dispatch reads through the shared crypto/tls record layer and does not populate it.
	if n != 0 && c.rawConn.Input.Len() == 0 && c.rawConn.RawInput.Len() > 0 &&
		c.rawConn.RawInput.Bytes()[0] == recordTypeAlert {
		err = c.readRecord()
		if err != nil {
			// The n bytes already read from Input are real data. Read (the slow path) returns
			// (n, err) for ANY error here - close-notify EOF and a non-EOF alert/decrypt error
			// alike - so the waiter must not release the buffer and turn the call into a pure
			// (nil, err): that loses the tail. Return the data now and queue the exact error for
			// the next call. This is the same delayed-delivery mechanism the EOF case used; it is
			// generalised so the fast path cannot be lossier than the slow path on errors.
			return c.deferReadErrorAfterData(buffer, err)
		}
	}

	c.readWaitOptions.PostReturn(buffer)
	return
}
