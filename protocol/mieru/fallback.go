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

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
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
	// dialSocketFailure is DEFINITE evidence from the socket layer: the kernel
	// refused the attempt, or reported that this endpoint cannot be reached.
	// The evidence is the typed error captured by the bridge (see
	// dial_observation.go), classified with the same errno sets as the rest of
	// the tree (common/dialer.ErroredWith), not a text guess: Windows formats
	// the message in the system language, so text is not a stable source.
	//
	// It behaves like dialTransportFailure for retry and cooldown, and differs
	// in exactly one decision: two definite socket failures are proof that both
	// endpoints are unusable right now, so the secondary preference may persist
	// through its cooldown instead of being reset after every request.
	dialSocketFailure
	// dialSocketTimeout is a socket-stage failure whose error is a kernel
	// connect timeout or a reset during connect. It is transport evidence and
	// must try the other transport, but it is NOT proof about the endpoint: a
	// silent path and a slow endpoint are indistinguishable.
	//
	// It deliberately carries NO FailureFacts. The SDK flattens this error to
	// text ("NewTCPUnderlay() failed: ... i/o timeout"), so the group's
	// isTimeoutFailure can no longer see a net.Error; a Path fact at the dial
	// stage would then demote a member for a mere stall, which is exactly the
	// demotion the group's timeout rule exists to prevent. Unknown is the
	// honest value, and it keeps the group's conservative default.
	dialSocketTimeout
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
	case dialSocketFailure:
		return "socket"
	case dialSocketTimeout:
		return "socket-timeout"
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
//
// `observed` is the bridge's typed record of what the fork dialer returned
// during this attempt. It is consulted before the text rules because the SDK
// flattens the error with fmt.Errorf("%v") and the type - the only thing that
// survives localization - is gone by now. See dial_observation.go.
func classifyDialError(ctx context.Context, err error, transport mieruTransport, observed *dialObservation) dialFailureClass {
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

	// Typed socket evidence, recovered from the fork dialer before the SDK
	// replaced the chain with a string.
	if observed != nil {
		if socketErr := observed.socketFailure(); socketErr != nil {
			if class, classified := classifySocketError(socketErr); classified {
				return class
			}
		}
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

// classifySocketError reads the bridge-captured socket error with the shared
// errno sets instead of matching English text.
//
// The sets and ErroredWith come from common/dialer because it is already the
// single place where the POSIX and Winsock spellings of these conditions live
// (see common/dialer/windows_socket_errors.go): on Windows the raw 10061
// "refused" never equals syscall.ECONNREFUSED, and the message itself is
// formatted in the system language, so neither a syscall constant nor an
// English string is a portable test.
//
// The distinction this function adds is between DEFINITE and soft evidence:
//
//	refused / unreachable / cannot assign     definite (dialSocketFailure)
//	kernel connect timeout, reset at connect  transport evidence, not proof
//
// A refused listener is a fact about this moment: something on the path
// answered and declined. An unreachable network is a fact that the packet
// never left. A timeout is not proof of anything - only that nobody answered.
func classifySocketError(err error) (dialFailureClass, bool) {
	switch {
	case dialer.ErroredWith(err, dialer.PeerAnsweredErrnos...):
		// Something answered: the TCP listener refused, or a connected UDP
		// socket received ICMP port-unreachable. The proxy endpoint is not
		// accepting this transport right now, which is definite.
		return dialSocketFailure, true
	case dialer.ErroredWith(err, dialer.UnreachableErrnos...):
		// The kernel says this address family/route cannot carry the packet.
		// Whatever the remote endpoint is doing, this attempt could not reach
		// it, so the other transport is the only useful next step.
		return dialSocketFailure, true
	case dialer.ErroredWith(err, dialer.PathFailureErrnos...):
		// The leftovers of that set are EADDRNOTAVAIL/ENETDOWN: this MACHINE's
		// interface could not provide an address. That is local, not the
		// member's path, and the other transport would fail the same way, so
		// it must neither switch preference nor demote the member.
		return dialCallerFailure, true
	case dialer.ErroredWith(err, dialer.TimeoutErrnos...):
		// The kernel's own connect timeout is not proof the endpoint is down.
		return dialSocketTimeout, true
	case dialer.ErroredWith(err, dialer.ResetErrnos...):
		// A reset during connect: something answered and closed, but one
		// answer is not proof that a later attempt fails.
		return dialSocketTimeout, true
	case errors.Is(err, os.ErrPermission):
		return dialCallerFailure, true
	case isTimeout(err):
		// A media timeout from the connect path (not a bare errno): the
		// attempt died before any protocol byte, but a stall is not proof.
		return dialSocketTimeout, true
	}
	return dialSuccess, false
}

// isTransportClass reports whether this class licenses trying the other
// transport in the same dial. The definite socket class does, because the
// evidence names the first hop to the proxy endpoint rather than the target.
func isTransportClass(class dialFailureClass) bool {
	return class == dialTransportFailure || class == dialSocketFailure || class == dialSocketTimeout
}

// isDefiniteSocketClass reports whether a failed attempt carries proof from
// the socket layer. Only reportBothFailed reads this: two such failures mean
// neither endpoint is accepting the transport, which is a stable state, while
// two silent handshakes may be wrong credentials on a healthy endpoint.
func isDefiniteSocketClass(class dialFailureClass) bool {
	return class == dialSocketFailure
}

// wrapFailureFacts attaches the narrow adapter failure fact to a Mieru dial
// error. This is the only producer needed for the cross-protocol policy: a
// group can then tell a target refusal from a first-hop path failure without
// parsing strings. Ambiguous EOF stays fact-less (Unknown), so the group's
// existing conservative errno policy still applies.
func wrapFailureFacts(err error, class dialFailureClass) error {
	if err == nil {
		return nil
	}
	var facts adapter.FailureFacts
	switch class {
	case dialTargetFailure:
		facts = adapter.TargetFailureFacts()
	case dialTransportFailure:
		facts = adapter.PathFailureFacts(adapter.FailureStageHandshake)
	case dialSocketFailure:
		// The proof came from the socket to the proxy endpoint, before any
		// Mieru byte could be exchanged: the dial stage, this member's path.
		facts = adapter.PathFailureFacts(adapter.FailureStageDial)
	case dialCallerFailure:
		facts = adapter.FailureFacts{Stage: adapter.FailureStageDial, Origin: adapter.FailureOriginCaller}
	default:
		// dialSocketTimeout and dialAmbiguousFailure stay fact-less on purpose:
		// see the class comments. Unknown is not a verdict.
		return err
	}
	return adapter.WrapFailure(err, facts)
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
	case dialTransportFailure, dialSocketFailure, dialSocketTimeout:
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
// transport-class failure but the secondary also failed before producing any
// SOCKS reply.
//
// Two proven socket failures are a stable fact: neither endpoint accepts this
// transport right now. Resetting to the primary there would make EVERY request
// re-dial a refused endpoint and then the secondary - twice the connection
// churn for the whole outage, with no additional information. So when both
// sides are definite, the secondary preference stands and the existing
// cooldown is what schedules the single recovery probe.
//
// Two silent handshakes are NOT that fact: they are exactly what a wrong
// password looks like through the official API (the server drops both
// transports), and staying on the secondary would strand a misconfigured
// account on the backup path. The conservative result there remains "try the
// primary again on the next business request".
//
// Mixed evidence (primary proven down, secondary silent) also resets: the
// primary is cheap to re-test, so re-testing it is how a recovered primary is
// noticed without waiting out a cooldown that only the primary's own failure
// earned.
func (s *fallbackSelector) reportBothFailed(generation uint64, primaryClass, secondaryClass dialFailureClass) {
	s.access.Lock()
	defer s.access.Unlock()
	if s.closed || generation != s.generation {
		return
	}
	if isDefiniteSocketClass(primaryClass) && isDefiniteSocketClass(secondaryClass) {
		return
	}
	if !isSilentHandshakeClass(primaryClass) && !isDefiniteSocketClass(primaryClass) {
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
	// A caller that has already given up must not cost an SDK handshake: the
	// public DialContext checks the context while building an underlay but the
	// SOCKS5 response read inside it is bounded only by the SDK's own 10s
	// deadline, so a cancelled request would otherwise sit here for seconds
	// with nobody waiting for it.
	if ctx != nil && ctx.Err() != nil {
		return nil, dialCallerFailure, ctx.Err()
	}
	// The SDK flattens the underlay error to a string before returning it; the
	// ledger keeps the typed proof for the classifier. See dial_observation.go.
	observedContext, observation := withDialObservation(ctx)
	conn, err := t.client.DialContext(observedContext, destination)
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, classifyDialError(ctx, err, t.transport, observation), err
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
