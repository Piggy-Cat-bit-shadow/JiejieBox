package adapter

// ResidencyOwnerCount is how many owners of ONE kind the retirement walks can reach, and how many of
// those expose the release half of the reusable-owner capability.
//
// # These are owner-reach counts, not resource counts
//
// A count here is a count of OBJECTS THE WALK CAN ASK, not of connections, sessions, streams,
// generations, bytes or terminations. One outbound, one endpoint, one DNS transport or one HTTP
// client manager counts once no matter what it currently holds, so a pool that reads
// {Reachable: 1, Releasable: 1} may be holding zero connections or a thousand.
//
// Neither number says anything about a termination either. "Releasable" only means the owner
// implements CloseIdleConnections; it does NOT mean a call to it finished closing, and it does not
// mean the resources are gone. The XHTTP XMUX pool is the standing example: its CloseIdleConnections
// detaches idle connections and defers every busy one's teardown until the last stream releases it,
// so a successful walk is followed by real closes that this count cannot see.
//
// The per-owner active/idle/draining ledger belongs to the owner itself. This type is the inventory
// of who can be asked, and it exists because the walks used to report a single integer that was read
// as a resource count.
type ResidencyOwnerCount struct {
	// Reachable is the number of owners of this kind the walk enumerates.
	Reachable int
	// Releasable is how many of those answer adapter.IdleConnectionReleaser.
	Releasable int
}

// ResidencySnapshot is the per-kind inventory of the owners the retirement walks reach.
//
// It is deliberately additive to the walk's return value rather than a replacement: the walks still
// report how many owners they acted on, and this reports how many existed per kind and how many of
// them the action can reach at all.
type ResidencySnapshot struct {
	Outbounds     ResidencyOwnerCount
	Endpoints     ResidencyOwnerCount
	DNSTransports ResidencyOwnerCount
	HTTPClients   ResidencyOwnerCount
}

// ResidencyReporter is implemented by the owner of the retirement walks and read by the OOM report.
//
// The reporter is the process's reference manager rather than an individual pool: a single answer
// per owner kind is what keeps the report's cost independent of the number of configured outbounds,
// and what keeps the walk's enumeration single-source. Per-pool ledgers remain the pools' own.
type ResidencyReporter interface {
	ResidencySnapshot() ResidencySnapshot
}
