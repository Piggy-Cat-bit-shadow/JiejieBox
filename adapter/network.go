package adapter

import (
	"context"
	"encoding/hex"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/sagernet/sing-box/common/netcap"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/control"
)

type NetworkManager interface {
	Lifecycle
	Initialize(ruleSets []RuleSet)
	InterfaceFinder() control.InterfaceFinder
	UpdateInterfaces() error
	DefaultNetworkInterface() *NetworkInterface
	NetworkInterfaces() []NetworkInterface
	NetworkEnvironment() uint64
	AutoDetectInterface() bool
	AutoDetectInterfaceFunc() control.Func
	ProtectFunc() control.Func
	DefaultOptions() NetworkOptions
	RegisterAutoRedirectOutputMark(mark uint32) error
	AutoRedirectOutputMark() uint32
	AutoRedirectOutputMarkFunc() control.Func
	RegisterBridgeInterface(interfaceName string)
	BridgeInterfaces() []string
	NetworkMonitor() tun.NetworkUpdateMonitor
	InterfaceMonitor() tun.DefaultInterfaceMonitor
	PackageManager() tun.PackageManager
	NeedWIFIState() bool
	WIFIState() WIFIState
	UpdateWIFIState(ctx context.Context)
	ResetNetwork(ctx context.Context)
	// ReleaseMemory is the aggressive pass: it resets the network and releases everything reusable.
	// It is the right answer to a genuine OOM and the wrong answer to memory that is merely
	// elevated, because rebuilding costs handshakes the user waits on.
	ReleaseMemory(ctx context.Context)
	// TrimMemory is the progressive pass: release what is reusable and cheap to lose, without a
	// network reset, without touching a resource that is carrying traffic, and without causing a
	// dial. It must only ever make the process smaller.
	TrimMemory(ctx context.Context)
	FlushSystemDNSCache()
}

// NetworkResetCounter is implemented by a network manager that can report how many network resets
// have run.
//
// # Why this is a counter and not a timestamp
//
// A pre-reset network operation - a dial, a listen - can succeed after the network has changed. The
// only way to tell whether the connection it produced still belongs to the current network is to
// compare the epoch it STARTED in against the epoch that is current when ownership is handed over.
// A timestamp cannot do that: two resets in quick succession, or a reset that happens to fall in the
// same clock tick, would be indistinguishable.
//
// # Why it is optional
//
// It is a capability rather than a method on NetworkManager. Every implementation and every test mock
// would otherwise have to grow a counter it does not care about, and a manager that cannot report an
// epoch is one where the ownership check simply does not apply - which is the correct degradation,
// not a compile error.
type NetworkResetCounter interface {
	// NetworkResetGeneration returns a value that increases every time a network reset BEGINS.
	//
	// It is advanced as the reset's first statement, not on completion, so that a network operation
	// running while a reset starts is already stale rather than being compared against the epoch it
	// was started under. It is therefore a reset epoch, not a count of finished resets.
	NetworkResetGeneration() uint64
}

// NetworkTransitionState reports whether the network is in the middle of a transition.
//
// # Why an epoch alone is not enough
//
// A monotonic epoch distinguishes "before" from "after". It cannot describe the state in between,
// and that state is not the same as either. While a transition is pending - the new environment is
// published, the ownership epoch has advanced, but the reset body has not yet run - the DNS
// generation has not moved and the transport pins still name the old network. An operation started
// there captures the NEW epoch, so an epoch comparison says it is current, while everything it can
// observe about the network still belongs to the old one. Handing it over is how the new network's
// answer ends up filed under the old network's namespace.
//
// The three states an operation must be able to tell apart are therefore:
//
//	stable A        the last transition committed; operations proceed normally
//	transitioning   a transition is pending; operations must not be accepted as stable results
//	stable B        the transition committed; operations proceed normally
//
// # Why it is a separate capability
//
// Like NetworkResetCounter, this is optional: a manager that cannot report it is one where the
// distinction does not arise, and every test mock would otherwise have to grow state it has no use
// for. A manager implementing only NetworkResetCounter keeps its previous behaviour exactly.
type NetworkTransitionState interface {
	// NetworkTransitionStable reports whether the network is currently in a settled state.
	//
	// It returns false from the moment a transition claims ownership until the reset body has
	// completed, and true otherwise.
	NetworkTransitionStable() bool
}

// NetworkTransitionSnapshotter reports the transition epoch and the settled state as ONE observation.
//
// # Why reading them separately is not equivalent
//
// A consumer needs both: the epoch to reject an operation that began before a transition, and the
// settled state to reject one that began during. Reading them as two calls lets the pair tear:
//
//	startedStable = true      <- read first
//	                          <- a transition begins here
//	capturedEpoch = R2        <- reads the NEW epoch
//
// The consumer then records "started settled, epoch R2", which is the state of an operation begun
// AFTER the transition - so an operation that actually began during the DURING window is recorded as
// a valid stable one, and the commit that follows accepts it. That is the unsafe direction.
//
// The manager can produce both values inside its own ownership critical section, so the pair is
// always a state the network really passed through. A manager that does not implement this keeps the
// two-call behaviour, which is no worse than before.
type NetworkTransitionSnapshotter interface {
	// NetworkTransitionSnapshot returns the current transition epoch and whether the network is
	// settled, as a single consistent observation.
	NetworkTransitionSnapshot() (epoch uint64, stable bool)
}

// NetworkFamilyCapability is what the local dial path can do with each address family on the
// network the manager is currently on.
//
// # What it is not
//
// It is not a claim that the public Internet is reachable over that family, and it is not a place to
// put one. "This network hands out an IPv6 address and a default route but blackholes public IPv6"
// is a real and common network - a campus, a hospital, a hotel portal, a half-configured CPE - and
// this value deliberately reports that network as Provisioned, because that is exactly what has been
// established. Learning the rest from real traffic is the scheduler's job, per dialer and per
// network environment; see common/dialer's family health.
//
// # Why it carries no settled flag of its own
//
// The settled state is a property of the INSTANT a reader looked, so it belongs to the observation
// rather than to the value. A flag stored beside the capability would report the state as of
// whenever the capability was last COMPUTED, and those are different instants: the capability is
// recomputed when the interfaces change and published before the transition that may accompany that
// change is claimed. A stored flag is therefore stale in exactly the window where it would be
// consulted - a reader that saw "settled" from it while a transition was being claimed would act on
// a verdict from a network the device is leaving.
// NetworkFamilyCapabilitySnapshot returns the settled state under the ownership lock instead, so the
// pair cannot disagree.
//
// # Why a consumer must treat the zero value as "no opinion"
//
// A manager that cannot report the capability keeps the previous behaviour exactly. The value is
// therefore read through FamilyCapability's own accessors, which answer "no restriction" for a zero
// value, rather than by comparing states at the call site.
type NetworkFamilyCapability struct {
	// Capability is the structural verdict for each family.
	Capability netcap.FamilyCapability
}

// NetworkFamilyCapabilitySnapshotter reports the transition epoch, the settled state and the local
// family capability as ONE observation.
//
// # Why this is one call and not three getters
//
// A consumer has to decide two things before it may use the capability:
//
//	"is the epoch I captured still the current one?"    rejects an operation that began BEFORE a
//	                                                    transition
//	"is the network settled?"                           rejects one that began DURING one
//
// and it must only trust the capability it read in the same instant. Read as separate calls the
// triple tears in the unsafe direction in both halves:
//
//	capability = X      <- read first, for the network being left
//	                    <- a transition begins, publishes capability Y and then commits
//	epoch, stable       <- reads the NEW epoch and "settled"
//
// The consumer then records a capability of X as belonging to the new generation, and X is the old
// network's answer - which is how a Wi-Fi verdict ends up deciding a cellular dial. Producing all
// three inside the manager's own ownership critical section makes the triple a state the network
// really passed through.
//
// This extends NetworkTransitionSnapshotter rather than replacing it: the epoch-and-stability pair
// is unchanged, so a manager that already reports it keeps working, and a consumer that only needs
// the pair needs no change.
type NetworkFamilyCapabilitySnapshotter interface {
	NetworkTransitionSnapshotter
	// NetworkFamilyCapabilitySnapshot returns the transition epoch, whether the network is settled,
	// and the local family capability, as a single consistent observation.
	NetworkFamilyCapabilitySnapshot() (epoch uint64, stable bool, capability NetworkFamilyCapability)
}

// IPv6EntryPresentmentReader reports what the application-facing entries present about IPv6.
//
// # What this is, and what it is not
//
// It is the ENTRY question: "should this process tell applications that IPv6 works?" It is not the
// dial question, which stays `netcap.HardUndialable` per destination. The two are answered from the
// same observed facts, but they are consumed at different places and one is much coarser than the
// other: an attempt is judged per destination and per socket, while an entry is judged once for
// everything the application can see.
//
// # Why it is a latch rather than a live projection
//
// The decision a READER needs is the decision the entries are actually applying, not the one the
// network currently justifies. Those differ for exactly one reason: the TUN's addressing is fixed
// when the interface is opened, and no platform in this tree offers a cheap in-place update of it.
// A reader that recomputed the decision from live facts would therefore approve AAAA answers for
// an IPv6 the TUN is not carrying - the split where DNS says the family is back and the interface
// still has no address for it. The reader returns the value the entry layer committed, so the two
// halves of the pipeline cannot disagree.
//
// A manager that does not implement this keeps its previous behaviour exactly: nothing is
// suppressed, and no entry has to know whether a manager is present.
type IPv6EntryPresentmentReader interface {
	// IPv6EntryPresentment returns the presentment this box committed, or netcap.PresentIPv6 when
	// nothing was committed.
	IPv6EntryPresentment() netcap.IPv6Presentment
}

type NetworkOptions struct {
	BindInterface        string
	RoutingMark          uint32
	DomainResolver       string
	DomainResolveOptions DNSQueryOptions
	NetworkStrategy      *C.NetworkStrategy
	NetworkType          []C.InterfaceType
	FallbackNetworkType  []C.InterfaceType
	FallbackDelay        time.Duration
}

type InterfaceUpdateListener interface {
	InterfaceUpdated(ctx context.Context)
}

type SystemDNSCacheFlusher interface {
	FlushSystemDNSCache()
}

type WIFIState struct {
	SSID  string
	BSSID string
}

func NormalizeWIFIBSSID(bssid string) string {
	bssid = strings.TrimSpace(bssid)
	if bssid == "" {
		return ""
	}
	parsed, err := net.ParseMAC(bssid)
	if err == nil && len(parsed) == 6 {
		return parsed.String()
	}
	if len(bssid) == 12 {
		decoded, err := hex.DecodeString(bssid)
		if err == nil {
			return net.HardwareAddr(decoded).String()
		}
	}
	return bssid
}

type NetworkInterface struct {
	control.Interface
	Type             C.InterfaceType
	DNSServers       []string
	DNSSearchDomains []string
	Gateways         []netip.Addr
	Expensive        bool
	Constrained      bool
}
