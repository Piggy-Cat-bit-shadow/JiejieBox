// Command verify_runtime_overlay proves that a packed Libbox.xcframework was compiled with the
// Go runtime overlay its own provenance record says it was.
//
// # Why this is a separate program
//
// The builder verifies the framework it just produced, in the same process, with the plan still in
// hand. That check cannot survive the artifact leaving that process. This one runs against the
// INSTALLED framework - the bytes that are about to be packed - and takes the expected plan from
// libbox.provenance rather than from a flag, so it answers "do these bytes match what the build
// recorded" instead of "do the caller and the builder agree".
//
// It is deliberately tiny and imports only cmd/internal/runtimeoverlay: the CI step that calls it
// runs after an hour of gomobile work and must not recompile a dependency graph to ask one
// question.
//
// Usage:
//
//	verify_runtime_overlay -xcframework <dir> [-provenance <libbox.provenance>]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sagernet/sing-box/cmd/internal/runtimeoverlay"
)

func main() {
	xcframework := flag.String("xcframework", "", "Libbox.xcframework to verify")
	provenance := flag.String("provenance", "libbox.provenance", "the provenance record the build wrote")
	flag.Parse()
	if *xcframework == "" {
		fail("-xcframework is required")
	}
	content, err := os.ReadFile(*provenance)
	if err != nil {
		fail("read %s: %v. The artifact's own record of which runtime overlay it compiled is required, "+
			"because a verifier told the mode by its caller can only confirm that the caller and the builder agree", *provenance, err)
	}
	recorded, err := runtimeoverlay.ParseProvenanceOverlay(string(content))
	if err != nil {
		fail("%v", err)
	}
	wanted := recorded.ExpectedMarker()
	checks, err := runtimeoverlay.RequireVerifiedXCframework(*xcframework, wanted)
	if err != nil {
		fail("%v", err)
	}
	fmt.Printf("runtime overlay: mode %s (Go %s) verified in %d slice(s)\n", recorded.Mode, recorded.GoVersion, len(checks))
	for _, check := range checks {
		state := "no marker"
		if check.AnyMarker {
			state = "marker present"
		}
		family := "not iOS family"
		if check.IOSFamily {
			family = "iOS family"
		}
		fmt.Printf("  %-40s %-14s %s\n", check.Slice, state, family)
	}
	if wanted == "" {
		fmt.Println("runtime overlay: mode base expects no marker in any slice, and found none")
	}
}

func fail(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "verify_runtime_overlay: "+format+"\n", arguments...)
	os.Exit(1)
}
