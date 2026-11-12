//go:build with_utls

package tls

// REALITY shares the TLS client, so the diagnostic has to name its stage from the CONFIG rather
// than guess it from an error. This test lives behind the build tag that defines the REALITY client:
// without uTLS there is no such handshake to name.

import (
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/stretchr/testify/require"
)

func TestAREALITYHandshakeIsReportedAsItsOwnStage(t *testing.T) {
	require.True(t, isRealityConfig(&RealityClientConfig{}))
	stage := handshakeStage(&RealityClientConfig{}, time.Now(), nil)
	require.Equal(t, adapter.DiagnoseStageReality, stage.Stage,
		"a REALITY handshake is the reality layer, not the tls layer")
	require.Equal(t, adapter.DiagnoseCodeTLSOK, stage.Code)
}
