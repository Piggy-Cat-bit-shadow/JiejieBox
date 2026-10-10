//go:build linux && go1.25 && badlinkname

package ktls

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"syscall"

	"github.com/sagernet/sing-box/common/badtls"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	N "github.com/sagernet/sing/common/network"
	aTLS "github.com/sagernet/sing/common/tls"

	"golang.org/x/sys/unix"
)

type Conn struct {
	aTLS.Conn
	ctx             context.Context
	logger          logger.ContextLogger
	conn            net.Conn
	rawConn         *badtls.RawConn
	syscallConn     syscall.Conn
	rawSyscallConn  syscall.RawConn
	readWaitOptions N.ReadWaitOptions
	kernelTx        bool
	kernelRx        bool
	pendingRxSplice bool
}

func NewConn(ctx context.Context, logger logger.ContextLogger, conn aTLS.Conn, txOffload, rxOffload bool) (aTLS.Conn, error) {
	err := Load()
	if err != nil {
		return nil, err
	}
	syscallConn, isSyscallConn := N.CastReader[interface {
		io.Reader
		syscall.Conn
	}](conn.NetConn())
	if !isSyscallConn {
		return nil, os.ErrInvalid
	}
	rawSyscallConn, err := syscallConn.SyscallConn()
	if err != nil {
		return nil, err
	}
	rawConn, err := badtls.NewRawConn(conn)
	if err != nil {
		return nil, err
	}
	if *rawConn.Vers != tls.VersionTLS13 {
		return nil, os.ErrInvalid
	}
	for rawConn.RawInput.Len() > 0 {
		err = rawConn.ReadRecord()
		if err != nil {
			return nil, err
		}
		for rawConn.Hand.Len() > 0 {
			err = rawConn.HandlePostHandshakeMessage()
			if err != nil {
				return nil, E.Cause(err, "handle post-handshake messages")
			}
		}
	}
	kConn := &Conn{
		Conn:           conn,
		ctx:            ctx,
		logger:         logger,
		conn:           conn.NetConn(),
		rawConn:        rawConn,
		syscallConn:    syscallConn,
		rawSyscallConn: rawSyscallConn,
	}
	err = kConn.setupKernel(txOffload, rxOffload)
	if err != nil {
		return nil, err
	}
	return kConn, nil
}

func (c *Conn) Upstream() any {
	return c.Conn
}

func (c *Conn) SyscallConnForRead() syscall.RawConn {
	if !c.kernelRx {
		return nil
	}
	if !*c.rawConn.IsClient {
		// Warn, not Debug, and the REASON is part of the message.
		//
		// kTLS RX splice is client-only. On a server this returns nil, the copy engine falls back to
		// the userspace path, and the kernel_rx the operator paid for does nothing - but the only
		// thing they were told is the generic "enabling kTLS RX will definitely reduce performance"
		// warning, which describes the option as merely slow rather than INERT here. An engineer
		// profiling a server would go looking for the wrong problem.
		//
		// The same message used to be logged at Debug, which is off in production, so the actual
		// reason was invisible precisely when it mattered.
		c.logger.WarnContext(c.ctx, "ktls: RX splice was requested but is unavailable on the server "+
			"side, since it causes an unknown failure; falling back to userspace copy. kernel_rx has "+
			"no effect on a server")
		return nil
	}
	c.logger.DebugContext(c.ctx, "ktls: RX splice requested")
	return c.rawSyscallConn
}

func (c *Conn) HandleSyscallReadError(inputErr error) ([]byte, error) {
	if errors.Is(inputErr, unix.EINVAL) {
		c.pendingRxSplice = true
		err := c.readRecord()
		if err != nil {
			return nil, E.Cause(err, "ktls: handle non-application-data record")
		}
		var input bytes.Buffer
		if c.rawConn.Input.Len() > 0 {
			_, err = c.rawConn.Input.WriteTo(&input)
			if err != nil {
				return nil, err
			}
		}
		return input.Bytes(), nil
	} else if errors.Is(inputErr, unix.EBADMSG) {
		return nil, c.rawConn.In.SetErrorLocked(c.sendAlert(alertBadRecordMAC))
	} else {
		return nil, E.Cause(inputErr, "ktls: unexpected errno")
	}
}

func (c *Conn) SyscallConnForWrite() syscall.RawConn {
	if !c.kernelTx {
		return nil
	}
	c.logger.DebugContext(c.ctx, "ktls: TX splice requested")
	return c.rawSyscallConn
}
