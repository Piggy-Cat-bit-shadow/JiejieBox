package urltest

// P02B — the key must distinguish every fact that can change an answer.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func baseKey() MeasurementKey {
	return MeasurementKey{
		Instance:       "box-live",
		NetworkEpoch:   7,
		Node:           "node-a",
		Path:           "detour-1",
		Target:         "https://probe.example/generate_204",
		ExpectedStatus: "204",
		Method:         "HEAD",
		TLSFingerprint: "chrome",
		Purpose:        PurposeHealth,
	}
}

func TestIdenticalMeasurementsShareOneAnswer(t *testing.T) {
	require.True(t, baseKey().SharesEvidenceWith(baseKey()))
}

func TestEveryFactThatCanChangeTheAnswerIsInTheKey(t *testing.T) {
	differences := map[string]func(*MeasurementKey){
		"instance":        func(k *MeasurementKey) { k.Instance = "box-temp" },
		"network epoch":   func(k *MeasurementKey) { k.NetworkEpoch++ },
		"node":            func(k *MeasurementKey) { k.Node = "node-b" },
		"path":            func(k *MeasurementKey) { k.Path = "direct" },
		"target":          func(k *MeasurementKey) { k.Target = "https://probe.example/other" },
		"expected status": func(k *MeasurementKey) { k.ExpectedStatus = "200" },
		"method":          func(k *MeasurementKey) { k.Method = "GET" },
		"tls policy":      func(k *MeasurementKey) { k.TLSFingerprint = "firefox" },
	}
	for name, mutate := range differences {
		other := baseKey()
		mutate(&other)
		require.False(t, baseKey().SharesEvidenceWith(other), "a different %s must not share an answer", name)
	}
}

func TestOnlyHealthEvidenceIsShared(t *testing.T) {
	display := baseKey()
	display.Purpose = PurposeDisplay
	require.False(t, baseKey().SharesEvidenceWith(display), "a display answer must not become a health verdict")
	require.False(t, display.SharesEvidenceWith(display), "a display probe does not share evidence with itself")

	diagnostic := baseKey()
	diagnostic.Purpose = PurposeDiagnostic
	require.False(t, baseKey().SharesEvidenceWith(diagnostic))
}
