package rule

// B4 — the precompiled rule-set comparison, as a reproducible experiment.
//
// The "candidate" is the binary SRS format (the precompiled representation the project already
// produces with common/srs); the current alternative is the source JSON profile of the same rule
// content. This measures what a switch would buy: encoded size, parse time, allocation during the
// load, and the compile step both paths still pay. It also records the compatibility difference:
// the source form has no format version gate, the binary form is version-checked by srs.Read.
//
// The result is reported, not asserted as a win: the remote binary path already uses SRS, and no
// new precompiled artifact or consumer was added on the strength of this table.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

const comparisonRuleSetDomains = 20000

func comparisonRuleSetPlain() option.PlainRuleSet {
	domains := make(badoption.Listable[string], comparisonRuleSetDomains)
	for index := range domains {
		domains[index] = fmt.Sprintf("host-%05d.compare.example", index)
	}
	return option.PlainRuleSet{
		Rules: []option.HeadlessRule{{
			Type:           C.RuleTypeDefault,
			DefaultOptions: option.DefaultHeadlessRule{Domain: domains},
		}},
	}
}

func encodeSourceRuleSet(t *testing.T, ruleSet option.PlainRuleSet) []byte {
	t.Helper()
	encoded, err := singjson.MarshalContext(context.Background(), option.PlainRuleSetCompat{
		Version: C.RuleSetVersionCurrent,
		Options: ruleSet,
	})
	require.NoError(t, err)
	return encoded
}

func encodeBinaryRuleSet(t *testing.T, ruleSet option.PlainRuleSet) []byte {
	t.Helper()
	var payload bytes.Buffer
	require.NoError(t, srs.Write(&payload, ruleSet, C.RuleSetVersionCurrent))
	return payload.Bytes()
}

type loadMeasurement struct {
	duration   time.Duration
	allocated  uint64
	peakHeap   uint64
	ruleCount  int
	firstMatch bool
}

func measureLoad(t *testing.T, parse func([]byte) (option.PlainRuleSetCompat, error), content []byte) loadMeasurement {
	t.Helper()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baseline := before.HeapAlloc
	peak := baseline
	stopSampling := make(chan struct{})
	samplingDone := make(chan struct{})
	go func() {
		defer close(samplingDone)
		ticker := time.NewTicker(200 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				var current runtime.MemStats
				runtime.ReadMemStats(&current)
				if current.HeapAlloc > peak {
					peak = current.HeapAlloc
				}
			}
		}
	}()
	started := time.Now()
	ruleSet, err := parse(content)
	require.NoError(t, err)
	rules := make([]adapter.HeadlessRule, 0, len(ruleSet.Options.Rules))
	for _, ruleOptions := range ruleSet.Options.Rules {
		rule, ruleErr := NewHeadlessRule(context.Background(), ruleOptions)
		require.NoError(t, ruleErr)
		rules = append(rules, rule)
	}
	elapsed := time.Since(started)
	close(stopSampling)
	<-samplingDone
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	firstMatch := false
	for _, rule := range rules {
		if rule.Match(&adapter.InboundContext{Domain: "host-00000.compare.example"}) {
			firstMatch = true
		}
	}
	return loadMeasurement{
		duration:   elapsed,
		allocated:  after.TotalAlloc - before.TotalAlloc,
		peakHeap:   peak - baseline,
		ruleCount:  len(rules),
		firstMatch: firstMatch,
	}
}

func medianDuration(values []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func digestOfRuleSetBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestPrecompiledRuleSetComparedWithSourceJSON(t *testing.T) {
	ruleSet := comparisonRuleSetPlain()
	source := encodeSourceRuleSet(t, ruleSet)
	binary := encodeBinaryRuleSet(t, ruleSet)
	require.NotEqual(t, digestOfRuleSetBytes(source), digestOfRuleSetBytes(binary),
		"the two encodings are different representations of the same rules")

	sourceParse := func(content []byte) (option.PlainRuleSetCompat, error) {
		return singjson.UnmarshalExtended[option.PlainRuleSetCompat](content)
	}
	binaryParse := func(content []byte) (option.PlainRuleSetCompat, error) {
		return srs.Read(bytes.NewReader(content), false)
	}

	const repetitions = 3
	sourceRuns := make([]loadMeasurement, 0, repetitions)
	binaryRuns := make([]loadMeasurement, 0, repetitions)
	for attempt := 0; attempt < repetitions; attempt++ {
		sourceRuns = append(sourceRuns, measureLoad(t, sourceParse, source))
		binaryRuns = append(binaryRuns, measureLoad(t, binaryParse, binary))
	}
	sourceTimes := make([]time.Duration, 0, repetitions)
	binaryTimes := make([]time.Duration, 0, repetitions)
	for index := range sourceRuns {
		sourceTimes = append(sourceTimes, sourceRuns[index].duration)
		binaryTimes = append(binaryTimes, binaryRuns[index].duration)
	}
	sourceMedian := medianDuration(sourceTimes)
	binaryMedian := medianDuration(binaryTimes)
	sourceRun := sourceRuns[repetitions/2]
	binaryRun := binaryRuns[repetitions/2]

	t.Logf("rule content: %d domains, %d rules", comparisonRuleSetDomains, sourceRun.ruleCount)
	t.Logf("encoded size: source JSON %d bytes, binary SRS %d bytes (binary %.1f%% of source)",
		len(source), len(binary), 100*float64(len(binary))/float64(len(source)))
	t.Logf("load+compile median: source %s, binary %s", sourceMedian, binaryMedian)
	t.Logf("allocated during load: source %d bytes, binary %d bytes", sourceRun.allocated, binaryRun.allocated)
	t.Logf("peak heap delta during load: source %d bytes, binary %d bytes", sourceRun.peakHeap, binaryRun.peakHeap)
	t.Logf("first rule matched by the compiled form: source=%v binary=%v", sourceRun.firstMatch, binaryRun.firstMatch)
	t.Logf("compatibility: source is version-less, binary is gated by srs.Read at rule-set version %d", C.RuleSetVersionCurrent)
	t.Logf("takeaway: the remote binary path already uses the smaller representation; no new precompiled artifact is added")

	require.True(t, sourceRun.firstMatch && binaryRun.firstMatch, "both forms must compile to a matching rule")
	require.Less(t, len(binary), len(source), "the binary representation is smaller than source JSON")
}

func benchmarkRuleSetLoad(b *testing.B, content []byte, parse func([]byte) (option.PlainRuleSetCompat, error)) {
	b.Helper()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		parsed, err := parse(content)
		if err != nil {
			b.Fatal(err)
		}
		for _, ruleOptions := range parsed.Options.Rules {
			if _, err = NewHeadlessRule(context.Background(), ruleOptions); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkRuleSetSourceJSONLoad(b *testing.B) {
	ruleSet := comparisonRuleSetPlain()
	encoded, err := singjson.MarshalContext(context.Background(), option.PlainRuleSetCompat{
		Version: C.RuleSetVersionCurrent,
		Options: ruleSet,
	})
	if err != nil {
		b.Fatal(err)
	}
	benchmarkRuleSetLoad(b, encoded, func(content []byte) (option.PlainRuleSetCompat, error) {
		return singjson.UnmarshalExtended[option.PlainRuleSetCompat](content)
	})
}

func BenchmarkRuleSetBinarySRSLoad(b *testing.B) {
	ruleSet := comparisonRuleSetPlain()
	var payload bytes.Buffer
	if err := srs.Write(&payload, ruleSet, C.RuleSetVersionCurrent); err != nil {
		b.Fatal(err)
	}
	benchmarkRuleSetLoad(b, payload.Bytes(), func(content []byte) (option.PlainRuleSetCompat, error) {
		return srs.Read(bytes.NewReader(content), false)
	})
}
