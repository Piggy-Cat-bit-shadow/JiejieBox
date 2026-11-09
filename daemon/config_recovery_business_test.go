package daemon

// C05 review — the recovery transaction with a business witness.
//
// The original three-step reproduction ends here: A is saved, B runs but the disk refuses to save
// it, C fails to start. Recovery starts A. This file sends REAL requests through the running Box
// before and after that sequence, so "the old configuration runs" is not inferred from a revision
// string: the traffic follows the old routing rule and the old provider node source, while the
// configuration file at the same path now contains B.
//
// The Box is the product composition: an HTTP inbound, a direct outbound, an inline provider and a
// selector over it. No stand-in sits between the entry and the request.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
	protocolblock "github.com/sagernet/sing-box/protocol/block"
	protocoldirect "github.com/sagernet/sing-box/protocol/direct"
	protocolgroup "github.com/sagernet/sing-box/protocol/group"
	protocolhttp "github.com/sagernet/sing-box/protocol/http"
	protocolsocks "github.com/sagernet/sing-box/protocol/socks"
	providerlocal "github.com/sagernet/sing-box/provider/local"
	providerremote "github.com/sagernet/sing-box/provider/remote"
	"github.com/sagernet/sing/service/filemanager"

	"github.com/stretchr/testify/require"
)

// recoveryBusinessContext is the registry set the product Box needs for these tests: one HTTP
// inbound, direct/selector/block outbounds, an inline provider and the local DNS transport.
func recoveryBusinessContext(t *testing.T) context.Context {
	t.Helper()
	inboundRegistry := inbound.NewRegistry()
	protocolhttp.RegisterInbound(inboundRegistry)
	outboundRegistry := outbound.NewRegistry()
	protocoldirect.RegisterOutbound(outboundRegistry)
	protocolblock.RegisterOutbound(outboundRegistry)
	protocolsocks.RegisterOutbound(outboundRegistry)
	protocolgroup.RegisterSelector(outboundRegistry)
	providerRegistry := boxprovider.NewRegistry()
	providerlocal.RegisterProviderInline(providerRegistry)
	providerlocal.RegisterProviderLocal(providerRegistry)
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

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

// recoveryBusinessConfig is the product profile under test. `ruleCIDR` is the destination range the
// routing rule rejects and `providerNode` is the type of the one node the inline provider serves.
func recoveryBusinessConfig(proxyPort int, ruleCIDR string, providerNode string) string {
	route := `"route": {"final": "selected"}`
	if ruleCIDR != "" {
		route = fmt.Sprintf(`"route": {
    "final": "selected",
    "rule_set": [{"type": "inline", "tag": "gate", "rules": [{"ip_cidr": [%q]}]}],
    "rules": [{"rule_set": "gate", "action": "reject"}]
  }`, ruleCIDR)
	}
	return fmt.Sprintf(`{
  "log": {"level": "error"},
  "inbounds": [{"type": "http", "tag": "proxy", "listen": "127.0.0.1", "listen_port": %d}],
  "outbounds": [
    {"type": "selector", "tag": "selected", "providers": ["nodes"], "default": "nodes/node"}
  ],
  "providers": [{"type": "inline", "tag": "nodes", "outbounds": [{"type": %q, "tag": "node"}]}],
  %s
}`, proxyPort, providerNode, route)
}

// newProxyClient is a plain HTTP client whose proxy is the Box's HTTP inbound.
func newProxyClient(t *testing.T, proxyPort int) *http.Client {
	t.Helper()
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", proxyPort)}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}
}

// proxyStatus is one request through the Box. A rejected connection is an error, which is what the
// `reject` action does to an HTTP proxy request.
func proxyStatus(t *testing.T, client *http.Client, target string) (int, error) {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

// TestARecoveryServesTheOldRoutingRuleAndProviderNodes is the C3 witness.
func TestARecoveryServesTheOldRoutingRuleAndProviderNodes(t *testing.T) {
	for _, scenario := range []struct {
		name string
		// saved and revived are the same document path and the same tags with different content.
		saved   func(port int) string
		revived func(port int) string
		// savedServes and revivedServes are what a real request does under each document.
		savedServes   bool
		revivedServes bool
		proves        string
	}{
		{
			name:          "routing rule",
			saved:         func(port int) string { return recoveryBusinessConfig(port, "127.0.0.1/32", "direct") },
			revived:       func(port int) string { return recoveryBusinessConfig(port, "10.0.0.0/8", "direct") },
			savedServes:   false, // the saved rule set rejects the loopback target
			revivedServes: true,  // B's rule set no longer matches it
			proves:        "the saved inline rule set is what serves the request",
		},
		{
			name:          "provider nodes",
			saved:         func(port int) string { return recoveryBusinessConfig(port, "", "direct") },
			revived:       func(port int) string { return recoveryBusinessConfig(port, "", "block") },
			savedServes:   true,  // the saved provider node is a direct outbound
			revivedServes: false, // B's provider node is a block outbound
			proves:        "the saved inline provider node is what carries the request",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			// The configuration FILE at a stable path: A first, then overwritten with B. Recovery
			// must use the stored document, not this file.
			configPath := filepath.Join(directory, "config.json")

			target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte("ok"))
			}))
			t.Cleanup(target.Close)

			proxyPort := freeLoopbackPort(t)
			saved := scenario.saved(proxyPort)
			revived := scenario.revived(proxyPort)
			require.NoError(t, os.WriteFile(configPath, []byte(saved), 0o644))

			store := recoveryStoreWithWriteFault(t, directory, []byte(revived))
			service := NewStartedService(ServiceOptions{Context: recoveryBusinessContext(t)})
			t.Cleanup(func() {
				_ = service.CloseService()
				service.Close()
			})
			service.ConfigureConfigRecovery(directory, true)
			installStore(service, store)

			require.NoError(t, service.StartOrReloadService(context.Background(), saved, nil))
			client := newProxyClient(t, proxyPort)
			defer client.CloseIdleConnections()
			// A reload replaces the Box, and with it the inbound and outbound objects. A fresh
			// client per phase keeps the witness about routing, not about a pooled connection to a
			// closed object.
			freshClient := func() {
				client.CloseIdleConnections()
				client = newProxyClient(t, proxyPort)
			}

			// `served` is the business answer: a 200 from the target. A reject action answers with
			// an HTTP error status, and a block node fails the dial, so both are "not served".
			served := func() bool {
				status, err := proxyStatus(t, client, target.URL)
				return err == nil && status == http.StatusOK
			}

			require.Equal(t, scenario.savedServes, served(), "the saved configuration's answer is the baseline")

			// B runs with the same tag and the same path, pointing at different content.
			require.NoError(t, service.StartOrReloadService(context.Background(), revived, nil))
			freshClient()
			bReport := service.LastServiceChange()
			require.Equal(t, persistenceDegraded, bReport.Persistence,
				"B runs but the disk refused it; recovery can only return to A")
			require.Equal(t, scenario.revivedServes, served(), "B is what is online now")

			// The same path now contains B. C fails to start; recovery returns to A.
			require.NoError(t, os.WriteFile(configPath, []byte(revived), 0o644))
			require.NoError(t, service.StartOrReloadService(context.Background(), buildFailingConfig, nil))
			freshClient()
			require.NotNil(t, service.Instance())
			require.Equal(t, saved, storedActive(t, directory),
				"the store still holds A: recovery reads the transaction, not the edited path")
			report := service.LastServiceChange()
			require.Equal(t, ServiceChangeStatusRecovered, report.Status)
			require.Equal(t, configRevisionOf(saved), report.ActiveRevision)
			require.False(t, report.Retained)

			require.Equal(t, scenario.savedServes, served(),
				"after recovery the saved behaviour is back: %s", scenario.proves)
		})
	}
}
