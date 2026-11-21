package build_shared

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The two runtime startup settings a build through this package may link on top of the
// compatibility defaults in flags.go.
//
// Neither is a registered godebug (internal/godebugs/table.go). runtime.parseRuntimeDebugVars
// parses them directly, so go.mod can never pin them and `go version -m`'s DefaultGODEBUG cannot
// show them. The only ways to make one the STARTUP value are:
//
//   - `-X runtime.godebugDefault=...` in the link, which is what this file composes; or
//   - the GODEBUG environment variable, which WINS, because parseRuntimeDebugVars applies the
//     linked default first and the environment second.
//
// Neither can be changed later. runtime.reparsedebugvars explicitly skips `memprofilerate`, and
// the adaptive starting stack size is a product of the GC's scan statistics, so no Setup/init code
// can reproduce either effect after the first sampled allocation.
const (
	// MemProfileRateSetting is runtime's allocation sampling rate: one sampled allocation per N
	// bytes on average. 0 disables heap/allocs sampling entirely; 512*1024 is runtime's default.
	MemProfileRateSetting = "memprofilerate"
	// AdaptiveStackStartSetting is runtime's adaptive starting stack size. 1 (runtime's default)
	// sets the starting size for a reused goroutine from the average stack size the GC scanned;
	// 0 keeps it at the platform's fixedStack.
	AdaptiveStackStartSetting = "adaptivestackstart"
)

// RuntimeMemProfileRateDefault is runtime/mprof.go's `var MemProfileRate int = 512 * 1024`.
//
// It is a constant here only so a test can say "inherit means this number" without restating the
// literal in three places; the policy never writes it unless a candidate explicitly asks for it.
const RuntimeMemProfileRateDefault = 512 * 1024

// StartupPolicy is an auditable description of the runtime startup defaults ONE build links.
//
// The zero value changes nothing: every setting is left out of the linked default, so the
// toolchain/runtime default applies. That is deliberate, and it is why every Apple build keeps its
// previous bytes until someone asks for a candidate here.
//
// Scope is the CALLER's decision, not this type's. `-X runtime.godebugDefault=` is a link-time
// constant of one Go binary, and in a libbox the process is shared by the main app and the network
// extension, so this is a PROCESS-level startup default: it is not per-Box state and cannot be
// varied per configuration. Applying it to an Android or desktop build is a different product
// decision; those builds keep calling LinkerFlags.
type StartupPolicy struct {
	memProfileRate     startupSetting
	adaptiveStackStart startupSetting
}

// startupSetting is one numeric GODEBUG value. The zero value is "inherit" (not written), which
// is what makes StartupPolicy's zero value the no-op policy.
type startupSetting struct {
	value int64
	set   bool
}

// StartupPolicyInherit returns the policy that changes nothing.
func StartupPolicyInherit() StartupPolicy {
	return StartupPolicy{}
}

// IsZero reports whether the policy writes no setting at all.
func (p StartupPolicy) IsZero() bool {
	return !p.memProfileRate.set && !p.adaptiveStackStart.set
}

// Entries returns the policy's GODEBUG entries in canonical order, skipping inherited settings.
// The order is fixed so the same policy always composes the same link string. A policy that
// inherits everything returns nil.
func (p StartupPolicy) Entries() []string {
	var entries []string
	if p.memProfileRate.set {
		entries = append(entries, MemProfileRateSetting+"="+strconv.FormatInt(p.memProfileRate.value, 10))
	}
	if p.adaptiveStackStart.set {
		entries = append(entries, AdaptiveStackStartSetting+"="+strconv.FormatInt(p.adaptiveStackStart.value, 10))
	}
	return entries
}

// String is the policy in the vocabulary ParseStartupPolicy accepts, with inherited settings made
// explicit. It is what a provenance record should carry, because a missing "memprofilerate" key in
// an artifact would otherwise be indistinguishable from a build that predates this policy.
func (p StartupPolicy) String() string {
	memProfileRate := "inherit"
	if p.memProfileRate.set {
		memProfileRate = strconv.FormatInt(p.memProfileRate.value, 10)
	}
	adaptiveStackStart := "inherit"
	if p.adaptiveStackStart.set {
		adaptiveStackStart = strconv.FormatInt(p.adaptiveStackStart.value, 10)
	}
	return MemProfileRateSetting + "=" + memProfileRate + "," + AdaptiveStackStartSetting + "=" + adaptiveStackStart
}

// WithMemProfileRate returns a copy of p with the allocation sampling rate replaced. Accepted
// values are "inherit" (write nothing), "off" or "0" (write 0), or a positive byte rate.
func (p StartupPolicy) WithMemProfileRate(value string) (StartupPolicy, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "inherit":
		p.memProfileRate = startupSetting{}
		return p, nil
	case "off":
		p.memProfileRate = startupSetting{value: 0, set: true}
		return p, nil
	}
	rate, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return StartupPolicy{}, fmt.Errorf("unknown %s value %q: want inherit, off, or a byte rate", MemProfileRateSetting, value)
	}
	if rate < 0 {
		return StartupPolicy{}, fmt.Errorf("unknown %s value %q: a rate cannot be negative", MemProfileRateSetting, value)
	}
	p.memProfileRate = startupSetting{value: rate, set: true}
	return p, nil
}

// WithAdaptiveStackStart returns a copy of p with the adaptive starting stack size replaced.
// Accepted values are "inherit" (write nothing), "on"/"1" and "off"/"0".
func (p StartupPolicy) WithAdaptiveStackStart(value string) (StartupPolicy, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "inherit":
		p.adaptiveStackStart = startupSetting{}
		return p, nil
	case "on", "1":
		p.adaptiveStackStart = startupSetting{value: 1, set: true}
		return p, nil
	case "off", "0":
		p.adaptiveStackStart = startupSetting{value: 0, set: true}
		return p, nil
	}
	return StartupPolicy{}, fmt.Errorf("unknown %s value %q: want inherit, on, or off", AdaptiveStackStartSetting, value)
}

// ParseStartupPolicy parses the comma-separated vocabulary a flag or environment variable carries,
// for example "memprofilerate=off,adaptivestackstart=off". An empty string is the inherit policy,
// spaces around fields are ignored, and an unknown key, a duplicate key or an out-of-range value is
// an error rather than a silently dropped setting: a typo here would otherwise ship a build that
// claims to have a policy it does not have.
func ParseStartupPolicy(text string) (StartupPolicy, error) {
	// "inherit" alone is the whole-document shorthand for "leave both settings to the runtime".
	// It exists because the reviewed default now turns both off, so the common way to undo it is to
	// ask for the inherited behaviour rather than to name every key.
	if strings.TrimSpace(strings.ToLower(text)) == "inherit" {
		return StartupPolicyInherit(), nil
	}
	var policy StartupPolicy
	seen := make(map[string]bool, 2)
	for _, field := range strings.Split(text, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key, value, found := strings.Cut(field, "=")
		if !found {
			return StartupPolicy{}, fmt.Errorf("malformed startup policy field %q: want key=value", field)
		}
		key = strings.TrimSpace(strings.ToLower(key))
		value = strings.TrimSpace(value)
		if seen[key] {
			return StartupPolicy{}, fmt.Errorf("startup policy sets %s more than once", key)
		}
		var err error
		switch key {
		case MemProfileRateSetting:
			policy, err = policy.WithMemProfileRate(value)
		case AdaptiveStackStartSetting:
			policy, err = policy.WithAdaptiveStackStart(value)
		default:
			return StartupPolicy{}, fmt.Errorf("unknown startup policy setting %q: want %s or %s (the TLS compatibility defaults are not a policy input; this package always links them)",
				key, MemProfileRateSetting, AdaptiveStackStartSetting)
		}
		if err != nil {
			return StartupPolicy{}, err
		}
		seen[key] = true
	}
	return policy, nil
}

// StartupPolicyEnvironmentVariable names the environment variable an Apple build can use to select
// a policy without a flag, mirroring JJ_RUNTIME_OVERLAY. It is a BUILD input read by the builder,
// not something the built binary reads at runtime.
const StartupPolicyEnvironmentVariable = "JJ_APPLE_STARTUP_POLICY"

// DefaultStartupPolicy is the policy an Apple build links when nothing overrides it.
//
// # The product decision, and exactly what it costs
//
// BOTH settings are off by default: heap sampling is disabled and the adaptive starting stack size
// is disabled. The product owner took this on the measurements in reports/R-GM2.md, which is the
// right place to re-read before changing it back.
//
// The saving: the profiler's bucket hash table is 1 439 992 bytes, allocated by the FIRST sample and
// never released, so disabling the sampler avoids it entirely - measured here as
// /memory/classes/profiling/buckets:bytes going from ~1.45 MB to ~5.6 KB while live heap is
// unchanged. It is Go runtime metadata, not iOS phys_footprint, and it must not be reported as the
// latter.
//
// The cost, stated where the next reader will look:
//
//   - `memprofilerate=0` means the OOM report's `heap` and `allocs` profiles are ALWAYS EMPTY.
//     Sampling history cannot be recreated: turning the rate on later only collects from that
//     moment, so a post-mortem of an allocation made before the switch shows nothing. A diagnostic
//     build that needs those profiles links `memprofilerate=inherit` explicitly.
//   - `adaptivestackstart=0` keeps a goroutine's starting stack at the platform's fixed size instead
//     of the GC-derived average. Under the shipping runtime overlay the extra saving is ~160-256 KiB,
//     NOT the multi-megabyte figure a synthetic churn experiment suggests, because the overlay's
//     exit-stack backlog already reclaims most of it.
//
// Both settings can be restored per build without touching this file:
//
//	JJ_APPLE_STARTUP_POLICY=inherit                                  # both runtime defaults
//	JJ_APPLE_STARTUP_POLICY=memprofilerate=inherit,adaptivestackstart=inherit
func DefaultStartupPolicy() StartupPolicy {
	policy, err := ParseStartupPolicy("memprofilerate=off,adaptivestackstart=off")
	if err != nil {
		// This constant is owned by this file, so a failure means someone edited it into an invalid
		// value. Panicking during a build is the loud outcome; silently falling back to inherit would
		// ship a binary whose provenance claims a policy it does not have.
		panic("build_shared: the reviewed default startup policy is not parseable: " + err.Error())
	}
	return policy
}

// StartupPolicyFromEnvironment resolves the environment variable over the default. An empty value
// means "the reviewed default", not "inherit", so `JJ_APPLE_STARTUP_POLICY=` cannot silently
// disable a default that a later commit changes.
func StartupPolicyFromEnvironment() (StartupPolicy, error) {
	value := strings.TrimSpace(os.Getenv(StartupPolicyEnvironmentVariable))
	if value == "" {
		return DefaultStartupPolicy(), nil
	}
	policy, err := ParseStartupPolicy(value)
	if err != nil {
		return StartupPolicy{}, fmt.Errorf("resolve %s: %w", StartupPolicyEnvironmentVariable, err)
	}
	return policy, nil
}

// GODEBUGDefaults composes the COMPLETE value of `-X runtime.godebugDefault=` for this policy: the
// compatibility defaults from flags.go followed by the policy's entries.
//
// The composition is additive here, but the LINK is not: `-X runtime.godebugDefault=` replaces the
// default the go command computes from go.mod instead of merging into it. That is why the base list
// is a constant in one package and why the TLS fingerprint settings must survive every policy -
// TestStartupPolicyKeepsTheCompatibilityDefaults holds that, and the same test asserts the policy
// keys never collide with the base list.
func (p StartupPolicy) GODEBUGDefaults() string {
	entries := p.Entries()
	if len(entries) == 0 {
		return godebugDefaults
	}
	return godebugDefaults + "," + strings.Join(entries, ",")
}

// LinkedGODEBUGDefaultsArePresent reports whether a linked artifact's bytes contain the exact
// godebug default string this policy composes.
//
// "Exact" matters for the inherit policy: its value is the compatibility list, which is a PREFIX of
// every policy build's value. A plain substring search would therefore report an inherit artifact
// for a binary that actually links `...,memprofilerate=0`. The check rejects a match that is
// extended by another comma-separated setting, so it answers "is this the whole linked value", not
// "does this prefix occur".
//
// It is a byte check and nothing more: the string being present proves the build intended the
// default, not that the process RUNS with it. runtime.MemProfileRate is forced back to 0 by the
// linker when nothing consumes the heap profile (cmd/link/internal/ld linksetup sets
// runtime.disableMemoryProfiling and proc.go applies it after parsing the default), so a present
// string can coexist with MemProfileRate == 0. Read the value in a live process for that claim.
func (p StartupPolicy) LinkedGODEBUGDefaultsArePresent(artifact []byte) bool {
	value := []byte(p.GODEBUGDefaults())
	for offset := 0; offset < len(artifact); {
		index := bytes.Index(artifact[offset:], value)
		if index < 0 {
			return false
		}
		end := offset + index + len(value)
		if end >= len(artifact) || artifact[end] != ',' {
			return true
		}
		offset = end
	}
	return false
}
