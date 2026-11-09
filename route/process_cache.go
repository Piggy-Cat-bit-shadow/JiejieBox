package route

import (
	"context"
	"net/netip"
	"slices"
	"strings"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/process"
)

type processCacheKey struct {
	Network     string
	Source      netip.AddrPort
	Destination netip.AddrPort
	// Paths separates the two answers one connection can have.
	//
	// The owner-only answer (uid, from the socket table) and the full answer (uid plus the /proc
	// walk's paths and the package lookup) describe the same tuple, but they are different results.
	// Sharing one key would let a connection that reaches a path rule receive the cached owner-only
	// entry - the rule would then be evaluated against a record that was never asked for paths - or
	// make a connection that needs only the uid pay for a walk it never used.
	Paths bool
}

type processCacheEntry struct {
	result *adapter.ConnectionOwner
	err    error
}

func (r *Router) findProcessInfoCached(ctx context.Context, network string, source netip.AddrPort, destination netip.AddrPort, paths bool) (*adapter.ConnectionOwner, error) {
	key := processCacheKey{
		Network:     network,
		Source:      source,
		Destination: destination,
		Paths:       paths,
	}
	if entry, ok := r.processCache.Get(key); ok {
		return entry.result, entry.err
	}
	// The generation is read BEFORE the lookup: a reset that lands while it runs invalidates the
	// answer, and caching it would preserve the pre-reset fact for the cache lifetime.
	epoch := r.processCacheEpoch.Load()
	var (
		result *adapter.ConnectionOwner
		err    error
	)
	if paths {
		result, err = process.FindProcessInfo(r.processSearcher, ctx, network, source, destination)
	} else {
		// Every process rule matches user/user_id alone, so the uid from the
		// socket diag table is enough; do not pay for a /proc walk.
		result, err = process.FindProcessOwner(r.processSearcher, ctx, network, source, destination)
	}
	if r.processCacheEpoch.Load() == epoch {
		r.processCache.Add(key, processCacheEntry{result: result, err: err})
	}
	return result, err
}

// processTruth is what the rule walk actually proved about the process metadata of THIS flow.
//
// # Why the flow has to state it
//
// "ProcessInfo is not nil" does not mean the process was found. The Linux lookup returns an
// owner-only record ({UserId}, no paths, no error) whenever the /proc scan is restricted - that is
// what searcher_linux.go does when it logs the walk's error and keeps the uid. A path rule evaluated
// against that record does not match, and reading that as "the path rule does not apply" is how a
// flow reaches a direct outbound through a rule that was never actually evaluated.
//
// The walk is the only place that knows a path-reading rule WAS reached, so it carries that fact
// here. A flow decided before any such rule has nothing unknown: the rule could not have changed
// the decision.
type processTruth struct {
	// pathsUnknown: a rule reading the process path was evaluated and the record has no path.
	pathsUnknown bool
	// packagesUnknown: a rule reading a package name was evaluated and the record has no package.
	packagesUnknown bool
}

// processMetadataIsProven reports whether the process metadata needed to judge a bypass was
// actually obtained.
//
// The distinction it preserves is between "no process rule can apply" and "we could not find out".
// PreMatch walks the whole rule set, so a process rule is evaluated against metadata.ProcessInfo;
// when it is nil the rule simply does not match. A flow that then reaches the default direct
// outbound would take an irreversible native bypass decided by the ABSENCE of metadata.
//
// Three cases genuinely have nothing to prove:
//   - the walk reached no rule that reads a process field (truth is zero)
//   - no searcher is configured, so no process rule can exist for this platform
//   - the source is not local, so the lookup was never applicable
//
// Otherwise an owner-only record is unknown, not proof: the field the rule reads was never
// obtained, and an unknown answer must fail closed.
func (r *Router) processMetadataIsProven(metadata *adapter.InboundContext, truth processTruth) bool {
	if truth.pathsUnknown || truth.packagesUnknown {
		return false
	}
	if r.processSearcher == nil {
		return true
	}
	if metadata.ProcessInfo != nil {
		return true
	}
	return !r.isLocalSource(metadata.Source.Addr)
}

// processTruthFor turns the accumulated need of the rules the walk reached into the truth that
// decision may rely on.
//
// A need only counts when the lookup could have answered it at all: a non-local source and a build
// with no searcher have no process to find, and the old behaviour (nothing to prove) is correct
// there.
func (r *Router) processTruthFor(metadata *adapter.InboundContext, reached processNeed) processTruth {
	if reached == processNeedNone || r.processSearcher == nil || !r.isLocalSource(metadata.Source.Addr) {
		return processTruth{}
	}
	// matchRule's owner-only answer says the uid is known and nothing else. If the walk never
	// completed it (because no path-reading rule was reached) the reach mask is zero and this is
	// not unknown - which is the whole point of tracking the reach mask instead of the config.
	if metadata.ProcessInfo == nil {
		return processTruth{}
	}
	return processTruth{
		pathsUnknown:    reached&processNeedPath != 0 && len(metadata.ProcessInfo.ProcessPaths) == 0,
		packagesUnknown: reached&processNeedPackage != 0 && len(metadata.ProcessInfo.PackageNames) == 0,
	}
}

// processNeedAt reports which process facts the rule at this index may read. A router built directly
// in a test may carry only ruleNeedsPaths; that is read as a path need, the conservative reading.
func (r *Router) processNeedAt(ruleIndex int) processNeed {
	if ruleIndex < 0 {
		return processNeedNone
	}
	if ruleIndex < len(r.ruleProcessNeeds) {
		return r.ruleProcessNeeds[ruleIndex]
	}
	if ruleIndex < len(r.ruleNeedsPaths) && r.ruleNeedsPaths[ruleIndex] {
		return processNeedPath
	}
	return processNeedNone
}

// processCacheQuery derives the tuple both process lookups share.
//
// The ORIGIN destination is preferred: when a destination was rewritten (a FakeIP placeholder
// mapped back to a domain, a sniffed name replacing the address), the socket the kernel knows is
// still the one the client opened, so the lookup must name that tuple. Without it the walk and the
// socket table are asked about a connection that never existed.
func processCacheQuery(metadata *adapter.InboundContext) (network string, source netip.AddrPort, destination netip.AddrPort) {
	if metadata.OriginDestination.IsValid() {
		destination = metadata.OriginDestination.AddrPort()
	} else if metadata.Destination.IsIP() {
		destination = metadata.Destination.AddrPort()
	}
	return metadata.Network, metadata.Source.AddrPort(), destination
}

func (r *Router) searchProcessInfo(ctx context.Context, metadata *adapter.InboundContext) {
	if r.processSearcher == nil || metadata.ProcessInfo != nil || !r.isLocalSource(metadata.Source.Addr) {
		return
	}
	network, source, originDestination := processCacheQuery(metadata)
	processInfo, err := r.findProcessInfoCached(ctx, network, source, originDestination, r.needProcessPaths)
	if err != nil {
		r.logger.InfoContext(ctx, "failed to search process: ", err)
		return
	}
	r.recordProcessInfo(ctx, metadata, processInfo)
}

// ensureProcessPaths completes the owner-only answer of THIS connection into the full one, once.
//
// It is the on-demand half of the process lookup. The owner (the uid) comes from the socket table
// and is enough for a user/user_id rule; the path and the package name need a /proc walk, which is
// the expensive part. A configuration whose path rules sit behind ordinary domain or address rules
// therefore pays for the walk only for the connections that actually reach one of them.
//
// A failed completion CLEARS the owner-only record rather than keeping it:
// processMetadataIsProven treats a non-nil record as proof that the metadata was obtained, and a
// path rule that could not be evaluated must not be read as "does not apply" by the bypass
// decision that follows.
func (r *Router) ensureProcessPaths(ctx context.Context, metadata *adapter.InboundContext) {
	if r.processSearcher == nil || !r.isLocalSource(metadata.Source.Addr) {
		return
	}
	network, source, destination := processCacheQuery(metadata)
	processInfo, err := r.findProcessInfoCached(ctx, network, source, destination, true)
	if err != nil {
		r.logger.InfoContext(ctx, "failed to complete the process path: ", err)
		metadata.ProcessInfo = nil
		return
	}
	r.recordProcessInfo(ctx, metadata, processInfo)
}

// recordProcessInfo publishes a resolved owner on the metadata and reports what was found.
func (r *Router) recordProcessInfo(ctx context.Context, metadata *adapter.InboundContext, processInfo *adapter.ConnectionOwner) {
	metadata.ProcessInfo = processInfo
	if len(processInfo.ProcessPaths) > 0 {
		processPath := strings.Join(processInfo.ProcessPaths, ", ")
		if processInfo.UserName != "" {
			r.logger.InfoContext(ctx, "found process path: ", processPath, ", user: ", processInfo.UserName)
		} else if processInfo.UserId != -1 {
			r.logger.InfoContext(ctx, "found process path: ", processPath, ", user id: ", processInfo.UserId)
		} else {
			r.logger.InfoContext(ctx, "found process path: ", processPath)
		}
		return
	}
	if len(processInfo.PackageNames) > 0 {
		r.logger.InfoContext(ctx, "found package name: ", strings.Join(processInfo.PackageNames, ", "))
		return
	}
	if processInfo.UserId != -1 {
		if processInfo.UserName != "" {
			r.logger.InfoContext(ctx, "found user: ", processInfo.UserName)
		} else {
			r.logger.InfoContext(ctx, "found user id: ", processInfo.UserId)
		}
	}
}

func (r *Router) isLocalSource(source netip.Addr) bool {
	if source.IsLoopback() {
		return true
	}
	if r.platformInterface != nil {
		if slices.Contains(r.platformInterface.MyInterfaceAddress(), source) {
			return true
		}
	}
	for _, netInterface := range r.network.InterfaceFinder().Interfaces() {
		for _, prefix := range netInterface.Addresses {
			if prefix.Addr() == source {
				return true
			}
		}
	}
	return false
}
