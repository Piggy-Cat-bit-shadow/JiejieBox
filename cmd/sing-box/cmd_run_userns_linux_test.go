//go:build linux

package main

import (
	"testing"

	"golang.org/x/sys/unix"

	"github.com/stretchr/testify/require"
)

// A rootless user namespace drops every capability, so a caller that already holds an effective
// capability other than CAP_SYS_ADMIN must be refused rather than silently losing it (upstream
// 9c24112d12). CAP_SYS_ADMIN itself is handled earlier by returning without a re-exec.
func TestHasEffectiveCapabilities(t *testing.T) {
	var empty [2]unix.CapUserData
	require.False(t, hasEffectiveCapabilities(empty))

	var lowWord [2]unix.CapUserData
	lowWord[0].Effective = 1 << unix.CAP_NET_RAW
	require.True(t, hasEffectiveCapabilities(lowWord))

	var highWord [2]unix.CapUserData
	highWord[1].Effective = 1 << (unix.CAP_MAC_OVERRIDE - 32)
	require.True(t, hasEffectiveCapabilities(highWord))
}
