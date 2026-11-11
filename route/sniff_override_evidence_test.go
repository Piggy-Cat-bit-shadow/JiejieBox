package route

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// Address evidence describes ONE destination. This file pins the target-identity rule for every
// entry point that can change the flow's destination, and the publication order of two resolve
// actions.
//
// # Why these tests inject the sniff action instead of configuring it
//
// `override_destination` was removed from the sniff action's configuration surface: the option
// struct has no such field, a configuration that writes it is REJECTED by the decoder, and nothing
// in this tree assigns `RuleActionSniff.OverrideDestination`. The three branches that honour the flag
// are therefore unreachable from a configuration today, and no end-to-end test can turn them on.
//
// They still exist in the action type and in the router, so the invalidation below is a latent
// correctness fix rather than a live bug fix: if the option (or any new caller) re-enables the flag,
// the rewrite must not keep judging the new target with the old target's addresses. The tests drive
// the real functions (`actionSniff`, `PreMatch`) with a directly constructed action, which is the
// only way to exercise that contract, and the report records the reachability finding separately.

func addressPrefix(cidr string) *badoption.Prefixable {
	prefix := badoption.Prefixable(netip.MustParsePrefix(cidr))
	return &prefix
}

func newIndexCIDRItem(t *testing.T, cidr string) *R.IPCIDRItem {
	t.Helper()
	item, err := R.NewIPCIDRItem(false, []*badoption.Prefixable{addressPrefix(cidr)})
	require.NoError(t, err)
	return item
}

// The sniff rewrite: only a REAL identity change invalidates evidence, and when it does, BOTH lists
// go. A sniff that confirms the name the client already used is not a change.
func TestApplySniffOverrideDropsEvidenceOnlyWhenTheTargetChanges(t *testing.T) {
	resolved := []netip.Addr{netip.MustParseAddr("198.51.100.10")}

	for _, testCase := range []struct {
		name            string
		metadata        adapter.InboundContext
		wantChanged     bool
		wantDropped     bool
		wantDestination M.Socksaddr
		wantEvidence    bool
	}{
		{
			name: "IP destination rewritten to the sniffed name",
			metadata: adapter.InboundContext{
				Destination:          M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), 8443),
				Domain:               "new.example",
				MatchAddresses:       resolved,
				DestinationAddresses: resolved,
			},
			wantChanged:     true,
			wantDropped:     true,
			wantDestination: M.Socksaddr{Fqdn: "new.example", Port: 8443},
			wantEvidence:    false,
		},
		{
			name: "named destination replaced by a different sniffed name",
			metadata: adapter.InboundContext{
				Destination:    M.Socksaddr{Fqdn: "old.example", Port: 443},
				Domain:         "new.example",
				MatchAddresses: resolved,
			},
			wantChanged:     true,
			wantDropped:     true,
			wantDestination: M.Socksaddr{Fqdn: "new.example", Port: 443},
			wantEvidence:    false,
		},
		{
			name: "sniff confirms the name already in use",
			metadata: adapter.InboundContext{
				Destination:    M.Socksaddr{Fqdn: "same.example", Port: 443},
				Domain:         "same.example",
				MatchAddresses: resolved,
			},
			wantChanged:     false,
			wantDropped:     false,
			wantDestination: M.Socksaddr{Fqdn: "same.example", Port: 443},
			wantEvidence:    true,
		},
		{
			name: "no usable domain: nothing is rewritten",
			metadata: adapter.InboundContext{
				Destination:    M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), 443),
				Domain:         "not a domain",
				MatchAddresses: resolved,
			},
			wantChanged:     false,
			wantDropped:     false,
			wantDestination: M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), 443),
			wantEvidence:    true,
		},
		{
			name: "rewrite without evidence reports no drop",
			metadata: adapter.InboundContext{
				Destination: M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), 443),
				Domain:      "new.example",
			},
			wantChanged:     true,
			wantDropped:     false,
			wantDestination: M.Socksaddr{Fqdn: "new.example", Port: 443},
			wantEvidence:    false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			metadata := testCase.metadata
			changed, dropped := applySniffOverride(&metadata)
			require.Equal(t, testCase.wantChanged, changed)
			require.Equal(t, testCase.wantDropped, dropped)
			require.Equal(t, testCase.wantDestination, metadata.Destination)
			if testCase.wantEvidence {
				require.Equal(t, resolved, metadata.MatchAddresses)
			} else {
				require.Empty(t, metadata.MatchAddresses)
			}
			require.Empty(t, metadata.DestinationAddresses, "both address lists move together")
		})
	}
}

// The unrelated state a rewrite must not touch: it describes a different fact.
func TestSniffOverrideLeavesUnrelatedFactsAlone(t *testing.T) {
	metadata := adapter.InboundContext{
		Destination:              M.Socksaddr{Fqdn: "old.example", Port: 443},
		OriginDestination:        M.SocksaddrFrom(netip.MustParseAddr("198.18.0.9"), 443),
		RouteOriginalDestination: M.Socksaddr{Fqdn: "original.example", Port: 443},
		Domain:                   "new.example",
		Protocol:                 "tls",
		MatchAddresses:           []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}

	changed, dropped := applySniffOverride(&metadata)
	require.True(t, changed)
	require.True(t, dropped)
	require.Equal(t, M.SocksaddrFrom(netip.MustParseAddr("198.18.0.9"), 443), metadata.OriginDestination,
		"the original destination records where the flow came from and survives the rewrite")
	require.Equal(t, M.Socksaddr{Fqdn: "original.example", Port: 443}, metadata.RouteOriginalDestination)
	require.Equal(t, "new.example", metadata.Domain)
	require.Equal(t, "tls", metadata.Protocol)
}

// `override_port` is not a target identity change: the address evidence still describes the same
// server, so it must survive. This is the contrast that keeps the sniff fix from being applied too
// broadly.
func TestOverridePortKeepsAddressEvidence(t *testing.T) {
	metadata := adapter.InboundContext{
		Destination:          M.Socksaddr{Fqdn: "same.example", Port: 443},
		MatchAddresses:       []netip.Addr{netip.MustParseAddr("198.51.100.10")},
		DestinationAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
	}
	applyRouteOptionsMetadata(&metadata, &R.RuleActionRouteOptions{OverridePort: 8443})
	require.Equal(t, uint16(8443), metadata.Destination.Port)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("198.51.100.10")}, metadata.MatchAddresses)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("198.51.100.10")}, metadata.DestinationAddresses)
}

// A plain resolve followed by a match_only resolve: the second action publishes the NEWER answer for
// matching and must not delete the first action's dial candidates. The two channels are separate
// publications, and their contents may legitimately be different answers.
func TestMatchOnlyAfterDialResolveKeepsCandidatesAndReplacesMatchingEvidence(t *testing.T) {
	resolver := &countingDNS{addresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")}}
	router := newResolveTestRouter(resolver)
	metadata := &adapter.InboundContext{Destination: M.Socksaddr{Fqdn: "order.example", Port: 443}}

	require.NoError(t, router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{}))
	require.Equal(t, []netip.Addr{netip.MustParseAddr("198.51.100.10")}, metadata.DestinationAddresses)
	require.Empty(t, metadata.MatchAddresses)

	resolver.addresses = []netip.Addr{netip.MustParseAddr("203.0.113.9")}
	require.NoError(t, router.actionResolve(context.Background(), metadata, &R.RuleActionResolve{MatchOnly: true}))

	require.Equal(t, []netip.Addr{netip.MustParseAddr("198.51.100.10")}, metadata.DestinationAddresses,
		"the earlier action's dial candidates are legitimate and must survive")
	require.Equal(t, []netip.Addr{netip.MustParseAddr("203.0.113.9")}, metadata.MatchAddresses,
		"the newer answer is the one published for matching")

	require.True(t, newIndexCIDRItem(t, "203.0.113.0/24").Match(metadata),
		"IP rules use the newest matching publication")
	require.False(t, newIndexCIDRItem(t, "198.51.100.0/24").Match(metadata),
		"the older dial candidate must not decide the rule while newer matching evidence exists")
}

// --- test doubles for the three rewrite sites -----------------------------------------------

// sniffStreamConn is a net.Conn whose read side is a fixed ClientHello.
type sniffStreamConn struct {
	reader *bytes.Reader
}

func (c *sniffStreamConn) Read(p []byte) (int, error)       { return c.reader.Read(p) }
func (c *sniffStreamConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *sniffStreamConn) Close() error                     { return nil }
func (c *sniffStreamConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *sniffStreamConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *sniffStreamConn) SetDeadline(time.Time) error      { return nil }
func (c *sniffStreamConn) SetReadDeadline(time.Time) error  { return nil }
func (c *sniffStreamConn) SetWriteDeadline(time.Time) error { return nil }

// sniffPacketConn is the packet connection the packet arm needs to exist. The fixture is handed in
// as a pre-read buffer, so no datagram is actually read from it.
type sniffPacketConn struct{}

func (c *sniffPacketConn) ReadPacket(*buf.Buffer) (M.Socksaddr, error) { return M.Socksaddr{}, io.EOF }
func (c *sniffPacketConn) WritePacket(*buf.Buffer, M.Socksaddr) error  { return nil }
func (c *sniffPacketConn) Close() error                                { return nil }
func (c *sniffPacketConn) LocalAddr() net.Addr                         { return &net.UDPAddr{} }
func (c *sniffPacketConn) SetDeadline(time.Time) error                 { return nil }
func (c *sniffPacketConn) SetReadDeadline(time.Time) error             { return nil }
func (c *sniffPacketConn) SetWriteDeadline(time.Time) error            { return nil }

// stubRule is an adapter.Rule whose condition and action are supplied directly, so a test can put a
// sniff action the configuration surface no longer exposes in front of the real rule walk.
type stubRule struct {
	matches func(*adapter.InboundContext) bool
	action  adapter.RuleAction
	// seen is the metadata as it looked when this rule was evaluated; nil when it was never reached.
	seen *adapter.InboundContext
}

func (r *stubRule) Match(metadata *adapter.InboundContext) bool {
	captured := *metadata
	r.seen = &captured
	if r.matches == nil {
		return true
	}
	return r.matches(metadata)
}

func (r *stubRule) Type() string { return C.RuleTypeDefault }

func (r *stubRule) Start() error { return nil }

func (r *stubRule) Close() error { return nil }

func (r *stubRule) Action() adapter.RuleAction { return r.action }

func (r *stubRule) String() string {
	if r.action == nil {
		return "stub"
	}
	return r.action.String()
}

// tlsClientHelloRecord captures the first TLS flight a real crypto/tls client sends for this SNI.
func tlsClientHelloRecord(t *testing.T, serverName string) []byte {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})
	go func() {
		// The handshake cannot complete (no server), and its error is irrelevant: the first flight is
		// already on the wire and is what the sniffer parses.
		_ = tls.Client(clientConn, &tls.Config{ServerName: serverName, InsecureSkipVerify: true}).Handshake()
	}()
	require.NoError(t, serverConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	header := make([]byte, 5)
	_, err := io.ReadFull(serverConn, header)
	require.NoError(t, err)
	body := make([]byte, int(binary.BigEndian.Uint16(header[3:5])))
	_, err = io.ReadFull(serverConn, body)
	require.NoError(t, err)
	return append(header, body...)
}

func sniffTestRouter(dnsRouter adapter.DNSRouter) *Router {
	return &Router{
		ctx:    context.Background(),
		logger: log.NewNOPFactory().Logger(),
		dns:    dnsRouter,
	}
}

// quicClientInitial captures the first QUIC Initial a real quic-go client sends for this SNI.
//
// It is generated rather than shipped as a fixture because the single-datagram PreMatch path can
// only carry a ClientHello that fits in one datagram: a browser capture is fragmented, and a
// quic-go client with a minimal configuration is not.
func quicClientInitial(t *testing.T, serverName string) []byte {
	t.Helper()
	serverConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	defer serverConn.Close()
	clientConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	require.NoError(t, err)
	defer clientConn.Close()

	captured := make(chan []byte, 1)
	go func() {
		buffer := make([]byte, 2048)
		_ = serverConn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _, readErr := serverConn.ReadFromUDP(buffer)
		if readErr != nil {
			return
		}
		captured <- append([]byte(nil), buffer[:n]...)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// The handshake cannot complete (this side never answers), and its error is irrelevant: the
		// Initial datagram is what the sniffer parses.
		_, _ = quic.Dial(ctx, clientConn, serverConn.LocalAddr(), &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: true,
			NextProtos:         []string{"h3"},
			// A single curve keeps the ClientHello inside one datagram: Go's default post-quantum
			// key share makes the hello exceed the Initial's capacity and the sniffer would need the
			// next datagram, which the single-datagram PreMatch path never provides.
			CurvePreferences: []tls.CurveID{tls.X25519},
		}, &quic.Config{})
	}()
	select {
	case packet := <-captured:
		cancel()
		return packet
	case <-time.After(5 * time.Second):
		t.Fatal("the QUIC client sent no Initial datagram")
		return nil
	}
}

// reverseMappingDNS answers forward lookups and reports no reverse mapping.
type reverseMappingDNS struct {
	adapter.DNSRouter
	addresses []netip.Addr
}

func (r *reverseMappingDNS) Lookup(context.Context, string, adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return r.addresses, nil
}

func (r *reverseMappingDNS) LookupReverseMapping(netip.Addr) (string, bool) {
	return "", false
}

// --- the three rewrite sites ----------------------------------------------------------------

// The stream arm: a TLS ClientHello on a TCP flow.
func TestSniffOverrideInvalidatesEvidenceOnTheStreamArm(t *testing.T) {
	hello := tlsClientHelloRecord(t, "new.example")

	for _, testCase := range []struct {
		name       string
		override   bool
		wantTarget string
	}{
		{name: "override on", override: true, wantTarget: "new.example"},
		{name: "override off", override: false, wantTarget: "old.example"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := sniffTestRouter(nil)
			metadata := &adapter.InboundContext{
				Network:              N.NetworkTCP,
				Destination:          M.Socksaddr{Fqdn: "old.example", Port: 443},
				MatchAddresses:       []netip.Addr{netip.MustParseAddr("198.51.100.10")},
				DestinationAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
			}
			buffer, _, err := router.actionSniff(context.Background(), metadata,
				&R.RuleActionSniff{OverrideDestination: testCase.override},
				&sniffStreamConn{reader: bytes.NewReader(hello)}, nil, nil, nil)
			require.NoError(t, err)
			if buffer != nil {
				buffer.Release()
			}

			require.Equal(t, "new.example", metadata.Domain, "the sniffer recovered the SNI in both cases")
			require.Equal(t, "tls", metadata.Protocol)
			require.Equal(t, testCase.wantTarget, metadata.Destination.Fqdn)
			require.Equal(t, uint16(443), metadata.Destination.Port, "the port is preserved")
			if testCase.override {
				require.Empty(t, metadata.MatchAddresses,
					"the previous target's matching evidence must not survive the rewrite")
				require.Empty(t, metadata.DestinationAddresses,
					"and neither must its dial candidates")
			} else {
				require.NotEmpty(t, metadata.MatchAddresses, "no rewrite, no invalidation")
				require.NotEmpty(t, metadata.DestinationAddresses)
			}
		})
	}
}

// The packet arm of the same function: a QUIC Initial on a datagram flow.
func TestSniffOverrideInvalidatesEvidenceOnThePacketArm(t *testing.T) {
	initial := quicClientInitial(t, "new.example")

	for _, testCase := range []struct {
		name       string
		override   bool
		wantTarget string
	}{
		{name: "override on", override: true, wantTarget: "new.example"},
		{name: "override off", override: false, wantTarget: "old.example"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := sniffTestRouter(nil)
			metadata := &adapter.InboundContext{
				Network:              N.NetworkUDP,
				Destination:          M.Socksaddr{Fqdn: "old.example", Port: 443},
				MatchAddresses:       []netip.Addr{netip.MustParseAddr("198.51.100.10")},
				DestinationAddresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")},
			}
			packetBuffers := []*N.PacketBuffer{
				{Buffer: buf.As(initial), Destination: metadata.Destination},
			}
			buffer, retained, err := router.actionSniff(context.Background(), metadata,
				&R.RuleActionSniff{OverrideDestination: testCase.override},
				nil, &sniffPacketConn{}, nil, packetBuffers)
			require.NoError(t, err)
			require.Empty(t, retained, "a complete ClientHello is not retained as a fragment")
			if buffer != nil {
				buffer.Release()
			}

			require.Equal(t, "new.example", metadata.Domain)
			require.Equal(t, "quic", metadata.Protocol)
			require.Equal(t, testCase.wantTarget, metadata.Destination.Fqdn)
			require.Equal(t, uint16(443), metadata.Destination.Port)
			if testCase.override {
				require.Empty(t, metadata.MatchAddresses)
				require.Empty(t, metadata.DestinationAddresses)
			} else {
				require.NotEmpty(t, metadata.MatchAddresses)
				require.NotEmpty(t, metadata.DestinationAddresses)
			}
		})
	}
}

// The pre-match loop: the same rewrite, then a rule that would reject the flow if the old answer
// survived. The capture rule at the end only runs when the CIDR rule did NOT fire, which is the
// differential; the control proves the CIDR rule is live before the rewrite.
func TestPreMatchSniffOverrideDropsEvidenceBeforeTheNextRule(t *testing.T) {
	initial := quicClientInitial(t, "new.example")

	buildMetadata := func() adapter.InboundContext {
		return adapter.InboundContext{
			Inbound:     "tun-in",
			InboundType: C.TypeTun,
			Network:     N.NetworkUDP,
			Source:      M.SocksaddrFrom(netip.MustParseAddr("198.18.0.2"), 40000),
			Destination: M.Socksaddr{Fqdn: "old.example", Port: 443},
		}
	}
	resolveStub := func() *stubRule {
		return &stubRule{action: &R.RuleActionResolve{MatchOnly: true}}
	}
	sniffStub := func() *stubRule {
		return &stubRule{action: &R.RuleActionSniff{OverrideDestination: true}}
	}
	cidrRejectStub := func(t *testing.T) *stubRule {
		item := newIndexCIDRItem(t, "198.51.100.0/24")
		return &stubRule{
			matches: item.Match,
			action:  &R.RuleActionReject{Method: C.RuleActionRejectMethodDefault},
		}
	}
	captureStub := func() *stubRule {
		return &stubRule{action: &R.RuleActionReject{Method: C.RuleActionRejectMethodDefault}}
	}

	t.Run("the rewrite stops the old answer from firing the rule", func(t *testing.T) {
		capture := captureStub()
		router := sniffTestRouter(&reverseMappingDNS{addresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")}})
		router.rules = []adapter.Rule{
			resolveStub(),
			sniffStub(),
			cidrRejectStub(t),
			capture,
		}

		result := router.PreMatch(buildMetadata(), initial)
		require.Equal(t, adapter.PreMatchReject, result.Action,
			"the walk must continue past the CIDR rule and end at the capture rule")
		require.NotNil(t, capture.seen,
			"the CIDR rule must NOT fire: reaching the capture rule is the proof the evidence was dropped")
		require.Equal(t, "new.example", capture.seen.Destination.Fqdn,
			"the destination is the sniffed name by the time the next rule sees it")
		require.Equal(t, uint16(443), capture.seen.Destination.Port)
		require.Empty(t, capture.seen.MatchAddresses)
		require.Empty(t, capture.seen.DestinationAddresses)
	})

	t.Run("control: before the rewrite the same rule fires", func(t *testing.T) {
		capture := captureStub()
		router := sniffTestRouter(&reverseMappingDNS{addresses: []netip.Addr{netip.MustParseAddr("198.51.100.10")}})
		router.rules = []adapter.Rule{
			resolveStub(),
			cidrRejectStub(t),
			sniffStub(),
			capture,
		}

		result := router.PreMatch(buildMetadata(), initial)
		require.Equal(t, adapter.PreMatchReject, result.Action)
		require.Nil(t, capture.seen,
			"the control proves the rule is live while the destination is still the old target")
	})
}

// The action the configuration surface can build never carries the flag, which is why the three
// branches above are latent. This pins the reachability fact the report states: a sniff action
// decoded from any option the schema accepts has OverrideDestination == false.
func TestSniffActionFromConfigurationNeverEnablesOverrideDestination(t *testing.T) {
	action, err := R.NewRuleAction(log.NewNOPFactory().Logger(), option.RuleAction{
		Action:       C.RuleActionTypeSniff,
		SniffOptions: option.RouteActionSniff{Sniffer: []string{"tls"}},
	})
	require.NoError(t, err)
	sniffAction, isSniff := action.(*R.RuleActionSniff)
	require.True(t, isSniff)
	require.False(t, sniffAction.OverrideDestination,
		"no option field feeds this flag today; the override branches are unreachable from a config")
}

// FuzzApplySniffOverride pins the invariants the rewrite must never break, for arbitrary domain
// strings and destination shapes: a reported change always yields the sniffed name with the port
// preserved, an unrewritten destination is untouched, and evidence is dropped exactly when the
// target changed.
func FuzzApplySniffOverride(f *testing.F) {
	f.Add("new.example", "old.example", uint16(443), true)
	f.Add("new.example", "new.example", uint16(443), true)
	f.Add("", "old.example", uint16(443), true)
	f.Add("not a domain", "old.example", uint16(80), true)
	f.Add("new.example", "", uint16(443), false)
	f.Add("a.b.c", "x.y", uint16(1), false)

	f.Fuzz(func(t *testing.T, domain string, destinationName string, port uint16, evidence bool) {
		metadata := adapter.InboundContext{
			Domain: domain,
		}
		if destinationName == "" {
			metadata.Destination = M.SocksaddrFrom(netip.MustParseAddr("203.0.113.5"), port)
		} else {
			metadata.Destination = M.Socksaddr{Fqdn: destinationName, Port: port}
		}
		if evidence {
			metadata.MatchAddresses = []netip.Addr{netip.MustParseAddr("198.51.100.10")}
			metadata.DestinationAddresses = []netip.Addr{netip.MustParseAddr("203.0.113.9")}
		}
		original := metadata.Destination

		changed, dropped := applySniffOverride(&metadata)

		if !M.IsDomainName(domain) {
			require.False(t, changed, "a value that is not a domain name is never a rewrite source")
			require.False(t, dropped)
			require.Equal(t, original, metadata.Destination)
			return
		}
		rewritten := M.Socksaddr{Fqdn: domain, Port: original.Port}
		if rewritten == original {
			require.False(t, changed, "confirming the same target is not a change")
			require.False(t, dropped)
			require.Equal(t, original, metadata.Destination)
			require.Equal(t, evidence, len(metadata.MatchAddresses) > 0)
			require.Equal(t, evidence, len(metadata.DestinationAddresses) > 0)
			return
		}
		require.True(t, changed)
		require.Equal(t, rewritten, metadata.Destination)
		require.Equal(t, original.Port, metadata.Destination.Port, "the port is preserved")
		require.Equal(t, evidence, dropped, "evidence is reported dropped exactly when it existed")
		require.Empty(t, metadata.MatchAddresses)
		require.Empty(t, metadata.DestinationAddresses)
	})
}
