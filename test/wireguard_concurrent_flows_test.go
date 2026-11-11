//go:build with_wireguard

package main

// Stage-2 coupling test: the in-process userspace WireGuard dataplane carries
// several concurrent flows through the SAME path the laboratory uses (SOCKS
// inbound -> route -> endpoint -> userspace stack -> target).
//
// It exists to bound the stage-1 B7 failure: the laboratory sees a two-process
// deployment fail on the second concurrent flow, and this test proves the stack
// and the route path are not the reason. If this test ever fails, the bug is in
// the process, not at the process boundary.

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
)

func TestWireGuardConcurrentFlowsInOneProcess(t *testing.T) {
	serverKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverPort := reserveTCPPort(t, netip.MustParseAddr("127.0.0.1"))
	clientSocksPort := reserveTCPPort(t, netip.MustParseAddr("127.0.0.1"))
	serverAddresses := []netip.Prefix{netip.MustParsePrefix("198.18.17.1/32")}
	clientAddresses := []netip.Prefix{netip.MustParsePrefix("198.18.17.2/32")}

	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		for {
			conn, acceptErr := target.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				buffer := make([]byte, 64*1024)
				for {
					n, readErr := conn.Read(buffer)
					if n > 0 {
						_, _ = conn.Write(buffer[:n])
					}
					if readErr != nil {
						return
					}
				}
			}()
		}
	}()
	targetPort := target.Addr().(*net.TCPAddr).Port

	instance := startInstance(t, option.Options{
		Endpoints: []option.Endpoint{
			{
				Type: C.TypeWireGuard,
				Tag:  "server",
				Options: &option.WireGuardEndpointOptions{
					MTU: 1280, Address: serverAddresses,
					PrivateKey: base64.StdEncoding.EncodeToString(serverKey.Bytes()),
					ListenPort: serverPort,
					Peers: []option.WireGuardPeer{{
						PublicKey:  base64.StdEncoding.EncodeToString(clientKey.PublicKey().Bytes()),
						AllowedIPs: clientAddresses,
					}},
				},
			},
			{
				Type: C.TypeWireGuard,
				Tag:  "client",
				Options: &option.WireGuardEndpointOptions{
					MTU: 1280, Address: clientAddresses,
					PrivateKey: base64.StdEncoding.EncodeToString(clientKey.Bytes()),
					Peers: []option.WireGuardPeer{{
						Address: "127.0.0.1", Port: serverPort,
						PublicKey:  base64.StdEncoding.EncodeToString(serverKey.PublicKey().Bytes()),
						AllowedIPs: serverAddresses,
					}},
				},
			},
		},
		Inbounds: []option.Inbound{{
			Type: C.TypeSOCKS,
			Tag:  "socks-in",
			Options: &option.SocksInboundOptions{
				ListenOptions: option.ListenOptions{
					Listen:     common.Ptr(badoption.Addr(netip.MustParseAddr("127.0.0.1"))),
					ListenPort: clientSocksPort,
				},
			},
		}},
		Outbounds: []option.Outbound{{Type: C.TypeDirect, Tag: "direct"}},
		Route: &option.RouteOptions{
			Rules: []option.Rule{{
				Type: C.RuleTypeDefault,
				DefaultOptions: option.DefaultRule{
					RawDefaultRule: option.RawDefaultRule{Inbound: []string{"socks-in"}},
					RuleAction:     option.RuleAction{Action: C.RuleActionTypeRoute, RouteOptions: option.RouteActionOptions{Outbound: "client"}},
				},
			}},
		},
	})
	_ = instance

	dialer := socks.NewClient(N.SystemDialer, M.ParseSocksaddrHostPort("127.0.0.1", clientSocksPort), socks.Version5, "", "")
	dial := func(index int) error {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		conn, dialErr := dialer.DialContext(ctx, N.NetworkTCP, M.SocksaddrFrom(netip.MustParseAddr("198.18.17.1"), uint16(targetPort)))
		if dialErr != nil {
			return fmt.Errorf("stream %d dial: %w", index, dialErr)
		}
		defer conn.Close()
		payload := make([]byte, 32*1024)
		if _, writeErr := conn.Write(payload); writeErr != nil {
			return fmt.Errorf("stream %d write: %w", index, writeErr)
		}
		if _, readErr := io.ReadFull(conn, payload); readErr != nil {
			return fmt.Errorf("stream %d read: %w", index, readErr)
		}
		return nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for index := 0; index < 4; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			errs <- dial(index)
		}(index)
	}
	wg.Wait()
	close(errs)
	for dialErr := range errs {
		if dialErr != nil {
			t.Error(dialErr)
		}
	}
}
