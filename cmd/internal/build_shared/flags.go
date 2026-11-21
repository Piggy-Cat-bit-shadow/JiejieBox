package build_shared

import "strings"

// godebugDefaults is the -X runtime.godebugDefault value every binary built through this package must
// carry, byte-identical to the one in release/LDFLAGS (flags_test.go asserts that).
//
// # Why the list must be complete
//
// `-X runtime.godebugDefault=...` REPLACES the string the go command computes from go.mod; it does not
// merge into it. Every setting the list omits therefore falls back to the TOOLCHAIN's default rather
// than to the language version's. This list used to be only `multipathtcp=0,tlssha1=1`, which dropped
// go.mod's `godebug tlssecpmlkem=0` from every libbox (Apple/Android library) and boxdd (desktop
// daemon) build while cmd/sing-box - built from release/LDFLAGS - kept it: the two shipped paths sent
// different TLS ClientHellos. Measured on Go 1.27.4 with scripts/ci/clienthello-probe:
//
//	go 1.26.0 + godebug tlssecpmlkem=0, no -X ....... groups [4588 29 23 24 25]  (the camouflage set)
//	with the old list .............................. groups [4588 4587 4589 29 23 24 25]
//	with this list ................................. groups [4588 29 23 24 25]
//
// The camouflage this product ships is the pre-1.26 key-share set, so the extra post-quantum groups
// are a fingerprint change, not a preference.
//
// Every workflow that compiles an artifact through this package must also list this file's directory
// in its push path filter (android-core-arm64.yml, mobile-contract.yml, windows-boxdd-tests.yml):
// editing only the flags is a change to the shipped wire, and a filter that does not match it lets
// that change ship with no artifact built from it.
const godebugDefaults = "multipathtcp=0,tlssha1=1,tlssecpmlkem=0,tracebacklabels=0,x509sslcertoverrideplatform=0"

func LinkerFlags(version string, debug bool) string {
	return linkerFlags(version, debug, godebugDefaults)
}

// LinkerFlagsWithStartupPolicy is LinkerFlags with a startup policy appended to the godebug default.
//
// It exists so the Apple library build can opt into a runtime startup candidate (see
// startup_policy.go) WITHOUT changing what every other product links: an Android AAR, the Windows
// daemon and `cmd/sing-box` keep calling LinkerFlags and keep their bytes. The inherit policy is
// byte-identical to LinkerFlags, which TestStartupPolicyLinkStrings asserts.
//
// The composed value still starts with the compatibility list above, because
// `-X runtime.godebugDefault=` REPLACES go.mod's computed default; a policy build that dropped
// `tlssecpmlkem=0`, say, would send a different ClientHello on every Apple slice.
func LinkerFlagsWithStartupPolicy(version string, debug bool, policy StartupPolicy) string {
	return linkerFlags(version, debug, policy.GODEBUGDefaults())
}

func linkerFlags(version string, debug bool, godebug string) string {
	flags := []string{
		"-X github.com/sagernet/sing-box/constant.Version=" + version,
		"-X runtime.godebugDefault=" + godebug,
		"-checklinkname=0",
	}
	if !debug {
		flags = append(flags, "-s", "-w", "-buildid=")
	}
	return strings.Join(flags, " ")
}
