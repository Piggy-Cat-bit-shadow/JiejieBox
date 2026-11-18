package libbox

// The control channel's failure states, over a real gRPC transport.
//
// # What these tests are about
//
// `set group expand: get command client: probe command server: rpc error: code = Unavailable
// ... connect: connection refused` is what the iOS client showed when the Network Extension was no
// longer running. Two things were wrong with that answer, and neither is the wording:
//
//  1. the client could not say the core was DOWN - the failure looked exactly like a transient
//     transport error, so the UI had nothing to branch on but a string;
//  2. a client that already held a connection kept holding it. The connection is dead, gRPC's own
//     reconnect backoff grows with every failed attempt, and a kernel that comes back a few seconds
//     later is then ignored by its own UI for as long as the backoff lasts.
//
// The tests below pin the replacement behaviour on a real TCP transport (a unix socket cannot be
// bound on a Windows host, and the decision under test is transport-independent):
//
//	core not running            -> ErrCommandServerNotRunning, and no cached connection survives
//	core comes back             -> the NEXT call succeeds, without any reconnect API being called
//	core answers "Unavailable"  -> the connection is KEPT; that is a verdict, not a dead transport
//
// The last case is the one that makes the first two safe: the daemon does return codes.Unavailable
// as a semantic answer (daemon/managed_service.go, "OOM recorder not available"), and tearing the
// transport down for it would kill live subscriptions over an answer.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type controlStubStartedService struct {
	daemon.UnimplementedStartedServiceServer

	expandCalls atomic.Int32
	expandError error

	// expandEntered and expandGates let a test ORDER two concurrent calls deterministically: the i-th
	// SetGroupExpand announces its index on expandEntered and then waits for expandGates[i-1] to close.
	// Nil means "do not gate", which is what every other test in this package wants.
	expandEntered chan int32
	expandGates   []chan struct{}
}

func (s *controlStubStartedService) GetStartedAt(context.Context, *emptypb.Empty) (*daemon.StartedAt, error) {
	return &daemon.StartedAt{StartedAt: time.Now().Unix()}, nil
}

func (s *controlStubStartedService) SetGroupExpand(context.Context, *daemon.SetGroupExpandRequest) (*emptypb.Empty, error) {
	index := s.expandCalls.Add(1)
	if s.expandEntered != nil {
		s.expandEntered <- index
	}
	if gate := s.expandGates; gate != nil && int(index) <= len(gate) {
		<-gate[index-1]
	}
	if s.expandError != nil {
		return nil, s.expandError
	}
	return &emptypb.Empty{}, nil
}

// controlServer is one command server listening on a fixed TCP port, so a test can stop it and bring
// a replacement back on the SAME address - which is the situation a relaunched kernel creates.
type controlServer struct {
	port      uint16
	server    *grpc.Server
	listener  net.Listener
	startedOn string
}

func startControlServer(t *testing.T, port uint16, implementation *controlStubStartedService) *controlServer {
	t.Helper()
	address := net.JoinHostPort("127.0.0.1", itoaPort(port))
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	server := grpc.NewServer()
	daemon.RegisterStartedServiceServer(server, implementation)
	go func() {
		_ = server.Serve(listener)
	}()
	return &controlServer{
		port:      uint16(listener.Addr().(*net.TCPAddr).Port),
		server:    server,
		listener:  listener,
		startedOn: address,
	}
}

func (s *controlServer) stop() {
	s.server.Stop()
	_ = s.listener.Close()
}

// useControlServerPort points the production client at the test listener and restores the previous
// globals afterwards. The client resolves its target from these same globals in production.
func useControlServerPort(t *testing.T, port uint16) {
	t.Helper()
	previousPort := sCommandServerListenPort
	previousBase := sBasePath
	previousTemp := sTempPath
	t.Cleanup(func() {
		sCommandServerListenPort = previousPort
		sBasePath = previousBase
		sTempPath = previousTemp
	})
	sCommandServerListenPort = port
	sBasePath = t.TempDir()
	sTempPath = t.TempDir()
}

// freePort binds and releases a port, which leaves it with no listener: the "kernel is not running"
// state, without relying on a hard-coded port being idle.
func freePort(t *testing.T) uint16 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	return port
}

func itoaPort(port uint16) string {
	if port == 0 {
		return "0"
	}
	return decimalUint16(port)
}

func decimalUint16(port uint16) string {
	if port == 0 {
		return "0"
	}
	var digits [5]byte
	index := len(digits)
	for port > 0 {
		index--
		digits[index] = byte('0' + port%10)
		port /= 10
	}
	return string(digits[index:])
}

func TestAStandaloneClientSaysTheCoreIsNotRunning(t *testing.T) {
	port := freePort(t)
	useControlServerPort(t, port)

	client := NewStandaloneCommandClient()
	started := time.Now()
	err := client.SetGroupExpand("proxy", true)
	elapsed := time.Since(started)

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCommandServerNotRunning),
		"a refused connection must be reported as 'the core is not running', got: %v", err)
	require.Contains(t, err.Error(), ErrCommandServerNotRunning.Error())
	require.Less(t, elapsed, 5*time.Second, "an unreachable core must fail a UI operation promptly")
}

func TestAHandlerBoundClientSaysTheCoreIsNotRunningWithoutHanging(t *testing.T) {
	port := freePort(t)
	useControlServerPort(t, port)

	// A handler-bound client is the one that waits out a starting extension: ten probes with a
	// growing delay. The wait must terminate, and it must still end in the classified answer.
	client := NewCommandClient(nil, nil)
	started := time.Now()
	err := client.SetGroupExpand("proxy", true)
	elapsed := time.Since(started)

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCommandServerNotRunning), "got: %v", err)
	require.Less(t, elapsed, 10*time.Second, "the establishment retries must be bounded")
}

func TestAClientRecoversWhenTheCoreReturns(t *testing.T) {
	implementation := &controlStubStartedService{}
	server := startControlServer(t, 0, implementation)
	useControlServerPort(t, server.port)

	client := NewCommandClient(nil, nil)
	require.NoError(t, client.SetGroupExpand("proxy", true), "the first call establishes and caches a connection")
	require.EqualValues(t, 1, implementation.expandCalls.Load())

	// The Network Extension dies. The cached connection is now a dead object.
	server.stop()
	time.Sleep(50 * time.Millisecond)
	require.Error(t, client.SetGroupExpand("proxy", false),
		"a call against a dead core must fail")

	client.clientAccess.RLock()
	cachedAfterFailure := client.grpcConn
	client.clientAccess.RUnlock()
	require.Nil(t, cachedAfterFailure,
		"the dead connection must not be kept: while it is cached, every later operation waits out "+
			"gRPC's reconnect backoff instead of re-establishing")

	// The kernel comes back on the same address. The next call must find it, without the platform
	// having to call Connect() again.
	restarted := rebindOrSkip(t, server.port, implementation)
	require.NoError(t, client.SetGroupExpand("proxy", true),
		"the first call after a restart must re-establish instead of reusing the dead connection")
	require.EqualValues(t, 2, implementation.expandCalls.Load())
	_ = restarted
}

// rebindOrSkip brings a replacement server up on the same port. A host that refuses to rebind a
// just-released port (some Windows configurations do, briefly) cannot test the recovery half, and
// saying so is better than a flaky failure.
func rebindOrSkip(t *testing.T, port uint16, implementation *controlStubStartedService) *controlServer {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", decimalUint16(port)))
		if err == nil {
			server := grpc.NewServer()
			daemon.RegisterStartedServiceServer(server, implementation)
			go func() {
				_ = server.Serve(listener)
			}()
			t.Cleanup(func() {
				server.Stop()
				_ = listener.Close()
			})
			return &controlServer{port: port, server: server, listener: listener}
		}
		if time.Now().After(deadline) {
			t.Skipf("the host will not rebind 127.0.0.1:%d after a stop (%v); the recovery half of "+
				"this test cannot run here", port, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestAServerAnswerOfUnavailableKeepsTheConnection(t *testing.T) {
	implementation := &controlStubStartedService{
		expandError: status.Error(codes.Unavailable, "the running box is not ready"),
	}
	server := startControlServer(t, 0, implementation)
	useControlServerPort(t, server.port)

	client := NewCommandClient(nil, nil)
	err := client.SetGroupExpand("proxy", true)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrCommandServerNotRunning),
		"a live server answering Unavailable is a verdict about the request, not a dead core: %v", err)

	client.clientAccess.RLock()
	cached := client.grpcConn
	client.clientAccess.RUnlock()
	require.NotNil(t, cached,
		"the connection must be kept: it is healthy, and the subscriptions riding on it would die if "+
			"a semantic Unavailable tore it down")
	require.EqualValues(t, 1, implementation.expandCalls.Load())
}

func TestProbeCommandServerReportsRunningAndNotRunning(t *testing.T) {
	implementation := &controlStubStartedService{}
	server := startControlServer(t, 0, implementation)
	useControlServerPort(t, server.port)

	client := NewCommandClient(nil, nil)
	require.Equal(t, "running", decodeCommandServerProbe(t, client.ProbeCommandServer()).State)

	server.stop()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, "notRunning", decodeCommandServerProbe(t, client.ProbeCommandServer()).State)
}

func TestProbeCommandServerReportsUnknownForAnXPCTransport(t *testing.T) {
	previousDialer := sXPCDialer
	t.Cleanup(func() { sXPCDialer = previousDialer })
	sXPCDialer = xpcDialerStub{}

	client := NewCommandClient(nil, nil)
	probe := decodeCommandServerProbe(t, client.ProbeCommandServer())
	require.Equal(t, "unknown", probe.State,
		"a transport this process cannot probe must not be reported as down")
}

// decodeCommandServerProbe parses the document the platform receives.
func decodeCommandServerProbe(t *testing.T, boxed *StringBox) commandServerProbe {
	t.Helper()
	require.NotNil(t, boxed)
	require.NotEmpty(t, boxed.Value)
	var probe commandServerProbe
	require.NoError(t, json.Unmarshal([]byte(boxed.Value), &probe))
	return probe
}

type xpcDialerStub struct{}

func (xpcDialerStub) DialXPC() (int32, error) { return -1, errors.New("stub") }

// TestCommandEndpointMatchesDialTarget keeps the probe's view of the transport and the dialer's
// transport from drifting apart. They are two implementations of one decision, and if they disagree
// the client would probe one endpoint and speak to another - reporting a healthy core as down, or a
// dead one as healthy.
func TestCommandEndpointMatchesDialTarget(t *testing.T) {
	previousPort := sCommandServerListenPort
	previousBase := sBasePath
	previousTemp := sTempPath
	previousDialer := sXPCDialer
	t.Cleanup(func() {
		sCommandServerListenPort = previousPort
		sBasePath = previousBase
		sTempPath = previousTemp
		sXPCDialer = previousDialer
	})

	sBasePath = t.TempDir()
	sTempPath = t.TempDir()

	// TCP: the dial target IS the endpoint address.
	sXPCDialer = nil
	sCommandServerListenPort = 47111
	target, contextDialer := dialTarget()
	require.Nil(t, contextDialer)
	endpoint := currentCommandEndpoint()
	require.Equal(t, "tcp", endpoint.network)
	require.Equal(t, target, endpoint.address)

	// Unix socket: the dial target is a passthrough name and the dialer closes over the resolved
	// path, so the endpoint must resolve the SAME path from the same inputs.
	sCommandServerListenPort = 0
	target, contextDialer = dialTarget()
	require.NotNil(t, contextDialer)
	require.Equal(t, "passthrough:///command-socket", target)
	socketPath, err := resolveCommandSocketPath(sBasePath, sTempPath)
	require.NoError(t, err)
	endpoint = currentCommandEndpoint()
	require.Equal(t, "unix", endpoint.network)
	require.Equal(t, socketPath, endpoint.address)

	// XPC: there is no address, and the probe must say so rather than guess.
	sXPCDialer = xpcDialerStub{}
	endpoint = currentCommandEndpoint()
	require.Empty(t, endpoint.network)
	require.False(t, len(endpoint.address) > 0)
}
