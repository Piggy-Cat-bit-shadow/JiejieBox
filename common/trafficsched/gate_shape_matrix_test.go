package trafficsched

import (
	"io"
	"testing"

	"github.com/sagernet/sing/common/buf"
	N "github.com/sagernet/sing/common/network"

	"github.com/stretchr/testify/require"
)

// The gate is built as one of four SHAPES depending on what the upstream offers, and the whole point of
// the split is that the shape must claim exactly the upstream's capabilities and no more:
//
//	isExtended && hasMTU -> gateExtendedMTU
//	isExtended           -> gateExtended
//	hasMTU               -> gateWriterMTU
//	neither              -> gateWriter
//
// Claiming one the upstream lacks is the failure that matters. The MTU case is documented in the code as
// catastrophic rather than untidy: a gate that always implemented WriterMTU would have to invent a value,
// and math.MaxInt makes `ReadWaitOptions.NewBuffer()` compute MaxInt+overhead, which overflows to a
// negative size and collapses the read buffer to a single byte.
//
// The existing coverage checks two hand-written pairs (plain, and ExtendedWriter-with-MTU). That leaves
// the two MIXED shapes - extended-without-MTU and plain-with-MTU - untested, and those are exactly the
// cases a wrong constructor branch would produce. This walks the full matrix instead.

// gateProbeWriter is a bare io.Writer: no WriteBuffer, no MTU.
type gateProbeWriter struct{ written int }

func (w *gateProbeWriter) Write(p []byte) (int, error) { w.written += len(p); return len(p), nil }

// gateProbeExtended adds WriteBuffer only.
type gateProbeExtended struct{ writes int }

func (w *gateProbeExtended) Write(p []byte) (int, error) { return len(p), nil }
func (w *gateProbeExtended) WriteBuffer(buffer *buf.Buffer) error {
	w.writes++
	buffer.Release()
	return nil
}

// gateProbeMTU adds WriterMTU only. The value is LIVE for the same reason the production one is: a chain
// can become constrained without the object changing.
type gateProbeMTU struct {
	gateProbeWriter
	mtu int
}

func (w *gateProbeMTU) WriterMTU() int { return w.mtu }

// gateProbeBoth is the fourth corner.
type gateProbeBoth struct {
	gateProbeExtended
	mtu int
}

func (w *gateProbeBoth) WriterMTU() int { return w.mtu }

func TestGateShapeMatrixClaimsExactlyTheUpstreamCapabilities(t *testing.T) {
	cases := []struct {
		name       string
		upstream   io.Writer
		wantExt    bool
		wantMTU    bool
		wantMTUVal int
	}{
		{"neither", &gateProbeWriter{}, false, false, 0},
		{"extended only", &gateProbeExtended{}, true, false, 0},
		{"mtu only", &gateProbeMTU{mtu: 1400}, false, true, 1400},
		{"both", &gateProbeBoth{mtu: 1400}, true, true, 1400},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gate := NewGate(testCase.upstream, nil)

			_, isExtended := gate.(N.ExtendedWriter)
			require.Equal(t, testCase.wantExt, isExtended,
				"the gate must claim WriteBuffer exactly when the upstream honours it; claiming it "+
					"otherwise makes the copy engine take a buffer path the upstream cannot serve")

			withMTU, hasMTU := gate.(N.WriterWithMTU)
			require.Equal(t, testCase.wantMTU, hasMTU,
				"the gate must implement WriterMTU exactly when the chain constrains it. An invented "+
					"value is not a neutral default here: sing resolves MTU as a MINIMUM, and a MaxInt "+
					"makes NewBuffer() compute MaxInt+overhead, which overflows negative and collapses "+
					"the read buffer to one byte")
			if testCase.wantMTU {
				require.Equal(t, testCase.wantMTUVal, withMTU.WriterMTU(),
					"and the value must come from the chain, not be synthesised")
			}

			// Every shape must stay non-replaceable, or the scheduler leaves the write path entirely.
			replaceable, isReplaceable := gate.(N.WriterWithUpstream)
			require.True(t, isReplaceable,
				"every shape must expose WriterReplaceable, because that is how the engine asks")
			require.False(t, replaceable.WriterReplaceable(),
				"no shape may be replaceable: the scheduler's whole purpose is to sit in the write path, "+
					"and a replaceable gate is silently unwrapped")
			require.Same(t, gate, N.UnwrapWriter(gate),
				"and the engine must not be able to unwrap past it")

			// The gate must not claim a capability on the READ side. It is a writer gate; promoting a
			// reader method through an embedded upstream would let the copy engine read around the
			// scheduler's accounting.
			_, isReader := gate.(N.ReaderWithUpstream)
			require.False(t, isReader, "a writer gate must not advertise reader replaceability")
		})
	}
}

// The MTU must be re-read from the chain on every call rather than captured at construction, because a
// connection can become constrained (or stop being constrained) without the object changing. A snapshot
// would be correct at build time and wrong forever after, which is the failure this pins.
func TestGateMTUIsLiveNotASnapshot(t *testing.T) {
	upstream := &gateProbeMTU{mtu: 1400}
	gate := NewGate(upstream, nil)

	withMTU, hasMTU := gate.(N.WriterWithMTU)
	require.True(t, hasMTU)
	require.Equal(t, 1400, withMTU.WriterMTU())

	// The chain changes underneath the gate, as it does on a real connection.
	upstream.mtu = 900

	require.Equal(t, 900, withMTU.WriterMTU(),
		"the gate must re-read the chain MTU on every call; a value captured at construction would "+
			"keep reporting 1400 for a connection that now constrains 900, and callers would size "+
			"buffers for a path that no longer exists")
}
