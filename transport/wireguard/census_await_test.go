package wireguard

// The waiter and the failure-side evidence for the receive-census assertions.
//
// `require.Eventually` around `socketCensus` was the source of two different defects on the macOS
// gate, and both are addressed here rather than in each call site:
//
//  1. It reports only "Condition never satisfied". The recorded macOS failure was exactly that, on
//     a test that had just established the expectation as 1 - so the reader could not tell whether
//     the endpoint had 0, 2, or a fluctuating number of receive goroutines. A census is an
//     expensive, indirect instrument; when it disagrees with the expectation, the evidence that
//     says WHICH witness is wrong has to be in the log.
//  2. It polls at 1ms. A census is a full `runtime.Stack` dump, so that spends most of every second
//     in the runtime, and the goroutine being waited for is one that has to be SCHEDULED. The
//     instrument starves its own subject. This waiter therefore runs at 25ms by default: the
//     deadlines and the exact equality assertion are unchanged, only the pressure the check puts on
//     the thing it measures.
//
// The waiter also records the whole distribution rather than only the final value, so a transient
// reading can be told apart from a value that never arrives.

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSettlingObservationKeepsTheReceiverThatArrivesLate is the deterministic regression test for the
// macOS expectation bug: the first non-zero census reading was 1 while the settled bind holds 2, and
// the old measurement recorded the 1 because it stopped sampling at the first non-zero value.
//
// The observer here produces that exact sequence without depending on the scheduler or on this
// machine having any particular address-family support, and the property asserted is the one the
// macOS failure violated: the established value is the maximum over the settling window, not the
// first value above zero.
func TestSettlingObservationKeepsTheReceiverThatArrivesLate(t *testing.T) {
	calls := 0
	observed, trace := observeSocketsPerStandardBindSettling(func() int {
		calls++
		if calls <= 2 {
			return 1
		}
		return 2
	}, 30*time.Millisecond, 20*time.Millisecond, 2*time.Second)

	require.Equal(t, 2, observed,
		"the settling window must keep the second receiver that becomes visible after the first: "+
			"observed %s", trace)
	require.Contains(t, trace, "1@", "the trace must record the early value, so a failure can show "+
		"what the expectation was mistaken for")
	require.Contains(t, trace, "2@", "the trace must record the value the window settled on")
}

// TestSettlingObservationTellsOneFamilyFromNoReading pins the other two outcomes the caller depends
// on: a genuinely one-family bind settles at 1 rather than being treated as transient, and a census
// that never sees anything returns 0 so the caller fails on its own "no settled reading" assertion.
func TestSettlingObservationTellsOneFamilyFromNoReading(t *testing.T) {
	oneFamily, trace := observeSocketsPerStandardBindSettling(func() int { return 1 },
		20*time.Millisecond, 10*time.Millisecond, time.Second)
	require.Equal(t, 1, oneFamily, "a stable single-family bind is a valid expectation: %s", trace)
	require.Contains(t, trace, "settled=true")

	none, trace := observeSocketsPerStandardBindSettling(func() int { return 0 },
		20*time.Millisecond, 10*time.Millisecond, 100*time.Millisecond)
	require.Zero(t, none, "a census that never sees a receiver must not establish an expectation")
	require.Contains(t, trace, "settled=false")
}

// censusAwaitDiagnostics returns extra lines printed only when the wait fails. It is evaluated at
// failure time, on the test goroutine, so it can read state that only makes sense in the failed
// state (the wake result, the gate, the port table).
type censusAwaitDiagnostics func() []string

// awaitSocketCensus waits for the receive-goroutine census to reach `expected`.
//
// On success it returns how long the wait took and logs nothing. On timeout it fails the test with
// one block that states the expectation, where the expectation came from, the observed
// distribution, the lifecycle evidence the assertion is paired with, and a scoped dump of the
// receive goroutines that actually exist - which separates "no receiver is running" from "the
// census cannot see the receiver" instead of leaving the next reader to guess.
func awaitSocketCensus(t *testing.T, endpoint *Endpoint, expected int, timeout, interval time.Duration, what string, diagnostics ...censusAwaitDiagnostics) time.Duration {
	t.Helper()
	start := time.Now()
	histogram := make(map[int]int)
	firstSeen := make(map[int]time.Duration)
	last := 0
	samples := 0
	for {
		device := endpoint.device.Load()
		value := -1
		if device != nil {
			value = deviceReceiveCensus(device)
		}
		elapsed := time.Since(start)
		if _, seen := firstSeen[value]; !seen {
			firstSeen[value] = elapsed
		}
		histogram[value]++
		last = value
		samples++
		if value == expected {
			return elapsed
		}
		if elapsed >= timeout {
			break
		}
		time.Sleep(interval)
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "%s: the receive-goroutine census never reached %d within %s "+
		"(samples=%d, interval=%s, last=%d)\n", what, expected, timeout, samples, interval, last)
	fmt.Fprintf(&builder, "  expected=%d; memo: %s\n", expected, describeSocketsPerStandardBind())
	for _, value := range sortedCensusValues(histogram) {
		fmt.Fprintf(&builder, "  observed: value=%d samples=%d first=%s\n",
			value, histogram[value], firstSeen[value].Round(time.Microsecond))
	}
	if device := endpoint.device.Load(); device == nil {
		builder.WriteString("  device: none published, so the census could not even be scoped\n")
	} else {
		fmt.Fprintf(&builder, "  device: %s (bind type %T)\n", endpointFrameAddress(device), device.Bind())
	}
	for _, diagnostic := range diagnostics {
		for _, line := range diagnostic() {
			fmt.Fprintf(&builder, "  %s\n", line)
		}
	}
	builder.WriteString(describeReceiveGoroutines(endpoint))
	t.Fatalf("%s", builder.String())
	return 0
}

func sortedCensusValues(histogram map[int]int) []int {
	values := make([]int, 0, len(histogram))
	for value := range histogram {
		values = append(values, value)
	}
	sort.Ints(values)
	return values
}

// describeReceiveGoroutines reports what the goroutine dump contains for this endpoint: how many
// goroutines run `RoutineReceiveIncoming` for THIS device, how many run it for another device (other
// fixtures' endpoints are alive until their cleanups run), and how many carry the marker but no
// readable receiver address. The last category is the one that makes a census undercount while the
// goroutine genuinely exists.
func describeReceiveGoroutines(endpoint *Endpoint) string {
	device := endpoint.device.Load()
	if device == nil {
		return "  receiver goroutines: no published device to scope the dump to"
	}
	expectedAddress := endpointFrameAddress(device)
	dump, truncated := runtimeStackDump()
	markerCount := 0
	matched := 0
	otherDevice := 0
	unaddressable := 0
	var otherFrames []string
	for _, goroutine := range strings.Split(dump, "\n\n") {
		if !strings.Contains(goroutine, markerReceiveIncoming) {
			continue
		}
		markerCount++
		address := frameReceiverAddress(goroutine, markerReceiveIncoming)
		switch {
		case address == expectedAddress:
			matched++
		default:
			if address == "" || strings.Contains(address, "?") {
				unaddressable++
			} else {
				otherDevice++
			}
			if len(otherFrames) < 2 {
				otherFrames = append(otherFrames, firstFrameLines(goroutine, 3))
			}
		}
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "  receiver goroutines: marker=%d matched_device=%d other_device=%d "+
		"unaddressable=%d dump_bytes=%d truncated=%v device=%q\n",
		markerCount, matched, otherDevice, unaddressable, len(dump), truncated, expectedAddress)
	for _, frame := range otherFrames {
		fmt.Fprintf(&builder, "  sample unmatched frame:\n%s\n", frame)
	}
	return builder.String()
}

func firstFrameLines(goroutine string, count int) string {
	lines := strings.Split(goroutine, "\n")
	if len(lines) > count {
		lines = lines[:count]
	}
	for index := range lines {
		lines[index] = "    " + lines[index]
	}
	return strings.Join(lines, "\n")
}

// udpWildcardProbeError is the OS port-table witness, with its failure reason kept.
//
// `udpPortIsFree` collapses every error into "occupied". That is the right answer for an assertion
// whose question is "can another bind take the port", but it hides a probe that failed for a
// different reason (permission, address-family policy) as if the endpoint still held the port. The
// diagnostic path reports the error itself so an occupied-port reading can be judged.
func udpWildcardProbeError(port uint16) error {
	listener, err := net.ListenPacket("udp4", "0.0.0.0:"+strconv.Itoa(int(port)))
	if err != nil {
		return err
	}
	_ = listener.Close()
	return nil
}
