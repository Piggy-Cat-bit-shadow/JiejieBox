package box

// P02B wiring acceptance: the Box must supply the measurement share, exactly like the coordinator
// and the history storage, so two URLTest groups in one Box can share an equivalent health probe.
//
// This file is part of the shared-file slice (box.go is the integration window's single-write
// file). It ships together with the three-line wiring in ensureURLTestServices.

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/common/urltest"

	"github.com/stretchr/testify/require"
)

func TestTheBoxSuppliesTheMeasurementShare(t *testing.T) {
	ctx, _ := ensureURLTestServices(context.Background())
	require.NotNil(t, urltest.MeasurementShareFromContext(ctx),
		"a Box without a share cannot deduplicate health probes across its groups")

	// Supplied, never overwritten: a caller that already chose a registry keeps it, and a second
	// call returns the same one.
	callerShare := urltest.NewMeasurementShare()
	callerCtx, _ := ensureURLTestServices(urltest.ContextWithMeasurementShare(context.Background(), callerShare))
	require.Same(t, callerShare, urltest.MeasurementShareFromContext(callerCtx))

	again, _ := ensureURLTestServices(ctx)
	require.Same(t, urltest.MeasurementShareFromContext(ctx), urltest.MeasurementShareFromContext(again),
		"a second ensure must share the registry it already installed")
}
