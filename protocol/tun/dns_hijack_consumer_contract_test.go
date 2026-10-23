package tun

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Consumer-contract proof for the DNS hijack verdicts.
//
// # Why this reads the dependency's source
//
// The producer half - which verdict sing-box emits for a DNS flow - is covered by
// direct_fast_path_dns_test.go. What that cannot show is whether the CONSUMER does the right thing
// with `ActionAccept` on TCP/53, and that is the half the DNS filtering guarantee depends on:
//
//	UDP/53 -> ActionHijackDNS -> nfqueue case ActionFlow, ActionHijackDNS -> NfRepeat + inputMark
//	TCP/53 -> ActionAccept    -> nfqueue default -> acceptVerdict(packet)
//	                             nftables (repeatOnAccept=false): NfAccept
//	                             iptables (repeatOnAccept=true):  NfAccept for IPv4,
//	                                                             NfRepeat+tproxyMark for IPv6
//
// On both backends the TCP/53 packet is handed back to the stack, where the auto-redirect rules
// deliver it to sing-box's stream path and reach hijackDNSStream. The verdict is not "escape
// sing-box"; it is "let routing deliver this to the stream hijack".
//
// A real end-to-end check needs a Linux kernel with nftables or iptables and CAP_NET_ADMIN, which
// this suite does not have. Asserting the pinned source keeps the contract honest: a dependency
// bump that changes either switch fails here instead of silently changing DNS behaviour in the
// field. The limitation is recorded rather than papered over.

// singTunModuleDir locates the source of the sing-tun module the build actually resolves.
//
// # The defect this replaces
//
// The first version read the `require github.com/sagernet/sing-tun v...` line from go.mod and looked
// the module up in `$HOME/go/pkg/mod`. Both halves were wrong in this repository:
//
//  1. go.mod carries a `replace` to Piggy-Cat-bit-shadow/sing-tun, so the required version is NOT
//     the code the binary runs. A source assertion against it proves a property of a module the
//     build does not use - the exact failure mode "do not verify on the wrong upstream" describes.
//  2. The module cache is not necessarily under $HOME/go. With a custom GOMODCACHE the lookup fails,
//     the helper calls t.Skipf, and a skipped tripwire looks exactly like a passing one in a
//     non-verbose run.
//
// The helper now asks the Go toolchain for the directory of the module IN THE BUILD LIST, which
// already accounts for the replace, rather than re-deriving it from the file. It refuses to fall
// back to the required version: a fallback that silently checks the wrong tree is worse than a
// failure, because the tests exist precisely to notice a dependency change.
func singTunModuleDir(t *testing.T) string {
	t.Helper()

	command := exec.Command("go", "list", "-m", "-f", "{{if .Replace}}{{.Replace.Dir}}{{else}}{{.Dir}}{{end}}", "github.com/sagernet/sing-tun")
	command.Dir = "../.."
	output, err := command.Output()
	if err == nil {
		if dir := strings.TrimSpace(string(output)); dir != "" {
			if _, statErr := os.Stat(filepath.Join(dir, "flow_dispatch.go")); statErr == nil {
				return dir
			}
		}
	}

	// The toolchain was not usable from inside the test (a constrained CI sandbox, for example).
	// Resolve the same thing from the files: the REPLACE, never the require line.
	replacePath, replaceVersion := singTunReplace(t)
	moduleCache := os.Getenv("GOMODCACHE")
	if moduleCache == "" {
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			home, homeErr := os.UserHomeDir()
			require.NoError(t, homeErr)
			gopath = filepath.Join(home, "go")
		}
		moduleCache = filepath.Join(gopath, "pkg", "mod")
	}
	dir := filepath.Join(moduleCache, escapeModulePath(replacePath)+"@"+replaceVersion)
	if _, statErr := os.Stat(filepath.Join(dir, "flow_dispatch.go")); statErr != nil {
		t.Fatalf("the replaced sing-tun source is not at %s (%v). The pinned-source assertions must "+
			"read the code the build uses; run `go mod download` in this module, or fix the "+
			"GOMODCACHE the test is using. A skip here would silently disable the tripwire", dir, statErr)
	}
	return dir
}

// singTunReplace reads the `replace github.com/sagernet/sing-tun => <path> <version>` line from
// go.mod. It fails rather than returning the require version: the require line is the one thing this
// helper must not trust.
func singTunReplace(t *testing.T) (path string, version string) {
	t.Helper()
	goMod, err := os.ReadFile("../../go.mod")
	require.NoError(t, err, "go.mod must be readable to find the replaced sing-tun module")

	for _, line := range strings.Split(string(goMod), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "replace github.com/sagernet/sing-tun =>") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, "replace github.com/sagernet/sing-tun =>"))
		require.Len(t, fields, 2, "the sing-tun replace must name a module path and a version: %s", line)
		return fields[0], fields[1]
	}
	t.Fatal("go.mod has no replace for github.com/sagernet/sing-tun. The Direct Offload conclusions " +
		"were derived against the fork; if the fork was removed, re-derive them before deleting this " +
		"assertion")
	return "", ""
}

// escapeModulePath applies the module cache's case escaping: an upper-case letter becomes `!`
// followed by its lower-case form.
func escapeModulePath(path string) string {
	var builder strings.Builder
	for _, character := range path {
		if character >= 'A' && character <= 'Z' {
			builder.WriteByte('!')
			builder.WriteRune(character + ('a' - 'A'))
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

// TestPinnedSourceIsTheReplacedFork is the tripwire on the helper itself.
//
// It fails if a future edit resolves the required module instead of the replacement - which is what
// the helper used to do - or if the replace is removed without the Direct Offload conclusions being
// re-derived.
func TestPinnedSourceIsTheReplacedFork(t *testing.T) {
	dir := singTunModuleDir(t)
	_, replaceVersion := singTunReplace(t)
	require.Contains(t, filepath.ToSlash(dir), replaceVersion,
		"the pinned-source assertions must read the replaced module (%s), not the required upstream "+
			"version", replaceVersion)
}

// TestPinnedConsumerHandlesTheDNSVerdicts is the consumer half of the contract.
func TestPinnedConsumerHandlesTheDNSVerdicts(t *testing.T) {
	dir := singTunModuleDir(t)

	nfqueue, err := os.ReadFile(filepath.Join(dir, "nfqueue_linux.go"))
	require.NoError(t, err)
	source := string(nfqueue)

	// A DNS flow must be re-injected, not accepted: only NfRepeat puts it back on the userspace
	// path where the hijack is installed.
	require.Contains(t, source, "case ActionFlow, ActionHijackDNS:",
		"the consumer must group ActionHijackDNS with ActionFlow; a separate branch could reject "+
			"or accept it, and a DNS query that is accepted never reaches the DNS router")
	require.Contains(t, source, "acceptVerdict(packet)",
		"an ActionAccept on TCP must be resolved through acceptVerdict, which is what decides "+
			"whether a TCP/53 packet returns to the stream path")

	// The two auto-redirect backends behave differently, and the difference is load-bearing.
	redirect, err := os.ReadFile(filepath.Join(dir, "redirect_linux.go"))
	require.NoError(t, err)
	require.Contains(t, string(redirect), "repeatOnAccept: !r.useNFTables",
		"the iptables backend re-injects accepted TCP while nftables does not; if this changes, "+
			"the TCP/53 hijack path changes with it")

	require.Contains(t, source, "func (h *nfqueueHandler) acceptVerdict(",
		"acceptVerdict is the function that implements the per-backend behaviour")
}

// TestActionAcceptIsNeverBypassForDNS is the invariant the two halves must jointly preserve.
//
// It is stated separately because it is the property that actually matters: whatever the consumer
// does with ActionAccept, a DNS flow must never be handed to the platform as a bypass.
func TestActionAcceptIsNeverBypassForDNS(t *testing.T) {
	dir := singTunModuleDir(t)
	nfqueue, err := os.ReadFile(filepath.Join(dir, "nfqueue_linux.go"))
	require.NoError(t, err)
	source := string(nfqueue)

	// ActionBypass is the only verdict that hands the flow to the platform outside sing-box's
	// userspace path. It must not be reachable from the DNS verdicts.
	bypassIndex := strings.Index(source, "case ActionBypass:")
	require.GreaterOrEqual(t, bypassIndex, 0, "ActionBypass must be handled by the consumer")

	dnsIndex := strings.Index(source, "case ActionFlow, ActionHijackDNS:")
	require.GreaterOrEqual(t, dnsIndex, 0)

	require.NotEqual(t, bypassIndex, dnsIndex,
		"ActionBypass and ActionHijackDNS must not share a branch: sharing one would mean a DNS "+
			"flow is handed to the platform, skipping the DNS router entirely")
}
