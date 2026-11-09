package v2rayxhttp

import (
	"bytes"
	"net"
	"net/http"
	"testing"
)

// The Box-facing shared budget (packetup_upload_budget.go).
//
// These tests act as the Box assembly would: one budget, several Clients bound to it,
// and a second budget for a second Box. They are the interface contract the integration
// window wires box.go against; they do not touch box.go themselves.

// sharedBudgetFixture builds clients on their own transports and a budget with explicit
// pools, then binds each client to it — the way a Box binds its outbounds before any
// dial.
func sharedBudgetFixture(t *testing.T, clients int, capacity, reserve, control int) ([]*Client, *UploadBudget, []*probeTransport, chan struct{}) {
	t.Helper()
	gate := make(chan struct{})
	transports := make([]*probeTransport, 0, clients)
	made := make([]*Client, 0, clients)
	for index := 0; index < clients; index++ {
		transport := newProbeTransport()
		transport.onPost = func(_ *http.Request, _ []byte) (*http.Response, error) {
			<-gate
			return okProbeResponse(), nil
		}
		client := batchClient(t, transport, metaOptions{ScMinPostsIntervalMs: "0"})
		transports = append(transports, transport)
		made = append(made, client)
	}
	budget := &UploadBudget{pool: newSharedUplinkBudgetWithControl(capacity, reserve, control)}
	for _, client := range made {
		if err := budget.Bind(client); err != nil {
			t.Fatalf("Bind: %v", err)
		}
		if client.budget != budget.pool {
			t.Fatal("Bind left the client on its own pool")
		}
	}
	return made, budget, transports, gate
}

// TestUploadBudgetSharesOneBoundAcrossClients is the Box-level claim: two Clients, one
// budget, one aggregate bound. The second client's stream is held by the FIRST client's
// charge, which is what "a Box with several XHTTP outbounds has one bound" means.
func TestUploadBudgetSharesOneBoundAcrossClients(t *testing.T) {
	t.Parallel()

	const (
		capacity = 64 * 1024
		reserve  = 16 * 1024
		control  = 32 * 1024
	)
	clients, budget, _, gate := sharedBudgetFixture(t, 2, capacity, reserve, control)
	pool := budget.pool
	first := dialBatchConn(t, clients[0])
	second := dialBatchConn(t, clients[1])

	payload := make([]byte, capacity)
	if result := awaitPayloadWrite(t, writePayloadAsync(first, payload), capacity); result.err != nil || result.n != capacity {
		t.Fatalf("first client's Write = (%d, %v), want (%d, nil)", result.n, result.err, capacity)
	}
	waitForPool(t, pool, "main pool full from the first client", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})

	// The second client's stream can only use the shared headroom; the main pool is
	// the first client's.
	blocked := writePayloadAsync(second, payload)
	waitForPool(t, pool, "the second client queued behind the shared bound", func(stats uplinkBudgetStats) bool {
		return stats.Waiters == 1
	})
	snapshot := budget.Snapshot()
	if snapshot.Streams != 2 {
		t.Fatalf("shared budget sees %d streams, want 2 (one per client)", snapshot.Streams)
	}
	if snapshot.Total() > capacity+control+reserve {
		t.Fatalf("shared total %d over the aggregate %d", snapshot.Total(), capacity+control+reserve)
	}
	held := first.(*packetConn).uplink.stats().SharedHeldBytes + second.(*packetConn).uplink.stats().SharedHeldBytes
	if held != snapshot.Total() {
		t.Fatalf("clients hold %d bytes but the shared budget reports %d", held, snapshot.Total())
	}
	select {
	case result := <-blocked:
		t.Fatalf("the second client's Write returned under the shared bound: (%d, %v)", result.n, result.err)
	default:
	}

	close(gate)
	if result := awaitPayloadWrite(t, blocked, capacity); result.err != nil || result.n != capacity {
		t.Fatalf("second client's Write after release = (%d, %v), want (%d, nil)", result.n, result.err, capacity)
	}
	waitForPool(t, pool, "shared budget drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
	for _, conn := range []net.Conn{first, second} {
		if err := conn.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
	final := budget.Snapshot()
	if final.Streams != 0 || final.Total() != 0 || final.Allocated != 0 {
		t.Fatalf("shared budget not back at baseline: %+v", final)
	}
	if final.GrantedBytes != final.ReleasedBytes || final.AllocGrantedBytes != final.AllocReleasedBytes {
		t.Fatalf("shared budget flow does not close: %+v", final)
	}
}

// TestUploadBudgetKeepsTwoBoxesIsolated is the other half of the contract: separate
// Boxes get separate budgets, so one Box under pressure (or draining a replaced
// outbound's late returns) cannot spend or block another Box's headroom.
func TestUploadBudgetKeepsTwoBoxesIsolated(t *testing.T) {
	t.Parallel()

	firstClients, firstBudget, _, firstGate := sharedBudgetFixture(t, 1, 16*1024, 8*1024, 16*1024)
	secondClients, secondBudget, _, secondGate := sharedBudgetFixture(t, 1, 64*1024, 16*1024, 32*1024)

	full := dialBatchConn(t, firstClients[0])
	if result := awaitPayloadWrite(t, writePayloadAsync(full, make([]byte, 16*1024)), 16*1024); result.err != nil || result.n != 16*1024 {
		t.Fatalf("first Box Write = (%d, %v)", result.n, result.err)
	}
	waitForPool(t, firstBudget.pool, "first Box full", func(stats uplinkBudgetStats) bool {
		return stats.Used == stats.Capacity
	})

	// The second Box is untouched by the first Box's saturation.
	other := dialBatchConn(t, secondClients[0])
	payload := bytes.Repeat([]byte("o"), 48*1024)
	if result := awaitPayloadWrite(t, writePayloadAsync(other, payload), len(payload)); result.err != nil || result.n != len(payload) {
		t.Fatalf("second Box Write = (%d, %v), want (%d, nil)", result.n, result.err, len(payload))
	}
	firstSnapshot := firstBudget.Snapshot()
	secondSnapshot := secondBudget.Snapshot()
	if firstSnapshot.Used != 16*1024 {
		t.Fatalf("first Box logical charge %d, want its own 16 KiB", firstSnapshot.Used)
	}
	if secondSnapshot.Used != len(payload) {
		t.Fatalf("second Box logical charge %d, want the %d it wrote", secondSnapshot.Used, len(payload))
	}
	if firstSnapshot.GrantedBytes == 0 || secondSnapshot.GrantedBytes == 0 {
		t.Fatalf("one of the budgets was never charged: %+v %+v", firstSnapshot, secondSnapshot)
	}

	// A late return in the first Box stays in the first Box: Close while its POST is
	// wedged keeps the charge, and the second Box's numbers do not move.
	if err := full.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if held := firstBudget.Snapshot().Total(); held == 0 {
		t.Fatalf("the first Box released a charge its wedged POST still owns: %+v", firstBudget.Snapshot())
	}
	if secondBudget.Snapshot().Total() != len(payload) {
		t.Fatalf("the first Box's Close moved the second Box's charge: %+v", secondBudget.Snapshot())
	}

	close(firstGate)
	close(secondGate)
	waitForPool(t, firstBudget.pool, "first Box drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
	waitForPool(t, secondBudget.pool, "second Box drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0 && stats.Allocated == 0
	})
}

// TestUploadBudgetRefusesRebindingALiveClient makes the split-ledger mistake loud: a
// client whose streams are already charged cannot be moved to another budget.
func TestUploadBudgetRefusesRebindingALiveClient(t *testing.T) {
	t.Parallel()

	clients, budget, _, gate := sharedBudgetFixture(t, 1, 32*1024, 8*1024, 16*1024)
	conn := dialBatchConn(t, clients[0])
	if _, err := conn.Write(make([]byte, 1024)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	other := &UploadBudget{pool: newSharedUplinkBudgetWithControl(32*1024, 8*1024, 16*1024)}
	if err := other.Bind(clients[0]); err == nil {
		t.Fatal("binding a client with live streams to a second budget was allowed")
	}
	if clients[0].budget != budget.pool {
		t.Fatal("the refused Bind still moved the client")
	}
	close(gate)
	waitForPool(t, budget.pool, "drained", func(stats uplinkBudgetStats) bool {
		return stats.Total() == 0
	})
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Only a client with no live streams may move: the guard is about the split
	// ledger, not about idle charges.
	waitForPool(t, budget.pool, "no live streams", func(stats uplinkBudgetStats) bool {
		return stats.Streams == 0
	})
	if err := other.Bind(clients[0]); err != nil {
		t.Fatalf("Bind after the client drained: %v", err)
	}
	if clients[0].budget != other.pool {
		t.Fatal("Bind after the client drained did not take")
	}
}

// TestUploadBudgetSizesLikeTheClientPool proves NewUploadBudget copies the policy rather
// than inventing a second one, so a Box's bound is the same shape as a client's.
func TestUploadBudgetSizesLikeTheClientPool(t *testing.T) {
	t.Parallel()

	transport := newProbeTransport()
	client := budgetedBatchClient(t, transport, metaOptions{})
	own := client.budget.stats()
	budget := client.NewUploadBudget()
	if budget == nil {
		t.Fatal("NewUploadBudget returned nil for a client with a pool")
	}
	shared := budget.Snapshot()
	if shared.Capacity != own.Capacity || shared.Control != own.Control || shared.Reserve != own.Reserve {
		t.Fatalf("shared budget pools %+v do not match the client's %+v", shared, own)
	}
	if shared.AllocCapacity != own.AllocCapacity {
		t.Fatalf("shared allocation bound %d != client's %d", shared.AllocCapacity, own.AllocCapacity)
	}
	if shared.Streams != 0 || shared.Total() != 0 {
		t.Fatalf("a new budget is not empty: %+v", shared)
	}
	// The explicit constructor is the same policy for a raw per-stream bound.
	bound := packetUpPendingBound(client.meta)
	direct := NewUploadBudgetFor(bound)
	if direct == nil || direct.Snapshot().Capacity != packetUpSharedCapacity(bound) {
		t.Fatalf("NewUploadBudgetFor(%d) capacity = %d, want %d",
			bound, direct.Snapshot().Capacity, packetUpSharedCapacity(bound))
	}
}

// TestUploadBudgetZeroValueIsRejected pins the error paths the wiring relies on.
func TestUploadBudgetZeroValueIsRejected(t *testing.T) {
	t.Parallel()

	var empty UploadBudget
	if err := empty.Bind(nil); err == nil {
		t.Fatal("a zero budget accepted a nil client")
	}
	if snapshot := empty.Snapshot(); snapshot != (UploadBudgetSnapshot{}) {
		t.Fatalf("a zero budget reported %+v", snapshot)
	}
	if err := empty.Bind(&Client{}); err == nil {
		t.Fatal("a zero budget accepted a client")
	}
	var client *Client
	if budget := client.NewUploadBudget(); budget != nil {
		t.Fatal("NewUploadBudget on a nil client returned a budget")
	}
}
