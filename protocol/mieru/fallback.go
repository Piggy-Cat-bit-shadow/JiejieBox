package mieru

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sagernet/sing-box/option"
	N "github.com/sagernet/sing/common/network"

	mieruclient "github.com/enfein/mieru/v3/apis/client"
	mierumodel "github.com/enfein/mieru/v3/apis/model"
	"github.com/enfein/mieru/v3/pkg/stderror"
)

// fallbackCooldown is the first window in which a failed primary transport is
// kept out of the preferred path. A failed recovery probe doubles the window
// up to fallbackCooldownMax, so a persistently dead endpoint is not probed at
// a fixed high rate. This is not a background probe interval: the first
// business dial after the window is the only thing that may test the primary.
const (
	fallbackCooldown    = 30 * time.Second
	fallbackCooldownMax = 5 * time.Minute
)

type dialFailureClass uint8

const (
	dialSuccess dialFailureClass = iota
	// dialTransportFailure is evidence that the proxy transport/session could
	// not carry the request. It is the only class that may change the primary
	// preference and make this dial try the secondary.
	dialTransportFailure
	// dialTargetFailure means the Mieru server answered with a SOCKS reply
	// (for example connection refused). The proxy path and authentication are
	// working; the destination failed. It must not trip the transport health.
	dialTargetFailure
	// dialCallerFailure is a local cancellation, DNS/address policy error,
	// permission error or a lifecycle error. Retrying the other transport
	// would usually just duplicate the same local failure.
	dialCallerFailure
	// dialAmbiguousFailure is the conservative bucket. A handshake that ends
	// with a bare EOF can be an authentication failure, a server-side close or
	// a dead underlay; the SDK does not expose a stable discriminator. Keeping
	// the primary preference and returning the real error is safer than
	// claiming an automatic recovery.
	dialAmbiguousFailure
)

func (c dialFailureClass) String() string {
	switch c {
	case dialSuccess:
		return "success"
	case dialTransportFailure:
		return "transport"
	case dialTargetFailure:
		return "target"
	case dialCallerFailure:
		return "caller"
	case dialAmbiguousFailure:
		return "ambiguous"
	default:
		return "unknown"
	}
}

// classifyDialError separates a failed SDK DialContext into the only decisions
// this selector is allowed to make.
//
// The SDK returns plain fmt errors from PostDialHandshake; only the final SOCKS
// reply is a stable protocol fact. Socket dial errors, write errors and read
// timeouts are transport evidence. A bare EOF is intentionally ambiguous:
// wrong credentials, a server-side target close and an underlay FIN all look
// the same at this API boundary. A valid non-success SOCKS reply is target
// evidence and is never allowed to mark the transport unhealthy.
func classifyDialError(ctx context.Context, err error, transport mieruTransport) dialFailureClass {
	if err == nil {
		return dialSuccess
	}
	if ctx != nil && ctx.Err() != nil {
		return dialCallerFailure
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return dialCallerFailure
	}
	if errors.Is(err, mieruclient.ErrClientIsNotRunning) {
		return dialCallerFailure
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return dialCallerFailure
	}
	var addressError *net.AddrError
	if errors.As(err, &addressError) {
		return dialCallerFailure
	}
	if errors.Is(err, os.ErrPermission) {
		return dialCallerFailure
	}

	var typedError stderror.TypedError
	if errors.As(err, &typedError) {
		switch stderror.GetErrorType(typedError) {
		case stderror.NETWORK_ERROR, stderror.PROTOCOL_ERROR:
			return dialTransportFailure
		case stderror.CRYPTO_ERROR, stderror.REPLAY_ERROR:
			return dialAmbiguousFailure
		}
	}

	message := err.Error()
	lower := strings.ToLower(message)
	if strings.Contains(message, "server returned socks5 error code") {
		return dialTargetFailure
	}
	if strings.Contains(message, "failed to read socks5 connection response") {
		if isTimeout(err) || strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
			return dialTransportFailure
		}
		if strings.Contains(lower, "connection reset") ||
			strings.Contains(lower, "connection aborted") ||
			strings.Contains(lower, "forcibly closed") ||
			strings.Contains(lower, "broken pipe") {
			return dialTransportFailure
		}
		if errors.Is(err, io.EOF) || strings.Contains(lower, "eof") {
			if transport.udp {
				// Linux reports an unreachable UDP peer as a bare EOF. A UDP
				// primary with no reply is exactly the blackhole this fallback
				// exists for; unlike a TCP EOF it cannot be a server-side
				// target close after an accepted stream.
				return dialTransportFailure
			}
			return dialAmbiguousFailure
		}
		return dialAmbiguousFailure
	}
	if strings.Contains(message, "failed to write socks5 connection request") {
		return dialTransportFailure
	}
	if strings.Contains(message, "NewTCPUnderlay() failed") ||
		strings.Contains(message, "NewUDPUnderlay() failed") ||
		strings.Contains(message, "AddSession() failed") {
		return dialTransportFailure
	}
	if isTimeout(err) {
		return dialTransportFailure
	}
	if strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no connection could be made") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "connection aborted") ||
		strings.Contains(lower, "network is unreachable") ||
		strings.Contains(lower, "host is unreachable") {
		return dialTransportFailure
	}
	if strings.Contains(lower, "client password is not set") ||
		strings.Contains(lower, "no server listening endpoint") ||
		strings.Contains(lower, "unsupported network") {
		return dialCallerFailure
	}
	return dialAmbiguousFailure
}

func isTimeout(err error) bool {
	var netError net.Error
	return errors.As(err, &netError) && netError.Timeout()
}

type fallbackPreference uint8

const (
	fallbackPrimary fallbackPreference = iota
	fallbackSecondary
)

func (p fallbackPreference) String() string {
	if p == fallbackSecondary {
		return "secondary"
	}
	return "primary"
}

type fallbackSnapshot struct {
	Generation     uint64
	Preferred      fallbackPreference
	PrimaryUntil   time.Time
	Probing        bool
	Probes         uint64
	PrimaryFailure uint64
	SecondaryWins  uint64
}

// fallbackSelector is the entire mutable fallback state. It never performs
// network I/O, never starts a goroutine and never holds a lock across a dial.
// A generation token makes a late completion from an old network unable to
// publish a preference for the current one.
type fallbackSelector struct {
	access       sync.Mutex
	now          func() time.Time
	baseCooldown time.Duration
	cooldown     time.Duration

	generation   uint64
	preferred    fallbackPreference
	primaryUntil time.Time
	probing      bool
	closed       bool

	probes         uint64
	primaryFailure uint64
	secondaryWins  uint64
}

func newFallbackSelector(generation uint64) *fallbackSelector {
	return &fallbackSelector{
		now:          time.Now,
		baseCooldown: fallbackCooldown,
		cooldown:     fallbackCooldown,
		generation:   generation,
		preferred:    fallbackPrimary,
	}
}

// choose decides which transport a new session should try.
//
// While the secondary is preferred, one caller at a time may be designated as
// the primary recovery probe once the cooldown has elapsed. Every other caller
// continues on the secondary; there is no global lock or probe storm.
func (s *fallbackSelector) choose(generation uint64) (fallbackPreference, bool, bool) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.closed || generation != s.generation {
		return fallbackPrimary, false, false
	}
	if s.preferred == fallbackPrimary {
		return fallbackPrimary, false, true
	}
	now := s.now()
	if now.Before(s.primaryUntil) {
		return fallbackSecondary, false, true
	}
	if s.probing {
		return fallbackSecondary, false, true
	}
	s.probing = true
	s.probes++
	return fallbackPrimary, true, true
}

// report publishes the outcome of one attempt. A target reply or a successful
// handshake proves the transport itself is healthy; only a transport-class
// failure starts or extends the primary cooldown.
func (s *fallbackSelector) report(generation uint64, choice fallbackPreference, probe bool, class dialFailureClass) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.closed || generation != s.generation {
		return
	}
	if choice == fallbackSecondary {
		if class == dialSuccess {
			s.secondaryWins++
		}
		return
	}
	switch class {
	case dialSuccess, dialTargetFailure:
		s.preferred = fallbackPrimary
		s.primaryUntil = time.Time{}
		s.cooldown = s.baseCooldown
		s.probing = false
	case dialTransportFailure:
		if probe {
			s.cooldown *= 2
			if s.cooldown > fallbackCooldownMax {
				s.cooldown = fallbackCooldownMax
			}
		} else {
			s.cooldown = s.baseCooldown
		}
		s.preferred = fallbackSecondary
		s.primaryUntil = s.now().Add(s.cooldown)
		s.probing = false
		s.primaryFailure++
	case dialCallerFailure:
		if probe {
			s.probing = false
		}
	case dialAmbiguousFailure:
		if probe {
			// A silent probe (credentials, server close or a dead underlay)
			// must not turn the next request into another immediate probe.
			s.cooldown *= 2
			if s.cooldown > fallbackCooldownMax {
				s.cooldown = fallbackCooldownMax
			}
			s.primaryUntil = s.now().Add(s.cooldown)
			s.probing = false
		}
	}
}

// reportBothFailed handles the case where the primary was classified as a
// transport failure but the secondary also failed before producing any SOCKS
// reply. Two silent handshakes are exactly what a wrong password looks like
// through the official API (the server drops both transports), so the
// conservative result is to stop preferring the secondary and let the next
// business request try the primary again. A successful secondary never reaches
// this method, and a valid SOCKS reply is target-class, not transport.
func (s *fallbackSelector) reportBothFailed(generation uint64, primaryClass, secondaryClass dialFailureClass) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.closed || generation != s.generation {
		return
	}
	if !isSilentHandshakeClass(primaryClass) || !isSilentHandshakeClass(secondaryClass) {
		return
	}
	s.preferred = fallbackPrimary
	s.primaryUntil = time.Time{}
	s.cooldown = s.baseCooldown
	s.probing = false
}

func isSilentHandshakeClass(class dialFailureClass) bool {
	return class == dialTransportFailure || class == dialAmbiguousFailure
}

func (s *fallbackSelector) reset(generation uint64) {
	s.access.Lock()
	s.generation = generation
	s.preferred = fallbackPrimary
	s.primaryUntil = time.Time{}
	s.cooldown = s.baseCooldown
	s.probing = false
	s.closed = false
	s.access.Unlock()
}

func (s *fallbackSelector) close() {
	s.access.Lock()
	s.closed = true
	s.probing = false
	s.access.Unlock()
}

func (s *fallbackSelector) snapshot() fallbackSnapshot {
	s.access.Lock()
	defer s.access.Unlock()
	return fallbackSnapshot{
		Generation:     s.generation,
		Preferred:      s.preferred,
		PrimaryUntil:   s.primaryUntil,
		Probing:        s.probing,
		Probes:         s.probes,
		PrimaryFailure: s.primaryFailure,
		SecondaryWins:  s.secondaryWins,
	}
}

// transportRuntime is one official Mieru client generation bound to exactly
// one underlay transport. Primary and secondary never share a client, a
// configuration, a serverDialer or a PacketConn registry.
type transportRuntime struct {
	label        string
	transport    mieruTransport
	client       mieruclient.Client
	config       *mieruclient.ClientConfig
	packetDialer *serverDialer

	startOnce sync.Once
	startErr  error
	started   bool

	stopOnce sync.Once
	stopErr  error
}

func newTransportRuntime(spec *transportSpec) (*transportRuntime, error) {
	transport, err := parseTransport(spec.options.Transport)
	if err != nil {
		return nil, err
	}
	serverDialer, err := newServerDialer(spec.dialer, spec.options.Server)
	if err != nil {
		return nil, err
	}
	config, err := buildClientConfig(spec.options, serverDialer, serverDialer, spec.resolver)
	if err != nil {
		return nil, err
	}
	client := mieruclient.NewClient()
	if err = client.Store(config); err != nil {
		return nil, fmt.Errorf("mieru: store %s client config: %w", spec.label, err)
	}
	return &transportRuntime{
		label:        spec.label,
		transport:    transport,
		client:       client,
		config:       config,
		packetDialer: serverDialer,
	}, nil
}

func (t *transportRuntime) start() error {
	t.startOnce.Do(func() {
		t.startErr = t.client.Start()
		t.started = t.startErr == nil
	})
	return t.startErr
}

// stop is idempotent. The SDK Client cannot be restarted after Stop, so every
// runtime is used for exactly one generation.
func (t *transportRuntime) stop() error {
	t.stopOnce.Do(func() {
		if t.started {
			t.stopErr = t.client.Stop()
		}
		if t.packetDialer != nil {
			t.packetDialer.shutdownPacketConns()
		}
		t.started = false
	})
	return t.stopErr
}

// dial performs one SDK dial and closes the connection the SDK returns
// together with a handshake error. mieruclient.DialContext returns that
// non-nil connection in this case; dropping it would leak a session.
func (t *transportRuntime) dial(ctx context.Context, destination mierumodel.NetAddrSpec) (net.Conn, dialFailureClass, error) {
	conn, err := t.client.DialContext(ctx, destination)
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, classifyDialError(ctx, err, t.transport), err
	}
	return conn, dialSuccess, nil
}

// transportSpec owns the immutable inputs needed to create a fresh runtime on
// every Start/InterfaceUpdated. The raw dialer and resolver are stateless with
// respect to Mieru sessions; the per-generation serverDialer and client are
// created by newTransportRuntime.
type transportSpec struct {
	label    string
	options  option.MieruOutboundOptions
	dialer   N.Dialer
	resolver *resolverBridge
}
