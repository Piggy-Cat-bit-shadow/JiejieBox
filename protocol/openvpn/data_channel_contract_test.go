package openvpn

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/device"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"

	ovpn "github.com/sagernet/sing-openvpn"

	"github.com/stretchr/testify/require"
)

// The DATA-CHANNEL contract between these endpoints and the shared device.
//
// # The incident these tests exist for
//
// Every wire below was present in this tree, was silently dropped by a merge resolution, and the
// result was not a slow path: the OpenVPN SERVER ENDPOINT COULD NOT START AT ALL. sing-openvpn
// refuses to construct a server whose `NewOutboundQueue` is nil, nothing under protocol/openvpn
// assigned it, and the integration fixture failed on all four subtests with
//
//	start outbound: start endpoint/openvpn-server[openvpn-server]: missing openvpn outbound queue
//
// The revert was invisible to a symbol-level audit because it happened inside a merge: the method it
// made dead (`ServerEndpoint.routeOutbound`) was still in the file, and only its ASSIGNMENT was gone.
// Every test here therefore asserts on a WIRE rather than on the existence of the code that needs
// one. `TestOpenVPNSelfToSelf` in test/ remains the behavioural proof; these are the fast, precise
// guards that say WHICH wire broke.

// endpointTestContext supplies the one service the endpoint constructors read.
//
// `NewServerEndpoint` and `NewClientEndpoint` take InterfaceFinder from the network manager in the
// context. Everything else they look up - the DNS router, the OOM killer - is read through
// service.FromContext and tolerates absence, so the network manager is the whole fixture.
func endpointTestContext() context.Context {
	return service.ContextWith[adapter.NetworkManager](context.Background(), &stubNetworkManager{
		interfaceFinder: control.NewDefaultInterfaceFinder(),
	})
}

// stubNetworkManager satisfies adapter.NetworkManager by embedding it, so the fixture cannot rot as
// the interface grows. The methods below are the ones the endpoint constructors actually reach, and
// they are answered rather than left to the embedded nil: a double that panics on the method under
// test reports the fixture instead of the code.
type stubNetworkManager struct {
	adapter.NetworkManager
	interfaceFinder  control.InterfaceFinder
	defaultOptions   adapter.NetworkOptions
	autoDetect       bool
	outputMark       uint32
	protectFunc      control.Func
	autoDetectFunc   control.Func
	redirectMarkFunc control.Func
}

func (m *stubNetworkManager) InterfaceFinder() control.InterfaceFinder {
	return m.interfaceFinder
}

func (m *stubNetworkManager) DefaultOptions() adapter.NetworkOptions {
	return m.defaultOptions
}

func (m *stubNetworkManager) AutoDetectInterface() bool {
	return m.autoDetect
}

func (m *stubNetworkManager) ProtectFunc() control.Func {
	return m.protectFunc
}

func (m *stubNetworkManager) AutoDetectInterfaceFunc() control.Func {
	return m.autoDetectFunc
}

func (m *stubNetworkManager) AutoRedirectOutputMark() uint32 {
	return m.outputMark
}

func (m *stubNetworkManager) AutoRedirectOutputMarkFunc() control.Func {
	return m.redirectMarkFunc
}

// serverTLSMaterial returns a self-signed certificate and key as the inline line lists the options
// take.
//
// ECDSA P-256 rather than the RSA the integration fixtures use: this test only has to survive option
// parsing, and certificate generation that costs a second per run is generation that gets skipped.
func serverTLSMaterial(t *testing.T) (certificate []string, key []string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sing-box openvpn headroom fixture"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	require.NoError(t, err)
	return pemLines(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		pemLines(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// pemLines turns a PEM document into the non-empty line list the inline options expect.
func pemLines(document []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(document), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// tlsServerOptions is a minimal but COMPLETE TLS-mode server configuration.
func tlsServerOptions(t *testing.T) option.OpenVPNServerEndpointOptions {
	t.Helper()
	certificate, key := serverTLSMaterial(t)
	return option.OpenVPNServerEndpointOptions{
		Address: badoption.Listable[netip.Prefix]{netip.MustParsePrefix("10.8.0.1/24")},
		TLS: &option.OpenVPNInboundTLSOptions{
			Certificate: certificate,
			Key:         key,
		},
	}
}

// staticKeyFixture builds a syntactically valid OpenVPN static key.
func staticKeyFixture(t *testing.T) string {
	t.Helper()
	material := make([]byte, 256)
	_, err := rand.Read(material)
	require.NoError(t, err)
	const hexDigits = "0123456789abcdef"
	encoded := make([]byte, 0, len(material)*2)
	for _, value := range material {
		encoded = append(encoded, hexDigits[value>>4], hexDigits[value&0x0f])
	}
	lines := []string{"-----BEGIN OpenVPN Static key V1-----"}
	for index := 0; index < len(encoded); index += 32 {
		lines = append(lines, string(encoded[index:index+32]))
	}
	return strings.Join(append(lines, "-----END OpenVPN Static key V1-----"), "\n")
}

// staticKeyServerOptions is a minimal but COMPLETE static-key server configuration.
func staticKeyServerOptions(t *testing.T) option.OpenVPNServerEndpointOptions {
	t.Helper()
	return option.OpenVPNServerEndpointOptions{
		Mode:        ovpn.ModeStaticKey,
		Network:     "udp",
		Address:     badoption.Listable[netip.Prefix]{netip.MustParsePrefix("10.8.0.1/24")},
		PeerAddress: peerAddressPointer(netip.MustParseAddr("10.8.0.2")),
		StaticKey:   badoption.Listable[string]{staticKeyFixture(t)},
		Remote:      "127.0.0.1",
		RemotePort:  1194,
	}
}

// peerAddressPointer wraps an address in the option type, whose field is unexported.
func peerAddressPointer(address netip.Addr) *badoption.Addr {
	peerAddress := badoption.Addr(address)
	return &peerAddress
}

// buildServerEndpoint constructs the production server endpoint and returns the concrete type.
func buildServerEndpoint(t *testing.T, options option.OpenVPNServerEndpointOptions) *ServerEndpoint {
	t.Helper()
	endpoint, err := NewServerEndpoint(endpointTestContext(), nil, log.NewNOPFactory().Logger(), "server", options)
	require.NoError(t, err)
	serverEndpoint, isServerEndpoint := endpoint.(*ServerEndpoint)
	require.True(t, isServerEndpoint, "the constructor must return the server endpoint")
	return serverEndpoint
}

// TestServerReservesTheDataChannelHeadroomOnTheDevice is the headroom wire.
//
// The assertion is an EQUALITY against the value the data channel itself computes, not a literal:
// what the endpoint must guarantee is that the device's reservation IS the channel's requirement,
// and that requirement is a fact of the negotiated protocol and ciphers rather than a constant this
// package owns.
func TestServerReservesTheDataChannelHeadroomOnTheDevice(t *testing.T) {
	serverEndpoint := buildServerEndpoint(t, tlsServerOptions(t))

	serverOptions, err := buildServerOptions(serverEndpoint.options)
	require.NoError(t, err)
	expectedFront, expectedRear := serverOptions.DataPacketHeadroom()
	require.Positive(t, expectedFront,
		"the fixture must negotiate a transport that needs front headroom, or this test cannot tell "+
			"a correct reservation from a missing one")

	deviceOptions := serverEndpoint.deviceOptions
	require.NotNil(t, deviceOptions)
	require.Equal(t, expectedFront, deviceOptions.PacketFrontHeadroom,
		"the device must reserve the front headroom the data channel needs; without it every "+
			"outbound packet takes the copy branch of the peer data path")
	require.Equal(t, expectedRear, deviceOptions.PacketRearHeadroom,
		"and the rear headroom, which is where the AEAD tag is written after the payload")
}

// TestServerRouteOutboundIsWiredToTheDevice is the per-destination routing wire.
//
// The SERVER is the side that needs it: a decapsulated packet is addressed to a client the server
// does not dial, so the return path is resolved per packet rather than per dialled address.
// `routeOutbound` still EXISTS when this wire is missing - which is exactly how the revert survived
// a symbol-level audit - so the assertion is on the assignment.
func TestServerRouteOutboundIsWiredToTheDevice(t *testing.T) {
	serverEndpoint := buildServerEndpoint(t, tlsServerOptions(t))
	require.NotNil(t, serverEndpoint.deviceOptions.Route,
		"the device must route through this endpoint's own resolver, or a server's reply has no "+
			"return path: `routeOutbound` is dead code without this assignment")
}

// TestStaticKeyServerWiresTheSameRoutingGuard is the mode-parameterised half.
//
// The per-destination routing and the queue factory are properties of being a SERVER, not of the key
// exchange, so they must hold for both modes. The headroom is deliberately asserted as ZERO here: a
// static-key channel writes no transport encapsulation, so reserving nothing is its CORRECT answer,
// and demanding room for a header it does not write would be a different bug.
func TestStaticKeyServerWiresTheSameRoutingGuard(t *testing.T) {
	serverEndpoint := buildServerEndpoint(t, staticKeyServerOptions(t))

	require.NotNil(t, serverEndpoint.deviceOptions.Route,
		"the per-destination routing requirement belongs to being a server, not to the key exchange")
	require.Zero(t, serverEndpoint.deviceOptions.PacketFrontHeadroom,
		"a static-key channel has no transport encapsulation to reserve room for")
	require.Zero(t, serverEndpoint.deviceOptions.PacketRearHeadroom)
}

// recordingDevice answers the two device methods the server wires delegate to, and records that it
// was asked. It embeds the interface so it cannot rot as the device grows, and it deliberately
// implements ONLY these two: if the production code reaches for anything else here, the panic names
// the method rather than hiding behind a zero value.
type recordingDevice struct {
	device.Device
	frontHeadroom int
	queueCalls    int
	headroomCalls int
}

func (d *recordingDevice) FrontHeadroom() int {
	d.headroomCalls++
	return d.frontHeadroom
}

func (d *recordingDevice) NewOutboundQueue(handler func(packetBuffers []*buf.Buffer)) *tun.OutboundQueue {
	d.queueCalls++
	return nil
}

// TestTheQueueFactorySignatureMatchesWhatTheLibraryRequires is the wire that made the server dead.
//
// # What is asserted, and why the assertion is on the DELEGATION
//
// sing-openvpn refuses to construct a server whose `NewOutboundQueue` is nil
// (`ErrMissingOutboundQueue`), which is a hard start failure rather than a degraded mode. So the
// property is not "the field is non-nil" - a stub could satisfy that - it is that the installed
// factory reaches the DEVICE's queue. The recording device below makes the difference observable:
// the assertion is that calling the assembled factory increments the device's own call count.
//
// MEASURED: a version of this test that only compared the method against the library's interface
// kept PASSING while the assignment was deliberately deleted - the exact shape of the original
// regression, where every symbol survived and only the wire was gone.
//
// The `Start` call site that invokes this assembly is covered behaviourally by
// `TestOpenVPNSelfToSelf`, which is the test that actually failed in production. This one exists so
// the same defect arrives in milliseconds with a message that names the missing wire.
func TestTheQueueFactorySignatureMatchesWhatTheLibraryRequires(t *testing.T) {
	serverEndpoint := buildServerEndpoint(t, tlsServerOptions(t))
	recorder := &recordingDevice{frontHeadroom: 17}
	serverEndpoint.device = recorder

	var assembled ovpn.ServerOptions
	serverEndpoint.applyDeviceWires(&assembled)

	require.NotNil(t, assembled.NewOutboundQueue,
		"sing-openvpn refuses to construct a server without this factory: ErrMissingOutboundQueue, "+
			"which is the start failure this test exists to prevent")
	require.NotNil(t, assembled.IncomingPacketHeadroom,
		"and the inbound headroom closure, which the same assembly owns")

	// The signature half is a compile-time check: a change on either side breaks this line.
	var queueFactory func(write func(buffers []*buf.Buffer)) ovpn.OutboundQueue = assembled.NewOutboundQueue
	require.NotNil(t, queueFactory)

	queueFactory(nil)
	require.Equal(t, 1, recorder.queueCalls,
		"the installed factory must reach the DEVICE's per-destination queue; a non-nil stub that "+
			"does not delegate would look wired while a server's return path stayed broken")

	require.Equal(t, 17, assembled.IncomingPacketHeadroom(),
		"the inbound headroom must be read from the device, which is what sized it")
	require.Equal(t, 1, recorder.headroomCalls)
}

// TestTheEndpointsNeedTheDevicesQueueFactory pins the DEVICE half of the same contract.
//
// The endpoints call `device.NewOutboundQueue` and `device.FrontHeadroom`, and the device they hold
// is chosen at run time by `system` and by the platform. Asserting the interface here means a device
// implementation that stops offering either is caught at this endpoint rather than by whichever
// platform a user happens to run.
func TestTheEndpointsNeedTheDevicesQueueFactory(t *testing.T) {
	// The assertion is on the VALUE the production constructor hands an endpoint, so dropping either
	// method from the device interface is caught here rather than by whichever platform a user runs.
	deviceInterface, _ := device.New(device.Options{})
	require.Implements(t, (*interface {
		NewOutboundQueue(handler func(packetBuffers []*buf.Buffer)) *tun.OutboundQueue
		FrontHeadroom() int
	})(nil), deviceInterface,
		"both endpoints depend on these two methods; the interface is what makes the dependency "+
			"visible to every device implementation")
}

// TestClientResolvesItsDeviceMTUAndReservesHeadroom covers the client half, where the defect was a
// wrong device MTU and a per-packet copy rather than a start failure.
//
// `options.MTU` is legitimately zero when the configuration does not set one, and the device was
// configured with that zero. The tunnel MTU is not the transport MTU, so the option's absence has to
// be resolved to the default here.
func TestClientResolvesItsDeviceMTUAndReservesHeadroom(t *testing.T) {
	// A TLS-mode client refuses to build without a trust anchor, and the anchor is irrelevant to
	// what this test asserts: the DEVICE options are built before the channel is negotiated.
	trustAnchor, _ := serverTLSMaterial(t)
	endpoint, err := NewClientEndpoint(endpointTestContext(), nil, log.NewNOPFactory().Logger(), "client",
		option.OpenVPNClientEndpointOptions{
			ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 1194},
			TLS:           &option.OpenVPNOutboundTLSOptions{Certificate: trustAnchor},
		})
	require.NoError(t, err)
	clientEndpoint, isClientEndpoint := endpoint.(*ClientEndpoint)
	require.True(t, isClientEndpoint, "the constructor must return the client endpoint")

	deviceOptions := clientEndpoint.deviceOptions
	require.NotNil(t, deviceOptions)
	require.NotZero(t, deviceOptions.MTU,
		"an unset MTU option must resolve to the transport default, not be passed through as zero")
	require.Equal(t, deviceOptions.MTU, deviceOptions.Configuration.MTU,
		"the device MTU and the tunnel configuration MTU are the same resolved value")
	require.Positive(t, deviceOptions.PacketFrontHeadroom,
		"the client's data channel needs headroom too, and sing's buf.ExtendHeader PANICS rather "+
			"than copying when the buffer has less room than requested")
}
