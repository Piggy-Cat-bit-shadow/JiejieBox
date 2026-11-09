package daemon

// C05 — a crash leaves a pending candidate; the next real start has to settle it.
//
// The store stages a candidate before it is committed, so a process that dies in between leaves a
// pending file. Nothing about that file is authoritative: the daemon was not running the document it
// holds. These cases drive the entry, not the store, because "what happens on the next start" is the
// action the acceptance asks for.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/common/configtx"

	"github.com/stretchr/testify/require"
)

func TestACrashPendingCandidateIsSettledByTheNextSuccessfulStart(t *testing.T) {
	directory := t.TempDir()
	first := recoveryService(t, directory, false)
	require.NoError(t, first.StartOrReloadService(context.Background(), minimalConfig, nil))
	first.Close()

	// A candidate was staged and the process died before Commit: this is the crash window.
	store, err := configtx.Open(directory, configtx.Options{})
	require.NoError(t, err)
	_, err = store.Stage([]byte(ruleConfig))
	require.NoError(t, err)
	require.True(t, store.Pending(), "the fixture must leave a pending candidate behind")

	second := recoveryService(t, directory, false)
	require.NoError(t, second.StartOrReloadService(context.Background(), minimalConfig, nil))

	settled, err := configtx.Open(directory, configtx.Options{})
	require.NoError(t, err)
	require.False(t, settled.Pending(), "a successful start must not leave the crashed candidate pending")
	require.Equal(t, minimalConfig, storedActive(t, directory),
		"the active document is what started, not the candidate the crashed process staged")
}
