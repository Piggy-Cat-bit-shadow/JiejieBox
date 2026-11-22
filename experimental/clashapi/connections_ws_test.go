package clashapi

// Tests for the connections WebSocket frame-buffer high-water policy.
//
// # What the policy protects against
//
// The stream encodes one JSON document per tick into a bytes.Buffer. Buffer.Reset keeps the backing
// array, so a dashboard that once saw a large connection list keeps that array for as long as the
// tab is open. These tests measure the frame sizes, the retained capacity, the live heap and the
// process working set when the list shrinks, separately for the old (never release) and new
// (release after sustained smaller frames) behaviour.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/ws"
	"github.com/sagernet/ws/wsutil"

	"github.com/go-chi/render"
	"github.com/stretchr/testify/require"
)

// trafficFixture is a real traffic manager with real tracked connections.
type trafficFixture struct {
	manager *trafficcontrol.Manager
	tracked []net.Conn
	peers   []net.Conn
}

// newTrafficFixture creates count live tracked connections. They are never read or written: the
// manager only needs their metadata.
func newTrafficFixture(t testing.TB, count int) *trafficFixture {
	t.Helper()
	manager := trafficcontrol.NewManager()
	outbound := &fixtureOutbound{tag: "node", typ: "shadowsocks", networks: []string{"tcp", "udp"}}
	fixture := &trafficFixture{manager: manager}
	destination := M.ParseSocksaddr("example.com:443")
	for index := 0; index < count; index++ {
		client, server := net.Pipe()
		metadata := adapter.InboundContext{
			Inbound:       "mixed-in",
			InboundType:   "mixed",
			Network:       "tcp",
			Source:        M.ParseSocksaddr(fmt.Sprintf("127.0.0.1:%d", 20000+index)),
			Destination:   destination,
			Domain:        "example.com",
			OutboundChain: []adapter.Outbound{outbound},
		}
		tracked := manager.RoutedConnection(context.Background(), server, metadata, nil, outbound)
		fixture.tracked = append(fixture.tracked, tracked)
		fixture.peers = append(fixture.peers, client)
	}
	t.Cleanup(func() {
		for _, tracked := range fixture.tracked {
			if tracked == nil {
				continue
			}
			_ = tracked.Close()
		}
		for _, peer := range fixture.peers {
			if peer == nil {
				continue
			}
			_ = peer.Close()
		}
	})
	return fixture
}

// closeRange removes the tracked connections [from, to).
func (f *trafficFixture) closeRange(from, to int) {
	for index := from; index < to && index < len(f.tracked); index++ {
		if f.tracked[index] != nil {
			_ = f.tracked[index].Close()
			f.tracked[index] = nil
		}
		if f.peers[index] != nil {
			_ = f.peers[index].Close()
			f.peers[index] = nil
		}
	}
}

// TestWSFrameBufferPolicy pins the release policy: correctness of the retention decision, including
// the two anti-churn guards.
func TestWSFrameBufferPolicy(t *testing.T) {
	large := bytes.Repeat([]byte("x"), 512<<10)
	medium := bytes.Repeat([]byte("x"), 200<<10)
	small := bytes.Repeat([]byte("x"), 2<<10)

	t.Run("releases after two sustained small frames", func(t *testing.T) {
		buffer := &wsFrameBuffer{}
		require.NoError(t, buffer.encode(large))
		require.Greater(t, buffer.capacity(), wsFrameBufferFloor)

		// First small frame: one dip is not enough.
		require.NoError(t, buffer.encode(small))
		require.False(t, buffer.releaseHighWater(len(small)))
		require.Greater(t, buffer.capacity(), wsFrameBufferFloor)

		// Second consecutive small frame: the capacity is no longer justified.
		require.NoError(t, buffer.encode(small))
		require.True(t, buffer.releaseHighWater(len(small)))
		require.Zero(t, buffer.capacity())
		require.Equal(t, 1, buffer.releases)

		// The next frame grows a fresh, small buffer instead of reusing the old array.
		require.NoError(t, buffer.encode(small))
		require.Less(t, buffer.capacity(), 64<<10)
	})

	t.Run("keeps a buffer the current frame still uses", func(t *testing.T) {
		buffer := &wsFrameBuffer{}
		require.NoError(t, buffer.encode(large))
		capacity := buffer.capacity()
		for tick := 0; tick < 5; tick++ {
			require.NoError(t, buffer.encode(medium))
			require.False(t, buffer.releaseHighWater(len(medium)),
				"a stream that keeps producing large frames must not reallocate every tick")
		}
		require.Equal(t, capacity, buffer.capacity())
	})

	t.Run("never releases small buffers", func(t *testing.T) {
		buffer := &wsFrameBuffer{}
		tiny := bytes.Repeat([]byte("x"), 1024)
		require.NoError(t, buffer.encode(tiny))
		require.LessOrEqual(t, buffer.capacity(), wsFrameBufferFloor)
		capacity := buffer.capacity()
		for tick := 0; tick < 10; tick++ {
			require.NoError(t, buffer.encode(tiny))
			require.False(t, buffer.releaseHighWater(len(tiny)))
		}
		require.Equal(t, capacity, buffer.capacity())
	})

	t.Run("a large frame resets the streak", func(t *testing.T) {
		buffer := &wsFrameBuffer{}
		require.NoError(t, buffer.encode(large))
		require.NoError(t, buffer.encode(small))
		require.False(t, buffer.releaseHighWater(len(small)))
		// A single large frame in between proves the stream still needs the capacity.
		require.NoError(t, buffer.encode(large))
		require.False(t, buffer.releaseHighWater(len(large)))
		require.NoError(t, buffer.encode(small))
		require.False(t, buffer.releaseHighWater(len(small)), "the streak was reset by the large frame")
	})

	t.Run("frame bytes are unchanged", func(t *testing.T) {
		buffer := &wsFrameBuffer{}
		snapshot := render.M{"connections": []any{}, "downloadTotal": 1, "uploadTotal": 2, "memory": 3}
		require.NoError(t, buffer.encode(snapshot))
		expected, err := json.Marshal(snapshot)
		require.NoError(t, err)
		require.Equal(t, append(expected, '\n'), buffer.bytes())
	})
}

// bufferedConn presents the buffered reader returned by ws.Dial as an io.ReadWriter, so frame reads
// consume bytes that arrived together with the handshake response.
type bufferedConn struct {
	reader io.Reader
	writer io.Writer
}

func (c *bufferedConn) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *bufferedConn) Write(p []byte) (int, error) { return c.writer.Write(p) }

// TestAConnectionsWebSocketFrames drives the real handler over a real WebSocket connection and
// checks that the frame layout and message flow are the ones panels consume.
func TestAConnectionsWebSocketFrames(t *testing.T) {
	fixture := newTrafficFixture(t, 50)
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(connectionRouter(ctx, nil, fixture.manager))
	t.Cleanup(func() {
		cancel()
		server.Close()
	})

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/?interval=100"
	conn, reader, _, err := ws.Dial(context.Background(), url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	// ws.Dial returns a pooled buffered reader ONLY when the server sent bytes together with the
	// handshake response; otherwise it is nil and the connection is read directly.
	stream := &bufferedConn{reader: conn, writer: conn}
	if reader != nil {
		stream.reader = reader
		t.Cleanup(func() { ws.PutReader(reader) })
	}

	readSnapshot := func() map[string]json.RawMessage {
		payload, readErr := wsutil.ReadServerText(stream)
		require.NoError(t, readErr)
		var document map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(payload, &document))
		return document
	}

	first := readSnapshot()
	require.Len(t, first, 4, "the frame keeps exactly the four documented keys")
	for _, key := range []string{"connections", "downloadTotal", "memory", "uploadTotal"} {
		require.Contains(t, first, key)
	}
	var connections []map[string]any
	require.NoError(t, json.Unmarshal(first["connections"], &connections))
	require.Len(t, connections, 50)
	require.Equal(t, "mixed/mixed-in", connections[0]["metadata"].(map[string]any)["type"])

	// Shrink the list; the next frame must report the smaller list over the same stream.
	fixture.closeRange(0, 45)
	second := readSnapshot()
	require.NoError(t, json.Unmarshal(second["connections"], &connections))
	require.Len(t, connections, 5)

	// Closing the client must end the handler's loop; the server's test cleanup would hang
	// otherwise, and a leaked per-stream goroutine would be a real defect on a mobile process.
	require.NoError(t, conn.Close())
}
