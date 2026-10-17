package direct

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// Every connection this outbound reports must name its destination.
//
// # The production evidence
//
// The log contains sixteen pairs of the form
//
//	INFO  outbound/direct[direct]: outbound packet connection
//	ERROR connection: listen packet connection using  using outbound/direct[direct]:
//	      local network has no public ipv6 path: ...
//
// with nothing identifying the flow. Every other outbound reports `outbound packet connection to
// host:port`, and this outbound's own stream and connected-packet reports do too, so the two methods that
// create a packet listener directly were the ones that dropped it.
//
// A packet connection is where the destination matters most: the family refusal happens BEFORE a socket
// exists, so this line is the only record of what was asked for. An operator could see that a packet flow
// was refused and could not see which one.

// recordingLogger captures formatted InfoContext lines and delegates everything else to a real no-op
// logger, so only the reports under test are recorded.
type recordingLogger struct {
	log.ContextLogger
	access sync.Mutex
	lines  []string
}

func newRecordingLogger() *recordingLogger {
	return &recordingLogger{ContextLogger: log.NewNOPFactory().Logger()}
}

func (l *recordingLogger) InfoContext(ctx context.Context, args ...any) {
	l.access.Lock()
	defer l.access.Unlock()
	l.lines = append(l.lines, fmt.Sprint(args...))
}

func (l *recordingLogger) reported() []string {
	l.access.Lock()
	defer l.access.Unlock()
	return append([]string(nil), l.lines...)
}

// newReportingOutbound builds the production object graph with a logger that records, so the assertions see
// what a real outbound writes rather than a hand-built line.
func newReportingOutbound(t *testing.T) (*Outbound, *recordingLogger) {
	t.Helper()
	recorder := newRecordingLogger()
	ctx := service.ContextWithDefaultRegistry(context.Background())
	built, err := NewOutbound(ctx, nil, recorder, "direct", option.DirectOutboundOptions{})
	require.NoError(t, err)
	outbound, isOutbound := built.(*Outbound)
	require.True(t, isOutbound)
	return outbound, recorder
}

// closePacketConn closes whatever a listener call returned, so a test that only wanted the log line does not
// leave a socket behind. The destination is the loopback address, so nothing leaves the machine.
func closePacketConn(conn net.PacketConn) {
	if conn != nil {
		_ = conn.Close()
	}
}

// TestPacketReportsNameTheDestination is the regression, asserted through the real constructor.
func TestPacketReportsNameTheDestination(t *testing.T) {
	// A loopback destination keeps the test hermetic: the family gate refuses nothing on a context with no
	// network manager, so these calls really do create a socket, and it must be a local one.
	target := M.SocksaddrFrom(netip.IPv6Loopback(), 8000)

	for _, testCase := range []struct {
		name string
		call func(*Outbound) []string
	}{
		{
			name: "ListenPacket",
			call: func(o *Outbound) []string {
				conn, _ := o.ListenPacket(context.Background(), target)
				closePacketConn(conn)
				return nil
			},
		},
		{
			name: "ListenSerialNetworkPacket",
			call: func(o *Outbound) []string {
				conn, _, _ := o.ListenSerialNetworkPacket(context.Background(), target, nil, nil, nil, nil, 0)
				closePacketConn(conn)
				return nil
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			outbound, logger := newReportingOutbound(t)
			testCase.call(outbound)

			var packetLine string
			for _, line := range logger.reported() {
				if strings.Contains(line, "outbound packet connection") {
					packetLine = line
					break
				}
			}
			require.NotEmpty(t, packetLine, "%s must report the connection it attempted", testCase.name)
			require.NotEqual(t, "outbound packet connection", packetLine,
				"the bare phrase is the defect: it names no destination")
			require.Contains(t, packetLine, target.String(),
				"the report must name the destination, because a packet refusal happens before any socket "+
					"exists and this line is the only record of which flow was asked for")
		})
	}
}

// TestStreamReportNamesTheDestination is the control: the stream path already did this, so a future
// refactor cannot pass by fixing only the packet half.
func TestStreamReportNamesTheDestination(t *testing.T) {
	target := M.SocksaddrFrom(netip.IPv6Loopback(), 443)

	outbound, logger := newReportingOutbound(t)
	_, _ = outbound.DialContext(context.Background(), N.NetworkTCP, target)

	var streamLine string
	for _, line := range logger.reported() {
		if strings.Contains(line, "outbound connection") {
			streamLine = line
			break
		}
	}
	require.NotEmpty(t, streamLine)
	require.Contains(t, streamLine, target.String())
}

// TestNoReportIsWrittenWithoutADestination is the structural half, read from the source because there is no
// seam that observes the shape of every logging call at once.
//
// The bare forms are matched as complete `InfoContext` calls rather than as substrings, so a comment that
// quotes the defect does not count as the defect.
func TestNoReportIsWrittenWithoutADestination(t *testing.T) {
	source, err := os.ReadFile("outbound.go")
	require.NoError(t, err, "this test reads the file it asserts about")
	text := string(source)

	for _, bare := range []string{
		`InfoContext(ctx, "outbound connection")`,
		`InfoContext(ctx, "outbound packet connection")`,
	} {
		require.NotContains(t, text, bare,
			"a report written as %s names no destination, so a refusal cannot be attributed to a flow", bare)
	}

	// And the destination-carrying forms must still be there, so this cannot pass by deleting the reports.
	for _, carrying := range []string{
		`InfoContext(ctx, "outbound connection to ", destination)`,
		`InfoContext(ctx, "outbound packet connection to ", destination)`,
	} {
		require.Contains(t, text, carrying,
			"the destination-carrying report %s must still exist", carrying)
	}
}
