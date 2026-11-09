package daemon

// C04/A3 — a live change must not cost an unrelated connection.
//
// The mode switch is the existing in-place owner. This test opens a real HTTP connection through
// the Box's proxy inbound, switches the Clash mode through the configuration entry, and sends the
// next request over the SAME connection: the reused flag is the witness that the Box, its inbound
// and its connection manager were not replaced.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"

	// The Clash API constructor registers the mode owner this test switches.
	_ "github.com/sagernet/sing-box/experimental/clashapi"

	"github.com/stretchr/testify/require"
)

func liveConnectionConfig(proxyPort int, mode string) string {
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"},
  "experimental": {"clash_api": {"default_mode": %q}}
}`, proxyPort, mode)
}

func TestALiveModeChangeKeepsAnUnrelatedConnectionServing(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(target.Close)

	proxyPort := freeLoopbackPort(t)
	service := NewStartedService(ServiceOptions{Context: recoveryBusinessContext(t)})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	require.NoError(t, service.StartOrReloadService(context.Background(), liveConnectionConfig(proxyPort, "Global"), nil))
	boxBefore := service.Instance()
	require.NotNil(t, boxBefore)

	client := newProxyClient(t, proxyPort)
	defer client.CloseIdleConnections()
	request := func() (int, bool) {
		var reused bool
		req, err := http.NewRequest(http.MethodGet, target.URL, nil)
		require.NoError(t, err)
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
		}))
		response, err := client.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return response.StatusCode, reused
	}

	status, _ := request()
	require.Equal(t, http.StatusOK, status)
	status, reused := request()
	require.Equal(t, http.StatusOK, status)
	require.True(t, reused, "the fixture must actually reuse one connection before the change")

	require.NoError(t, service.StartOrReloadService(context.Background(), liveConnectionConfig(proxyPort, "Direct"), nil))
	require.Same(t, boxBefore, service.Instance(), "a mode-only change must keep the Box")
	report := service.LastServiceChange()
	require.NotNil(t, report)
	require.Equal(t, ServiceChangeStatusAppliedLive, report.Status)
	require.False(t, report.RestartRequired)

	status, reused = request()
	require.Equal(t, http.StatusOK, status, "the connection must still exchange data after the live change")
	require.True(t, reused, "the request after the change must run on the same connection")
}
