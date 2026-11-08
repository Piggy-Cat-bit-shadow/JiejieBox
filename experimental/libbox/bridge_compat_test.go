package libbox

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/daemon"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// O06 — the cross-language boundary and the old/new version contract.
//
// The gomobile surface itself is pinned by gomobile_surface_test.go (no new
// pointer-bearing result frames). What this file adds is the behaviour on that
// boundary and the two version directions:
//
//   - typed-nil: a nil *CommandServer reached through the bound type must return
//     a clear error, not panic inside the Go runtime;
//   - nil-success: an error never carries a partial result object;
//   - inspection: the parse error stays a *json.UnmarshalTypeError chain member
//     instead of being flattened into a string at the boundary;
//   - version negotiation: a client reads the API version the service reports;
//   - missing capability: a service that does not implement the call produces a
//     clear gRPC error (UNIMPLEMENTED), not a panic or a hang. That is the
//     "new client against an old service" direction; the reverse direction is a
//     binding/ABI concern covered by the surface register, not something a Go
//     test can fake.

// bridgeTestServer starts an in-process gRPC server with the given registration
// and returns its address.
func bridgeTestServer(t *testing.T, register func(server *grpc.Server), options ...grpc.ServerOption) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(options...)
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String()
}

// bridgeTestClient returns a CommandClient wired to address, bypassing the
// remote/standalone dial so the test controls the service it talks to.
func bridgeTestClient(t *testing.T, address string) *CommandClient {
	t.Helper()
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(unaryClientAuthInterceptor),
	)
	require.NoError(t, err)
	client := NewCommandClient(nil, &CommandClientOptions{})
	client.grpcConn = connection
	client.grpcClient = daemon.NewStartedServiceClient(connection)
	t.Cleanup(func() {
		client.cancel()
		_ = connection.Close()
	})
	return client
}

// TestExplainPathOnANilServerReturnsAnError is the typed-nil half: the bound
// type can reach the platform as a nil pointer, and calling through it must
// produce the documented error instead of dereferencing the receiver.
func TestExplainPathOnANilServerReturnsAnError(t *testing.T) {
	var nilServer *CommandServer
	explanation, err := nilServer.ExplainPath("proxy", "tcp")
	require.Error(t, err)
	require.Nil(t, explanation, "an error must not carry a result object")
	require.Contains(t, err.Error(), "not started")

	// The same pointer behind the interface the platform holds: still a nil
	// receiver, not a typed-nil panic.
	type explainer interface {
		ExplainPath(root string, network string) (*StringBox, error)
	}
	var asInterface explainer = nilServer
	explanation, err = asInterface.ExplainPath("proxy", "tcp")
	require.Error(t, err)
	require.Nil(t, explanation)
}

// TestAnalyzeConfigKeepsTheErrorInspectable pins nil-success plus the error
// chain: the result is nil (never a partial document), the chain is not
// flattened into a string at the bridge, and errors.As still works where the
// decoder exposes an exported type.
//
// The type error below is matched by walking Unwrap rather than by As because the
// extended JSON decoder is a fork of encoding/json with its own internal
// UnmarshalTypeError; the public package exports only SyntaxError, so a syntax
// failure is where As can be asserted through the boundary.
func TestAnalyzeConfigKeepsTheErrorInspectable(t *testing.T) {
	analysis, err := AnalyzeConfig(`{"inbounds": "not-an-array"}`)
	require.Error(t, err)
	require.Nil(t, analysis, "an error must not carry a partial result (nil-success semantics)")
	cause := errors.Unwrap(err)
	require.NotNil(t, cause, "the boundary must keep the error chain (Unwrap) instead of stringifying it: %v", err)
	require.Contains(t, cause.Error(), "inbounds", "the chain must still name the failing field: %v", cause)
	require.False(t, strings.Contains(err.Error(), "github.com/"), "the error must not be a flattened Go chain: %v", err)

	_, syntaxErr := AnalyzeConfig(`{"outbounds": [}`)
	require.Error(t, syntaxErr)
	var jsonSyntaxError *singjson.SyntaxError
	require.True(t, errors.As(syntaxErr, &jsonSyntaxError),
		"errors.As must reach the decoder's syntax error through the boundary, got %T: %v", syntaxErr, syntaxErr)
}

// TestNewClientReadsTheServersAPIVersion is the version-negotiation direction a
// Go test can pin: the service reports its API version, and the client passes
// that value through so an old/new pairing can decide what to call.
func TestNewClientReadsTheServersAPIVersion(t *testing.T) {
	address := bridgeTestServer(t, func(server *grpc.Server) {
		daemon.RegisterStartedServiceServer(server, daemon.NewStartedService(daemon.ServiceOptions{Context: context.Background()}))
	})
	client := bridgeTestClient(t, address)

	version, err := client.GetAPIVersion()
	require.NoError(t, err)
	require.Equal(t, int32(daemon.APIVersion), version,
		"the client must surface the version the service reports so the pairing can be checked")
}

// TestAnOldServiceWithoutTheCapabilitySurfacesAClearError is the negative
// control for the direction above: a service that predates the call answers
// UNIMPLEMENTED for every method. The client must report that as an error with
// the gRPC code intact (so a caller can tell "old daemon" from "broken daemon"),
// not panic or wait forever.
func TestAnOldServiceWithoutTheCapabilitySurfacesAClearError(t *testing.T) {
	// Nothing is registered: UnknownServiceHandler answers every method with
	// UNIMPLEMENTED, which is what an old daemon does for a method it lacks.
	address := bridgeTestServer(t, func(server *grpc.Server) {},
		grpc.UnknownServiceHandler(func(srv any, stream grpc.ServerStream) error {
			return status.Error(codes.Unimplemented, "method not implemented by this daemon")
		}))
	client := bridgeTestClient(t, address)

	version, err := client.GetAPIVersion()
	require.Error(t, err, "a missing capability must surface as an error")
	require.Zero(t, version)
	require.Equal(t, codes.Unimplemented, status.Code(err),
		"the gRPC code must survive to the caller so an old daemon is distinguishable from a broken one: %v", err)
}
