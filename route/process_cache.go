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
	r.processCache.Add(key, processCacheEntry{result: result, err: err})
	return result, err
}

// processMetadataIsProven reports whether the process metadata needed to judge a bypass was
// actually obtained.
//
// The distinction it preserves is between "no process rule can apply" and "we could not find out".
// PreMatch walks the whole rule set, so a process rule is evaluated against metadata.ProcessInfo;
// when it is nil the rule simply does not match. A flow that then reaches the default direct
// outbound would take an irreversible native bypass decided by the ABSENCE of metadata.
//
// Two cases genuinely have nothing to prove:
//   - no searcher is configured, so no process rule can exist for this platform
//   - the source is not local, so the lookup was never applicable
//
// Otherwise, ProcessInfo being nil after the search means the answer is unknown, and an unknown
// answer must fail closed.
func (r *Router) processMetadataIsProven(metadata *adapter.InboundContext) bool {
	if r.processSearcher == nil {
		return true
	}
	if metadata.ProcessInfo != nil {
		return true
	}
	return !r.isLocalSource(metadata.Source.Addr)
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
