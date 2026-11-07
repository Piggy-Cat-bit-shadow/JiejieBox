package box_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

// TestProviderDownloadDNSBootstrapIsRefused is the DNS-mediated bootstrap loop.
//
// The provider's URL is a domain, the dialer resolves it through route.default_domain_resolver,
// and that DNS server detours into the group the provider itself feeds. Before the fix the Box starts
// and every refresh fails inside DNS resolution on the empty group, with no start-time verdict.
func TestProviderDownloadDNSBootstrapIsRefused(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "dns": {"servers": [{"type": "udp", "tag": "dns1", "server": "127.0.0.1", "server_port": 5353, "detour": "g"}]},
  "providers": [{"type": "remote", "tag": "p", "url": "http://airport.invalid/subscription"}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"default_domain_resolver": {"server": "dns1"}, "final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	err := instance.Start()
	require.ErrorContains(t, err, "cannot resolve the download host through outbound group g")
	require.Contains(t, err.Error(), "dns1", "the diagnostic must name the resolver it went through")
}

// TestProviderDownloadDNSSystemResolverIsAllowed is the negative control: without an explicit
// default resolver the host is resolved by the system, so the check must not invent a DNS loop.
func TestProviderDownloadDNSSystemResolverIsAllowed(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "remote", "tag": "p", "url": "http://airport.invalid/subscription"}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())
}

// TestUseAllProvidersWithNoProvidersIsRefused turns a permanently empty group into a start error:
// there is nothing to wait for, so "waiting for providers" would be a lie.
func TestUseAllProvidersWithNoProvidersIsRefused(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "selector", "tag": "g", "use_all_providers": true},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.ErrorContains(t, instance.Start(), "use_all_providers is set but no outbound providers are configured")
}

// TestUseAllProvidersWithAStaticMemberIsAllowed is the negative control.
func TestUseAllProvidersWithAStaticMemberIsAllowed(t *testing.T) {
	config := `{
  "log": {"disabled": true},
  "outbounds": [
    {"type": "selector", "tag": "g", "use_all_providers": true, "outbounds": ["direct"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())
	require.Equal(t, []string{"direct"}, outboundGroup(t, instance, "g").All())
}

// TestProviderGroupFiltersExcludeEverythingIsDiagnosed separates "the provider has published
// nothing" from "the provider published nodes and this group filtered them all out".
func TestProviderGroupFiltersExcludeEverythingIsDiagnosed(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "hk", "server": "127.0.0.1", "server_port": 1080, "version": "5"}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"], "include": "nomatch"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())
	group := outboundGroup(t, instance, "g")
	require.Empty(t, group.All())
	dialer := group.(adapter.Outbound)
	_, err := dialer.DialContext(context.Background(), "tcp", M.ParseSocksaddr("127.0.0.1:1"))
	require.ErrorContains(t, err, "filters exclude all of them")
}

// TestFastPathOnProviderOnlyGroupKeepsWorking verifies the fast_path group policy still accepts a
// provider-fed selector and that the published member is selectable. The bypass decision itself needs
// a TUN transport and is not exercised here.
func TestFastPathOnProviderOnlyGroupKeepsWorking(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "hk", "server": "127.0.0.1", "server_port": 1080, "version": "5"}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"], "fast_path": true},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())
	group := outboundGroup(t, instance, "g")
	require.Equal(t, []string{"p/hk"}, group.All())
	require.NotNil(t, group.Selected("tcp"))
}

// TestStaticDetourToARemoteProviderNodeIsRefused documents the protection that keeps direct detour
// references out of the refreshable namespace: the start-order sort refuses the missing dependency
// before the provider has a chance to publish.
func TestStaticDetourToARemoteProviderNodeIsRefused(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "socks", "tag": "hk", "server": "127.0.0.1", "server_port": 1080, "version": "5"}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `}],
  "outbounds": [
    {"type": "socks", "tag": "anchor", "server": "127.0.0.1", "server_port": 1081, "version": "5", "detour": "p/hk"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.ErrorContains(t, instance.Start(), "dependency[p/hk] not found")
}

// TestProviderTCPOnlyMemberInUDPLiveGroupIsARuntimeFailure is the documented capability limitation:
// provider members are graph-validated on publication but the capability dry run is not re-run, so a
// TCP-only provider node behind a UDP-carrying group fails the flow at runtime with no start warning.
func TestProviderTCPOnlyMemberInUDPLiveGroupIsARuntimeFailure(t *testing.T) {
	nodesPath := writeProviderFile(t, `{"outbounds": [
		{"type": "http", "tag": "h", "server": "127.0.0.1", "server_port": 8080}
	]}`)
	config := `{
  "log": {"disabled": true},
  "providers": [{"type": "local", "tag": "p", "path": ` + quotePath(nodesPath) + `}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"rules": [{"network": "udp", "outbound": "g"}], "final": "direct"}
}`
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start())
	group := outboundGroup(t, instance, "g")
	require.Equal(t, []string{"p/h"}, group.All())
	dialer := group.(adapter.Outbound)
	_, err := dialer.DialContext(context.Background(), "udp", M.ParseSocksaddr("127.0.0.1:1"))
	require.Error(t, err, "the UDP flow fails at runtime because the provider member carries TCP only")
	require.NotContains(t, err.Error(), "no available member")
}

// --- end-to-end: named HTTP client, remote airport, provider-only selector, SOCKS detour chain ---

func startEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var wait sync.WaitGroup
	done := make(chan struct{})
	wait.Add(1)
	go func() {
		defer wait.Done()
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
		<-done
		wait.Wait()
	}
}

// startSOCKS5Server is a minimal CONNECT-only SOCKS5 server. It is used twice in the chain: as the
// provider node's own server and, transitively, as the target the outer SOCKS node dials. Every
// accepted connection is tracked so teardown can close it and unblock the relay goroutines.
func startSOCKS5Server(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var (
		access  sync.Mutex
		active  = make(map[net.Conn]struct{})
		wait    sync.WaitGroup
		closing = false
	)
	track := func(conn net.Conn) bool {
		access.Lock()
		defer access.Unlock()
		if closing {
			return false
		}
		active[conn] = struct{}{}
		wait.Add(1)
		return true
	}
	untrack := func(conn net.Conn) {
		access.Lock()
		delete(active, conn)
		access.Unlock()
		wait.Done()
	}
	done := make(chan struct{})
	wait.Add(1)
	go func() {
		defer wait.Done()
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			if !track(conn) {
				_ = conn.Close()
				return
			}
			go func() {
				defer untrack(conn)
				serveSOCKS5(conn)
			}()
		}
	}()
	return listener.Addr().String(), func() {
		access.Lock()
		closing = true
		for conn := range active {
			_ = conn.Close()
		}
		access.Unlock()
		_ = listener.Close()
		<-done
		wait.Wait()
	}
}

func serveSOCKS5(conn net.Conn) {
	defer conn.Close()
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil || greeting[0] != 0x05 {
		return
	}
	methods := make([]byte, int(greeting[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	request := make([]byte, 4)
	if _, err := io.ReadFull(conn, request); err != nil || request[0] != 0x05 || request[1] != 0x01 {
		return
	}
	var host string
	switch request[3] {
	case 0x01:
		address := make([]byte, 4)
		if _, err := io.ReadFull(conn, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return
		}
		address := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, address); err != nil {
			return
		}
		host = string(address)
	case 0x04:
		address := make([]byte, 16)
		if _, err := io.ReadFull(conn, address); err != nil {
			return
		}
		host = net.IP(address).String()
	default:
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBytes); err != nil {
		return
	}
	port := int(portBytes[0])<<8 | int(portBytes[1])
	upstream, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	if _, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, conn)
		_ = upstream.Close()
		_ = conn.Close()
		close(copied)
	}()
	_, _ = io.Copy(conn, upstream)
	_ = conn.Close()
	_ = upstream.Close()
	<-copied
}

// TestProviderChainEndToEndWithNamedHTTPClient is the reported topology, end to end and local-only:
//
//	remote airport provider -> provider-only selector -> static SOCKS detour chain
//
// The download uses its own named http_client, so it does not follow route.final or the selector.
// Before the provider publishes, the group waits and the dial names the provider; after the download
// publishes the node, the outer SOCKS chain actually carries bytes through the provider member.
func TestProviderChainEndToEndWithNamedHTTPClient(t *testing.T) {
	socksAddress, closeSOCKS := startSOCKS5Server(t)
	t.Cleanup(closeSOCKS)
	echoAddress, closeEcho := startEchoServer(t)
	t.Cleanup(closeEcho)

	release := make(chan struct{})
	var releaseOnce sync.Once
	socksHost, socksPort := hostPort(t, socksAddress)
	document := fmt.Sprintf(`{"outbounds": [
		{"type": "socks", "tag": "exit", "server": %q, "server_port": %d, "version": "5"}
	]}`, socksHost, socksPort)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = writer.Write([]byte(document))
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		server.Close()
	})

	config := fmt.Sprintf(`{
  "log": {"disabled": true},
  "http_clients": [{"tag": "dl"}],
  "providers": [{"type": "remote", "tag": "p", "url": %q, "http_client": "dl", "update_interval": "24h"}],
  "outbounds": [
    {"type": "selector", "tag": "g", "providers": ["p"]},
    {"type": "socks", "tag": "a", "server": %q, "server_port": %d, "version": "5", "detour": "g"},
    {"type": "direct", "tag": "direct"}
  ],
  "route": {"final": "direct"}
}`, server.URL+"/sub", socksHost, socksPort)
	instance, _ := newProviderLifecycleBox(t, config)
	require.NoError(t, instance.Start(), "the empty group must wait instead of refusing the start")
	group := outboundGroup(t, instance, "g")
	require.Empty(t, group.All())

	chain, loaded := instance.Outbound().Outbound("a")
	require.True(t, loaded)
	_, beforeErr := chain.DialContext(context.Background(), "tcp", M.ParseSocksaddr(echoAddress))
	require.ErrorContains(t, beforeErr, "waiting for outbound provider p")
	require.NotContains(t, beforeErr.Error(), "unusable outbound path member")

	releaseOnce.Do(func() { close(release) })
	waitForGroupMembers(t, group, "p/exit")
	require.Equal(t, "p/exit", group.Selected("tcp").Tag())

	dialContext, cancelDial := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelDial()
	conn, err := chain.DialContext(dialContext, "tcp", M.ParseSocksaddr(echoAddress))
	require.NoError(t, err, "after publication the SOCKS detour chain must dial end to end")
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	echo := make([]byte, 4)
	_, err = io.ReadFull(conn, echo)
	require.NoError(t, err)
	require.Equal(t, "ping", string(echo))
}

func hostPort(t *testing.T, address string) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return host, port
}
