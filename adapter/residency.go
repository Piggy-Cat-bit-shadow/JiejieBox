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

// ResidencyUnit names WHAT one counted object is. It exists because the audit's Q15 finding was that
// a stream, a session and a device connection were being added into one "physical connection" number.
// A sum is only meaningful between objects of the same unit, so the unit travels with the counts and
// the aggregated snapshot keeps the totals split by it.
type ResidencyUnit string

const (
	// ResidencyUnitSession is one application session: the resident object of Hysteria2, TUIC and
	// the trusttunnel client (one session carries its streams; they are its Active/Idle
	// distinction, not separate objects).
	ResidencyUnitSession ResidencyUnit = "session"
	// ResidencyUnitConnection is one pooled transport connection: the shared QUIC connection of
	// v2rayquic and one XMUX pooled connection of v2rayxhttp.
	ResidencyUnitConnection ResidencyUnit = "connection"
	// ResidencyUnitGeneration is one pool GENERATION - the transport object that a pool builds its
	// connections under. It is the unit where "across generations" is the question: the current
	// generation is Active or Idle, every retired one is Draining until its last user returns.
	ResidencyUnitGeneration ResidencyUnit = "generation"
	// ResidencyUnitStream is one multiplexed stream, for an owner that can only observe streams
	// (a library owns the pooled connections underneath and does not expose them).
	ResidencyUnitStream ResidencyUnit = "stream"
	// ResidencyUnitDeviceConnection is one connection/flow the device side owns. It is NOT a
	// session: the tunnel can carry it or decline it, and it exists before any session does.
	ResidencyUnitDeviceConnection ResidencyUnit = "device-connection"
	// ResidencyUnitDevice is one device-side instance: a WireGuard device, a tunnel instance. It is
	// NOT a session and not a connection - the audit's "stream/session/device" list is exactly the
	// set of unlike objects that must not be summed.
	ResidencyUnitDevice ResidencyUnit = "device"
	// ResidencyUnitUnspecified is the unit an owner that does not name one is counted under. It is
	// its own record rather than a synonym for any real unit: an unnamed object must not be added
	// to a named one's total.
	ResidencyUnitUnspecified ResidencyUnit = "unspecified"
)

// ResidencyLedger is ONE owner's object-identity counts.
//
// # How it differs from ResidencyOwnerCount
//
// That type counts OWNERS the walk can ask; this counts the owners' OWN OBJECTS. One outbound that
// holds forty pooled sessions contributes {Reachable: 1} there and forty objects here, and the two
// numbers answer different questions - "who can be asked" versus "how much is resident".
//
// # The states, and why there are four
//
// A resident object is in exactly ONE of the states, so the four counts never overlap:
//
//	Creating  reserved/allocated but not yet usable (a dial or handshake in flight, a session whose
//	          first version is still being negotiated). An object in this state costs memory.
//	Active    usable and carrying work (a stream, a request, a packet flow).
//	Idle      usable and carrying nothing; it is the state the retirement walks may reclaim.
//	Draining  detached from reuse and waiting for its last user to finish. It is NOT released:
//	          counting it as gone is the specific error this state exists to prevent.
//
// Terminated is a MONOTONIC total across the owner's lifetime, not a state: it is the count of
// objects whose physical release the owner actually completed. It never decreases and it is not
// included in Live().
//
// # Across generations
//
// Unit names what the objects are, and Generation/RetiredGenerations say how many GENERATIONS are
// holding them: a pool that retired the previous generation while a stream was still running reports
// Draining=1 and RetiredGenerations=1, not a zero. The counts never subtract a retired generation: it
// leaves Draining only when its release really completed, and that release is what Terminated counts.
//
// # Exact versus unknown
//
// Exact is false when any number in the record is an estimate or a bound rather than a count of
// real objects, and Note then names the boundary. A native object with no observation interface
// must be reported as Exact=false (or not reported at all), never as a precise zero - see the Q14
// unknown boundary this deliberately mirrors.
type ResidencyLedger struct {
	Creating uint64
	Active   uint64
	Idle     uint64
	Draining uint64
	// Unit is what one counted object IS. It is required for the aggregation to keep unlike objects
	// apart; a ledger without a unit is treated as its own unknown unit rather than being added to
	// every other owner's total.
	Unit ResidencyUnit
	// Generation is the owner's CURRENT generation ordinal, or zero when the owner has no
	// generation concept. It is the owner's own numbering: comparing two owners' numbers is
	// meaningless, and only the owner's own snapshot does.
	Generation uint64
	// RetiredGenerations is how many generations are no longer current but still hold objects. A
	// nonzero value with Draining counting the same objects is the honest cross-generation account;
	// a retired generation whose last object is gone is counted in Terminated, not here.
	RetiredGenerations int
	// Terminated is the owner's lifetime total of completed releases.
	Terminated uint64
	// Exact reports whether the counts are real object counts.
	Exact bool
	// Answered reports whether the owner can answer this question at all.
	//
	// It exists because a reporter is also how an owner that CANNOT count its objects is reached by
	// the walk: returning an empty ledger would be read as "this owner holds zero objects", which is
	// the precise-zero lie the audit forbids. An owner that cannot observe them returns
	// Answered=false and says why in Note; the snapshot counts it as Unknown, exactly as if the
	// interface were not implemented.
	Answered bool
	// Note names what is estimated when Exact is false, or what the ledger deliberately does not
	// count when it is true.
	Note string
}

// Live is the number of objects this owner holds right now: every state except Terminated.
//
// It is a method rather than a stored field so the four states cannot drift from the total.
func (l ResidencyLedger) Live() uint64 {
	return l.Creating + l.Active + l.Idle + l.Draining
}

// ResidencyLedgerReporter is implemented by an owner that can count its own objects.
//
// It is OPTIONAL and separate from the retirement capability: a pool may be reclaimable without
// being able to say how many objects it holds (it would then count as an unknown owner in the
// snapshot), and a pool may know its ledger and not implement the walks. The reference manager
// asks both questions of the same enumeration, so an owner can never be counted twice.
type ResidencyLedgerReporter interface {
	ResidencyLedger() ResidencyLedger
}

// ResidencyObjectCounts is one owner KIND's aggregated object-identity record.
//
// Owners and Reporting are the coverage statement the audit asks for: of the owners of this kind
// the walk reaches, how many answered with a ledger. An owner that cannot answer is an UNKNOWN, not
// a zero, and Unknown is carried explicitly for that reason.
//
// # Why the totals are split by unit
//
// A kind can hold owners that count different objects: today the outbound kind holds providers that
// count sessions and one that can only count streams. Adding those together is the audit's Q15
// finding - a stream, a session and a device connection are not one physical connection count - so
// the totals are per unit, and MixedUnits says when this kind's flat fields span more than one unit.
// The flat fields are kept (and equal the sum over units) so an existing reader does not break; the
// Units records are what a reader that is about to add two numbers together must use.
type ResidencyObjectCounts struct {
	Owners    int
	Reporting int
	Unknown   int
	// Estimated counts the reporting owners whose ledger is not exact.
	Estimated int
	// MixedUnits is true when the reporting owners of this kind count MORE THAN ONE unit. The flat
	// totals below are then a sum of unlike objects and must not be presented as one count.
	MixedUnits bool
	// Units is one record per unit the reporting owners count, ordered by unit name.
	Units []ResidencyUnitCounts
	// Sums of the reporting owners' ledgers. They are per-object totals: an object belongs to one
	// owner, so summing across owners of one kind cannot double count it. Use Units to keep unlike
	// objects apart.
	Creating   uint64
	Active     uint64
	Idle       uint64
	Draining   uint64
	Terminated uint64
	// Generations is how many generations the reporting owners hold objects in, current ones
	// included; RetiredGenerations is the subset that are no longer current. Both are summed over
	// the owners that answered - a count of generations, never a count of objects.
	Generations        int
	RetiredGenerations int
}

// ResidencyUnitCounts is one kind's totals for ONE unit of object.
type ResidencyUnitCounts struct {
	Unit ResidencyUnit
	// Owners is how many reporting owners counted objects of this unit.
	Owners int
	// Exact is false when any contributing owner's ledger was an estimate.
	Exact bool
	// Generation is the highest current-generation ordinal reported by an owner of this unit; it is
	// the owner's own numbering, so it is reported as a maximum and not as a shared clock.
	Generation         uint64
	Generations        int
	RetiredGenerations int
	Creating           uint64
	Active             uint64
	Idle               uint64
	Draining           uint64
	Terminated         uint64
}

// ResidencyObjectSnapshot is the object-identity half of the residency report.
//
// It is a SECOND answer, not a replacement for ResidencySnapshot: the owner-reach counts stay
// exactly as they were (their contract says they are not resource counts), and this one is the
// resource count those numbers were being misread as. Keeping them apart is what lets a reader see
// both "how many owners can be asked" and "how much is resident", which the audit found conflated.
type ResidencyObjectSnapshot struct {
	Outbounds     ResidencyObjectCounts
	Endpoints     ResidencyObjectCounts
	DNSTransports ResidencyObjectCounts
	HTTPClients   ResidencyObjectCounts
}

// ObjectResidencyReporter is implemented by the same owner as ResidencyReporter, for the second
// answer. It is a separate interface so an embedder that only implements owner-reach keeps working.
type ObjectResidencyReporter interface {
	ObjectResidencySnapshot() ResidencyObjectSnapshot
}
