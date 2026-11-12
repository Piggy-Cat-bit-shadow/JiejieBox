package daemon

// Q25 — the reload the daemon performs must be the reload the caller asked for.
//
// The apply path already carries a request token (service_change_request_identity_test.go). The
// RELOAD path did not: ManagedService.ReloadService was Empty -> Empty, so a caller could only
// observe "a reload happened", never "MY reload happened, and it left this instance running".
// These tests drive the wire contract that closes that gap, over a real gRPC server: the request
// token is required, echoed unchanged, and joined with the instance/revision the handler reports
// afterwards. They also pin the two compatibility answers the clients depend on - the legacy
// token-less RPC keeps working, and a handler that cannot identify requests answers Unimplemented
// instead of fabricating a confirmation.

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type reloadRecordingHandler struct {
	// baseManagedHandler supplies the five methods these tests never call.
	baseManagedHandler

	access sync.Mutex

	instanceID     string
	activeRevision string
	reloadCalls    int

	requestIDs  []string
	expectedIDs []string

	// failuresLeft injects a send failure for a given request identity. It is consumed by the
	// first attempt that carries that identity, which is what a retry after a failed send sees.
	failuresLeft map[string]int
	// entered receives the request identity as each request starts, so a test can order two
	// in-flight attempts instead of hoping the scheduler cooperates.
	entered chan string
	// release blocks a request identity until the test closes its channel.
	release map[string]chan struct{}
}

// baseManagedHandler implements the legacy half with harmless behaviour.
type baseManagedHandler struct{}

func (baseManagedHandler) ServiceStop() error                                  { return nil }
func (baseManagedHandler) ServiceReload(context.Context) error                 { return nil }
func (baseManagedHandler) SystemProxyStatus() (*SystemProxyStatus, error)      { return &SystemProxyStatus{}, nil }
func (baseManagedHandler) SetSystemProxyEnabled(bool) error                    { return nil }
func (baseManagedHandler) TriggerNativeCrash() error                           { return nil }

func (h *reloadRecordingHandler) ServiceReload(ctx context.Context) error {
	h.access.Lock()
	h.reloadCalls++
	h.access.Unlock()
	return nil
}

func (h *reloadRecordingHandler) ServiceReloadWithRequest(ctx context.Context, requestID string, expectedInstanceID string) (*ReloadOutcome, error) {
	h.access.Lock()
	previousInstanceID := h.instanceID
	h.requestIDs = append(h.requestIDs, requestID)
	h.expectedIDs = append(h.expectedIDs, expectedInstanceID)
	fail := h.failuresLeft[requestID] > 0
	if fail {
		h.failuresLeft[requestID]--
	}
	entered := h.entered
	release := h.release[requestID]
	h.access.Unlock()

	if entered != nil {
		entered <- requestID
	}
	if release != nil {
		<-release
	}
	if fail {
		return nil, status.Error(codes.Unavailable, "injected reload send failure")
	}
	stale := expectedInstanceID != "" && expectedInstanceID != previousInstanceID
	h.access.Lock()
	h.instanceID = "instance-after-" + requestID
	h.activeRevision = "revision-after-" + requestID
	outcome := &ReloadOutcome{
		RequestID:      requestID,
		InstanceID:     h.instanceID,
		ActiveRevision: h.activeRevision,
		Stale:          stale,
		Status:         ReloadStatusRestarted,
	}
	h.access.Unlock()
	return outcome, nil
}

func startManagedServiceServer(t *testing.T, handler ManagedHandler) ManagedServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	RegisterManagedServiceServer(server, NewManagedService(ManagedServiceOptions{Handler: handler}))
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = connection.Close()
	})
	return NewManagedServiceClient(connection)
}

func reloadContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestAReloadRequestIsEchoedWithTheInstanceItLeftRunning(t *testing.T) {
	handler := &reloadRecordingHandler{instanceID: "instance-before", activeRevision: "revision-before"}
	client := startManagedServiceServer(t, handler)

	response, err := client.ReloadServiceWithRequest(reloadContext(t), &ReloadServiceRequest{
		RequestId:          "reload-41",
		ExpectedInstanceId: "instance-before",
	})
	require.NoError(t, err)
	require.Equal(t, "reload-41", response.GetRequestId(), "the caller's own identity comes back")
	require.Equal(t, "instance-after-reload-41", response.GetInstanceId(), "the instance it left running, not the one that was asked about")
	require.Equal(t, "revision-after-reload-41", response.GetActiveRevision())
	require.False(t, response.GetStale(), "the caller expected the instance that was actually running")
	require.Equal(t, ReloadStatusRestarted, response.GetStatus())
}

func TestAReloadWithoutARequestIdentityIsRefused(t *testing.T) {
	handler := &reloadRecordingHandler{instanceID: "instance-before"}
	client := startManagedServiceServer(t, handler)

	_, err := client.ReloadServiceWithRequest(reloadContext(t), &ReloadServiceRequest{})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = client.ReloadServiceWithRequest(reloadContext(t), &ReloadServiceRequest{RequestId: "   "})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Empty(t, handler.requestIDs, "a request without an identity never reaches the handler")
}

func TestAHandlerThatCannotIdentifyRequestsAnswersUnimplemented(t *testing.T) {
	client := startManagedServiceServer(t, baseManagedHandler{})

	_, err := client.ReloadServiceWithRequest(reloadContext(t), &ReloadServiceRequest{RequestId: "reload-1"})
	require.Error(t, err)
	require.Equal(t, codes.Unimplemented, status.Code(err),
		"a capability answer, so the client falls back to the legacy reload instead of claiming confirmation")
}

func TestTheLegacyReloadStillWorksOnTheRequestAwareDaemon(t *testing.T) {
	handler := &reloadRecordingHandler{instanceID: "instance-before"}
	client := startManagedServiceServer(t, handler)

	_, err := client.ReloadService(reloadContext(t), &emptypb.Empty{})
	require.NoError(t, err)
	require.Equal(t, 1, handler.reloadCalls, "an old client keeps its old entry")
	require.Empty(t, handler.requestIDs, "and the daemon does not invent an identity for it")
}

func TestASecondAttemptWithTheSameRequestIdentityIsAnsweredAsThatAttempt(t *testing.T) {
	handler := &reloadRecordingHandler{
		instanceID:   "instance-before",
		failuresLeft: map[string]int{"reload-retry": 1},
	}
	client := startManagedServiceServer(t, handler)
	request := &ReloadServiceRequest{RequestId: "reload-retry", ExpectedInstanceId: "instance-before"}
	ctx := reloadContext(t)

	first, err := client.ReloadServiceWithRequest(ctx, request)
	require.Error(t, err)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Nil(t, first)

	second, err := client.ReloadServiceWithRequest(ctx, request)
	require.NoError(t, err)
	require.Equal(t, "reload-retry", second.GetRequestId(), "the retry answers to the same attempt")
	require.Equal(t, "instance-after-reload-retry", second.GetInstanceId())
	require.Equal(t, []string{"reload-retry", "reload-retry"}, handler.requestIDs,
		"the daemon saw one attempt twice, not two attempts")
}

func TestTwoInFlightReloadRequestsKeepTheirOwnIdentity(t *testing.T) {
	firstRelease := make(chan struct{})
	handler := &reloadRecordingHandler{
		instanceID: "instance-before",
		entered:    make(chan string, 4),
		release:    map[string]chan struct{}{"reload-a": firstRelease},
	}
	client := startManagedServiceServer(t, handler)

	type answer struct {
		response *ReloadServiceResponse
		err      error
	}
	firstAnswer := make(chan answer, 1)
	go func() {
		response, err := client.ReloadServiceWithRequest(reloadContext(t), &ReloadServiceRequest{RequestId: "reload-a"})
		firstAnswer <- answer{response: response, err: err}
	}()
	require.Equal(t, "reload-a", <-handler.entered, "the first attempt is in flight")

	secondAnswer := make(chan answer, 1)
	go func() {
		response, err := client.ReloadServiceWithRequest(reloadContext(t), &ReloadServiceRequest{RequestId: "reload-b"})
		secondAnswer <- answer{response: response, err: err}
	}()
	require.Equal(t, "reload-b", <-handler.entered, "the second attempt is in flight too")

	second := <-secondAnswer
	require.NoError(t, second.err)
	require.Equal(t, "reload-b", second.response.GetRequestId(), "the answer that arrived first names its own attempt")
	require.Equal(t, "instance-after-reload-b", second.response.GetInstanceId())

	close(firstRelease)
	first := <-firstAnswer
	require.NoError(t, first.err)
	require.Equal(t, "reload-a", first.response.GetRequestId(), "the late answer does not settle the newer attempt")
	require.Equal(t, "instance-after-reload-a", first.response.GetInstanceId())
}

func TestAnExpectedInstanceMismatchIsReportedAsStaleButStillReloads(t *testing.T) {
	handler := &reloadRecordingHandler{instanceID: "instance-replaced"}
	client := startManagedServiceServer(t, handler)

	response, err := client.ReloadServiceWithRequest(reloadContext(t), &ReloadServiceRequest{
		RequestId:          "reload-stale",
		ExpectedInstanceId: "instance-remembered",
	})
	require.NoError(t, err)
	require.True(t, response.GetStale(), "the reload was decided against an instance the caller had not seen")
	require.Equal(t, "instance-after-reload-stale", response.GetInstanceId(), "but refusing would leave the caller stuck")
	require.Equal(t, []string{"instance-remembered"}, handler.expectedIDs)
}
