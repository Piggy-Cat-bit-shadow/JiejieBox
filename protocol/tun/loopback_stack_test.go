package tun

import "testing"

// The switch injects the address on iOS only, and only the `go` stack answers for it. A profile
// that selects another stack would otherwise look enabled and do nothing; this is the one place
// the mismatch is reported, so its conditions are pinned rather than assumed.

func TestLoopbackStackWarningStaysQuietOnTheReflectingStack(t *testing.T) {
	t.Parallel()
	for _, stack := range []string{"", "go"} {
		if warning := loopbackStackWarning(stack, 1, true); warning != "" {
			t.Fatalf("stack %q warned: %s", stack, warning)
		}
	}
}

func TestLoopbackStackWarningReportsAStackThatCannotReflect(t *testing.T) {
	t.Parallel()
	for _, stack := range []string{"gvisor", "mixed", "system"} {
		if warning := loopbackStackWarning(stack, 1, true); warning == "" {
			t.Fatalf("stack %q did not warn", stack)
		}
	}
}

func TestLoopbackStackWarningIgnoresOtherPlatformsAndOtherConfigurations(t *testing.T) {
	t.Parallel()
	// Linux auto-redirect installs rules for these same addresses, so a warning there would be
	// wrong; and with no address configured there is nothing to report.
	if warning := loopbackStackWarning("gvisor", 1, false); warning != "" {
		t.Fatalf("a non-iOS build warned: %s", warning)
	}
	if warning := loopbackStackWarning("gvisor", 0, true); warning != "" {
		t.Fatalf("warning with no addresses: %s", warning)
	}
}
