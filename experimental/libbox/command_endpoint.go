package libbox

// This file answers one question the control channel could not answer before: is the core LISTENING,
// as opposed to this particular request having failed?
//
// # The incident it comes from
//
// A strategy-group expand produced:
//
//	Failed to update group expansion
//	set group expand: get command client: probe command server: rpc error: code = Unavailable
//	  desc = ... dial unix /private/var/mobile/Containers/Shared/AppGroup/<uuid>/command.sock:
//	  connect: connection refused
//
// `ECONNREFUSED` on a unix socket is a specific fact, and it is not "the path is wrong" (that is
// ENOENT) and not "the path is too long" (that is EINVAL at bind time). It means the socket FILE
// exists and no process is accepting on it. A Network Extension that exits without running its
// Close - a SIGSEGV, a Jetsam kill - leaves exactly that: Go's UnixListener unlinks its socket file
// on Close, and a process that dies does not reach Close.
//
// So the platform was told "rpc error: code = Unavailable ... connection refused", which is accurate
// and useless: it is the same shape as a transient transport failure, so the only thing a UI can do
// with it is show it. The core in that report had crashed; the honest answer is "the core is not
// running", and the fix for the next tap is to re-establish, not to retry against a stale socket.
//
// # What is decided here, and what is not
//
// Nothing in this file retries an RPC. Retrying an operation whose effect nobody observed is how a
// single user tap becomes two applied selections; the caller is told what happened instead. What is
// decided is (1) whether a failed call should leave the client believing a dead connection is still
// usable, and (2) whether the failure is reported as "the core is not running".
//
// Both decisions are made by an INDEPENDENT probe of the endpoint, not by reading the gRPC error
// text. That matters twice: the probe is transport truth rather than a string match, and it is only
// consulted when a call has already failed, so the healthy path pays nothing.

import (
	std_json "encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// commandEndpointProbeTimeout bounds the liveness probe.
//
// It is short on purpose. The probe runs on the failure path of a user-visible operation and it runs
// on the NetworkExtension's control thread's caller, not during establishment: a connect(2) to a
// local unix socket or to 127.0.0.1 either answers at once or never will. The value also bounds how
// long a "nothing is listening" verdict may take on a platform that blackholes the call.
const commandEndpointProbeTimeout = 150 * time.Millisecond

// ErrCommandServerNotRunning reports that the core's control channel has no listener, so the request
// was never delivered and nothing was applied.
//
// It is a sentinel so a caller in Go can branch with errors.Is. Across the gomobile bridge an
// exception arrives as a string, so the error text starts with the same words; the Apple client can
// either match that prefix or call CommandClient.ProbeCommandServer, which returns the same fact as
// a document instead of as an error.
var ErrCommandServerNotRunning = errors.New("command server is not running")

// commandServerNotRunningError carries the original failure under a stable, classifiable head.
type commandServerNotRunningError struct {
	cause error
}

func (e *commandServerNotRunningError) Error() string {
	return ErrCommandServerNotRunning.Error() + ": " + e.cause.Error()
}

func (e *commandServerNotRunningError) Unwrap() error {
	return e.cause
}

func (e *commandServerNotRunningError) Is(target error) bool {
	return target == ErrCommandServerNotRunning
}

// commandEndpoint is where the local command server listens, described in terms a probe can use.
//
// network is empty when this process cannot tell: an XPC connection or a remote daemon is not an
// address, and pretending otherwise would let a probe report a confident "not running" about
// something it never looked at.
type commandEndpoint struct {
	network string
	address string
}

// currentCommandEndpoint mirrors dialTarget's transport decision WITHOUT creating a connection.
//
// It repeats the decision rather than deriving it from dialTarget because dialTarget returns a
// dialer closure that has already resolved the socket path; asking it to also describe its target
// would mean either a second return value at every call site or a probe that dials for real and
// drops the connection. The three branches here are the same three there, and
// TestCommandEndpointMatchesDialTarget fails if they ever disagree.
func currentCommandEndpoint() commandEndpoint {
	if sXPCDialer != nil {
		return commandEndpoint{}
	}
	if sCommandServerListenPort == 0 {
		socketPath, err := resolveCommandSocketPath(sBasePath, sTempPath)
		if err != nil {
			return commandEndpoint{}
		}
		return commandEndpoint{network: "unix", address: socketPath}
	}
	return commandEndpoint{
		network: "tcp",
		address: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sCommandServerListenPort))),
	}
}

// probe reports whether a listener answers at the endpoint.
//
// The second result says whether the answer is DEFINITIVE: a connection refused or a missing file is
// proof that nothing is listening, while a timeout, a permission error or an unsupported network is
// not. Callers must not turn "could not tell" into "not running": that is how a working core gets
// reported as dead, which is a worse failure than the raw RPC error it replaces.
func (e commandEndpoint) probe() (listening bool, definitive bool) {
	if e.network == "" || e.address == "" {
		return false, false
	}
	dialer := net.Dialer{Timeout: commandEndpointProbeTimeout}
	connection, err := dialer.Dial(e.network, e.address)
	if err == nil {
		_ = connection.Close()
		return true, true
	}
	if isNoListenerError(err) {
		return false, true
	}
	return false, false
}

// isNoListenerError reports whether err is proof that nothing is accepting connections.
//
// ECONNREFUSED is the socket file exists / nothing bound case, and ENOENT the file is not there at
// all. Both mean the same thing to a caller ("no server"), and the difference is diagnostic only:
// ENOENT says the core never started, ECONNREFUSED says it started and stopped without unlinking.
// errors.Is traverses net.OpError and os.SyscallError. The refused code is platform-specific and
// lives in command_endpoint_unix.go / command_endpoint_windows.go.
func isNoListenerError(err error) bool {
	return isConnectionRefused(err) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, os.ErrNotExist)
}

// isUnreachableStatus reports whether err is the status a control channel that could not be reached
// produces.
//
// Two codes qualify, for two different reasons:
//
//	Unavailable      the transport could not carry the call;
//	DeadlineExceeded the establishment retry loop ran out of attempts while redialing.
//
// The second one is the one the incident produced. A handler-bound client probes with
// WaitForReady(true), so a core that is simply not there does not answer "unavailable" - the probe
// waits out its own deadline and reports DEADLINE_EXCEEDED, which reads like "slow" and invites a
// retry of an operation nothing ever received. The probe decides the truth in both cases.
//
// The code is extracted with errors.As because the call sites wrap the error with context first, and
// status.Code does not unwrap.
func isUnreachableStatus(err error) bool {
	switch commandStatusCode(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

// commandStatusCode returns err's gRPC code through any amount of wrapping.
func commandStatusCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	var statusError interface{ GRPCStatus() *status.Status }
	if errors.As(err, &statusError) {
		if grpcStatus := statusError.GRPCStatus(); grpcStatus != nil {
			return grpcStatus.Code()
		}
	}
	return codes.Unknown
}

// classifyCommandFailure decides what a failed call means for the connection that produced it.
//
//   - The failure is not "the control channel could not be reached" -> returned unchanged. An answer
//     from a live server is a verdict about the REQUEST, and tearing the connection down for it
//     would kill the subscriptions riding on it.
//   - Nothing is listening at the endpoint this client dialed -> the connection is worthless: it is
//     closed, so the next operation re-establishes instead of waiting out gRPC's reconnect backoff
//     (which grows to minutes and would keep failing long after the kernel came back). The error is
//     then reported as ErrCommandServerNotRunning, because "no listener" is a fact the platform can
//     act on.
//   - The endpoint cannot be probed, or something IS listening -> returned unchanged. The connection
//     may still recover on its own, and claiming the core is down without evidence is worse than
//     forwarding the original error.
//
// The endpoint probed is the one THIS client dialed, remembered at establishment, not whatever the
// current process globals say: a client handed a connection from somewhere else (a test, an embedder,
// an XPC handoff) has no address to probe, and guessing one would let a healthy core be declared dead.
func (c *CommandClient) classifyCommandFailure(err error) error {
	if err == nil {
		return nil
	}
	if c.remote != nil {
		// A remote daemon is reached through a transport this process cannot liveness-check; the
		// classification would be a guess.
		return err
	}
	if !isUnreachableStatus(err) {
		return err
	}
	c.clientAccess.RLock()
	endpoint := c.serverEndpoint
	c.clientAccess.RUnlock()
	listening, definitive := endpoint.probe()
	if !definitive || listening {
		return err
	}
	c.closeConnection()
	return &commandServerNotRunningError{cause: err}
}

// CommandServerState is the answer to "can this client reach the core right now".
type CommandServerState int32

const (
	// CommandServerStateUnknown means this process cannot tell: the transport is XPC, a remote
	// daemon, or the socket path does not resolve.
	CommandServerStateUnknown CommandServerState = 0
	// CommandServerStateRunning means a listener accepted the probe.
	CommandServerStateRunning CommandServerState = 1
	// CommandServerStateNotRunning means nothing is listening: the core is stopped or has exited.
	CommandServerStateNotRunning CommandServerState = 2
)

func (s CommandServerState) string() string {
	switch s {
	case CommandServerStateRunning:
		return "running"
	case CommandServerStateNotRunning:
		return "notRunning"
	default:
		return "unknown"
	}
}

// commandServerProbe is the document ProbeCommandServer returns.
type commandServerProbe struct {
	State string `json:"state"`
	// Detail is a bounded diagnostic: the endpoint's final path element and directory length, or how
	// the transport was chosen. It never carries a container path; the same rule as the server's own
	// diagnostics, because this string reaches a UI and an issue report.
	Detail string `json:"detail,omitempty"`
}

// ProbeCommandServer answers "is the core reachable" without performing any operation, so a UI can
// tell "the VPN is not running" from "this action failed" instead of parsing an RPC error.
//
// It returns a document rather than an error for the same reason the other new entry points do: a
// gomobile call that returns an error loses its result in the generated bridge, and "not running" is
// an answer, not a failure of the probe. The probe itself cannot fail.
//
// This is a capability the Apple client needs to stop treating a dead control channel as a generic
// error: see docs/fork/ios-command-client-state.md for the contract and the minimum UI change.
func (c *CommandClient) ProbeCommandServer() *StringBox {
	endpoint := currentCommandEndpoint()
	if c != nil && c.remote != nil {
		return wrapString(marshalCommandServerProbe(commandServerProbe{
			State:  CommandServerStateUnknown.string(),
			Detail: "remote daemon",
		}))
	}
	if endpoint.network == "" {
		return wrapString(marshalCommandServerProbe(commandServerProbe{
			State:  CommandServerStateUnknown.string(),
			Detail: "transport has no probatory address",
		}))
	}
	listening, definitive := endpoint.probe()
	state := CommandServerStateUnknown
	detail := socketPathDiagnostics(endpoint.address)
	if endpoint.network == "tcp" {
		detail = "tcp listener in a " + strconv.Itoa(len(endpoint.address)) + "-byte address"
	}
	if definitive {
		if listening {
			state = CommandServerStateRunning
		} else {
			state = CommandServerStateNotRunning
		}
	}
	return wrapString(marshalCommandServerProbe(commandServerProbe{
		State:  state.string(),
		Detail: detail,
	}))
}

// marshalCommandServerProbe encodes the probe document. The struct is fixed and local, so a marshal
// failure is impossible in practice; an empty box is still better than a panic on the UI thread.
func marshalCommandServerProbe(probe commandServerProbe) string {
	encoded, err := std_json.Marshal(probe)
	if err != nil {
		return ""
	}
	return string(encoded)
}
