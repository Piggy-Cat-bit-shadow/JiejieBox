package route

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// guardScriptedSource hands out a fixed list of datagrams, then reports an error.
type guardScriptedSource struct {
	datagrams []M.Socksaddr
	index     int
}

func (s *guardScriptedSource) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	if s.index >= len(s.datagrams) {
		return M.Socksaddr{}, errors.New("scripted source exhausted")
	}
	destination := s.datagrams[s.index]
	s.index++
	_, err := buffer.WriteString("datagram")
	if err != nil {
		return M.Socksaddr{}, err
	}
	return destination, nil
}

func (s *guardScriptedSource) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	buffer.Release()
	return nil
}

func (s *guardScriptedSource) Close() error { return nil }

// N.PacketConn carries the address and deadline methods as well as the two packet ones.
func (s *guardScriptedSource) LocalAddr() net.Addr { return &net.UDPAddr{} }

func (s *guardScriptedSource) SetDeadline(t time.Time) error      { return nil }
func (s *guardScriptedSource) SetReadDeadline(t time.Time) error  { return nil }
func (s *guardScriptedSource) SetWriteDeadline(t time.Time) error { return nil }

// guardFailingDNS stands in for a resolver that cannot answer. The embedded interface supplies the rest of
// adapter.DNSRouter, which this test never reaches.
type guardFailingDNS struct {
	adapter.DNSRouter
	err error
}

func (r *guardFailingDNS) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return nil, r.err
}

// whyRejectedNotJustThatItWas is the reason hook's whole purpose.
//
// `g.rejectErr` was written at four sites and read at none, while the production hook logged the FIXED
// string "rejected by route rule" whatever had happened. Two very different situations therefore produced
// the same log line:
//
//   - the rules looked at this destination and refused it (go and read the rules), and
//   - the destination could not be judged at all because the lookup behind it failed (go and read the DNS
//     path; the rules were never consulted).
//
// An operator debugging a UDP session sees only the log, so reporting both as a rule rejection sends them
// to the wrong one - and the guard is the last place that still knows which happened.
//
// This drives a real resolution failure through the guard and asserts the hook receives it. A test that
// only checked "the hook was called" would pass on the bug, which is exactly how it survived.
func TestPacketGuardReportsTheReasonNotJustTheRefusal(t *testing.T) {
	resolverErr := errors.New("resolver unavailable")
	source := &guardScriptedSource{datagrams: []M.Socksaddr{
		{Fqdn: "unresolvable.example", Port: 443},
	}}

	var (
		reported []M.Socksaddr
		reasons  []error
	)
	guard := &packetDestinationGuard{
		PacketConn: source,
		router: &Router{
			ctx: context.Background(),
			dns: &guardFailingDNS{err: resolverErr},
		},
		ctx:      context.Background(),
		metadata: adapter.InboundContext{},
		allowed:  make(map[string]struct{}),
		rejected: make(map[string]struct{}),
		onReject: func(destination M.Socksaddr, reason error) {
			reported = append(reported, destination)
			reasons = append(reasons, reason)
		},
	}

	// The first read is refused, so the guard loops and the source is then exhausted; the error the
	// caller sees is the source's, which is fine - what is under test is the hook.
	_, _ = guard.ReadPacket(buf.New())

	require.Len(t, reported, 1, "the refusal must be reported once")
	require.Equal(t, "unresolvable.example", reported[0].Fqdn)
	require.ErrorIs(t, reasons[0], resolverErr,
		"the hook must receive the RESOLUTION failure. Reporting a rule rejection here sends the reader "+
			"to rules that were never consulted, which is what the fixed log line did")
}

var (
	_ N.PacketConn      = (*guardScriptedSource)(nil)
	_ adapter.DNSRouter = (*guardFailingDNS)(nil)
)
