package libbox

import (
	"context"
	std_json "encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type CommandClient struct {
	handler           CommandClientHandler
	grpcConn          *grpc.ClientConn
	grpcClient        daemon.StartedServiceClient
	grpcManagedClient daemon.ManagedServiceClient
	options           CommandClientOptions
	remote            *remoteConnection
	ctx               context.Context
	cancel            context.CancelFunc
	clientAccess      sync.RWMutex
	connectAccess     sync.Mutex
	standalone        bool

	// connectionGeneration identifies the client's CURRENT connection. A stream delivers events
	// through a handler the platform may block in, so a cancellation alone cannot stop a batch that
	// is already in flight: every delivery re-checks this generation, and a newer connect makes the
	// older stream's remaining batches and its Disconnected callback stale.
	connectionGeneration atomic.Uint64
}

// connectionSubscriptionID numbers connection subscriptions PROCESS-WIDE, so two clients (or one
// client reconnecting) can never share an identity. The consumer uses it to reject a batch that
// belongs to a subscription which has already been superseded.
var connectionSubscriptionID atomic.Uint64

type CommandClientOptions struct {
	commands       []int32
	StatusInterval int64
}

func (o *CommandClientOptions) AddCommand(command int32) {
	o.commands = append(o.commands, command)
}

type CommandClientHandler interface {
	Connected()
	Disconnected(message string)
	SetDefaultLogLevel(level int32)
	ClearLogs()
	WriteLogs(messageList LogIterator)
	WriteStatus(message *StatusMessage)
	WriteGroups(message OutboundGroupIterator)
	WriteOutbounds(message OutboundGroupItemIterator)
	InitializeClashMode(modeList StringIterator, currentMode string)
	UpdateClashMode(newMode string)
	WriteConnectionEvents(events *ConnectionEvents)
}

type LogEntry struct {
	Level   int32
	Message string
}

type LogIterator interface {
	Len() int32
	HasNext() bool
	Next() *LogEntry
}

type XPCDialer interface {
	DialXPC() (int32, error)
}

var sXPCDialer XPCDialer

func SetXPCDialer(dialer XPCDialer) {
	sXPCDialer = dialer
}

func NewStandaloneCommandClient() *CommandClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &CommandClient{
		ctx:        ctx,
		cancel:     cancel,
		standalone: true,
	}
}

func NewCommandClient(handler CommandClientHandler, options *CommandClientOptions) *CommandClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &CommandClient{
		handler: handler,
		options: common.PtrValueOrDefault(options),
		ctx:     ctx,
		cancel:  cancel,
	}
}

func unaryClientAuthInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if sCommandServerSecret != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-command-secret", sCommandServerSecret)
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

func streamClientAuthInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	if sCommandServerSecret != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-command-secret", sCommandServerSecret)
	}
	return streamer(ctx, desc, cc, method, opts...)
}

const (
	commandClientDialAttempts  = 10
	commandClientDialBaseDelay = 100 * time.Millisecond
	commandClientDialStepDelay = 50 * time.Millisecond
	commandClientProbeTimeout  = 2 * time.Second
)

func commandClientDialDelay(attempt int) time.Duration {
	return commandClientDialBaseDelay + time.Duration(attempt)*commandClientDialStepDelay
}

func dialTarget() (string, func(context.Context, string) (net.Conn, error)) {
	if sXPCDialer != nil {
		return "passthrough:///xpc", func(ctx context.Context, _ string) (net.Conn, error) {
			fileDescriptor, err := sXPCDialer.DialXPC()
			if err != nil {
				return nil, E.Cause(err, "dial xpc")
			}
			return networkConnectionFromFileDescriptor(fileDescriptor)
		}
	}
	if sCommandServerListenPort == 0 {
		socketPath, sockErr := resolveCommandSocketPath(sBasePath, sTempPath)
		if sockErr != nil {
			// The address is computed here but used lazily; keep this function's shape and
			// report the failure when a dial actually asks for it.
			return "passthrough:///command-socket", func(context.Context, string) (net.Conn, error) {
				return nil, sockErr
			}
		}
		return "passthrough:///command-socket", func(ctx context.Context, _ string) (net.Conn, error) {
			var networkDialer net.Dialer
			return networkDialer.DialContext(ctx, "unix", socketPath)
		}
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sCommandServerListenPort))), nil
}

func networkConnectionFromFileDescriptor(fileDescriptor int32) (net.Conn, error) {
	file := os.NewFile(uintptr(fileDescriptor), "xpc-command-socket")
	if file == nil {
		return nil, E.New("invalid file descriptor")
	}
	networkConnection, err := net.FileConn(file)
	if err != nil {
		file.Close()
		return nil, E.Cause(err, "create connection from fd")
	}
	file.Close()
	return networkConnection, nil
}

func localDialOptions(contextDialer func(context.Context, string) (net.Conn, error)) []grpc.DialOption {
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(daemon.UnaryClientLocaleInterceptor, unaryClientAuthInterceptor),
		grpc.WithChainStreamInterceptor(daemon.StreamClientLocaleInterceptor, streamClientAuthInterceptor),
	}
	if contextDialer != nil {
		options = append(options, grpc.WithContextDialer(contextDialer))
	}
	return options
}

// establishConnection dials the command server the client is bound to: the
// local command server (over socket/XPC) or a remote API service.
func (c *CommandClient) establishConnection(ctx context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
	if c.remote != nil {
		return c.dialRemote(ctx)
	}
	target, contextDialer := dialTarget()
	return c.dialWithRetry(ctx, target, localDialOptions(contextDialer), !c.standalone)
}

// dialWithRetry connects to the local command server. For a handler-bound
// client the retry loop waits out the server starting up: WaitForReady keeps
// the probe redialing and the loop reissues it with a growing delay, so a
// freshly launched extension is picked up without surfacing a transient
// "unavailable" to the UI. A standalone client issues a single fail-fast
// probe instead: it serves a query from a UI that does not own the service
// lifecycle, and a server that is not running is reported immediately.
func (c *CommandClient) dialWithRetry(ctx context.Context, target string, dialOptions []grpc.DialOption, retryDial bool) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
	if !retryDial {
		connection, err := grpc.NewClient(target, dialOptions...)
		if err != nil {
			return nil, nil, E.Cause(err, "create command client")
		}
		client := daemon.NewStartedServiceClient(connection)
		probeCtx, cancel := context.WithTimeout(ctx, commandClientProbeTimeout)
		_, err = client.GetStartedAt(probeCtx, &emptypb.Empty{}, grpc.WaitForReady(false))
		cancel()
		if err != nil {
			connection.Close()
			return nil, nil, E.Cause(err, "probe command server")
		}
		return connection, client, nil
	}

	var connection *grpc.ClientConn
	var client daemon.StartedServiceClient
	var lastError error

	for attempt := range commandClientDialAttempts {
		if connection == nil {
			var err error
			connection, err = grpc.NewClient(target, dialOptions...)
			if err != nil {
				lastError = err
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(commandClientDialDelay(attempt)):
				}
				continue
			}
			client = daemon.NewStartedServiceClient(connection)
		}
		probeCtx, cancel := context.WithTimeout(ctx, commandClientDialDelay(attempt))
		_, err := client.GetStartedAt(probeCtx, &emptypb.Empty{}, grpc.WaitForReady(true))
		cancel()
		if err == nil {
			return connection, client, nil
		}
		lastError = err
		if ctx.Err() != nil {
			break
		}
	}

	if connection != nil {
		connection.Close()
	}
	return nil, nil, E.Cause(lastError, "probe command server")
}

func (c *CommandClient) dialRemote(ctx context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
	connection, err := grpc.NewClient(c.remote.target, c.remote.dialOptions...)
	if err != nil {
		return nil, nil, E.Cause(err, "create remote command client")
	}
	client := daemon.NewStartedServiceClient(connection)
	probeCtx, cancel := context.WithTimeout(ctx, commandClientRemoteProbeTimeout)
	defer cancel()
	_, err = client.GetStartedAt(probeCtx, &emptypb.Empty{})
	if err != nil {
		connection.Close()
		return nil, nil, E.Cause(err, "connect to remote server")
	}
	return connection, client, nil
}

func (c *CommandClient) Connect() error {
	return c.connect(c.establishConnection)
}

func (c *CommandClient) ConnectWithFD(fd int32) error {
	networkConnection, err := networkConnectionFromFileDescriptor(fd)
	if err != nil {
		return err
	}
	err = c.connect(func(ctx context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error) {
		return c.dialWithRetry(ctx, "passthrough:///xpc", localDialOptions(func(context.Context, string) (net.Conn, error) {
			return networkConnection, nil
		}), false)
	})
	if err != nil {
		networkConnection.Close()
	}
	return err
}

func (c *CommandClient) connect(dial func(context.Context) (*grpc.ClientConn, daemon.StartedServiceClient, error)) error {
	ctx, cancel := context.WithCancel(context.Background())
	// A new connection attempt invalidates every stream of the previous one AT ONCE: a handler that
	// is already running an old batch must not apply it after this point.
	//
	// The value is the connection's IDENTITY, and it is carried to every stream handler this connect
	// starts. Reading the current generation later, inside a handler, is not equivalent: a handler
	// whose Subscribe call blocked across the next connect would read the NEW generation and claim
	// the old subscription as current.
	generation := c.connectionGeneration.Add(1)
	c.clientAccess.Lock()
	c.cancel()
	previousConnection := c.grpcConn
	c.grpcConn = nil
	c.grpcClient = nil
	c.grpcManagedClient = nil
	c.ctx = ctx
	c.cancel = cancel
	c.clientAccess.Unlock()
	common.Close(common.PtrOrNil(previousConnection))

	c.connectAccess.Lock()
	if ctx.Err() != nil {
		c.connectAccess.Unlock()
		return ctx.Err()
	}
	c.closeConnection()
	connection, client, err := dial(ctx)
	if err != nil {
		c.connectAccess.Unlock()
		return err
	}
	c.clientAccess.Lock()
	if ctx.Err() != nil {
		c.clientAccess.Unlock()
		c.connectAccess.Unlock()
		connection.Close()
		return ctx.Err()
	}
	c.grpcConn = connection
	c.grpcClient = client
	c.grpcManagedClient = daemon.NewManagedServiceClient(connection)
	c.clientAccess.Unlock()
	c.connectAccess.Unlock()

	c.handler.Connected()
	return c.dispatchCommands(client, ctx, generation)
}

func (c *CommandClient) dispatchCommands(client daemon.StartedServiceClient, ctx context.Context, generation uint64) error {
	for _, command := range c.options.commands {
		switch command {
		case CommandLog:
			go c.handleLogStream(client, ctx, generation)
		case CommandStatus:
			go c.handleStatusStream(client, ctx, generation)
		case CommandGroup:
			go c.handleGroupStream(client, ctx, generation)
		case CommandClashMode:
			go c.handleClashModeStream(client, ctx, generation)
		case CommandConnections:
			go c.handleConnectionsStream(client, ctx, generation)
		case CommandOutbounds:
			go c.handleOutboundsStream(client, ctx, generation)
		default:
			return E.New("unknown command: ", command)
		}
	}
	return nil
}

// stillConnected reports whether generation is still the client's current connection.
func (c *CommandClient) stillConnected(generation uint64) bool {
	return c.connectionGeneration.Load() == generation
}

// reportDisconnected reports a stream failure to the platform handler ONLY while the connection that
// produced it is still the current one.
//
// Every stream handler holds the generation of the connection it was started for. A failure that
// arrives after a newer connect describes a connection that no longer exists, and telling the
// platform "the current connection failed" is a fact about the wrong object: on the platform side
// that clears the new session's state and shows an error for a tunnel that is up.
func (c *CommandClient) reportDisconnected(generation uint64, cause error) {
	if !c.stillConnected(generation) {
		return
	}
	c.handler.Disconnected(cause.Error())
}

func (c *CommandClient) Disconnect() error {
	if c == nil {
		return nil
	}
	c.clientAccess.Lock()
	c.cancel()
	connection := c.grpcConn
	c.grpcConn = nil
	c.grpcClient = nil
	c.grpcManagedClient = nil
	c.clientAccess.Unlock()
	return common.Close(common.PtrOrNil(connection))
}

func (c *CommandClient) getClientForCall() (daemon.StartedServiceClient, context.Context, error) {
	c.clientAccess.RLock()
	if c.grpcClient != nil {
		defer c.clientAccess.RUnlock()
		return c.grpcClient, c.ctx, nil
	}
	ctx := c.ctx
	c.clientAccess.RUnlock()

	c.connectAccess.Lock()
	defer c.connectAccess.Unlock()

	c.clientAccess.RLock()
	if ctx.Err() != nil {
		c.clientAccess.RUnlock()
		return nil, nil, ctx.Err()
	}
	if c.grpcClient != nil {
		defer c.clientAccess.RUnlock()
		return c.grpcClient, ctx, nil
	}
	c.clientAccess.RUnlock()

	connection, client, err := c.establishConnection(ctx)
	if err != nil {
		return nil, nil, E.Cause(err, "get command client")
	}
	c.clientAccess.Lock()
	if ctx.Err() != nil {
		c.clientAccess.Unlock()
		connection.Close()
		return nil, nil, ctx.Err()
	}
	c.grpcConn = connection
	c.grpcClient = client
	c.grpcManagedClient = daemon.NewManagedServiceClient(connection)
	c.clientAccess.Unlock()
	return client, ctx, nil
}

func (c *CommandClient) closeConnection() {
	c.clientAccess.Lock()
	connection := c.grpcConn
	c.grpcConn = nil
	c.grpcClient = nil
	c.grpcManagedClient = nil
	c.clientAccess.Unlock()
	common.Close(common.PtrOrNil(connection))
}

func callWithResult[T any](c *CommandClient, call func(ctx context.Context, client daemon.StartedServiceClient) (T, error)) (T, error) {
	if c == nil {
		// The bound type can reach the platform as a typed nil pointer. Every client call goes
		// through here, so one guard keeps a nil client from dereferencing on the binding surface.
		var zero T
		return zero, E.New("command client is nil")
	}
	client, ctx, err := c.getClientForCall()
	if err != nil {
		var zero T
		return zero, err
	}
	if c.standalone {
		defer c.closeConnection()
	}
	return call(ctx, client)
}

func callManagedWithResult[T any](c *CommandClient, call func(ctx context.Context, client daemon.ManagedServiceClient) (T, error)) (T, error) {
	if c == nil {
		var zero T
		return zero, E.New("command client is nil")
	}
	_, ctx, err := c.getClientForCall()
	if err != nil {
		var zero T
		return zero, err
	}
	if c.standalone {
		defer c.closeConnection()
	}
	c.clientAccess.RLock()
	client := c.grpcManagedClient
	c.clientAccess.RUnlock()
	if client == nil {
		var zero T
		return zero, os.ErrClosed
	}
	return call(ctx, client)
}

func (c *CommandClient) handleLogStream(client daemon.StartedServiceClient, ctx context.Context, generation uint64) {
	stream, err := client.SubscribeLog(ctx, &emptypb.Empty{})
	if err != nil {
		c.reportDisconnected(generation, E.Cause(err, "subscribe log"))
		return
	}
	if !c.stillConnected(generation) {
		return
	}
	defaultLogLevel, err := client.GetDefaultLogLevel(ctx, &emptypb.Empty{})
	if err != nil {
		c.reportDisconnected(generation, E.Cause(err, "get default log level"))
		return
	}
	if !c.stillConnected(generation) {
		return
	}
	c.handler.SetDefaultLogLevel(int32(defaultLogLevel.Level))
	for {
		logMessage, err := stream.Recv()
		if err != nil {
			c.reportDisconnected(generation, E.Cause(err, "log stream recv"))
			return
		}
		if !c.stillConnected(generation) {
			return
		}
		if logMessage.Reset_ {
			c.handler.ClearLogs()
		}
		var messages []*LogEntry
		for _, msg := range logMessage.Messages {
			messages = append(messages, &LogEntry{
				Level:   int32(msg.Level),
				Message: msg.Message,
			})
		}
		c.handler.WriteLogs(newIterator(messages))
	}
}

func (c *CommandClient) handleStatusStream(client daemon.StartedServiceClient, ctx context.Context, generation uint64) {
	interval := c.options.StatusInterval

	stream, err := client.SubscribeStatus(ctx, &daemon.SubscribeStatusRequest{
		Interval: interval,
	})
	if err != nil {
		c.reportDisconnected(generation, E.Cause(err, "subscribe status"))
		return
	}
	if !c.stillConnected(generation) {
		return
	}

	for {
		status, err := stream.Recv()
		if err != nil {
			c.reportDisconnected(generation, E.Cause(err, "status stream recv"))
			return
		}
		if !c.stillConnected(generation) {
			return
		}
		c.handler.WriteStatus(statusMessageFromGRPC(status))
	}
}

func (c *CommandClient) handleGroupStream(client daemon.StartedServiceClient, ctx context.Context, generation uint64) {
	stream, err := client.SubscribeGroups(ctx, &emptypb.Empty{})
	if err != nil {
		c.reportDisconnected(generation, E.Cause(err, "subscribe groups"))
		return
	}
	if !c.stillConnected(generation) {
		return
	}

	for {
		groups, err := stream.Recv()
		if err != nil {
			c.reportDisconnected(generation, E.Cause(err, "groups stream recv"))
			return
		}
		if !c.stillConnected(generation) {
			return
		}
		c.handler.WriteGroups(outboundGroupIteratorFromGRPC(groups))
	}
}

func (c *CommandClient) handleClashModeStream(client daemon.StartedServiceClient, ctx context.Context, generation uint64) {
	modeStatus, err := client.GetClashModeStatus(ctx, &emptypb.Empty{})
	if err != nil {
		if status.Code(err) != codes.NotFound {
			c.reportDisconnected(generation, E.Cause(err, "get clash mode status"))
			return
		}
		modeStatus = &daemon.ClashModeStatus{}
	}
	if !c.stillConnected(generation) {
		return
	}

	if sFixAndroidStack {
		go c.handler.InitializeClashMode(newIterator(modeStatus.ModeList), modeStatus.CurrentMode)
	} else {
		c.handler.InitializeClashMode(newIterator(modeStatus.ModeList), modeStatus.CurrentMode)
	}

	if len(modeStatus.ModeList) == 0 {
		return
	}

	stream, err := client.SubscribeClashMode(ctx, &emptypb.Empty{})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return
		}
		c.reportDisconnected(generation, E.Cause(err, "subscribe clash mode"))
		return
	}
	if !c.stillConnected(generation) {
		return
	}

	for {
		mode, err := stream.Recv()
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return
			}
			c.reportDisconnected(generation, E.Cause(err, "clash mode stream recv"))
			return
		}
		if !c.stillConnected(generation) {
			return
		}
		c.handler.UpdateClashMode(mode.Mode)
	}
}

// connectionResyncAttempts bounds how many times a lost increment is repaired by resubscribing
// before the client reports the stream as failed. A repair costs one snapshot, so a stream that keeps
// losing events must not spin: the backoff doubles per attempt.
const connectionResyncAttempts = 4

// handleConnectionsStream delivers the connection-event stream, and repairs a lost increment by
// resubscribing for a fresh snapshot.
//
// # Why the wire fields matter here
//
// The subscription identity can only tell "this batch belongs to an older stream" apart; it cannot
// tell "batches 7 and 8 never arrived". The daemon stamps each batch with its instanceId, sequence and
// cumulative dropped count for exactly that question. A gap, a rising drop count, or an increment
// from a different instance without a Reset means the consumer's table is missing a change (the
// reviewed symptom: a CLOSED event was lost and the row stayed forever). The incomplete batch is NOT
// delivered; the stream is cancelled and a new subscription is opened, whose first batch is the
// daemon's snapshot. That snapshot is delivered with `Resynced` set, so the platform can tell a
// repair from an ordinary connect.
func (c *CommandClient) handleConnectionsStream(client daemon.StartedServiceClient, ctx context.Context, generation uint64) {
	interval := c.options.StatusInterval
	stillCurrent := func() bool { return c.stillConnected(generation) }

	resynced := false
	for attempt := 0; attempt <= connectionResyncAttempts; attempt++ {
		if !stillCurrent() {
			return
		}
		// The subscription's identity is bound BEFORE the call that can block.
		//
		// It used to be taken after SubscribeConnections returned, and that is the reviewed failure:
		// a call parked in establishment across the next connect would come back, read the CURRENT
		// generation, and claim the old subscription as the new one - including a subscription
		// NUMBER higher than the new stream's, which made the consumer accept its snapshot as
		// fresher. The identity belongs to the subscription's creation, so it is taken here,
		// together with the connection it belongs to.
		subscription := connectionSubscriptionID.Add(1)
		streamCtx, cancelStream := context.WithCancel(ctx)
		stream, err := client.SubscribeConnections(streamCtx, &daemon.SubscribeConnectionsRequest{
			Interval: interval,
		})
		if err != nil {
			cancelStream()
			if stillCurrent() {
				c.handler.Disconnected(E.Cause(err, "subscribe connections").Error())
			}
			return
		}
		if !stillCurrent() {
			// Established late: this stream belongs to a connection a newer one has replaced.
			// Nothing it carries may be applied, and its termination says nothing about the current
			// state.
			cancelStream()
			return
		}
		lost := c.consumeConnectionsStream(stream, subscription, stillCurrent, resynced)
		cancelStream()
		if !lost {
			return
		}
		if attempt == connectionResyncAttempts {
			if stillCurrent() {
				c.handler.Disconnected("connection event stream could not be resynchronised after a lost increment")
			}
			return
		}
		resynced = true
		select {
		case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
		case <-ctx.Done():
			return
		}
	}
}

// consumeConnectionsStream reads one subscription until it ends, and reports whether the stream
// should be replaced by a fresh subscription (a lost increment).
//
// The accept/stale/resync decision is the SHARED classifier used by Connections.ApplyEvents. The
// client's own responsibility is narrower than it used to be: it no longer keeps a second, subtly
// different watermark. A stale duplicate is skipped without delivery and WITHOUT moving the
// watermark (the reviewed `1R,2,1,3` failure turned 3 into a gap and resubscribed for nothing), and
// only an accepted batch advances the state.
func (c *CommandClient) consumeConnectionsStream(stream daemon.StartedService_SubscribeConnectionsClient, subscription uint64, stillCurrent func() bool, resynced bool) bool {
	state := connectionBatchState{subscription: subscription}
	var assembly *connectionSnapshotAssembly
	for {
		events, err := stream.Recv()
		if err != nil {
			if !stillCurrent() {
				// A reconnect already replaced this stream; its termination says nothing about the
				// new state and must not clear it.
				return false
			}
			c.handler.Disconnected(E.Cause(err, "connections stream recv").Error())
			return false
		}
		if !stillCurrent() {
			return false
		}
		libboxEvents := connectionEventsFromGRPC(events)
		if libboxEvents == nil {
			libboxEvents = &ConnectionEvents{}
		}
		if libboxEvents.snapshotParts > 1 {
			if libboxEvents.snapshotPart == 1 {
				var ok bool
				assembly, ok = startSnapshot(libboxEvents, subscription)
				if !ok {
					return true
				}
			} else if !assembly.add(libboxEvents, subscription) {
				return true
			}
			if !assembly.complete() {
				continue
			}
			libboxEvents = assembly.finish()
			assembly = nil
		} else if assembly != nil {
			// A non-fragment arrived in the middle of a group: the stream cannot be ordered.
			return true
		}
		// Every subscription starts from a baseline, whatever the peer sent. The server's first
		// message IS the snapshot; forcing Reset covers an older daemon that never set it and a
		// reconnect where the platform still holds rows from the stream that just died.
		baselinePending := !state.hasBaseline
		libboxEvents = connectionEventsForDeliveryFrom(libboxEvents, baselinePending, subscription)
		switch classifyConnectionBatch(&state, libboxEvents) {
		case batchStale:
			continue
		case batchResync:
			return true
		}
		if baselinePending {
			libboxEvents.Resynced = resynced
		}
		state.apply(libboxEvents)
		c.handler.WriteConnectionEvents(libboxEvents)
	}
}

// connectionEventsForDelivery converts one received batch and applies the first-batch baseline rule.
//
// It is a function rather than two inline statements so the rule can be pinned without a gRPC
// stream: the batch a consumer receives first must ALWAYS be a reset, because the consumer's duty is
// to discard whatever it held before this subscription.
func connectionEventsForDelivery(events *daemon.ConnectionEvents, baselinePending bool, subscriptions ...uint64) (*ConnectionEvents, bool) {
	return connectionEventsForDeliveryFrom(connectionEventsFromGRPC(events), baselinePending, subscriptions...), false
}

// connectionEventsForDeliveryFrom applies the first-batch baseline rule to an already converted batch
// (the assembled snapshot is one such batch).
func connectionEventsForDeliveryFrom(libboxEvents *ConnectionEvents, baselinePending bool, subscriptions ...uint64) *ConnectionEvents {
	if libboxEvents == nil {
		libboxEvents = &ConnectionEvents{}
	}
	if len(subscriptions) > 0 {
		libboxEvents.subscription = subscriptions[0]
	}
	if baselinePending {
		libboxEvents.Reset = true
	}
	return libboxEvents
}

func (c *CommandClient) handleOutboundsStream(client daemon.StartedServiceClient, ctx context.Context, generation uint64) {
	stream, err := client.SubscribeOutbounds(ctx, &emptypb.Empty{})
	if err != nil {
		c.reportDisconnected(generation, E.Cause(err, "subscribe outbounds"))
		return
	}
	if !c.stillConnected(generation) {
		return
	}

	for {
		list, err := stream.Recv()
		if err != nil {
			c.reportDisconnected(generation, E.Cause(err, "outbounds stream recv"))
			return
		}
		if !c.stillConnected(generation) {
			return
		}
		c.handler.WriteOutbounds(outboundGroupItemListFromGRPC(list))
	}
}

// SelectOutbound keeps the shipped signature: it performs the selection and discards the
// read-back. A caller that has to settle its own pending state uses SelectOutboundWithRequest,
// which carries the request identity and returns what the group actually holds afterwards.
func (c *CommandClient) SelectOutbound(groupTag string, outboundTag string) error {
	_, err := c.selectOutbound(groupTag, outboundTag, 0, 0)
	if err != nil {
		return E.Cause(err, "select outbound")
	}
	return nil
}

type selectOutboundResult struct {
	Supported          bool   `json:"supported"`
	Unconfirmed        bool   `json:"unconfirmed,omitempty"`
	RequestID          uint64 `json:"requestId"`
	AppliedOutboundTag string `json:"appliedOutboundTag,omitempty"`
	AppliedRevision    uint64 `json:"appliedRevision,omitempty"`
	Stale              bool   `json:"stale,omitempty"`
}

// SelectOutboundWithRequest performs a selection carrying the caller's identity for this attempt
// and the group revision it last observed. The answer is the read-back, encoded as JSON so the
// gomobile bridge can pass it as a string box:
//
//	{"supported":true,"requestId":41,"appliedOutboundTag":"b","appliedRevision":7,"stale":false}
//
// # Why the parameters are int64 and not uint64
//
// They are wire values the daemon treats as uint64, but gomobile does not bind uint64 parameters:
// a method declared with them is skipped silently, so the Android and Apple clients would compile
// against an AAR/XCFramework that does not contain it. int64 binds to a Java/Kotlin long and a
// Swift Int64, and the values here are non-negative counters and revisions; a negative value is
// rejected rather than wrapped.
//
// A daemon that predates the identity fields answers requestId 0. That is a CAPABILITY answer,
// not a failed selection: the selection was performed, so the document says supported=false and
// unconfirmed=true rather than inventing a revision the caller could mistake for its own.
func (c *CommandClient) SelectOutboundWithRequest(groupTag string, outboundTag string, requestID int64, expectedRevision int64) (*StringBox, error) {
	if requestID < 0 || expectedRevision < 0 {
		return nil, E.New("select outbound: negative request identity")
	}
	result, err := c.selectOutbound(groupTag, outboundTag, uint64(requestID), uint64(expectedRevision))
	if err != nil {
		return nil, err
	}
	encoded, err := std_json.Marshal(result)
	if err != nil {
		return nil, E.Cause(err, "encode select outbound result")
	}
	return wrapString(string(encoded)), nil
}

func (c *CommandClient) selectOutbound(groupTag string, outboundTag string, requestID uint64, expectedRevision uint64) (*selectOutboundResult, error) {
	response, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.SelectOutboundResponse, error) {
		return client.SelectOutbound(ctx, &daemon.SelectOutboundRequest{
			GroupTag:         groupTag,
			OutboundTag:      outboundTag,
			RequestId:        requestID,
			ExpectedRevision: expectedRevision,
		})
	})
	if err != nil {
		return nil, E.Cause(err, "select outbound")
	}
	if requestID != 0 && response.GetRequestId() != requestID {
		// Zero is what an older daemon sends: it never saw the identity field. The selection
		// itself was performed, so this is an unconfirmed result, not a failure.
		return &selectOutboundResult{Supported: false, Unconfirmed: true, RequestID: requestID}, nil
	}
	return &selectOutboundResult{
		Supported:          true,
		RequestID:          response.GetRequestId(),
		AppliedOutboundTag: response.GetAppliedOutboundTag(),
		AppliedRevision:    response.GetAppliedRevision(),
		Stale:              response.GetStale(),
	}, nil
}

func (c *CommandClient) URLTest(outboundTag string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.URLTest(ctx, &daemon.URLTestRequest{
			OutboundTag: outboundTag,
		})
	})
	if err != nil {
		return E.Cause(err, "url test")
	}
	return nil
}

func (c *CommandClient) SetClashMode(newMode string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.SetClashMode(ctx, &daemon.ClashMode{
			Mode: newMode,
		})
	})
	if err != nil {
		return E.Cause(err, "set clash mode")
	}
	return nil
}

// RefreshRemoteProviders asks the daemon to refresh its remote providers and returns the
// per-provider report as JSON. The selection is a comma-separated tag list; an empty string
// refreshes every provider the running box serves, and timeoutMilliseconds bounds each one.
//
// This is the CLI/socket path (PP02's binding); the in-process platform path is
// CommandServer.RefreshProviders.
//
// # Two things the answer must be able to say without an error
//
// A gomobile call that returns an error loses its result in the generated bridge (the Swift method
// throws and the box is dropped), so an error is the wrong place for a fact the caller has to act
// on. Two such facts live here:
//
//   - the running kernel predates this RPC. That is a CAPABILITY answer, not a failed refresh, and
//     it is returned as `{"supported":false,...}` so a client can tell "this kernel cannot refresh
//     providers" from "the refresh failed". It is detected from gRPC's Unimplemented status on the
//     Go side, where the code exists: the Go error travelling through gomobile carries no gRPC code
//     (the app's old `NSError.code == 12` test never matched), and no client-side code mapping is
//     stable across the bridge.
//   - the whole call ran out of time. It is reported as an error because there is no partial
//     document to hand back, but the batch ceiling below is what makes that bound real: without it
//     the call waited for every provider's own timeout, in sequence, with no upper limit.
func (c *CommandClient) RefreshRemoteProviders(tags string, timeoutMilliseconds int64) (*StringBox, error) {
	selection := make([]string, 0)
	for _, tag := range strings.Split(tags, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		selection = append(selection, tag)
	}
	providerTimeout := time.Duration(timeoutMilliseconds) * time.Millisecond
	if providerTimeout <= 0 {
		providerTimeout = defaultProviderRefreshTimeout
	}
	batchDeadline := refreshBatchDeadline(providerTimeout)
	client, ctx, err := c.getClientForCall()
	if err != nil {
		return nil, err
	}
	if c.standalone {
		defer c.closeConnection()
	}
	callContext, cancel := context.WithTimeout(ctx, batchDeadline)
	defer cancel()
	result, err := client.RefreshRemoteProviders(callContext, &daemon.RefreshRemoteProvidersRequest{
		Tags:      selection,
		TimeoutMs: timeoutMilliseconds,
	})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return wrapString(encodeUnsupportedProviderRefresh("the running kernel predates RefreshRemoteProviders")), nil
		}
		if errors.Is(callContext.Err(), context.DeadlineExceeded) {
			return nil, E.Cause(callContext.Err(), "refresh remote providers (batch ceiling %s)", batchDeadline)
		}
		return nil, E.Cause(err, "refresh remote providers")
	}
	encoded, err := std_json.Marshal(result)
	if err != nil {
		return nil, E.Cause(err, "encode provider refresh result")
	}
	return wrapString(string(encoded)), nil
}
func (c *CommandClient) CloseConnection(connId string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.CloseConnection(ctx, &daemon.CloseConnectionRequest{
			Id: connId,
		})
	})
	if err != nil {
		return E.Cause(err, "close connection")
	}
	return nil
}

func (c *CommandClient) CloseConnections() error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.CloseAllConnections(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return E.Cause(err, "close all connections")
	}
	return nil
}

type serviceReloadResult struct {
	Supported      bool   `json:"supported"`
	Unconfirmed    bool   `json:"unconfirmed,omitempty"`
	RequestID      string `json:"requestId"`
	InstanceID     string `json:"instanceId,omitempty"`
	ActiveRevision string `json:"activeRevision,omitempty"`
	Stale          bool   `json:"stale,omitempty"`
	Status         string `json:"status,omitempty"`
}

// ServiceReload keeps the shipped signature, but it is no longer token-less on the wire: it runs
// through the request-identified RPC whenever the daemon serves it, so the reload the daemon
// performed is the one this call asked for. A daemon that predates that RPC gets the legacy call
// and no confirmation.
func (c *CommandClient) ServiceReload() error {
	_, err := c.serviceReload("", "")
	if err != nil {
		return E.Cause(err, "reload service")
	}
	return nil
}

// ServiceReloadWithRequest reloads the service with the caller's identity for this attempt and
// the instance it last saw. The answer is a JSON document:
//
//	{"supported":true,"requestId":"r-7","instanceId":"...","activeRevision":"...","status":"restarted"}
//
// A daemon that predates request identity still reloads, but answers supported=false and
// unconfirmed=true: the caller must not report that attempt as confirmed.
func (c *CommandClient) ServiceReloadWithRequest(requestID string, expectedInstanceID string) (*StringBox, error) {
	result, err := c.serviceReload(requestID, expectedInstanceID)
	if err != nil {
		return nil, err
	}
	encoded, err := std_json.Marshal(result)
	if err != nil {
		return nil, E.Cause(err, "encode reload result")
	}
	return wrapString(string(encoded)), nil
}

// reloadRequestIDCounter numbers reload attempts process-wide, so two clients (or one client
// reloading twice) can never share an identity.
var reloadRequestIDCounter atomic.Uint64

// newReloadRequestID returns an identity for one reload attempt. It is only a label: the daemon
// echoes it, and the caller decides which pending attempt it settles.
func newReloadRequestID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(reloadRequestIDCounter.Add(1), 36)
}

func (c *CommandClient) serviceReload(requestID string, expectedInstanceID string) (*serviceReloadResult, error) {
	if strings.TrimSpace(requestID) == "" {
		requestID = newReloadRequestID()
	}
	response, err := callManagedWithResult(c, func(ctx context.Context, client daemon.ManagedServiceClient) (*daemon.ReloadServiceResponse, error) {
		return client.ReloadServiceWithRequest(ctx, &daemon.ReloadServiceRequest{
			RequestId:          requestID,
			ExpectedInstanceId: expectedInstanceID,
		})
	})
	if err != nil {
		if status.Code(err) != codes.Unimplemented {
			return nil, E.Cause(err, "reload service")
		}
		// The daemon predates the request-identified RPC. The reload still has to happen: an old
		// daemon is not a reason to refuse a reload, only a reason not to call it confirmed.
		_, legacyErr := callManagedWithResult(c, func(ctx context.Context, client daemon.ManagedServiceClient) (*emptypb.Empty, error) {
			return client.ReloadService(ctx, &emptypb.Empty{})
		})
		if legacyErr != nil {
			return nil, E.Cause(legacyErr, "reload service")
		}
		return &serviceReloadResult{Supported: false, Unconfirmed: true, RequestID: requestID}, nil
	}
	if response.GetRequestId() != requestID {
		return nil, E.New("the daemon confirmed reload request ", strconv.Quote(response.GetRequestId()), " instead of ", strconv.Quote(requestID))
	}
	return &serviceReloadResult{
		Supported:      true,
		RequestID:      requestID,
		InstanceID:     response.GetInstanceId(),
		ActiveRevision: response.GetActiveRevision(),
		Stale:          response.GetStale(),
		Status:         response.GetStatus(),
	}, nil
}

func (c *CommandClient) ServiceClose() error {
	_, err := callManagedWithResult(c, func(ctx context.Context, client daemon.ManagedServiceClient) (*emptypb.Empty, error) {
		return client.StopService(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return E.Cause(err, "stop service")
	}
	return nil
}

func (c *CommandClient) ClearLogs() error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.ClearLogs(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return E.Cause(err, "clear logs")
	}
	return nil
}

func (c *CommandClient) GetSystemProxyStatus() (*SystemProxyStatus, error) {
	return callManagedWithResult(c, func(ctx context.Context, client daemon.ManagedServiceClient) (*SystemProxyStatus, error) {
		status, err := client.GetSystemProxyStatus(ctx, &emptypb.Empty{})
		if err != nil {
			return nil, E.Cause(err, "get system proxy status")
		}
		return systemProxyStatusFromGRPC(status), nil
	})
}

func (c *CommandClient) SetSystemProxyEnabled(isEnabled bool) error {
	_, err := callManagedWithResult(c, func(ctx context.Context, client daemon.ManagedServiceClient) (*emptypb.Empty, error) {
		return client.SetSystemProxyEnabled(ctx, &daemon.SetSystemProxyEnabledRequest{
			Enabled: isEnabled,
		})
	})
	if err != nil {
		return E.Cause(err, "set system proxy enabled")
	}
	return nil
}

func (c *CommandClient) TriggerGoCrash() error {
	_, err := callManagedWithResult(c, func(ctx context.Context, client daemon.ManagedServiceClient) (*emptypb.Empty, error) {
		return client.TriggerDebugCrash(ctx, &daemon.DebugCrashRequest{
			Type: daemon.DebugCrashRequest_GO,
		})
	})
	if err != nil {
		return E.Cause(err, "trigger debug crash")
	}
	return nil
}

func (c *CommandClient) TriggerNativeCrash() error {
	_, err := callManagedWithResult(c, func(ctx context.Context, client daemon.ManagedServiceClient) (*emptypb.Empty, error) {
		return client.TriggerDebugCrash(ctx, &daemon.DebugCrashRequest{
			Type: daemon.DebugCrashRequest_NATIVE,
		})
	})
	if err != nil {
		return E.Cause(err, "trigger native crash")
	}
	return nil
}

func (c *CommandClient) TriggerOOMReport() error {
	_, err := callManagedWithResult(c, func(ctx context.Context, client daemon.ManagedServiceClient) (*emptypb.Empty, error) {
		return client.TriggerOOMReport(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return E.Cause(err, "trigger oom report")
	}
	return nil
}

func (c *CommandClient) GetDeprecatedNotes() (DeprecatedNoteIterator, error) {
	return callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (DeprecatedNoteIterator, error) {
		warnings, err := client.GetDeprecatedWarnings(ctx, &emptypb.Empty{})
		if err != nil {
			return nil, E.Cause(err, "get deprecated warnings")
		}
		var notes []*DeprecatedNote
		for _, warning := range warnings.Warnings {
			notes = append(notes, &DeprecatedNote{
				Description:       warning.Description,
				DeprecatedVersion: warning.DeprecatedVersion,
				ScheduledVersion:  warning.ScheduledVersion,
				MigrationLink:     warning.MigrationLink,
			})
		}
		return newIterator(notes), nil
	})
}

func (c *CommandClient) GetStartedAt() (int64, error) {
	return callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (int64, error) {
		startedAt, err := client.GetStartedAt(ctx, &emptypb.Empty{})
		if err != nil {
			return 0, E.Cause(err, "get started at")
		}
		return startedAt.StartedAt, nil
	})
}

// configurationJSON returns the JSON document, and an EMPTY string (no error) when the daemon
// predates the call: a caller must be able to tell "old kernel, feature unavailable" from
// "new kernel, no recovery yet" (which is the JSON literal null).
func (c *CommandClient) configurationJSON(call func(context.Context, daemon.StartedServiceClient) (*daemon.JSONDocument, error)) (*StringBox, error) {
	return callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*StringBox, error) {
		document, err := call(ctx, client)
		if err != nil {
			if status.Code(err) == codes.Unimplemented {
				return wrapString(""), nil
			}
			return nil, err
		}
		return wrapString(document.Json), nil
	})
}

func (c *CommandClient) PreviewServiceChange(configContent string) (*StringBox, error) {
	document, err := c.configurationJSON(func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.JSONDocument, error) {
		return client.PreviewServiceChange(ctx, &daemon.PreviewServiceChangeRequest{ConfigContent: configContent})
	})
	if err != nil {
		return nil, E.Cause(err, "preview service change")
	}
	return document, nil
}

func (c *CommandClient) GetLastServiceChange() (*StringBox, error) {
	document, err := c.configurationJSON(func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.JSONDocument, error) {
		return client.GetLastServiceChange(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return nil, E.Cause(err, "get last service change")
	}
	return document, nil
}

func (c *CommandClient) GetLastConfigRecovery() (*StringBox, error) {
	document, err := c.configurationJSON(func(ctx context.Context, client daemon.StartedServiceClient) (*daemon.JSONDocument, error) {
		return client.GetLastConfigRecovery(ctx, &emptypb.Empty{})
	})
	if err != nil {
		return nil, E.Cause(err, "get last config recovery")
	}
	return document, nil
}

func (c *CommandClient) GetAPIVersion() (int32, error) {
	return callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (int32, error) {
		version, err := client.GetVersion(ctx, &emptypb.Empty{})
		if err != nil {
			return 0, E.Cause(err, "get version")
		}
		return version.ApiVersion, nil
	})
}

func (c *CommandClient) SetGroupExpand(groupTag string, isExpand bool) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.SetGroupExpand(ctx, &daemon.SetGroupExpandRequest{
			GroupTag: groupTag,
			IsExpand: isExpand,
		})
	})
	if err != nil {
		return E.Cause(err, "set group expand")
	}
	return nil
}

func (c *CommandClient) StartNetworkQualityTest(configURL string, outboundTag string, serial bool, maxRuntimeSeconds int32, http3 bool, handler NetworkQualityTestHandler) (*NetworkQualityTestSession, error) {
	client, parentCtx, err := c.getClientForCall()
	if err != nil {
		return nil, E.Cause(err, "start network quality test")
	}

	streamCtx, cancel := context.WithCancel(parentCtx)
	session := &NetworkQualityTestSession{
		streamSession: streamSession{
			ctx:       streamCtx,
			cancel:    cancel,
			closeDone: make(chan struct{}),
		},
	}

	failStart := func(cause error, message string) (*NetworkQualityTestSession, error) {
		cancel()
		if c.standalone {
			c.closeConnection()
		}
		return nil, E.Cause(cause, message)
	}

	stream, err := client.StartNetworkQualityTest(streamCtx, &daemon.NetworkQualityTestRequest{
		ConfigURL:         configURL,
		OutboundTag:       outboundTag,
		Serial:            serial,
		MaxRuntimeSeconds: maxRuntimeSeconds,
		Http3:             http3,
	})
	if err != nil {
		return failStart(err, "start network quality test")
	}

	standalone := c.standalone
	go func() {
		defer func() {
			close(session.closeDone)
			if standalone {
				c.closeConnection()
			}
		}()
		for {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				if session.ctx.Err() != nil {
					return
				}
				handler.OnError(E.Cause(recvErr, "network quality test recv").Error())
				return
			}
			if event.IsFinal {
				if event.Error != "" {
					handler.OnError(event.Error)
				} else {
					handler.OnResult(&NetworkQualityResult{
						DownloadCapacity:         event.DownloadCapacity,
						UploadCapacity:           event.UploadCapacity,
						DownloadRPM:              event.DownloadRPM,
						UploadRPM:                event.UploadRPM,
						IdleLatencyMs:            event.IdleLatencyMs,
						DownloadCapacityAccuracy: event.DownloadCapacityAccuracy,
						UploadCapacityAccuracy:   event.UploadCapacityAccuracy,
						DownloadRPMAccuracy:      event.DownloadRPMAccuracy,
						UploadRPMAccuracy:        event.UploadRPMAccuracy,
					})
				}
				return
			}
			handler.OnProgress(networkQualityProgressFromGRPC(event))
		}
	}()

	return session, nil
}

func (c *CommandClient) StartSTUNTest(server string, outboundTag string, handler STUNTestHandler) (*STUNTestSession, error) {
	client, parentCtx, err := c.getClientForCall()
	if err != nil {
		return nil, E.Cause(err, "start stun test")
	}

	streamCtx, cancel := context.WithCancel(parentCtx)
	session := &STUNTestSession{
		streamSession: streamSession{
			ctx:       streamCtx,
			cancel:    cancel,
			closeDone: make(chan struct{}),
		},
	}

	failStart := func(cause error, message string) (*STUNTestSession, error) {
		cancel()
		if c.standalone {
			c.closeConnection()
		}
		return nil, E.Cause(cause, message)
	}

	stream, err := client.StartSTUNTest(streamCtx, &daemon.STUNTestRequest{
		Server:      server,
		OutboundTag: outboundTag,
	})
	if err != nil {
		return failStart(err, "start stun test")
	}

	standalone := c.standalone
	go func() {
		defer func() {
			close(session.closeDone)
			if standalone {
				c.closeConnection()
			}
		}()
		for {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				if session.ctx.Err() != nil {
					return
				}
				handler.OnError(E.Cause(recvErr, "stun test recv").Error())
				return
			}
			if event.IsFinal {
				if event.Error != "" {
					handler.OnError(event.Error)
				} else {
					handler.OnResult(&STUNTestResult{
						ExternalAddr:     event.ExternalAddr,
						LatencyMs:        event.LatencyMs,
						NATMapping:       event.NatMapping,
						NATFiltering:     event.NatFiltering,
						NATTypeSupported: event.NatTypeSupported,
					})
				}
				return
			}
			handler.OnProgress(stunTestProgressFromGRPC(event))
		}
	}()

	return session, nil
}

func subscribeStatus[T any](c *CommandClient, session *streamSession, name string, start func(context.Context, daemon.StartedServiceClient) (grpc.ServerStreamingClient[T], error), onUpdate func(*T), onError func(string)) error {
	client, parentCtx, err := c.getClientForCall()
	if err != nil {
		return E.Cause(err, "subscribe ", name)
	}

	streamCtx, cancel := context.WithCancel(parentCtx)
	*session = streamSession{
		ctx:       streamCtx,
		cancel:    cancel,
		closeDone: make(chan struct{}),
	}

	stream, err := start(streamCtx, client)
	if err != nil {
		cancel()
		if c.standalone {
			c.closeConnection()
		}
		return E.Cause(err, "subscribe ", name)
	}

	standalone := c.standalone
	go func() {
		defer func() {
			close(session.closeDone)
			if standalone {
				c.closeConnection()
			}
		}()
		for {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				if session.ctx.Err() != nil {
					return
				}
				if status.Code(recvErr) == codes.NotFound || status.Code(recvErr) == codes.Unavailable {
					return
				}
				onError(E.Cause(recvErr, name, " recv").Error())
				return
			}
			onUpdate(event)
		}
	}()
	return nil
}

func (c *CommandClient) SubscribeTailscaleStatus(handler TailscaleStatusHandler) (*TailscaleStatusSubscription, error) {
	session := new(TailscaleStatusSubscription)
	err := subscribeStatus(c, &session.streamSession, "tailscale status", func(ctx context.Context, client daemon.StartedServiceClient) (grpc.ServerStreamingClient[daemon.TailscaleStatusUpdate], error) {
		return client.SubscribeTailscaleStatus(ctx, &emptypb.Empty{})
	}, func(update *daemon.TailscaleStatusUpdate) {
		handler.OnStatusUpdate(tailscaleStatusUpdateFromGRPC(update))
	}, handler.OnError)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (c *CommandClient) SubscribeUSBIPServerStatus(handler USBIPServerStatusHandler) (*USBIPServerStatusSubscription, error) {
	session := new(USBIPServerStatusSubscription)
	err := subscribeStatus(c, &session.streamSession, "usbip server status", func(ctx context.Context, client daemon.StartedServiceClient) (grpc.ServerStreamingClient[daemon.USBIPServerStatusUpdate], error) {
		return client.SubscribeUSBIPServerStatus(ctx, &emptypb.Empty{})
	}, func(update *daemon.USBIPServerStatusUpdate) {
		handler.OnStatusUpdate(usbipServerStatusUpdateFromGRPC(update))
	}, handler.OnError)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (c *CommandClient) SubscribeOpenConnectStatus(handler OpenConnectStatusHandler) (*OpenConnectStatusSubscription, error) {
	session := new(OpenConnectStatusSubscription)
	err := subscribeStatus(c, &session.streamSession, "openconnect status", func(ctx context.Context, client daemon.StartedServiceClient) (grpc.ServerStreamingClient[daemon.OpenConnectStatusUpdate], error) {
		return client.SubscribeOpenConnectStatus(ctx, &emptypb.Empty{})
	}, func(update *daemon.OpenConnectStatusUpdate) {
		handler.OnStatusUpdate(openConnectStatusUpdateFromGRPC(update))
	}, handler.OnError)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (c *CommandClient) SubmitOpenConnectAuthResponse(endpointTag string, challengeID string, response *OpenConnectAuthResponse) error {
	submission := &daemon.OpenConnectAuthResponseSubmission{
		EndpointTag: endpointTag,
		ChallengeID: challengeID,
	}
	if response.formValues != nil {
		submission.Response = &daemon.OpenConnectAuthResponseSubmission_Form{Form: &daemon.OpenConnectAuthFormResponse{
			Values: response.formValues.values,
		}}
	}
	if response.browserResult != nil {
		submission.Response = &daemon.OpenConnectAuthResponseSubmission_Browser{Browser: &daemon.OpenConnectBrowserResult{
			FinalURL: response.browserResult.FinalURL,
			Cookies: common.Map(response.browserResult.cookies, func(cookie openConnectBrowserCookie) *daemon.OpenConnectBrowserCookie {
				return &daemon.OpenConnectBrowserCookie{Name: cookie.Name, Value: cookie.Value}
			}),
			Headers: common.Map(response.browserResult.headers, func(header openConnectBrowserHeader) *daemon.OpenConnectBrowserHeader {
				return &daemon.OpenConnectBrowserHeader{Name: header.Name, Values: header.Values}
			}),
		}}
	}
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.SubmitOpenConnectAuthResponse(ctx, submission)
	})
	if err != nil {
		return E.Cause(err, "submit openconnect authentication response")
	}
	return nil
}

func (c *CommandClient) CancelOpenConnectAuthChallenge(endpointTag string, challengeID string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.CancelOpenConnectAuthChallenge(ctx, &daemon.OpenConnectAuthChallengeCancel{
			EndpointTag: endpointTag,
			ChallengeID: challengeID,
		})
	})
	if err != nil {
		return E.Cause(err, "cancel openconnect authentication challenge")
	}
	return nil
}

func (c *CommandClient) SubscribeOpenVPNStatus(handler OpenVPNStatusHandler) (*OpenVPNStatusSubscription, error) {
	session := new(OpenVPNStatusSubscription)
	err := subscribeStatus(c, &session.streamSession, "openvpn status", func(ctx context.Context, client daemon.StartedServiceClient) (grpc.ServerStreamingClient[daemon.OpenVPNStatusUpdate], error) {
		return client.SubscribeOpenVPNStatus(ctx, &emptypb.Empty{})
	}, func(update *daemon.OpenVPNStatusUpdate) {
		handler.OnStatusUpdate(openVPNStatusUpdateFromGRPC(update))
	}, handler.OnError)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (c *CommandClient) SubmitOpenVPNChallengeResponse(endpointTag string, challengeID string, response *OpenVPNChallengeResponse) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.SubmitOpenVPNChallengeResponse(ctx, &daemon.OpenVPNChallengeSubmission{
			EndpointTag: endpointTag,
			ChallengeID: challengeID,
			Username:    response.Username,
			Password:    response.Password,
			Secret:      response.Secret,
		})
	})
	if err != nil {
		return E.Cause(err, "submit openvpn challenge response")
	}
	return nil
}

func (c *CommandClient) CancelOpenVPNChallenge(endpointTag string, challengeID string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.CancelOpenVPNChallenge(ctx, &daemon.OpenVPNChallengeCancel{
			EndpointTag: endpointTag,
			ChallengeID: challengeID,
		})
	})
	if err != nil {
		return E.Cause(err, "cancel openvpn challenge")
	}
	return nil
}

func (c *CommandClient) SetTailscaleExitNode(endpointTag string, stableID string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.SetTailscaleExitNode(ctx, &daemon.SetTailscaleExitNodeRequest{
			EndpointTag: endpointTag,
			StableID:    stableID,
		})
	})
	if err != nil {
		return E.Cause(err, "set tailscale exit node")
	}
	return nil
}

func (c *CommandClient) TailscaleLogout(endpointTag string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.TailscaleLogout(ctx, &daemon.TailscaleLogoutRequest{
			EndpointTag: endpointTag,
		})
	})
	if err != nil {
		return E.Cause(err, "tailscale logout")
	}
	return nil
}

func (c *CommandClient) StartTailscalePing(endpointTag string, peerIP string, handler TailscalePingHandler) (*TailscalePingSession, error) {
	client, parentCtx, err := c.getClientForCall()
	if err != nil {
		return nil, E.Cause(err, "start tailscale ping")
	}

	streamCtx, cancel := context.WithCancel(parentCtx)
	session := &TailscalePingSession{
		streamSession: streamSession{
			ctx:       streamCtx,
			cancel:    cancel,
			closeDone: make(chan struct{}),
		},
	}

	failStart := func(cause error, message string) (*TailscalePingSession, error) {
		cancel()
		if c.standalone {
			c.closeConnection()
		}
		return nil, E.Cause(cause, message)
	}

	stream, err := client.StartTailscalePing(streamCtx, &daemon.TailscalePingRequest{
		EndpointTag: endpointTag,
		PeerIP:      peerIP,
	})
	if err != nil {
		return failStart(err, "start tailscale ping")
	}

	standalone := c.standalone
	go func() {
		defer func() {
			close(session.closeDone)
			if standalone {
				c.closeConnection()
			}
		}()
		for {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				if session.ctx.Err() != nil {
					return
				}
				handler.OnError(E.Cause(recvErr, "tailscale ping recv").Error())
				return
			}
			handler.OnPingResult(tailscalePingResultFromGRPC(event))
		}
	}()

	return session, nil
}

func (c *CommandClient) StartTailscaleSSHSession(opts *TailscaleSSHOptions, handler TailscaleSSHHandler) (*TailscaleSSHSession, error) {
	client, parentCtx, err := c.getClientForCall()
	if err != nil {
		return nil, E.Cause(err, "start tailscale ssh session")
	}

	streamCtx, cancel := context.WithCancel(parentCtx)
	failStart := func(cause error, message string) (*TailscaleSSHSession, error) {
		cancel()
		if c.standalone {
			c.closeConnection()
		}
		return nil, E.Cause(cause, message)
	}

	stream, err := client.StartTailscaleSSHSession(streamCtx)
	if err != nil {
		return failStart(err, "start tailscale ssh session")
	}

	sendErr := stream.Send(&daemon.TailscaleSSHClientMessage{
		Message: &daemon.TailscaleSSHClientMessage_Start{Start: &daemon.TailscaleSSHStart{
			EndpointTag:  opts.EndpointTag,
			PeerAddress:  opts.PeerAddress,
			Username:     opts.Username,
			TerminalType: opts.TerminalType,
			Columns:      opts.Columns,
			Rows:         opts.Rows,
			WidthPixels:  opts.WidthPixels,
			HeightPixels: opts.HeightPixels,
			HostKeys:     iteratorToArray[string](opts.HostKeys),
			ForwardAgent: opts.ForwardAgent,
		}},
	})
	if sendErr != nil {
		return failStart(sendErr, "send tailscale ssh start")
	}

	session := &TailscaleSSHSession{
		stream:    stream,
		inputCh:   make(chan []byte, 8),
		resizeCh:  make(chan tailscaleSSHResize, 1),
		ctx:       streamCtx,
		cancel:    cancel,
		closeDone: make(chan struct{}),
	}

	session.wg.Go(func() {
		for {
			select {
			case <-streamCtx.Done():
				return
			case data := <-session.inputCh:
				sendErr := stream.Send(&daemon.TailscaleSSHClientMessage{
					Message: &daemon.TailscaleSSHClientMessage_Input{Input: &daemon.TailscaleSSHInput{Data: data}},
				})
				if sendErr != nil {
					cancel()
					return
				}
			case resize := <-session.resizeCh:
				sendErr := stream.Send(&daemon.TailscaleSSHClientMessage{
					Message: &daemon.TailscaleSSHClientMessage_Resize{Resize: &daemon.TailscaleSSHResize{
						Columns:      resize.columns,
						Rows:         resize.rows,
						WidthPixels:  resize.widthPixels,
						HeightPixels: resize.heightPixels,
					}},
				})
				if sendErr != nil {
					cancel()
					return
				}
			}
		}
	})

	session.wg.Go(func() {
		for {
			msg, recvErr := stream.Recv()
			if recvErr == io.EOF {
				cancel()
				return
			}
			if recvErr != nil {
				handler.OnError(E.Cause(recvErr, "tailscale ssh recv").Error())
				cancel()
				return
			}
			switch payload := msg.GetMessage().(type) {
			case *daemon.TailscaleSSHServerMessage_AuthBanner:
				handler.OnAuthBanner(payload.AuthBanner.Message)
			case *daemon.TailscaleSSHServerMessage_Ready:
				handler.OnReady()
			case *daemon.TailscaleSSHServerMessage_Output:
				handler.OnOutput(payload.Output.Data)
			case *daemon.TailscaleSSHServerMessage_Exit:
				handler.OnExit(payload.Exit.ExitCode, payload.Exit.Signal, payload.Exit.ErrorMessage)
				cancel()
				return
			case *daemon.TailscaleSSHServerMessage_Error:
				handler.OnError(payload.Error.Message)
			}
		}
	})

	standalone := c.standalone
	go func() {
		session.wg.Wait()
		close(session.closeDone)
		if standalone {
			c.closeConnection()
		}
	}()

	return session, nil
}

func (c *CommandClient) ProvideUSBDevices(handler USBProviderHandler) (*USBProviderSession, error) {
	client, parentCtx, err := c.getClientForCall()
	if err != nil {
		return nil, E.Cause(err, "provide usb devices")
	}

	streamCtx, cancel := context.WithCancel(parentCtx)
	stream, err := client.ProvideUSBDevices(streamCtx)
	if err != nil {
		cancel()
		if c.standalone {
			c.closeConnection()
		}
		return nil, E.Cause(err, "provide usb devices")
	}

	session := &USBProviderSession{
		stream:    stream,
		ctx:       streamCtx,
		cancel:    cancel,
		closeDone: make(chan struct{}),
	}

	standalone := c.standalone
	go func() {
		defer close(session.closeDone)
		for {
			message, recvErr := stream.Recv()
			if recvErr == io.EOF {
				cancel()
				break
			}
			if recvErr != nil {
				handler.OnError("", E.Cause(recvErr, "usb provider recv").Error())
				cancel()
				break
			}
			switch payload := message.GetMessage().(type) {
			case *daemon.USBServerMessage_Ready:
				handler.OnReady(payload.Ready.GetDeviceId(), payload.Ready.GetBusId())
			case *daemon.USBServerMessage_UrbRequest:
				handler.OnURBRequest(usbURBRequestFromGRPC(payload.UrbRequest))
			case *daemon.USBServerMessage_Abort:
				handler.OnAbort(payload.Abort.GetDeviceId(), int32(payload.Abort.GetEndpoint()))
			case *daemon.USBServerMessage_Error:
				handler.OnError(payload.Error.GetDeviceId(), payload.Error.GetMessage())
			}
		}
		if standalone {
			c.closeConnection()
		}
	}()

	return session, nil
}

func (c *CommandClient) SubscribeTaildropInbox(endpointTag string, handler TaildropInboxHandler) (*TaildropInboxSubscription, error) {
	session := new(TaildropInboxSubscription)
	err := subscribeStatus(c, &session.streamSession, "taildrop inbox", func(ctx context.Context, client daemon.StartedServiceClient) (grpc.ServerStreamingClient[daemon.TaildropInbox], error) {
		return client.SubscribeTaildropInbox(ctx, &daemon.SubscribeTaildropInboxRequest{EndpointTag: endpointTag})
	}, func(update *daemon.TaildropInbox) {
		handler.OnInboxUpdate(taildropInboxFromGRPC(update))
	}, handler.OnError)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (c *CommandClient) SendTaildropFiles(options *TaildropSendOptions, handler TaildropSendHandler) (*TaildropSendSession, error) {
	client, parentCtx, err := c.getClientForCall()
	if err != nil {
		return nil, E.Cause(err, "send taildrop files")
	}

	streamCtx, cancel := context.WithCancel(parentCtx)
	failStart := func(cause error, message string) (*TaildropSendSession, error) {
		cancel()
		if c.standalone {
			c.closeConnection()
		}
		return nil, E.Cause(cause, message)
	}

	stream, err := client.SendTaildropFiles(streamCtx)
	if err != nil {
		return failStart(err, "send taildrop files")
	}

	sendErr := stream.Send(&daemon.TaildropSendClientMessage{
		Message: &daemon.TaildropSendClientMessage_Start{Start: &daemon.TaildropSendStart{
			EndpointTag:  options.EndpointTag,
			PeerStableID: options.PeerStableID,
			Files:        options.files,
		}},
	})
	if sendErr != nil {
		return failStart(sendErr, "send taildrop start")
	}
	session := &TaildropSendSession{
		streamSession: streamSession{
			ctx:       streamCtx,
			cancel:    cancel,
			closeDone: make(chan struct{}),
		},
		stream: stream,
	}

	standalone := c.standalone
	go func() {
		defer func() {
			close(session.closeDone)
			if standalone {
				c.closeConnection()
			}
		}()
		for {
			message, recvErr := stream.Recv()
			if recvErr != nil {
				switch {
				case recvErr == io.EOF:
					handler.OnFinish("")
				case streamCtx.Err() == nil:
					handler.OnFinish(E.Cause(recvErr, "taildrop send").Error())
				}
				cancel()
				return
			}
			progress := message.GetProgress()
			if progress == nil {
				continue
			}
			if progress.FileCompleted {
				handler.OnFileCompleted(progress.FileIndex, progress.SentBytes)
			} else {
				handler.OnProgress(progress.FileIndex, progress.SentBytes)
			}
		}
	}()

	return session, nil
}

func (c *CommandClient) DownloadTaildropFile(endpointTag string, name string, destinationPath string, handler TaildropDownloadHandler) (*TaildropDownloadSession, error) {
	client, parentCtx, err := c.getClientForCall()
	if err != nil {
		return nil, E.Cause(err, "download taildrop file")
	}

	streamCtx, cancel := context.WithCancel(parentCtx)
	failStart := func(cause error, message string) (*TaildropDownloadSession, error) {
		cancel()
		if c.standalone {
			c.closeConnection()
		}
		return nil, E.Cause(cause, message)
	}

	stream, err := client.DownloadTaildropFile(streamCtx, &daemon.DownloadTaildropFileRequest{
		EndpointTag: endpointTag,
		Name:        name,
	})
	if err != nil {
		return failStart(err, "download taildrop file")
	}
	firstChunk, err := stream.Recv()
	if err != nil {
		return failStart(err, "download taildrop file")
	}
	destinationFile, err := os.Create(destinationPath)
	if err != nil {
		return failStart(err, "download taildrop file")
	}

	session := &TaildropDownloadSession{
		streamSession: streamSession{
			ctx:       streamCtx,
			cancel:    cancel,
			closeDone: make(chan struct{}),
		},
	}

	standalone := c.standalone
	go func() {
		defer func() {
			close(session.closeDone)
			if standalone {
				c.closeConnection()
			}
		}()
		totalSize := firstChunk.Size
		var (
			downloaded   int64
			lastProgress time.Time
		)
		writeChunk := func(data []byte) error {
			if len(data) == 0 {
				return nil
			}
			_, writeErr := destinationFile.Write(data)
			if writeErr != nil {
				return writeErr
			}
			downloaded += int64(len(data))
			now := time.Now()
			if downloaded == totalSize || now.Sub(lastProgress) >= daemon.TaildropProgressMinInterval {
				lastProgress = now
				handler.OnProgress(downloaded, totalSize)
			}
			return nil
		}
		downloadErr := writeChunk(firstChunk.Data)
		for downloadErr == nil {
			var chunk *daemon.DownloadTaildropFileChunk
			chunk, downloadErr = stream.Recv()
			if downloadErr == io.EOF {
				downloadErr = nil
				break
			}
			if downloadErr != nil {
				downloadErr = E.Cause(downloadErr, "download taildrop file")
				break
			}
			downloadErr = writeChunk(chunk.Data)
		}
		if downloadErr == nil {
			downloadErr = destinationFile.Close()
		} else {
			destinationFile.Close()
		}
		if downloadErr != nil {
			os.Remove(destinationPath)
			if streamCtx.Err() == nil {
				handler.OnFinish(downloadErr.Error())
			}
			cancel()
			return
		}
		handler.OnFinish("")
		cancel()
	}()

	return session, nil
}

func (c *CommandClient) DeleteTaildropFile(endpointTag string, name string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.DeleteTaildropFile(ctx, &daemon.DeleteTaildropFileRequest{
			EndpointTag: endpointTag,
			Name:        name,
		})
	})
	if err != nil {
		return E.Cause(err, "delete taildrop file")
	}
	return nil
}

func (c *CommandClient) CancelTaildropReceiving(endpointTag string, senderID string, name string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.CancelTaildropReceiving(ctx, &daemon.CancelTaildropReceivingRequest{
			EndpointTag: endpointTag,
			SenderID:    senderID,
			Name:        name,
		})
	})
	if err != nil {
		return E.Cause(err, "cancel taildrop receiving")
	}
	return nil
}

func (c *CommandClient) MarkTaildropInboxRead(endpointTag string) error {
	_, err := callWithResult(c, func(ctx context.Context, client daemon.StartedServiceClient) (*emptypb.Empty, error) {
		return client.MarkTaildropInboxRead(ctx, &daemon.MarkTaildropInboxReadRequest{
			EndpointTag: endpointTag,
		})
	})
	if err != nil {
		return E.Cause(err, "mark taildrop inbox read")
	}
	return nil
}
