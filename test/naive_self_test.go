//go:build with_naive_outbound

package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/naive"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

func TestNaiveSelf(t *testing.T) {
	caPem, certPem, keyPem := createSelfSignedCertificate(t, "example.org")
	caPemContent, err := os.ReadFile(caPem)
	require.NoError(t, err)
	startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
			{
				Type: C.TypeNaive,
				Tag:  "naive-in",
				Options: &option.NaiveInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Users: []auth.User{
						{
							Username: "sekai",
							Password: "password",
						},
					},
					Network: network.NetworkTCP,
					InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
						TLS: &option.InboundTLSOptions{
							Enabled:         true,
							ServerName:      "example.org",
							CertificatePath: certPem,
							KeyPath:         keyPem,
						},
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeNaive,
				Tag:  "naive-out",
				Options: &option.NaiveOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					Username: "sekai",
					Password: "password",
					OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
						TLS: &option.OutboundTLSOptions{
							Enabled:     true,
							ServerName:  "example.org",
							Certificate: []string{string(caPemContent)},
						},
					},
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "naive-out",
							},
						},
					},
				},
			},
		},
	})
	testTCP(t, clientPort, testPort)
}

func TestNaiveSelfECH(t *testing.T) {
	caPem, certPem, keyPem := createSelfSignedCertificate(t, "example.org")
	caPemContent, err := os.ReadFile(caPem)
	require.NoError(t, err)
	echConfig, echKey := common.Must2(tls.ECHKeygenDefault("not.example.org"))
	instance := startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
			{
				Type: C.TypeNaive,
				Tag:  "naive-in",
				Options: &option.NaiveInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Users: []auth.User{
						{
							Username: "sekai",
							Password: "password",
						},
					},
					Network: network.NetworkTCP,
					InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
						TLS: &option.InboundTLSOptions{
							Enabled:         true,
							ServerName:      "example.org",
							CertificatePath: certPem,
							KeyPath:         keyPem,
							ECH: &option.InboundECHOptions{
								Enabled: true,
								Key:     []string{echKey},
							},
						},
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeNaive,
				Tag:  "naive-out",
				Options: &option.NaiveOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					Username: "sekai",
					Password: "password",
					OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
						TLS: &option.OutboundTLSOptions{
							Enabled:     true,
							ServerName:  "example.org",
							Certificate: []string{string(caPemContent)},
							ECH: &option.OutboundECHOptions{
								Enabled: true,
								Config:  []string{echConfig},
							},
						},
					},
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "naive-out",
							},
						},
					},
				},
			},
		},
	})

	naiveOut, ok := instance.Outbound().Outbound("naive-out")
	require.True(t, ok)
	naiveOutbound := naiveOut.(*naive.Outbound)

	// Same lifecycle rules as the concurrency test below: a per-test directory instead of a shared
	// /tmp path (which does not exist on Windows), and one stop instead of a stop plus a deferred
	// second stop. Verbose logging stays on here because the assertion is about a param value.
	netLogPath := filepath.Join(t.TempDir(), "naive-ech-netlog.json")
	engine := naiveOutbound.Client().Engine()
	require.True(t, engine.StartNetLogToFile(netLogPath, true))
	stopNetLog := sync.OnceFunc(func() { engine.StopNetLog() })
	defer stopNetLog()

	testTCP(t, clientPort, testPort)

	stopNetLog()

	logContent, err := os.ReadFile(netLogPath)
	require.NoError(t, err)
	logStr := string(logContent)

	require.True(t, strings.Contains(logStr, `"encrypted_client_hello":true`),
		"ECH should be accepted in TLS handshake. NetLog saved to: %s", netLogPath)
}

func TestNaiveSelfInsecureConcurrency(t *testing.T) {
	caPem, certPem, keyPem := createSelfSignedCertificate(t, "example.org")
	caPemContent, err := os.ReadFile(caPem)
	require.NoError(t, err)

	instance := startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
			{
				Type: C.TypeNaive,
				Tag:  "naive-in",
				Options: &option.NaiveInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Users: []auth.User{
						{
							Username: "sekai",
							Password: "password",
						},
					},
					Network: network.NetworkTCP,
					InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
						TLS: &option.InboundTLSOptions{
							Enabled:         true,
							ServerName:      "example.org",
							CertificatePath: certPem,
							KeyPath:         keyPem,
						},
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeNaive,
				Tag:  "naive-out",
				Options: &option.NaiveOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					Username:            "sekai",
					Password:            "password",
					InsecureConcurrency: 3,
					OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
						TLS: &option.OutboundTLSOptions{
							Enabled:     true,
							ServerName:  "example.org",
							Certificate: []string{string(caPemContent)},
						},
					},
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "naive-out",
							},
						},
					},
				},
			},
		},
	})

	naiveOut, ok := instance.Outbound().Outbound("naive-out")
	require.True(t, ok)
	naiveOutbound := naiveOut.(*naive.Outbound)

	// The NetLog id for HTTP2_SESSION_INITIALIZED is resolved from the file's own constants block,
	// because Chromium renumbers these events between versions (249 in one release, 267 in the one
	// this test runs against). Hardcoding either value is what made this test silently count zero.
	//
	// The file lives in t.TempDir() so a failing run cannot leave a hundred-megabyte artifact behind,
	// and verbose logging is off: the session events needed here are recorded either way, and the
	// non-verbose file is a fraction of the size.
	netLogPath := filepath.Join(t.TempDir(), "naive-concurrency-netlog.json")
	engine := naiveOutbound.Client().Engine()
	require.True(t, engine.StartNetLogToFile(netLogPath, false))
	stopNetLog := sync.OnceFunc(func() { engine.StopNetLog() })
	defer stopNetLog()

	// Send multiple sequential connections to trigger round-robin
	// With insecure_concurrency=3, connections will be distributed to 3 pools
	for i := 0; i < 6; i++ {
		testTCP(t, clientPort, testPort)
	}

	stopNetLog()

	// Verify NetLog contains multiple independent HTTP/2 sessions
	sessionCount, eventID, err := netLogEventCount(netLogPath, "HTTP2_SESSION_INITIALIZED")
	require.NoError(t, err, "the NetLog could not be read; the file is kept for this failure only")
	require.GreaterOrEqual(t, sessionCount, 3,
		"Expected at least 3 HTTP/2 sessions with insecure_concurrency=3, found %d (NetLog event id %d, file %s)",
		sessionCount, eventID, netLogPath)
	t.Logf("HTTP2_SESSION_INITIALIZED id=%d sessions=%d", eventID, sessionCount)
}

func TestNaiveSelfQUIC(t *testing.T) {
	caPem, certPem, keyPem := createSelfSignedCertificate(t, "example.org")
	caPemContent, err := os.ReadFile(caPem)
	require.NoError(t, err)
	startInstance(t, option.Options{
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  "mixed-in",
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: clientPort,
					},
				},
			},
			{
				Type: C.TypeNaive,
				Tag:  "naive-in",
				Options: &option.NaiveInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
						ListenPort: serverPort,
					},
					Users: []auth.User{
						{
							Username: "sekai",
							Password: "password",
						},
					},
					Network: network.NetworkUDP,
					InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
						TLS: &option.InboundTLSOptions{
							Enabled:         true,
							ServerName:      "example.org",
							CertificatePath: certPem,
							KeyPath:         keyPem,
						},
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: C.TypeDirect,
			},
			{
				Type: C.TypeNaive,
				Tag:  "naive-out",
				Options: &option.NaiveOutboundOptions{
					ServerOptions: option.ServerOptions{
						Server:     "127.0.0.1",
						ServerPort: serverPort,
					},
					Username: "sekai",
					Password: "password",
					QUIC:     true,
					OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
						TLS: &option.OutboundTLSOptions{
							Enabled:     true,
							ServerName:  "example.org",
							Certificate: []string{string(caPemContent)},
						},
					},
				},
			},
		},
		Route: &option.RouteOptions{
			Rules: []option.Rule{
				{
					Type: C.RuleTypeDefault,
					DefaultOptions: option.DefaultRule{
						RawDefaultRule: option.RawDefaultRule{
							Inbound: []string{"mixed-in"},
						},
						RuleAction: option.RuleAction{
							Action: C.RuleActionTypeRoute,
							RouteOptions: option.RouteActionOptions{
								Outbound: "naive-out",
							},
						},
					},
				},
			},
		},
	})
	testTCP(t, clientPort, testPort)
}

func TestNaiveSelfQUICCongestionControl(t *testing.T) {
	testCases := []struct {
		name              string
		congestionControl string
	}{
		{"BBR", "bbr"},
		{"BBR2", "bbr2"},
		{"Cubic", "cubic"},
		{"Reno", "reno"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			caPem, certPem, keyPem := createSelfSignedCertificate(t, "example.org")
			caPemContent, err := os.ReadFile(caPem)
			require.NoError(t, err)
			startInstance(t, option.Options{
				Inbounds: []option.Inbound{
					{
						Type: C.TypeMixed,
						Tag:  "mixed-in",
						Options: &option.HTTPMixedInboundOptions{
							ListenOptions: option.ListenOptions{
								Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
								ListenPort: clientPort,
							},
						},
					},
					{
						Type: C.TypeNaive,
						Tag:  "naive-in",
						Options: &option.NaiveInboundOptions{
							ListenOptions: option.ListenOptions{
								Listen:     common.Ptr(badoption.Addr(netip.IPv4Unspecified())),
								ListenPort: serverPort,
							},
							Users: []auth.User{
								{
									Username: "sekai",
									Password: "password",
								},
							},
							Network:               network.NetworkUDP,
							QUICCongestionControl: tc.congestionControl,
							InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
								TLS: &option.InboundTLSOptions{
									Enabled:         true,
									ServerName:      "example.org",
									CertificatePath: certPem,
									KeyPath:         keyPem,
								},
							},
						},
					},
				},
				Outbounds: []option.Outbound{
					{
						Type: C.TypeDirect,
					},
					{
						Type: C.TypeNaive,
						Tag:  "naive-out",
						Options: &option.NaiveOutboundOptions{
							ServerOptions: option.ServerOptions{
								Server:     "127.0.0.1",
								ServerPort: serverPort,
							},
							Username:              "sekai",
							Password:              "password",
							QUIC:                  true,
							QUICCongestionControl: tc.congestionControl,
							OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
								TLS: &option.OutboundTLSOptions{
									Enabled:     true,
									ServerName:  "example.org",
									Certificate: []string{string(caPemContent)},
								},
							},
						},
					},
				},
				Route: &option.RouteOptions{
					Rules: []option.Rule{
						{
							Type: C.RuleTypeDefault,
							DefaultOptions: option.DefaultRule{
								RawDefaultRule: option.RawDefaultRule{
									Inbound: []string{"mixed-in"},
								},
								RuleAction: option.RuleAction{
									Action: C.RuleActionTypeRoute,
									RouteOptions: option.RouteActionOptions{
										Outbound: "naive-out",
									},
								},
							},
						},
					},
				},
			})
			testTCP(t, clientPort, testPort)
		})
	}
}
