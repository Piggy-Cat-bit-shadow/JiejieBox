package http

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloseIdleConnectionsOnAnEmptyPool(t *testing.T) {
	client := &Client{}
	client.CloseIdleConnections()
	require.Empty(t, client.http2Conns)
}
