package daemon

// C2/Provider — remote and local provider node sources are versioned and pinned on recovery.
//
// A's provider file names a direct node and is saved; B's document is written to the same path
// (block node) and B runs but the disk refuses to save it; C fails; recovery A must serve through
// A's node, not the block node now in the file. The remote provider uses its `path` document, so
// no network is involved; the local provider reads the same kind of file.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// startProviderSocksServer runs a minimal SOCKS5 CONNECT server, so a provider node is a real
// outbound the parser accepts and the probe can actually traverse.
func startProviderSocksServer(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func(connection net.Conn) {
				defer connection.Close()
				header := make([]byte, 2)
				if _, err := io.ReadFull(connection, header); err != nil || header[0] != 5 {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(connection, methods); err != nil {
					return
				}
				if _, err := connection.Write([]byte{5, 0}); err != nil {
					return
				}
				request := make([]byte, 4)
				if _, err := io.ReadFull(connection, request); err != nil || request[1] != 1 {
					return
				}
				var host string
				switch request[3] {
				case 1:
					address := make([]byte, 4)
					if _, err := io.ReadFull(connection, address); err != nil {
						return
					}
					host = net.IP(address).String()
				case 3:
					length := make([]byte, 1)
					if _, err := io.ReadFull(connection, length); err != nil {
						return
					}
					name := make([]byte, int(length[0]))
					if _, err := io.ReadFull(connection, name); err != nil {
						return
					}
					host = string(name)
				default:
					return
				}
				portBytes := make([]byte, 2)
				if _, err := io.ReadFull(connection, portBytes); err != nil {
					return
				}
				destination := net.JoinHostPort(host, fmt.Sprintf("%d", int(portBytes[0])<<8|int(portBytes[1])))
				upstream, dialErr := net.Dial("tcp", destination)
				if dialErr != nil {
					_, _ = connection.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer upstream.Close()
				_, _ = connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				done := make(chan struct{}, 1)
				go func() {
					_, _ = io.Copy(upstream, connection)
					done <- struct{}{}
				}()
				_, _ = io.Copy(connection, upstream)
				<-done
			}(connection)
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func providerNodeDocument(port int) string {
	return fmt.Sprintf(`{"outbounds":[{"type":"socks","tag":"node","server":"127.0.0.1","server_port":%d}]}`, port)
}

func providerRecoveryProfile(proxyPort int, provider string, logLevel string) string {
	return fmt.Sprintf(`{
  "log": {"level": %q},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "providers": [%s],
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "selector", "tag": "selected", "providers": ["nodes"]}
  ],
  "route": {
    "final": "direct",
    "rules": [{"inbound": ["proxy"], "action": "route", "outbound": "selected"}]
  }
}`, logLevel, proxyPort, provider)
}

func TestARecoveryPinsTheExactProviderNodeSource(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		provider func(nodePath string) string
	}{
		{
			name: "remote subscription document",
			provider: func(nodePath string) string {
				return fmt.Sprintf(`{"type": "remote", "tag": "nodes", "url": "https://provider.example/list", "path": %q}`, nodePath)
			},
		},
		{
			name: "local provider file",
			provider: func(nodePath string) string {
				return fmt.Sprintf(`{"type": "local", "tag": "nodes", "path": %q}`, nodePath)
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := recoveryBusinessContext(t)
			directory := t.TempDir()
			nodePath := filepath.Join(directory, "nodes.json")
			socksPort := startProviderSocksServer(t)
			require.NoError(t, os.WriteFile(nodePath, []byte(providerNodeDocument(socksPort)), 0o644))

			target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte("ok"))
			}))
			t.Cleanup(target.Close)

			proxyPort := freeLoopbackPort(t)
			configA := providerRecoveryProfile(proxyPort, scenario.provider(nodePath), "error")
			configB := providerRecoveryProfile(proxyPort, scenario.provider(nodePath), "warn")
			revisionA := configRevisionOf(configA)

			store := recoveryStoreWithWriteFault(t, directory, []byte(configB))
			service := NewStartedService(ServiceOptions{Context: ctx})
			t.Cleanup(func() {
				_ = service.CloseService()
				service.Close()
			})
			service.ConfigureConfigRecovery(directory, true)
			installStore(service, store)

			require.NoError(t, service.StartOrReloadService(context.Background(), configA, nil))
			client := newProxyClient(t, proxyPort)
			defer client.CloseIdleConnections()
			served := func() bool {
				status, err := proxyStatus(t, client, target.URL)
				return err == nil && status == http.StatusOK
			}
			require.True(t, served(), "A's provider node carries the request")

			references, err := store.References(revisionA)
			require.NoError(t, err)
			require.NotEmpty(t, references, "the saved configuration names its provider snapshot")
			var pinnedDigest string
			for _, digest := range references {
				pinnedDigest = digest
			}
			require.NotEmpty(t, pinnedDigest)

			// The same path now contains B's nodes (a dead proxy). B runs them, but the disk
			// refuses B.
			require.NoError(t, os.WriteFile(nodePath, []byte(providerNodeDocument(1)), 0o644))
			require.NoError(t, service.StartOrReloadService(context.Background(), configB, nil))
			client.CloseIdleConnections()
			require.False(t, served(), "B's provider node is a block")
			require.Equal(t, persistenceDegraded, service.LastServiceChange().Persistence)

			// C fails; recovery to A must use A's snapshot, not the file B wrote.
			require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))
			client.CloseIdleConnections()
			report := service.LastServiceChange()
			require.Equal(t, ServiceChangeStatusRecovered, report.Status)
			require.Equal(t, revisionA, report.ActiveRevision)
			require.True(t, served(), "recovery must serve through A's provider node, not the block now in the file")
		})
	}
}
