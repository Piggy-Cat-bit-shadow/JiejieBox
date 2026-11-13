//go:build with_quic

package trusttunnel

// The QUIC forms of a TLS verdict and of an authentication refusal.
//
// The standard-library checks in fallbackEligible (`tls.AlertError`, the x509 types) describe the SAME
// events when this process produced them, but a remote verdict arrives as a CONNECTION_CLOSE in the
// CRYPTO_ERROR range: *quic.TransportError with a code of 0x0100 plus the TLS alert, whose Unwrap does
// not restore a tls.AlertError. These cases therefore drive the pinned type, not a stand-in, through
// both the classifier and the real DialContext path.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/sagernet/quic-go"
	E "github.com/sagernet/sing/common/exceptions"

	"github.com/stretchr/testify/require"
)

func TestFallbackEligibilityClassifiesQUICRepresentations(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"remote handshake_failure", E.Cause(&quic.TransportError{Remote: true, ErrorCode: 0x0128}, "QUIC handshake"), false},
		{"remote bad_certificate", E.Cause(&quic.TransportError{Remote: true, ErrorCode: 0x012a}, "QUIC handshake"), false},
		{"first crypto code", E.Cause(&quic.TransportError{ErrorCode: 0x0100}, "QUIC handshake"), false},
		{"last crypto code", E.Cause(&quic.TransportError{ErrorCode: 0x01ff}, "QUIC handshake"), false},
		{"below the crypto range", E.Cause(&quic.TransportError{ErrorCode: 0x00ff}, "QUIC handshake"), true},
		{"above the crypto range", E.Cause(&quic.TransportError{ErrorCode: 0x0200}, "QUIC handshake"), true},
		{"stateless reset is not a TLS verdict", E.Cause(&quic.TransportError{Remote: true, ErrorCode: 0x000a}, "QUIC handshake"), true},
		{"authentication application error", E.Cause(&quic.ApplicationError{Remote: true, ErrorCode: 0x1f}, "QUIC handshake"), false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, fallbackEligible(testCase.err))
		})
	}
}

// A remote TLS alert must keep its own meaning on the real path: no HTTP/2 leg, no cooldown.
func TestFallbackDoesNotFallBackOnAQUICRemoteTLSAlert(t *testing.T) {
	for _, code := range []quic.TransportErrorCode{0x0128, 0x012a} {
		code := code
		t.Run(fmt.Sprintf("crypto_%x", uint64(code)), func(t *testing.T) {
			var h2Calls atomic.Int32
			remoteAlert := &quic.TransportError{Remote: true, ErrorCode: code}
			client, _ := newFallbackTestClient(t,
				func(context.Context) (sessionConn, error) {
					return nil, E.Cause(remoteAlert, "QUIC handshake")
				},
				func(context.Context) (sessionConn, error) { h2Calls.Add(1); return newFakeSession(), nil },
			)
			err := dialFallback(t, context.Background(), client)
			require.Error(t, err, "a remote TLS verdict must stay an error, never a fallback success")
			require.True(t, errors.As(err, new(*quic.TransportError)), "the verdict must reach the caller")
			require.Zero(t, h2Calls.Load(), "the HTTP/2 leg presents the same certificate and would be refused alike")
			require.False(t, client.h3Cordoned(), "and it must not keep the node off HTTP/3")
		})
	}
}

// The ordinary transport failures the fallback exists for still qualify, so the classification above
// is not "everything from quic-go is a verdict".
func TestFallbackStillFallsBackOnAQUICTransportFailure(t *testing.T) {
	var h2Calls atomic.Int32
	client, _ := newFallbackTestClient(t,
		func(context.Context) (sessionConn, error) {
			return nil, E.Cause(&quic.TransportError{Remote: true, ErrorCode: 0x000a}, "QUIC handshake")
		},
		func(context.Context) (sessionConn, error) { h2Calls.Add(1); return newFakeSession(), nil },
	)
	require.NoError(t, dialFallback(t, context.Background(), client))
	require.EqualValues(t, 1, h2Calls.Load())
	require.True(t, client.h3Cordoned())
}
