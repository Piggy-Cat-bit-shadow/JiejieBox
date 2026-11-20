package build_shared

// The two builders that ship a product - `build_libbox` (the Apple/Android library every client links)
// and `build_boxdd` (the desktop daemon) - take their ldflags from this package, while
// `cmd/sing-box` and the release recipes take them from `release/LDFLAGS`. Both strings carry a
// `-X runtime.godebugDefault=...`, and that form REPLACES the default the go command computes from
// go.mod instead of merging into it (§go.mod "the go line moved to 1.26"): every setting the list
// omits silently falls back to the TOOLCHAIN default.
//
// The setting this test exists for is `tlssecpmlkem`. go.mod pins it off because this product's TLS
// camouflage is the pre-1.26 key-share set; with the short list this package used to carry
// (`multipathtcp=0,tlssha1=1`), a libbox/boxdd build offered TWO EXTRA groups in every ClientHello
// (SecP256r1MLKEM768, SecP384r1MLKEM1024) while `cmd/sing-box` shipped from release/LDFLAGS did not.
// Measured with scripts/ci/clienthello-probe on Go 1.27.4:
//
//	go 1.26.0 + `godebug tlssecpmlkem=0`, no -X ............... groups [4588 29 23 24 25]
//	the same, with this package's old -X value ................ groups [4588 4587 4589 29 23 24 25]
//	the same, with release/LDFLAGS' -X value ................. groups [4588 29 23 24 25]
//
// So the two lists must stay identical, and this test is what makes that a property instead of a
// comment.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// godebugDefaultFrom extracts the value of `-X runtime.godebugDefault=...` from one ldflags string.
func godebugDefaultFrom(t *testing.T, flags string) string {
	t.Helper()
	const marker = "-X runtime.godebugDefault="
	index := strings.Index(flags, marker)
	require.GreaterOrEqual(t, index, 0, "no -X runtime.godebugDefault in %q", flags)
	value := flags[index+len(marker):]
	if end := strings.IndexAny(value, " \t\n"); end >= 0 {
		value = value[:end]
	}
	require.NotEmpty(t, value)
	return value
}

// TestLinkerFlagsCarryTheReleaseGODEBUGDefaults pins the two build paths together.
func TestLinkerFlagsCarryTheReleaseGODEBUGDefaults(t *testing.T) {
	releaseLDFLAGS, err := os.ReadFile(filepath.Join("..", "..", "..", "release", "LDFLAGS"))
	require.NoError(t, err, "release/LDFLAGS is the string cmd/sing-box ships with")
	want := godebugDefaultFrom(t, string(releaseLDFLAGS))

	for _, debug := range []bool{false, true} {
		got := godebugDefaultFrom(t, LinkerFlags("v1.0.0", debug))
		require.Equal(t, want, got,
			"build_shared and release/LDFLAGS disagree on runtime.godebugDefault (debug=%v); because -X replaces the go.mod-computed default, the shorter list changes the ClientHello every shipped libbox/boxdd build sends", debug)
		require.Contains(t, got, "tlssecpmlkem=0",
			"the shipped libraries must keep the pre-1.26 key-share set the product's camouflage is built for")
	}
}
