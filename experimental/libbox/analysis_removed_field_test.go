package libbox

// C01/C02 — where a REMOVED field's real source is.
//
// Q21's minimum condition asks for a real source for "defaults / removed / unavailable" rather than a
// re-derived table. This file pins the two halves of that answer:
//
//   - a REMOVED (deprecated, `schema:"omit"`) field is not silently ignored: the core's own decoder
//     rejects it with the migration message, and AnalyzeConfig returns that error. The source is the
//     decoder in option/, not a list maintained here.
//   - a field the schema omits from documentation is therefore reachable through this document: a
//     client that analyses a profile and gets this error learns exactly what CheckConfig would say,
//     without a Box being built.
//
// The case is the direct outbound's destination override, which option/direct.go marks Deprecated and
// removes with an explicit message. If a future release drops the field without a message, this test
// fails: that is the reminder that the error IS the source.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestARemovedFieldIsReportedWithTheCoresOwnMessage(t *testing.T) {
	document := `{
		"outbounds": [
			{"type": "direct", "tag": "direct-out", "override_address": "203.0.113.7"}
		]
	}`

	analysis, err := AnalyzeConfig(document)
	require.Error(t, err, "a removed field must fail the parse, not vanish silently")
	require.Nil(t, analysis, "a failed parse carries no partial document")
	require.Contains(t, err.Error(), "deprecated",
		"the message must come from the core's decoder, so a client reports the migration the core reports")
	require.Contains(t, err.Error(), "route options",
		"the message must name the replacement, which is what makes it actionable")
}

// TestAnOrdinaryDirectOutboundStillAnalyses is the control: the error above is about the removed
// field, not about the direct outbound.
func TestAnOrdinaryDirectOutboundStillAnalyses(t *testing.T) {
	document := `{
		"outbounds": [
			{"type": "direct", "tag": "direct-out"}
		]
	}`
	analysis, err := AnalyzeConfig(document)
	require.NoError(t, err)
	require.NotNil(t, analysis)
	require.Contains(t, analysis.Value, `"parsed":true`)
	require.Contains(t, analysis.Value, `"outbounds":1`)
}
