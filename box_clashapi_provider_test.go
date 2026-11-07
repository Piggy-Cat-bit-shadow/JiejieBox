//go:build with_clash_api

package box_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/stretchr/testify/require"
)

// TestClashAPIListsProviderNodesAndSelectsThem is the Clash API half of the integration audit: the
// provider's rendered tags must be addressable exactly like configured nodes, both in the proxy
// list and through the selection endpoint, because that is how a dashboard drives a selector.
func TestClashAPIListsProviderNodesAndSelectsThem(t *testing.T) {
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, listenErr)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	config := fmt.Sprintf(`{
  "log": {"disabled": true},
  "providers": [
    {
      "type": "inline",
      "tag": "air",
      "outbounds": [
        {"type": "socks", "tag": "hk", "server": "127.0.0.1", "server_port": 1080, "version": "5"},
        {"type": "socks", "tag": "sg", "server": "127.0.0.1", "server_port": 1081, "version": "5"}
      ]
    }
  ],
  "outbounds": [
    {"type": "selector", "tag": "proxy", "providers": ["air"], "default": "air/hk"},
    {"type": "direct", "tag": "direct"}
  ],
  "experimental": {
    "clash_api": {"external_controller": %q}
  }
}`, address)
	ctx := include.Context(context.Background())
	options, decodeErr := singjson.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	require.NoError(t, decodeErr)
	instance, newErr := box.New(box.Options{Context: ctx, Options: options})
	require.NoError(t, newErr)
	t.Cleanup(func() { _ = instance.Close() })
	require.NoError(t, instance.Start())

	baseURL := "http://" + address
	client := &http.Client{Timeout: 5 * time.Second}
	type proxyEntry struct {
		Type string   `json:"type"`
		Name string   `json:"name"`
		Now  string   `json:"now"`
		All  []string `json:"all"`
	}
	var proxies map[string]proxyEntry
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, err := client.Get(baseURL + "/proxies")
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			require.NoError(t, readErr)
			if response.StatusCode == http.StatusOK {
				var payload struct {
					Proxies map[string]proxyEntry `json:"proxies"`
				}
				require.NoError(t, json.Unmarshal(body, &payload))
				proxies = payload.Proxies
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Clash API did not answer on %s: %v", baseURL, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	require.Contains(t, proxies, "air/hk")
	require.Contains(t, proxies, "air/sg")
	require.Contains(t, proxies, "proxy")
	hk := proxies["air/hk"]
	require.Equal(t, "air/hk", hk.Name)
	require.Equal(t, "SOCKS", hk.Type)
	selector := proxies["proxy"]
	require.Equal(t, "air/hk", selector.Now, "the configured default must be the reported selection")
	require.ElementsMatch(t, []string{"air/hk", "air/sg"}, selector.All)

	body := bytes.NewBufferString(`{"name": "air/sg"}`)
	request, requestErr := http.NewRequest(http.MethodPut, baseURL+"/proxies/proxy", body)
	require.NoError(t, requestErr)
	request.Header.Set("Content-Type", "application/json")
	response, doErr := client.Do(request)
	require.NoError(t, doErr)
	response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode)

	response, getErr := client.Get(baseURL + "/proxies/proxy")
	require.NoError(t, getErr)
	defer response.Body.Close()
	bodyBytes, readErr := io.ReadAll(response.Body)
	require.NoError(t, readErr)
	require.Equal(t, http.StatusOK, response.StatusCode)
	var selected proxyEntry
	require.NoError(t, json.Unmarshal(bodyBytes, &selected))
	require.Equal(t, "air/sg", selected.Now)

	// The provider-management endpoints are STUBS, and the test pins that so a dashboard cannot be
	// built on an interface the core does not implement. `/proxies` lists provider nodes and selects
	// them; `/providers/proxies` reports no providers and answers 404 for a named one.
	response, getErr = client.Get(baseURL + "/providers/proxies")
	require.NoError(t, getErr)
	providersBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, readErr)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.JSONEq(t, `{"providers":{}}`, string(providersBody),
		"the provider-management API is not implemented; it must report an empty set, not our providers")

	response, getErr = client.Get(baseURL + "/providers/proxies/air")
	require.NoError(t, getErr)
	response.Body.Close()
	require.Equal(t, http.StatusNotFound, response.StatusCode,
		"a named outbound provider must not be addressable through the Clash provider API")
}
