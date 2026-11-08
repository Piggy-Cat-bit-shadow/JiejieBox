package oomkiller

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

// The residency key is the report half of the retirement inventory. Its whole content contract is
// that it reports OWNER REACH, not resources: these tests pin the shape, the semantics marker, the
// absence of the key when no owner registered, and the production splice into the metadata document
// the client reads.

// fakeResidencyReporter answers with a fixed snapshot so the renderer can be tested without a route.
type fakeResidencyReporter struct {
	snapshot adapter.ResidencySnapshot
}

func (r fakeResidencyReporter) ResidencySnapshot() adapter.ResidencySnapshot {
	return r.snapshot
}

// TestResidencyMetadataReportsOwnerReachPerKind pins the document shape and the semantics marker.
func TestResidencyMetadataReportsOwnerReachPerKind(t *testing.T) {
	reporter := fakeResidencyReporter{snapshot: adapter.ResidencySnapshot{
		Outbounds:     adapter.ResidencyOwnerCount{Reachable: 3, Releasable: 1},
		Endpoints:     adapter.ResidencyOwnerCount{Reachable: 2, Releasable: 0},
		DNSTransports: adapter.ResidencyOwnerCount{Reachable: 4, Releasable: 4},
		HTTPClients:   adapter.ResidencyOwnerCount{Reachable: 1, Releasable: 1},
	}}
	killer := &Service{ctx: service.ContextWith[adapter.ResidencyReporter](context.Background(), reporter)}

	rendered := killer.residencyMetadata()
	require.NotNil(t, rendered)
	document, ok := rendered.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "owner-reach", document["measure"],
		"the document must say what the numbers are: owner reach, not a resource ledger")
	require.Len(t, document, 5, "one measure marker and four owner kinds, nothing else")

	expected := map[string][2]int{
		"outbounds":     {3, 1},
		"endpoints":     {2, 0},
		"dnsTransports": {4, 4},
		"httpClients":   {1, 1},
	}
	for kind, counts := range expected {
		entry, isEntry := document[kind].(map[string]any)
		require.True(t, isEntry, "%s must be an object", kind)
		require.Equal(t, counts[0], entry["reachableOwners"], "%s reachable", kind)
		require.Equal(t, counts[1], entry["releasableOwners"], "%s releasable", kind)
		require.Len(t, entry, 2,
			"%s must not grow resource fields: the per-pool ledger is not this key's business", kind)
	}
}

// TestResidencyMetadataIsAbsentWithoutAReporter keeps the document unchanged for a process with no
// started reference manager: a nil producer makes the Recorder skip the key rather than write zeros
// that a reader could mistake for a measured empty inventory.
func TestResidencyMetadataIsAbsentWithoutAReporter(t *testing.T) {
	require.Nil(t, (&Service{}).residencyMetadata())
	require.Nil(t, (&Service{ctx: context.Background()}).residencyMetadata())
}

// TestReportMetadataCarriesTheResidencyInventoryNextToTheClientDocument proves the production write
// path: the key is spliced into the metadata document the report contains, and the client's own
// keys survive it unchanged.
func TestReportMetadataCarriesTheResidencyInventoryNextToTheClientDocument(t *testing.T) {
	basePath := t.TempDir()
	recorder := NewRecorder(RecorderOptions{
		BasePath: basePath,
		MetadataCallback: func(status ReportStatus) any {
			return map[string]any{"schema": 1, "peakMemory": status.PeakMemory}
		},
	})
	recorder.Start()
	t.Cleanup(func() { _ = recorder.Close() })

	reporter := fakeResidencyReporter{snapshot: adapter.ResidencySnapshot{
		Outbounds: adapter.ResidencyOwnerCount{Reachable: 2, Releasable: 1},
	}}
	killer := &Service{ctx: service.ContextWith[adapter.ResidencyReporter](context.Background(), reporter)}
	recorder.SetExtraMetadata("residency", killer.residencyMetadata)
	require.NoError(t, recorder.ensureDraftLocked())

	content, err := os.ReadFile(filepath.Join(basePath, DraftDirectoryName, metadataFileName))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, json.Unmarshal(content, &document))
	require.Contains(t, document, "residency")
	require.Contains(t, document, "schema", "the client's own keys must survive the splice")
	require.Contains(t, document, "peakMemory")

	residency, isMap := document["residency"].(map[string]any)
	require.True(t, isMap)
	require.Equal(t, "owner-reach", residency["measure"])
	outbounds, isMap := residency["outbounds"].(map[string]any)
	require.True(t, isMap)
	require.Equal(t, float64(2), outbounds["reachableOwners"])
	require.Equal(t, float64(1), outbounds["releasableOwners"])
}
