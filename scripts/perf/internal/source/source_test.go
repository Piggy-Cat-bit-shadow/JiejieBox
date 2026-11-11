package source

// Stage-3 infrastructure audit: the recorded pins must describe what was BUILT,
// not what go.mod's require block asked for. Every fork dependency in this
// repository is redirected by a replace directive, so a parser that skips replace
// lines records the upstream version the fork replaced - a value that never ran.

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGoMod(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go.mod")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestModulePinsRecordTheReplacementThatWasBuilt(t *testing.T) {
	path := writeGoMod(t, `module example.com/test

go 1.25.0

require (
	github.com/sagernet/sing-tun v0.9.7-0.20261109145802-79ed0653fdc6
	github.com/sagernet/sing-quic v0.7.2
)

replace github.com/sagernet/sing-tun => github.com/Piggy-Cat-bit-shadow/sing-tun v0.9.7-0.20261110222329-99abf3c27666
`)
	pins := modulePins(path)
	if got := pins["github.com/sagernet/sing-tun"]; got != "v0.9.7-0.20261110222329-99abf3c27666" {
		t.Fatalf("sing-tun pin = %q, want the replacement version that was built", got)
	}
	if got := pins["github.com/sagernet/sing-quic"]; got != "v0.7.2" {
		t.Fatalf("unreplaced module pin = %q, want v0.7.2", got)
	}
}

func TestModulePinsHandleAReplaceBlock(t *testing.T) {
	path := writeGoMod(t, `module example.com/test

go 1.25.0

require github.com/enfein/mieru v3.39.0

replace (
	github.com/enfein/mieru => github.com/Piggy-Cat-bit-shadow/mieru v3.40.0-fork.1
)
`)
	pins := modulePins(path)
	if got := pins["github.com/enfein/mieru"]; got != "v3.40.0-fork.1" {
		t.Fatalf("block-style replacement pin = %q, want v3.40.0-fork.1", got)
	}
}

func TestModulePinsHandleALocalReplacement(t *testing.T) {
	path := writeGoMod(t, `module example.com/test

go 1.25.0

require github.com/sagernet/sing-tun v0.9.7
replace github.com/sagernet/sing-tun => ../sing-tun
`)
	pins := modulePins(path)
	if got := pins["github.com/sagernet/sing-tun"]; got != "../sing-tun" {
		t.Fatalf("local replacement pin = %q, want ../sing-tun", got)
	}
}
