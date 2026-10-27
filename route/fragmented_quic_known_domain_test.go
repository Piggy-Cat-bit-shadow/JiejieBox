package route

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// TestPacketSniffKnownFakeIPDomainDecision pins the branch that stops a fragmented-QUIC sniff from
// waiting on a flow whose domain is already authoritative. The real TUN/FakeIP path rewrites
// Destination to Fqdn and sets FakeIP, but intentionally leaves Domain empty; a check on Domain
// alone never fires there. An explicit packet sniffer keeps the full protocol contract.
func TestPacketSniffKnownFakeIPDomainDecision(t *testing.T) {
	fqdnMetadata := &adapter.InboundContext{
		FakeIP:      true,
		Destination: M.ParseSocksaddrHostPort("rr1---sn-a5meknsy.googlevideo.com", 443),
	}
	domainMetadata := &adapter.InboundContext{Domain: "rr1---sn-a5meknsy.googlevideo.com"}
	emptyAction := &rule.RuleActionSniff{}
	explicitAction := &rule.RuleActionSniff{PacketSniffers: []sniff.PacketSniffer{sniff.QUICClientHello}}

	testCases := []struct {
		name     string
		metadata *adapter.InboundContext
		action   *rule.RuleActionSniff
		err      error
		expected bool
	}{
		{"fakeip Fqdn, default sniffers, fragmented", fqdnMetadata, emptyAction, sniff.ErrNeedMoreData, true},
		{"domain, default sniffers, fragmented", domainMetadata, emptyAction, sniff.ErrNeedMoreData, true},
		{"fakeip Fqdn, explicit sniffer keeps its contract", fqdnMetadata, explicitAction, sniff.ErrNeedMoreData, false},
		{"no domain, default sniffers", &adapter.InboundContext{}, emptyAction, sniff.ErrNeedMoreData, false},
		{"Fqdn without FakeIP is not authoritative here", &adapter.InboundContext{Destination: fqdnMetadata.Destination}, emptyAction, sniff.ErrNeedMoreData, false},
		{"fakeip Fqdn, non-fragmented error", fqdnMetadata, emptyAction, io.EOF, false},
		{"fakeip Fqdn, nil error", fqdnMetadata, emptyAction, nil, false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := packetSniffUsesKnownFakeIPDomain(testCase.metadata, testCase.action, testCase.err); got != testCase.expected {
				t.Fatalf("got %v, want %v", got, testCase.expected)
			}
		})
	}
}

// oneDatagramPacketConn serves one datagram and then EOF, so an implementation that reads a second
// datagram to complete the ClientHello is distinguishable from one that trusts the FakeIP domain.
type oneDatagramPacketConn struct {
	packet []byte
	reads  int
}

func (c *oneDatagramPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	if c.reads > 0 {
		return M.Socksaddr{}, io.EOF
	}
	c.reads++
	_, _ = buffer.Write(c.packet)
	return M.ParseSocksaddrHostPort("203.0.113.9", 443), nil
}

func (c *oneDatagramPacketConn) WritePacket(*buf.Buffer, M.Socksaddr) error { return nil }
func (c *oneDatagramPacketConn) Close() error                               { return nil }
func (c *oneDatagramPacketConn) LocalAddr() net.Addr                        { return &net.UDPAddr{} }
func (c *oneDatagramPacketConn) SetDeadline(time.Time) error                { return nil }
func (c *oneDatagramPacketConn) SetReadDeadline(time.Time) error            { return nil }
func (c *oneDatagramPacketConn) SetWriteDeadline(time.Time) error           { return nil }

// TestKnownFakeIPDestinationStopsFragmentedSniffOnFirstDatagram is the production-shaped regression:
// the domain lives in Destination.Fqdn (FakeIP), not Domain. The first datagram must be kept for
// replay, no second read may happen, and the sniff must return without a fatal error.
func TestKnownFakeIPDestinationStopsFragmentedSniffOnFirstDatagram(t *testing.T) {
	previousDefaults := defaultPacketSniffers
	defaultPacketSniffers = []sniff.PacketSniffer{
		func(context.Context, *adapter.InboundContext, []byte) error {
			return sniff.ErrNeedMoreData
		},
	}
	defer func() { defaultPacketSniffers = previousDefaults }()

	recorder := newSniffLogRecorder()
	router := &Router{logger: recorder}
	firstDatagram := []byte("one-datagram-that-needs-more-data")
	fixture := &oneDatagramPacketConn{packet: firstDatagram}
	metadata := adapter.InboundContext{
		FakeIP:      true,
		Destination: M.ParseSocksaddrHostPort("rr1---sn-a5meknsy.googlevideo.com", 443),
	}
	action := &rule.RuleActionSniff{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, packetBuffers, err := router.actionSniff(ctx, &metadata, action, nil, fixture, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, fixture.reads, "the known FakeIP domain must not trigger a second read")
	require.Len(t, packetBuffers, 1, "the first datagram must be retained for replay")
	require.Equal(t, firstDatagram, packetBuffers[0].Buffer.Bytes(), "replay must carry the original bytes")
	N.ReleaseMultiPacketBuffer(packetBuffers)
}
