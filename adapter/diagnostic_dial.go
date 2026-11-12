package adapter

// DiagnosticDial — the marker that tells an outbound group "this dial is a DIAGNOSTIC probe, not a
// flow".
//
// # The defect this closes
//
// The active connection test runs the production route decision and dials its result, which is what
// makes it a real observation rather than a model lookup. The price of that honesty is that the dial
// goes through the real outbound, and a real outbound with failover capability KEEPS A LEDGER of what
// happened: a successful dial records proof of life, a failed one records a penalty, and enough
// penalties demote the member for every OTHER flow and ask for a forced health round.
//
// That is correct for a flow and wrong for a probe. A user pressing "diagnose" while the network is
// briefly down would demote the member that carries their traffic, and the diagnostic that is
// supposed to explain a selection would have moved it. The package that promises "It writes no
// health evidence" was therefore making a claim its own dial path did not honour.
//
// # Why a context marker and not a separate dial path
//
// A second "diagnostic outbound" would be a parallel system: it would not be the production decision
// any more, which is the whole point of the active half. The marker travels on the context the dial
// already carries, so a group that can write health evidence asks one question at the moment it is
// about to write, and a group that has nothing to write ignores it. Callers that never mark a context
// are byte-for-byte unchanged.
//
// # Who must honour it
//
// Any owner of long-lived health or selection state written as a side effect of a dial:
// LoadBalance's failover ledger (penalty, proof of life, the forced retest it can request). A dial
// that only READS health (the healthy() filter) needs no change: reading a ledger cannot move a
// selection.
//
// It is NOT a licence to skip work: the dial, the retry, the address candidates and the resolver are
// all still the production ones. Only the RECORDING of the outcome as evidence about the path's
// health is suppressed, because that record is what the next production flow would act on.

import "context"

// diagnosticDialKey is the context key. It is an unexported struct type, so nothing outside this
// package can install the marker by spelling a string key, and a group can trust what it reads.
type diagnosticDialKey struct{}

// WithDiagnosticDial marks ctx as belonging to a diagnostic probe.
//
// A nil ctx is returned unchanged: a diagnostic never dials without one, and treating nil as
// "marked" would let a zero-value context silently suppress a production ledger write.
func WithDiagnosticDial(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, diagnosticDialKey{}, true)
}

// IsDiagnosticDial reports whether ctx was marked by WithDiagnosticDial.
//
// The zero value answers false, so an ordinary flow is never accidentally treated as a probe.
func IsDiagnosticDial(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	marked, _ := ctx.Value(diagnosticDialKey{}).(bool)
	return marked
}
