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
const godebugDefaults = "multipathtcp=0,tlssha1=1,tlssecpmlkem=0,tracebacklabels=0,x509sslcertoverrideplatform=0"

func LinkerFlags(version string, debug bool) string {
	flags := []string{
		"-X github.com/sagernet/sing-box/constant.Version=" + version,
		"-X runtime.godebugDefault=" + godebugDefaults,
		"-checklinkname=0",
	}
	if !debug {
		flags = append(flags, "-s", "-w", "-buildid=")
	}
	return strings.Join(flags, " ")
}
