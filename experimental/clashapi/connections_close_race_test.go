package clashapi

// Q3: the connections endpoints against a traffic manager that is being torn down.
//
// A Box reload closes the old Box's scope, which runs trafficcontrol.Manager.Clear and the
// connection manager's CloseAll while a dashboard's WebSocket may still be ticking. The
// /connections handler reads trafficManager.Total() and trafficManager.Connections(); the
// closing side deletes trackers and clears the closed window. This test drives that
// collision on the real handler, the real WebSocket codec and a real traffic manager,
// under the race detector, and asserts the stream either delivers valid frames or ends
// cleanly - never a panic or a hang.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/ws"
	"github.com/sagernet/ws/wsutil"

	"github.com/stretchr/testify/require"
)

func TestConnectionsWebSocketSurvivesConcurrentCloseAndClear(t *testing.T) {
	const connections = 64
	fixture := newTrafficFixture(t, connections)
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(connectionRouter(ctx, nil, fixture.manager))
	t.Cleanup(func() {
		cancel()
		server.Close()
	})

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/?interval=1"
	conn, reader, _, err := ws.Dial(context.Background(), url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	stream := &bufferedConn{reader: conn, writer: conn}
	if reader != nil {
		stream.reader = reader
		t.Cleanup(func() { ws.PutReader(reader) })
	}

	var frames atomic.Int64
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			payload, readErr := wsutil.ReadServerText(stream)
			if readErr != nil {
				return
			}
			var document map[string]json.RawMessage
			if json.Unmarshal(payload, &document) != nil {
				return
			}
			var reported []json.RawMessage
			if json.Unmarshal(document["connections"], &reported) != nil {
				return
			}
			frames.Add(1)
		}
	}()
	require.Eventually(t, func() bool { return frames.Load() > 0 }, 5*time.Second, time.Millisecond,
		"the WebSocket stream produced no snapshot")

	var waitGroup sync.WaitGroup
	waitGroup.Add(4)
	go func() {
		defer waitGroup.Done()
		for round := 0; round < 8; round++ {
			fixture.closeRange(round*4, round*4+4)
			time.Sleep(time.Millisecond)
		}
	}()
	go func() {
		defer waitGroup.Done()
		for round := 0; round < 8; round++ {
			fixture.manager.Clear()
			time.Sleep(time.Millisecond)
		}
	}()
	go func() {
		defer waitGroup.Done()
		for round := 0; round < 8; round++ {
			response, getErr := http.Get(server.URL + "/")
			if getErr != nil {
				continue
			}
			_ = response.Body.Close()
			time.Sleep(time.Millisecond)
		}
	}()
	go func() {
		defer waitGroup.Done()
		for round := 0; round < 8; round++ {
			_ = fixture.manager.ConnectionsLen()
			_, _ = fixture.manager.Total()
			time.Sleep(time.Millisecond)
		}
	}()
	waitGroup.Wait()

	// Closing the client must end the handler loop; a hang here would be a leaked
	// per-stream goroutine and a deadlock on the manager locks.
	require.NoError(t, conn.Close())
	select {
	case <-readerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the WebSocket read loop did not observe the close")
	}
	require.Greater(t, frames.Load(), int64(0))
}
