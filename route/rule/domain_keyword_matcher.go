package rule

import (
	"sort"
)

// domainKeywordMatcher is the compiled form of a large domain_keyword set: an Aho-Corasick
// automaton frozen into flat node/edge columns after construction.
//
// # Why it exists
//
// The current DomainKeywordItem loops over every keyword with strings.Contains. That is the right
// shape for a handful of keywords - the first one wins in tens of nanoseconds - but a large rule set
// pays O(host length x keywords) on every miss. The measured prototype (1024 keywords, miss) went
// from ~8us to ~0.12us with this structure; first-keyword hits stayed faster in the loop, which is
// why compilation is threshold-gated rather than unconditional.
//
// # Semantics it must not change
//
// The query host is lowercased exactly once, keywords are used byte-exact (no lowercasing), an empty
// host is not a match, and an empty keyword matches any non-empty host. Any boundary or failure
// falls back to the keyword loop; this type never makes a decision the loop would not.
type domainKeywordMatcher struct {
	nodes []domainKeywordNode
	edges []domainKeywordEdge
}

type domainKeywordNode struct {
	fail  uint32
	start uint32
	count uint16
	out   bool
}

type domainKeywordEdge struct {
	label byte
	to    uint32
}

type buildKeywordNode struct {
	next map[byte]int
	fail int
	out  bool
}

// Compile limits. They are deliberately conservative: a rule set past them keeps the loop rather
// than growing an unbounded index. The node cap is also the uint16 edge-count safety margin.
const (
	domainKeywordCompileThreshold = 64
	domainKeywordMaxPatterns      = 4096
	domainKeywordMaxBytes         = 1 << 20
	domainKeywordMaxNodes         = 1 << 16
)

// newDomainKeywordMatcher compiles patterns, or returns nil when the set should stay on the loop.
// The caller treats nil as "use the loop".
func newDomainKeywordMatcher(patterns []string) *domainKeywordMatcher {
	if len(patterns) < domainKeywordCompileThreshold || len(patterns) > domainKeywordMaxPatterns {
		return nil
	}
	var totalBytes int
	for _, pattern := range patterns {
		totalBytes += len(pattern)
		if totalBytes > domainKeywordMaxBytes {
			return nil
		}
	}

	build := []buildKeywordNode{{next: make(map[byte]int)}}
	for _, pattern := range patterns {
		at := 0
		for i := 0; i < len(pattern); i++ {
			label := pattern[i]
			child, loaded := build[at].next[label]
			if !loaded {
				if len(build)+1 > domainKeywordMaxNodes {
					return nil
				}
				child = len(build)
				build[at].next[label] = child
				build = append(build, buildKeywordNode{next: make(map[byte]int)})
			}
			at = child
		}
		build[at].out = true
	}

	// Failure links, breadth first. Root's children fail to the root.
	queue := make([]int, 0, len(build))
	for _, child := range build[0].next {
		queue = append(queue, child)
	}
	for head := 0; head < len(queue); head++ {
		at := queue[head]
		for label, child := range build[at].next {
			failure := build[at].fail
			for {
				if next, loaded := build[failure].next[label]; loaded {
					build[child].fail = next
					break
				}
				if failure == 0 {
					break
				}
				failure = build[failure].fail
			}
			build[child].out = build[child].out || build[build[child].fail].out
			queue = append(queue, child)
		}
	}

	matcher := &domainKeywordMatcher{
		nodes: make([]domainKeywordNode, len(build)),
		edges: make([]domainKeywordEdge, 0, len(build)-1),
	}
	for index, current := range build {
		matcher.nodes[index] = domainKeywordNode{
			fail:  uint32(current.fail),
			start: uint32(len(matcher.edges)),
			count: uint16(len(current.next)),
			out:   current.out,
		}
		labels := make([]int, 0, len(current.next))
		for label := range current.next {
			labels = append(labels, int(label))
		}
		sort.Ints(labels)
		for _, label := range labels {
			matcher.edges = append(matcher.edges, domainKeywordEdge{
				label: byte(label),
				to:    uint32(current.next[byte(label)]),
			})
		}
	}
	return matcher
}

// transition finds the child edge by binary search over the sorted edge column.
func (m *domainKeywordMatcher) transition(state uint32, label byte) (uint32, bool) {
	current := m.nodes[state]
	low := int(current.start)
	high := low + int(current.count)
	end := high
	for low < high {
		middle := low + (high-low)/2
		if m.edges[middle].label < label {
			low = middle + 1
		} else {
			high = middle
		}
	}
	if low < end && m.edges[low].label == label {
		return m.edges[low].to, true
	}
	return 0, false
}

// matchLower matches a host that the caller has ALREADY lowercased, preserving the loop's single
// lowering step and its empty-host rule.
func (m *domainKeywordMatcher) matchLower(host string) bool {
	if host == "" || len(m.nodes) == 0 {
		return false
	}
	if m.nodes[0].out {
		// An empty keyword matches every non-empty host, exactly like strings.Contains(host, "").
		return true
	}
	var state uint32
	for index := 0; index < len(host); index++ {
		for {
			if next, loaded := m.transition(state, host[index]); loaded {
				state = next
				break
			}
			if state == 0 {
				break
			}
			state = m.nodes[state].fail
		}
		if m.nodes[state].out {
			return true
		}
	}
	return false
}
