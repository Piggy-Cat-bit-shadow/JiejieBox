package mieru

// The shipped Mieru SDK's teardown/attach guard, exercised from the CONSUMING module.
//
// Q04's race is inside the SDK: Mux.DialContext picks an underlay, a teardown closes it, and the
// attach used to succeed anyway, so the flow died later as a SOCKS EOF. The regression lives in the
// SDK (pkg/protocol), and this test is the consumption half: it builds a real underlay through the
// pinned fork's exported API, closes it, and requires AddSession to refuse. If the replace is ever
// dropped or moved back to an upstream version without the guard, this test goes red.

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	apicommon "github.com/enfein/mieru/v3/apis/common"
	"github.com/enfein/mieru/v3/pkg/cipher"
	"github.com/enfein/mieru/v3/pkg/protocol"
)

func TestTheShippedSDKRefusesASessionOnAClosedUnderlay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()

	block, err := cipher.BlockCipherFromPassword([]byte("test-password"), false)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	underlay, err := protocol.NewStreamUnderlay(
		context.Background(),
		&net.Dialer{},
		net.DefaultResolver,
		&apicommon.ClientDNSConfig{BypassDialerDNS: true},
		"tcp",
		listener.Addr().String(),
		1280,
		block,
		nil,
	)
	if err != nil {
		t.Fatalf("NewStreamUnderlay: %v", err)
	}
	if err = underlay.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	session := protocol.NewSession(1, true, 1280, nil, nil)
	err = underlay.AddSession(session, nil)
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("the shipped mieru SDK accepted a session on a closed underlay: %v", err)
	}
}
