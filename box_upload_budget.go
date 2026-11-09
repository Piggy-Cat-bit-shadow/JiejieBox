package box

import (
	"sync"

	"github.com/sagernet/sing-box/transport/v2rayxhttp"
)

// The Box-scoped packet-up upload budget.
//
// Each XHTTP client used to own one upload pool, so a Box with N XHTTP outbounds bounded
// N times the memory. The transport now asks the construction context for a shared
// budget (v2rayxhttp.WithUploadBudgetProvider); this holder is what the Box puts there.
//
// # Why a holder instead of the budget itself
//
// The budget is sized from a client's own pool policy (sc_max_each_post_bytes), which is
// only known when the first XHTTP client is constructed - after the Box has created its
// context but before any traffic. The holder creates it on that first request and returns
// the same one for every later client, so the Box needs no second copy of the policy and
// no option/ABI change.
//
// # Heterogeneous outbounds
//
// The FIRST XHTTP client fixes the aggregate's sizing. Later XHTTP outbounds with a
// different sc_max_each_post_bytes keep their own per-stream bound but share that
// aggregate; a Box that really needs per-outbound aggregates should give those outbounds
// their own Box (or the transport could grow a per-sizing map - deliberately not invented
// until a configuration needs it).
type boxUploadBudget struct {
	access   sync.Mutex
	budget   *v2rayxhttp.UploadBudget
	requests int
}

func newBoxUploadBudget() *boxUploadBudget {
	return &boxUploadBudget{}
}

// UploadBudgetFor implements v2rayxhttp.UploadBudgetProvider: one budget for the whole
// Box, created from the first client's sizing.
func (b *boxUploadBudget) UploadBudgetFor(sizing v2rayxhttp.UploadBudgetSizing) *v2rayxhttp.UploadBudget {
	if b == nil {
		return nil
	}
	b.access.Lock()
	defer b.access.Unlock()
	b.requests++
	if b.budget == nil {
		b.budget = sizing.NewBudget()
	}
	return b.budget
}

// requestCount reports how many XHTTP clients asked for the budget. A Box with two XHTTP
// outbounds calls it twice and still holds one budget; diagnostics and the root test use
// the count to tell "shared" from "never bound".
func (b *boxUploadBudget) requestCount() int {
	if b == nil {
		return 0
	}
	b.access.Lock()
	defer b.access.Unlock()
	return b.requests
}

// snapshot reports the shared budget's accounting, or a zero snapshot when no XHTTP
// client was ever constructed. Diagnostics read it; a test uses it to prove the clients
// actually charged one ledger.
func (b *boxUploadBudget) snapshot() v2rayxhttp.UploadBudgetSnapshot {
	if b == nil {
		return v2rayxhttp.UploadBudgetSnapshot{}
	}
	b.access.Lock()
	defer b.access.Unlock()
	if b.budget == nil {
		return v2rayxhttp.UploadBudgetSnapshot{}
	}
	return b.budget.Snapshot()
}

// XHTTPUploadBudgetStatus is the Box-wide packet-up upload budget as diagnostics see it:
// how many XHTTP clients asked for it (each asks once, at construction) and the shared
// ledger's snapshot. A Box with two XHTTP outbounds reports Clients=2 and ONE snapshot.
type XHTTPUploadBudgetStatus struct {
	Clients  int
	Snapshot v2rayxhttp.UploadBudgetSnapshot
}

// XHTTPUploadBudgetStatus reports the Box's shared XHTTP upload budget. A Box that never
// constructed an XHTTP client reports the zero status: the budget is created on first
// use, not at assembly.
func (s *Box) XHTTPUploadBudgetStatus() XHTTPUploadBudgetStatus {
	if s == nil || s.uploadBudget == nil {
		return XHTTPUploadBudgetStatus{}
	}
	return XHTTPUploadBudgetStatus{
		Clients:  s.uploadBudget.requestCount(),
		Snapshot: s.uploadBudget.snapshot(),
	}
}
