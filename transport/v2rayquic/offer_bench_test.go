//go:build with_quic

package v2rayquic

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/common/trafficclass"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

// BenchmarkSharedOfferCreation prices one shared-QUIC creation attempt that fails immediately.
//
// # What the two variants separate
//
// NoGuard is the cost every platform pays: the offer context, the pending slot, one goroutine and a
// constructor call. Round 1 derives that context from the triggering demand (values kept, lifetime
// from the client) instead of straight from the client, and this is the measurement of that change.
//
// WithGuard adds the process admission hook when the construction context carries a guard: one
// Acquire/Release pair around the constructor. It only runs with admission on the branch; on the
// base tree the guard is inert, which is exactly the "before" number.
func BenchmarkSharedOfferCreation(b *testing.B) {
	b.Run("NoGuard", func(b *testing.B) {
		benchmarkOffer(b, context.Background())
	})
	b.Run("WithGuard", func(b *testing.B) {
		guard := resourceguard.NewGuard(resourceguard.DefaultNetworkExtensionConfig(), nil)
		ctx := resourceguard.NewContext(context.Background(), guard)
		benchmarkOffer(b, ctx)
	})
}

func benchmarkOffer(b *testing.B, ctx context.Context) {
	logger := log.NewNOPFactory().NewLogger("v2rayquic-bench")
	tlsConfig, err := tls.NewClient(context.Background(), logger, "localhost", option.OutboundTLSOptions{
		Enabled:    true,
		ServerName: "localhost",
		Insecure:   true,
	})
	if err != nil {
		b.Fatal(err)
	}
	transport, err := NewClient(ctx, benchUnderlyingDialer{}, M.ParseSocksaddrHostPort("localhost", 443), option.V2RayQUICOptions{}, tlsConfig)
	if err != nil {
		b.Fatal(err)
	}
	client, isClient := transport.(*Client)
	if !isClient {
		b.Fatalf("NewClient returned %T", transport)
	}
	b.Cleanup(func() { _ = client.Close() })

	dialErr := errors.New("benchmark quic handshake refused")
	callerCtx := trafficclass.WithClass(context.Background(), trafficclass.ClassInteractive)
	client.dialConn = func(context.Context) (*clientConnection, error) {
		return nil, dialErr
	}

	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		_, _ = client.offer(callerCtx)
	}
}

// benchUnderlyingDialer is never reached: the QUIC constructor is replaced by a failing stub.
type benchUnderlyingDialer struct{}

func (benchUnderlyingDialer) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("benchmark must not reach the underlying dialer")
}

func (benchUnderlyingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("benchmark must not reach the underlying dialer")
}
