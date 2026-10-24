package config

import (
	"os"
	"strings"
	"testing"
)

// This fork deliberately keeps the packaged service unprivileged. Root is not required for TUN,
// routing or split DNS: CAP_NET_ADMIN/CAP_NET_RAW cover the device and routes, the sysusers
// account owns state, and the polkit rule grants only the resolved methods. Upstream's root
// switch would widen every deployment to fix a permission problem the unit already scopes.
func TestPackagedServiceKeepsLeastPrivilege(t *testing.T) {
	service, err := os.ReadFile("sing-box.service")
	if err != nil {
		t.Fatal(err)
	}
	text := string(service)
	for _, required := range []string{
		"User=sing-box",
		"CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_SYS_PTRACE CAP_DAC_READ_SEARCH",
		"AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE CAP_SYS_PTRACE CAP_DAC_READ_SEARCH",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("sing-box.service lost %q; the root switch must not remove the service sandbox", required)
		}
	}

	template, err := os.ReadFile("sing-box@.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), "User=sing-box") {
		t.Fatal("sing-box@.service lost User=sing-box")
	}

	sysusers, err := os.ReadFile("sing-box.sysusers")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sysusers), "u sing-box") {
		t.Fatal("the sysusers entry no longer creates the service account")
	}

	rules, err := os.ReadFile("sing-box.rules")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rules), "org.freedesktop.resolve1.set-dns-servers") {
		t.Fatal("the polkit rule no longer scopes resolved control to sing-box")
	}
}
