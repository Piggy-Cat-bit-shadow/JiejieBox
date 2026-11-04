package tls

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"strings"

	"github.com/sagernet/sing-box/common/badtls"
	"github.com/sagernet/sing-box/common/resourceguard"
	"github.com/sagernet/sing-box/common/tlsspoof"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	aTLS "github.com/sagernet/sing/common/tls"
)

var errMissingServerName = E.New("missing server_name or insecure=true")

func parseTLSSpoofOptions(serverName string, options option.OutboundTLSOptions) (string, tlsspoof.Method, error) {
	spoof, method, err := tlsspoof.ParseOptions(options.Spoof, options.SpoofMethod)
	if err != nil {
		return "", 0, err
	}
	if spoof == "" {
		return "", 0, nil
	}
	if options.DisableSNI || serverName == "" || M.ParseAddr(serverName).IsValid() {
		return "", 0, E.New("`spoof` requires TLS ClientHello with SNI")
	}
	if strings.EqualFold(spoof, serverName) {
		return "", 0, E.New("`spoof` must differ from `server_name`")
	}
	return spoof, method, nil
}

func applyTLSSpoof(conn net.Conn, spoof string, method tlsspoof.Method) (net.Conn, error) {
	if spoof == "" {
		return conn, nil
	}
	return tlsspoof.NewConn(conn, method, spoof)
}

func NewDialerFromOptions(ctx context.Context, logger logger.ContextLogger, dialer N.Dialer, serverAddress string, options option.OutboundTLSOptions) (N.Dialer, error) {
	if !options.Enabled {
		return dialer, nil
	}
	config, err := NewClientWithOptions(ClientOptions{
		Context:       ctx,
		Logger:        logger,
		ServerAddress: serverAddress,
		Options:       options,
	})
	if err != nil {
		return nil, err
	}
	return NewDialer(dialer, config), nil
}

func NewClient(ctx context.Context, logger logger.ContextLogger, serverAddress string, options option.OutboundTLSOptions) (Config, error) {
	return NewClientWithOptions(ClientOptions{
		Context:       ctx,
		Logger:        logger,
		ServerAddress: serverAddress,
		Options:       options,
	})
}

type ClientOptions struct {
	Context              context.Context
	Logger               logger.ContextLogger
	ServerAddress        string
	Options              option.OutboundTLSOptions
	AllowEmptyServerName bool
	KTLSCompatible       bool
}

func NewClientWithOptions(options ClientOptions) (Config, error) {
	if !options.Options.Enabled {
		return nil, nil
	}
	if !options.KTLSCompatible {
		if options.Options.KernelTx {
			options.Logger.Warn("enabling kTLS TX in current scenarios will definitely reduce performance, please checkout https://sing-box.sagernet.org/configuration/shared/tls/#kernel_tx")
		}
	}
	if options.Options.KernelRx {
		options.Logger.Warn("enabling kTLS RX will definitely reduce performance, please checkout https://sing-box.sagernet.org/configuration/shared/tls/#kernel_rx")
	}
	switch options.Options.Engine {
	case "", C.TLSEngineGo:
	case C.TLSEngineApple:
		return newAppleClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
	case C.TLSEngineWindows:
		return newWindowsClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
	default:
		return nil, E.New("unknown tls engine: ", options.Options.Engine)
	}
	if options.Options.Reality != nil && options.Options.Reality.Enabled {
		return newRealityClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
	} else if options.Options.UTLS != nil && options.Options.UTLS.Enabled {
		return newUTLSClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
	}
	return newSTDClient(options.Context, options.Logger, options.ServerAddress, options.Options, options.AllowEmptyServerName)
}

func ClientHandshake(ctx context.Context, conn net.Conn, config Config) (Conn, error) {
	tlsConn, err := aTLS.ClientHandshake(ctx, conn, config)
	if err != nil {
		return nil, err
	}
	readWaitConn, err := badtls.NewReadWaitConn(tlsConn)
	if err == nil {
		return readWaitConn, nil
	} else if err != os.ErrInvalid {
		return nil, err
	}
	return tlsConn, nil
}

type Dialer interface {
	N.Dialer
	DialTLSContext(ctx context.Context, destination M.Socksaddr) (Conn, error)
}

type defaultDialer struct {
	dialer N.Dialer
	config Config
}

func NewDialer(dialer N.Dialer, config Config) Dialer {
	return &defaultDialer{dialer, config}
}

func (d *defaultDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	if N.NetworkName(network) != N.NetworkTCP {
		return nil, os.ErrInvalid
	}
	return d.DialTLSContext(ctx, destination)
}

func (d *defaultDialer) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, os.ErrInvalid
}

func (d *defaultDialer) DialTLSContext(ctx context.Context, destination M.Socksaddr) (Conn, error) {
	return handshakeWithAdmission(ctx, func() (Conn, error) {
		return d.dialContext(ctx, destination, true)
	})
}

// handshakeWithAdmission runs one TLS dial under the process admission policy.
//
// # Why the shared TLS dialer, and not each outbound
//
// Every TLS-based outbound - Trojan, VLESS, VMess, the HTTP transports, DoT/DoH - builds this
// dialer, and each logical stream they open is a TCP connect plus a full TLS handshake. Before
// this, only AnyTLS was admitted, so a memory storm driven by any of the others was unbounded
// even though the cost per stream is the same. One seam here covers all of them instead of
// copying an admission call into a dozen outbounds, and it sits before the handshake - the point
// where the buffers, the key exchange and the per-connection goroutines begin.
//
// # Why it can be skipped
//
// An outbound that already admits at its own seam (AnyTLS) marks the context, and this function
// then runs the dial without taking a second permit: one handshake, one slot. Without that, the
// two gates would halve the effective limit and queue the same dial twice.
//
// On a process with no budget the guard is nil and Acquire returns (nil, nil): one nil check and
// the old behaviour.
func handshakeWithAdmission(ctx context.Context, dial func() (Conn, error)) (Conn, error) {
	if resourceguard.HasHeldPermit(ctx) {
		return dial()
	}
	permit, err := resourceguard.FromContext(ctx).Acquire(ctx, resourceguard.ClassNormal)
	if err != nil {
		return nil, err
	}
	defer permit.Release()
	return dial()
}

func (d *defaultDialer) dialContext(ctx context.Context, destination M.Socksaddr, echRetry bool) (Conn, error) {
	conn, err := d.dialer.DialContext(ctx, N.NetworkTCP, destination)
	if err != nil {
		return nil, err
	}
	tlsConn, err := aTLS.ClientHandshake(ctx, conn, d.config)
	if err != nil {
		conn.Close()
		var echErr *tls.ECHRejectionError
		if echRetry && errors.As(err, &echErr) && len(echErr.RetryConfigList) > 0 {
			if echConfig, isECH := d.config.(ECHCapableConfig); isECH {
				echConfig.SetECHConfigList(echErr.RetryConfigList)
				return d.dialContext(ctx, destination, false)
			}
		}
		return nil, err
	}
	return tlsConn, nil
}

func (d *defaultDialer) Upstream() any {
	return d.dialer
}
