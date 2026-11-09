package box

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/v2rayxhttp"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// The Box-level XHTTP upload budget (box_upload_budget.go):
//
//   - the construction context carries the Box's provider, and the transport binds every
//     XHTTP client it builds to the budget that provider returns;
//   - two clients built under ONE Box context share the same budget OBJECT (not two
//     equal pools), which is the difference between one aggregate bound and N of them;
//   - two Boxes stay isolated, so a replaced Box's late returns settle against its own
//     ledger and cannot spend or block the new Box's headroom.
//
// The clients are built through v2rayxhttp.NewClient - the same constructor the vless
// outbound reaches through the v2ray transport registry - so this covers the wiring the
// transport owns. It does not dial: dialing is the transport's own test suite.

func newXHTTPClientForBoxTest(t *testing.T, ctx context.Context) *v2rayxhttp.Client {
	t.Helper()
	transport, err := v2rayxhttp.NewClient(ctx, N.SystemDialer, M.ParseSocksaddr("127.0.0.1:443"), option.V2RayXHTTPOptions{}, nil)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	client, isClient := transport.(*v2rayxhttp.Client)
	if !isClient {
		t.Fatalf("NewClient returned %T", transport)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return client
}

func TestBoxSharesOneUploadBudgetAcrossItsXHTTPClients(t *testing.T) {
	holder := newBoxUploadBudget()
	ctx := v2rayxhttp.WithUploadBudgetProvider(context.Background(), holder)
	first := newXHTTPClientForBoxTest(t, ctx)
	second := newXHTTPClientForBoxTest(t, ctx)

	if first.UploadBudget() == nil || second.UploadBudget() == nil {
		t.Fatal("a client built under the Box context did not bind a shared budget")
	}
	if first.UploadBudget() != second.UploadBudget() {
		t.Fatalf("two clients of one Box bound different budgets: %p vs %p",
			first.UploadBudget(), second.UploadBudget())
	}
	if first.UploadBudget() != holder.UploadBudgetFor(v2rayxhttp.UploadBudgetSizing{Capacity: 1}) {
		t.Fatal("the holder returned a different budget for a later request")
	}
	snapshot := holder.snapshot()
	if snapshot.Capacity <= 0 {
		t.Fatalf("the shared budget has no pools: %+v", snapshot)
	}
	if snapshot.Streams != 0 || snapshot.Total() != 0 {
		t.Fatalf("a fresh Box budget is not empty: %+v", snapshot)
	}
}

func TestBoxUploadBudgetsAreIsolatedBetweenBoxes(t *testing.T) {
	firstHolder := newBoxUploadBudget()
	secondHolder := newBoxUploadBudget()
	first := newXHTTPClientForBoxTest(t, v2rayxhttp.WithUploadBudgetProvider(context.Background(), firstHolder))
	second := newXHTTPClientForBoxTest(t, v2rayxhttp.WithUploadBudgetProvider(context.Background(), secondHolder))
	if first.UploadBudget() == second.UploadBudget() {
		t.Fatal("two Boxes share one upload budget: a replaced Box's late returns would spend the new Box's headroom")
	}
	if first.UploadBudget() == nil || second.UploadBudget() == nil {
		t.Fatal("a Box did not get its own budget")
	}
	// The sizing rule: the first client fixes the aggregate; a later client with the same
	// policy shares it. (Different sizings sharing the first aggregate is documented in
	// box_upload_budget.go; asserting it here would need a second option shape.)
	if got := firstHolder.snapshot().Capacity; got != packetUpSharedCapacityForTest(t, first) {
		t.Fatalf("shared capacity %d does not match the first client's own pool", got)
	}
	if got := secondHolder.snapshot().Capacity; got != packetUpSharedCapacityForTest(t, second) {
		t.Fatalf("second Box capacity %d does not match its client's own pool", got)
	}
}

// packetUpSharedCapacityForTest reads the client's own pool capacity. A client bound to a
// shared budget draws on it, so the client's OWN budget numbers are the shared budget's -
// which is the point: the per-client pool is gone after binding.
func packetUpSharedCapacityForTest(t *testing.T, client *v2rayxhttp.Client) int {
	t.Helper()
	if budget := client.UploadBudget(); budget != nil {
		return budget.Snapshot().Capacity
	}
	t.Fatal("client has no shared budget")
	return 0
}

func TestBoxUploadBudgetSnapshotWithoutClients(t *testing.T) {
	holder := newBoxUploadBudget()
	if snapshot := holder.snapshot(); snapshot.Capacity != 0 || snapshot.Total() != 0 {
		t.Fatalf("a holder with no client reported %+v", snapshot)
	}
	var nilHolder *boxUploadBudget
	if budget := nilHolder.UploadBudgetFor(v2rayxhttp.UploadBudgetSizing{Capacity: 1024}); budget != nil {
		t.Fatal("a nil holder returned a budget")
	}
	if snapshot := nilHolder.snapshot(); snapshot != (v2rayxhttp.UploadBudgetSnapshot{}) {
		t.Fatalf("a nil holder reported %+v", snapshot)
	}
}
