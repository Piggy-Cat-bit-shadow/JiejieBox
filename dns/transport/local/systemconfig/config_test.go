//go:build linux

package systemconfig

import (
	"testing"
	"time"
)

// SystemdResolved is part of the environment identity: if it is left out of Equal, a change
// between the stub resolver and the plain system path is invisible and the local transport keeps
// using the previous source. This pins that the flag is compared.
func TestConfigEqualIncludesSystemdResolved(t *testing.T) {
	base := &Config{
		Servers:         nil,
		Search:          []string{"example.com"},
		Timeout:         time.Second,
		Attempts:        2,
		SystemdResolved: true,
	}
	other := *base
	if !base.Equal(&other) {
		t.Fatal("identical configs compared unequal")
	}
	other.SystemdResolved = false
	if base.Equal(&other) {
		t.Fatal("changing SystemdResolved compared equal; resolved detection would be ignored")
	}
}
