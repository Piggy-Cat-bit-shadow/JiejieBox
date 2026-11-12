package transport

// A pooled DNS connection outlives the exchange that dialled it. "Which outbound carried this
// query" is then a property of the CONNECTION, and a query that reuses it must be attributed to the
// path the connection was opened on - not to whatever the group holds when the query runs, and not
// to an empty path that makes the attribution look unavailable.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"

	mDNS "github.com/miekg/dns"

	"github.com/stretchr/testify/require"
)

func TestAReusedConnectionPublishesItsDialPath(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()
		// Two sequential queries on ONE connection: the second exchange is the reuse case.
		for index := 0; index < 2; index++ {
			request, readErr := ReadMessage(conn)
			if readErr != nil {
				serverDone <- readErr
				return
			}
			response := new(mDNS.Msg)
			response.SetReply(request)
			if writeErr := WriteMessage(conn, request.Id, response); writeErr != nil {
				serverDone <- writeErr
				return
			}
		}
		serverDone <- nil
	}()

	var dials int
	multiplexer := newQueryMultiplexer(queryMultiplexerOptions{
		dial: func(ctx context.Context) (net.Conn, error) {
			dials++
			// The transport's dial boundary and the group that chose the member, recorded exactly
			// where the real ones do it.
			path := adapter.DialPathFromContext(ctx)
			path.BeginDial()
			path.Record("proxy", 0, false)
			path.Record("node-a", 3, true)
			return net.Dial("tcp", listener.Addr().String())
		},
		write: func(conn net.Conn, message *mDNS.Msg, queryId uint16) error {
			return WriteMessage(conn, queryId, message)
		},
		readNext: func(conn net.Conn) (*mDNS.Msg, error) {
			return ReadMessage(conn)
		},
		retryReadError: true,
	})
	defer multiplexer.Close()

	firstPath := adapter.NewDialPath()
	firstMessage := new(mDNS.Msg)
	firstMessage.SetQuestion("first.example.com.", mDNS.TypeA)
	firstCtx, firstCancel := context.WithTimeout(adapter.WithDialPath(context.Background(), firstPath), time.Second)
	_, err = multiplexer.Exchange(firstCtx, firstMessage)
	firstCancel()
	require.NoError(t, err)

	first := firstPath.Result()
	require.Equal(t, 1, first.Dials)
	require.Equal(t, []string{"proxy", "node-a"}, dialPathTags(first), "the dialling exchange records the chain")

	secondPath := adapter.NewDialPath()
	secondMessage := new(mDNS.Msg)
	secondMessage.SetQuestion("second.example.com.", mDNS.TypeAAAA)
	secondCtx, secondCancel := context.WithTimeout(adapter.WithDialPath(context.Background(), secondPath), time.Second)
	_, err = multiplexer.Exchange(secondCtx, secondMessage)
	secondCancel()
	require.NoError(t, err)

	second := secondPath.Result()
	require.Equal(t, 1, dials, "the second query must reuse the pooled connection")
	require.Equal(t, 0, second.Dials, "a reused connection was not established by this exchange")
	require.True(t, second.Replayed)
	require.Equal(t, []string{"proxy", "node-a"}, dialPathTags(second),
		"the reusing exchange reports the path its connection was opened on")
	require.Equal(t, adapter.DialPathReasonNone, second.Reason())

	select {
	case serverErr := <-serverDone:
		require.NoError(t, serverErr)
	case <-time.After(5 * time.Second):
		t.Fatal("the DNS server did not finish")
	}
}

func dialPathTags(result adapter.DialPathResult) []string {
	tags := make([]string, 0, len(result.Steps))
	for _, step := range result.Steps {
		tags = append(tags, step.Tag)
	}
	return tags
}
