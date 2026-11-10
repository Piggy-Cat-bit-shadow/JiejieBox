package daemon

// C07 - the product sequence with two independent business witnesses.
//
// C05 proved recovery with one direct/block witness per sub-scenario. This fixture is what the
// authenticated product client is meant to drive: both witnesses are observable at every step,
// and both come from the configuration that is actually serving.
//
//   * the routing rule rejects one destination port;
//   * the outbound is a selector over an inline provider, whose single node is an HTTP outbound to
//     a canned loopback proxy. The proxy answers every tunnelled request with a marker, so the
//     response BODY names the provider node that carried it. "The old provider node was used" is
//     then a body byte, not an inference from a revision string.
//
// Sequence: A saved -> B runs but the disk refuses it -> C_parse (refusal) -> C_build (build
// failure, real recovery) -> close the service and replay C_build from a NEW object on the same
// directory.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxprovider "github.com/sagernet/sing-box/adapter/provider"
	boxservice "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	protocolgroup "github.com/sagernet/sing-box/protocol/group"
	protocolhttp "github.com/sagernet/sing-box/protocol/http"
	providerlocal "github.com/sagernet/sing-box/provider/local"
	providerremote "github.com/sagernet/sing-box/provider/remote"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// productWitnessContext is C05's registry set plus the HTTP outbound the provider node uses.
func productWitnessContext(t *testing.T) context.Context {
	t.Helper()
	inboundRegistry := inbound.NewRegistry()
	protocolhttp.RegisterInbound(inboundRegistry)
	outboundRegistry := outbound.NewRegistry()
	protocolhttp.RegisterOutbound(outboundRegistry)
	protocolgroup.RegisterSelector(outboundRegistry)
	providerRegistry := boxprovider.NewRegistry()
	providerlocal.RegisterProviderInline(providerRegistry)
	providerremote.RegisterProvider(providerRegistry)
	dnsRegistry := dns.NewTransportRegistry()
	local.RegisterTransport(dnsRegistry)
	ctx := box.Context(context.Background(),
		inboundRegistry,
		outboundRegistry,
		providerRegistry,
		endpoint.NewRegistry(),
		dnsRegistry,
		boxservice.NewRegistry(),
		certificate.NewRegistry(),
	)
	workingPath := t.TempDir()
	return filemanager.WithDefault(ctx, workingPath, workingPath, -1, -1)
}

// cannedProxy answers every request that reaches it with `marker` in the body. It speaks the HTTP
// proxy protocol (CONNECT + a tunnelled request), which is what the HTTP outbound sends.
func cannedProxy(t *testing.T, marker string) int {
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
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				firstLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				for {
					header, headerErr := reader.ReadString('\n')
					if headerErr != nil {
						return
					}
					if header == "\r\n" {
						break
					}
				}
				if strings.HasPrefix(firstLine, "CONNECT ") {
					_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
					// The tunnelled request follows on the same connection.
					for {
						header, headerErr := reader.ReadString('\n')
						if headerErr != nil {
							return
						}
						if header == "\r\n" {
							break
						}
					}
				}
				_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n%s", len(marker), marker)
			}(connection)
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

// productWitnessConfig is the product profile: HTTP inbound, selector over an inline provider with
// one HTTP node, and one inline rule that rejects `rulePort`.
func productWitnessConfig(proxyPort int, nodePort int, rulePort int) string {
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [{"type": "selector", "tag": "selected", "providers": ["nodes"], "default": "nodes/node"}],
  "providers": [{"type": "inline", "tag": "nodes", "outbounds": [{"type": "http", "tag": "node", "server": "127.0.0.1", "server_port": %d}]}],
  "route": {
    "rule_set": [{"type": "inline", "tag": "gate", "rules": [{"port": [%d]}]}],
    "rules": [{"rule_set": "gate", "action": "reject"}],
    "final": "selected"
  }
}`, proxyPort, nodePort, rulePort)
}

// productWitnessClient is a plain HTTP client whose proxy is the Box's HTTP inbound.
func productWitnessClient(t *testing.T, proxyPort int) *http.Client {
	t.Helper()
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", proxyPort)}),
		},
		Timeout: 5 * time.Second,
	}
}

func TestTheProductSequenceUsesTheOldProviderNodeAndRule(t *testing.T) {
	directory := t.TempDir()
	proxyPort := freeLoopbackPort(t)
	markerA := cannedProxy(t, "node-A")
	markerB := cannedProxy(t, "node-B")
	rejectedByA := freeLoopbackPort(t)
	rejectedByB := freeLoopbackPort(t)
	openPort := freeLoopbackPort(t)

	configA := productWitnessConfig(proxyPort, markerA, rejectedByA)
	configB := productWitnessConfig(proxyPort, markerB, rejectedByB)

	store := recoveryStoreWithWriteFault(t, directory, []byte(configB))
	service := NewStartedService(ServiceOptions{Context: productWitnessContext(t), ConfigRecoveryDirectory: directory, RecoverToLastGood: true})
	t.Cleanup(func() {
		_ = service.CloseService()
		service.Close()
	})
	installStore(service, store)

	// served returns the marker body when the request reached the provider node, and false when the
	// rule rejected it.
	served := func(port int) (string, bool) {
		client := productWitnessClient(t, proxyPort)
		defer client.CloseIdleConnections()
		response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			return "", false
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return "", false
		}
		body, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return "", false
		}
		return string(body), true
	}
	requireMarker := func(port int, want string) {
		t.Helper()
		body, ok := served(port)
		require.True(t, ok, "port %d must reach the provider node", port)
		require.Equal(t, want, body, "the body names the provider node that carried the request")
	}
	requireRejected := func(port int) {
		t.Helper()
		_, ok := served(port)
		require.False(t, ok, "port %d must be rejected by the routing rule", port)
	}

	// A is saved and serving: its node answers, its rule rejects its own port, and the other port
	// is not its rule's business.
	require.NoError(t, service.StartOrReloadService(context.Background(), configA, nil))
	requireMarker(openPort, "node-A")
	requireRejected(rejectedByA)
	requireMarker(rejectedByB, "node-A")

	// B runs with the disk refusing to save it: the NEW node and the NEW rule are both in service,
	// while the store still holds A.
	require.NoError(t, service.StartOrReloadService(context.Background(), configB, nil))
	requireMarker(openPort, "node-B")
	requireRejected(rejectedByB)
	requireMarker(rejectedByA, "node-B")
	require.Equal(t, persistenceDegraded, service.LastServiceChange().Persistence,
		"B runs but the disk refused it")
	require.Equal(t, configA, storedActive(t, directory), "the store still holds A")

	// C_parse is a refusal: B keeps running, no recovery event, the store is untouched.
	require.Error(t, service.StartOrReloadService(context.Background(), duplicateInboundTagConfig, nil))
	requireMarker(openPort, "node-B")
	operationReport := service.LastServiceChange()
	require.Equal(t, ServiceChangeStatusFailed, operationReport.Status)
	require.True(t, operationReport.Retained)
	require.Equal(t, configRevisionOf(configB), operationReport.ActiveRevision)
	require.Nil(t, service.LastConfigRecovery(), "a refusal is not a recovery")
	require.Equal(t, configA, storedActive(t, directory))

	// C_build fails while the Box is built; recovery brings A back - both its node and its rule.
	require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))
	requireMarker(openPort, "node-A")
	requireRejected(rejectedByA)
	requireMarker(rejectedByB, "node-A")
	recoveryReport := service.LastServiceChange()
	require.Equal(t, ServiceChangeStatusRecovered, recoveryReport.Status)
	require.Equal(t, configRevisionOf(configA), recoveryReport.ActiveRevision)
	recoveryInfo := service.LastConfigRecovery()
	require.NotNil(t, recoveryInfo)
	require.Equal(t, configRevisionOf(buildFailingConfig), recoveryInfo.FailedRevision)
	require.Equal(t, configRevisionOf(configA), recoveryInfo.ActiveRevision)
	require.Equal(t, configA, storedActive(t, directory))

	// A new object on the same directory replays C_build: recovery reads the transaction from disk.
	require.NoError(t, service.CloseService())
	service.Close()
	restarted := NewStartedService(ServiceOptions{Context: productWitnessContext(t), ConfigRecoveryDirectory: directory, RecoverToLastGood: true})
	t.Cleanup(func() {
		_ = restarted.CloseService()
		restarted.Close()
	})
	require.NoError(t, restarted.StartOrReloadService(context.Background(), buildFailingConfig, nil))
	restartedReport := restarted.LastServiceChange()
	require.Equal(t, ServiceChangeStatusRecovered, restartedReport.Status)
	require.Equal(t, configRevisionOf(configA), restartedReport.ActiveRevision)
	requireMarkerWith(t, restarted, proxyPort, openPort, "node-A")
	requireRejectedWith(t, restarted, proxyPort, rejectedByA)
	require.Equal(t, configA, storedActive(t, directory))
}

// The restarted service listens on the same proxy port; these helpers rebuild the client each time
// because a reload replaces the inbound and outbound objects.
func requireMarkerWith(t *testing.T, service *StartedService, proxyPort int, port int, want string) {
	t.Helper()
	require.NotNil(t, service.Instance())
	client := productWitnessClient(t, proxyPort)
	defer client.CloseIdleConnections()
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, want, string(body))
}

func requireRejectedWith(t *testing.T, service *StartedService, proxyPort int, port int) {
	t.Helper()
	client := productWitnessClient(t, proxyPort)
	defer client.CloseIdleConnections()
	response, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err == nil {
		response.Body.Close()
		require.NotEqual(t, http.StatusOK, response.StatusCode)
	}
}

// TestWriteProductFixtureDocuments exports the exact documents the fixture proves, with fixed
// ports, so the authenticated product run drives the same JSON. Set BOXDD_FIXTURE_OUT to a
// directory; the test is a no-op otherwise.
func TestWriteProductFixtureDocuments(t *testing.T) {
	output := os.Getenv("BOXDD_FIXTURE_OUT")
	if output == "" {
		t.Skip("set BOXDD_FIXTURE_OUT to write the fixture documents")
	}
	const (
		proxyPort = 18080
		nodeA     = 18081
		nodeB     = 18082
		ruleA     = 18091
		ruleB     = 18092
	)
	documents := map[string]string{
		"A.json":       productWitnessConfig(proxyPort, nodeA, ruleA),
		"B.json":       productWitnessConfig(proxyPort, nodeB, ruleB),
		"C_parse.json": duplicateInboundTagConfig,
		"C_build.json": buildFailingConfig,
	}
	require.NoError(t, os.MkdirAll(output, 0o755))
	for name, content := range documents {
		require.NoError(t, os.WriteFile(filepath.Join(output, name), []byte(content), 0o644))
	}
}
