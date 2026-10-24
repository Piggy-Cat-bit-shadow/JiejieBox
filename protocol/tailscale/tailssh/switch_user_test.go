//go:build with_tailscale && !windows

package tailssh

import (
	"os"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter"
)

// A non-root process must reject a switch-user request BEFORE it tries to exec the shell.
// Requesting a different uid is the privilege boundary; the error must name the missing
// privilege rather than surfacing later as an opaque exec failure.
func TestNonRootSwitchUserIsRejected(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the rejection path is for non-root processes; root has the capability")
	}
	err := verifyShellIdentity(nil, &adapter.PlatformUser{
		Username: "someone-else",
		Uid:      os.Getuid() + 1,
		Gid:      os.Getgid() + 1,
	})
	if err == nil {
		t.Fatal("a non-root process accepted a switch to another uid")
	}
	if !strings.Contains(err.Error(), "requires sing-box to run as root") {
		t.Fatalf("the rejection must name the missing privilege, got: %v", err)
	}
}

// The same uid/gid is not a switch: it must stay allowed without any privilege, because the
// common SSH-as-the-service-user case must not start requiring root.
func TestSameUserIsNotASwitch(t *testing.T) {
	err := verifyShellIdentity(nil, &adapter.PlatformUser{
		Username: "self",
		Uid:      os.Getuid(),
		Gid:      os.Getgid(),
	})
	if err != nil {
		t.Fatalf("the process's own uid/gid was rejected: %v", err)
	}
}
