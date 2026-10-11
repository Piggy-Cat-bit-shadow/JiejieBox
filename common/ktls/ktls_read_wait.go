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

func (c *Conn) WaitReadBuffer() (buffer *buf.Buffer, err error) {
	c.rawConn.In.Lock()
	defer c.rawConn.In.Unlock()
	for c.rawConn.Input.Len() == 0 {
		err = c.readRecord()
		if err != nil {
			return
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
			buffer.Release()
			return nil, err
		}
	}

	c.readWaitOptions.PostReturn(buffer)
	return
}
