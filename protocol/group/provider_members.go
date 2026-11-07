package group

import (
	"maps"
	"regexp"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
)

// providerUpdateCheckScheduler collapses a burst of provider notifications into one membership
// rebuild, and never runs two rebuilds at once.
//
// # Why a scheduler rather than a plain goroutine per notification
//
// A provider fires its callbacks once per apply, but several providers may each be refreshing at
// the same time and one apply can touch a group that names all of them. Rebuilding on every
// notification would rebuild the same member list N times and could interleave two rebuilds of one
// group - and a rebuild publishes a member map, so interleaving means a reader can observe the
// older map winning. The scheduler runs one rebuild, notices the pending flag, and runs once more
// with the newest provider state.
type providerUpdateCheckScheduler struct {
	access  sync.Mutex
	running bool
	pending bool
}

func (c *providerUpdateCheckScheduler) Schedule(check func()) {
	c.access.Lock()
	c.pending = true
	if c.running {
		c.access.Unlock()
		return
	}
	c.running = true
	c.access.Unlock()
	go func() {
		for {
			c.access.Lock()
			c.pending = false
			c.access.Unlock()
			check()
			c.access.Lock()
			if !c.pending {
				c.running = false
				c.access.Unlock()
				return
			}
			c.access.Unlock()
		}
	}()
}

// collectProviderOutbounds rebuilds a group's member list: the declared static tags first, then the
// nodes of every provider the group names, filtered by include/exclude.
//
// updatedTag is the provider whose nodes just changed. Every other provider's contribution is taken
// from the cache, so a rebuild triggered by provider A does not have to re-read provider B (which
// may be mid-apply) and cannot miss B's nodes either. An empty result is returned as an empty list
// rather than replaced by a fallback outbound: a group with no members yet is a real state while a
// provider has not published anything, and selecting a node the user did not configure would be a
// worse answer than failing the dial.
func collectProviderOutbounds(
	updatedTag string,
	directTags []string,
	outboundManager adapter.OutboundManager,
	providers map[string]adapter.Provider,
	providerTags []string,
	outboundsCache map[string][]adapter.Outbound,
	exclude *regexp.Regexp,
	include *regexp.Regexp,
) ([]string, []adapter.Outbound, map[string][]adapter.Outbound, error) {
	if updatedTag != "" {
		if _, loaded := providers[updatedTag]; !loaded {
			return nil, nil, nil, E.New("outbound provider not found: ", updatedTag)
		}
	}
	newCache := make(map[string][]adapter.Outbound, len(outboundsCache))
	maps.Copy(newCache, outboundsCache)
	var (
		tags      = make([]string, 0, len(directTags))
		outbounds = make([]adapter.Outbound, 0, len(directTags))
	)
	for i, tag := range directTags {
		detour, loaded := outboundManager.Outbound(tag)
		if !loaded {
			return nil, nil, nil, E.New("outbound ", i, " not found: ", tag)
		}
		tags = append(tags, tag)
		outbounds = append(outbounds, detour)
	}
	for _, providerTag := range providerTags {
		if providerTag != updatedTag {
			if cachedOutbounds := newCache[providerTag]; cachedOutbounds != nil {
				for _, detour := range cachedOutbounds {
					tags = append(tags, detour.Tag())
				}
				outbounds = append(outbounds, cachedOutbounds...)
				continue
			}
		}
		provider := providers[providerTag]
		if provider == nil {
			return nil, nil, nil, E.New("outbound provider not found: ", providerTag)
		}
		cachedOutbounds := make([]adapter.Outbound, 0)
		for _, detour := range provider.Outbounds() {
			tag := detour.Tag()
			if exclude != nil && exclude.MatchString(tag) {
				continue
			}
			if include != nil && !include.MatchString(tag) {
				continue
			}
			tags = append(tags, tag)
			outbounds = append(outbounds, detour)
			cachedOutbounds = append(cachedOutbounds, detour)
		}
		newCache[providerTag] = cachedOutbounds
	}
	return tags, outbounds, newCache, nil
}
