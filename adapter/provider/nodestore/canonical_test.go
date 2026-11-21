package nodestore

import (
	"math"
	"net/netip"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/stretchr/testify/require"
)

// edgeChild exercises a nested pointer whose pointees are equal but distinct under DeepEqual.
type edgeChild struct {
	Name    string
	Weights []int
}

// edgeOptions covers the value shapes the option tree actually uses: bool (including explicit
// false), integers of every width that matter, floats, strings, slices (nil versus empty), maps
// (insertion order), pointers, interfaces of several dynamic types, netip.Addr (unexported fields),
// and badoption.Duration (a named int64).
type edgeOptions struct {
	Enabled  bool
	Count    int
	Port     uint16
	Ratio    float64
	Server   string
	Labels   []string
	Meta     map[string]string
	Child    *edgeChild
	Anything any
	Address  netip.Addr
	Delay    badoption.Duration
	Empty    []byte
}

func edgeBase() edgeOptions {
	return edgeOptions{
		Enabled:  true,
		Count:    7,
		Port:     443,
		Ratio:    0.1,
		Server:   "example.com",
		Labels:   []string{"h2", "http/1.1"},
		Meta:     map[string]string{"Host": "example.com", "User-Agent": "x"},
		Child:    &edgeChild{Name: "child", Weights: []int{1, 2}},
		Anything: "text",
		Address:  netip.MustParseAddr("198.51.100.7"),
		Delay:    badoption.Duration(5_000_000_000),
		Empty:    []byte{},
	}
}

func trojanNode(tag, serverName string, alpn []string, insecure bool) option.Outbound {
	return option.Outbound{
		Type: "trojan",
		Tag:  tag,
		Options: &option.TrojanOutboundOptions{
			DialerOptions: option.DialerOptions{Detour: "upstream"},
			ServerOptions: option.ServerOptions{Server: "example.com", ServerPort: 443},
			Password:      "secret",
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
				TLS: &option.OutboundTLSOptions{
					Enabled:    true,
					ServerName: serverName,
					Insecure:   insecure,
					ALPN:       badoption.Listable[string](alpn),
				},
			},
		},
	}
}

func selectorNode(tag string, members []string) option.Outbound {
	return option.Outbound{
		Type: "selector",
		Tag:  tag,
		Options: &option.SelectorOutboundOptions{
			Outbounds: members,
		},
	}
}

func realisticCorpus() []any {
	base := edgeBase()
	duplicate := edgeBase()
	nilLabels := edgeBase()
	nilLabels.Labels = nil
	emptyLabels := edgeBase()
	emptyLabels.Labels = []string{}
	nilMeta := edgeBase()
	nilMeta.Meta = nil
	emptyMeta := edgeBase()
	emptyMeta.Meta = map[string]string{}
	noChild := edgeBase()
	noChild.Child = nil
	zeroChild := edgeBase()
	zeroChild.Child = &edgeChild{}
	flippedBool := edgeBase()
	flippedBool.Enabled = false
	byteSlice := edgeBase()
	byteSlice.Empty = nil
	flippedAddr := edgeBase()
	flippedAddr.Address = netip.MustParseAddr("198.51.100.8")
	otherInt := edgeBase()
	otherInt.Count = 8
	otherType := edgeBase()
	otherType.Anything = int64(120)
	otherDelay := edgeBase()
	otherDelay.Delay = badoption.Duration(5_000_000_001)
	swappedLabels := edgeBase()
	swappedLabels.Labels = []string{"http/1.1", "h2"}
	distinctChild := edgeBase()
	distinctChild.Child = &edgeChild{Name: "child", Weights: []int{1, 2}}

	return []any{
		base, duplicate, distinctChild,
		nilLabels, emptyLabels,
		nilMeta, emptyMeta,
		noChild, zeroChild,
		flippedBool, byteSlice, flippedAddr, otherInt, otherType, otherDelay, swappedLabels,
		trojanNode("a", "example.com", []string{"h2", "http/1.1"}, false),
		trojanNode("a", "example.com", []string{"h2", "http/1.1"}, false),
		trojanNode("a", "other.example", []string{"h2", "http/1.1"}, false),
		trojanNode("a", "example.com", []string{"http/1.1", "h2"}, false),
		trojanNode("a", "example.com", []string{"h2", "http/1.1"}, true),
		trojanNode("b", "example.com", []string{"h2", "http/1.1"}, false),
		selectorNode("group", []string{"a", "b"}),
		selectorNode("group", []string{"b", "a"}),
		selectorNode("group", []string{"a", "b"}),
	}
}

// TestCanonicalEquivalenceMatchesDeepEqual is the differential test the task requires: for every
// ordered pair in a corpus that covers the value shapes of the option tree, the canonical byte
// comparison must give exactly the answer reflect.DeepEqual gives. Equal pairs include distinct
// allocations with equal values (the ordinary refresh case); unequal pairs include the distinctions
// that a JSON round-trip would lose: nil versus empty slice/map, an explicit false, a zero-valued
// pointer versus nil, integer width/precision, map insertion order and dynamic interface types.
func TestCanonicalEquivalenceMatchesDeepEqual(t *testing.T) {
	corpus := realisticCorpus()
	for i := range corpus {
		for j := range corpus {
			want := reflect.DeepEqual(corpus[i], corpus[j])
			got := Equivalent(corpus[i], corpus[j])
			require.Equal(t, want, got, "pair (%d, %d) disagrees with DeepEqual: %#v vs %#v", i, j, corpus[i], corpus[j])
		}
	}
}

// TestCanonicalEqualValuesEncodeEqualBytes is the stronger property behind the comparison: two
// values DeepEqual calls equal must produce identical bytes, not merely a matching verdict.
func TestCanonicalEqualValuesEncodeEqualBytes(t *testing.T) {
	first, err := Encode(trojanNode("a", "example.com", []string{"h2", "http/1.1"}, false))
	require.NoError(t, err)
	second, err := Encode(trojanNode("a", "example.com", []string{"h2", "http/1.1"}, false))
	require.NoError(t, err)
	require.True(t, reflect.DeepEqual(trojanNode("a", "example.com", []string{"h2", "http/1.1"}, false), trojanNode("a", "example.com", []string{"h2", "http/1.1"}, false)))
	require.Equal(t, first.Hash, second.Hash)
	require.Equal(t, first.Bytes, second.Bytes)
}

// TestCanonicalNilVersusEmpty distinguishes the two shapes that JSON cannot: a nil collection and an
// empty non-nil collection are different values to DeepEqual, so they must be different records.
func TestCanonicalNilVersusEmpty(t *testing.T) {
	withNil := edgeBase()
	withNil.Labels = nil
	withNil.Meta = nil
	withEmpty := edgeBase()
	withEmpty.Labels = []string{}
	withEmpty.Meta = map[string]string{}
	require.False(t, reflect.DeepEqual(withNil, withEmpty))
	require.False(t, Equivalent(withNil, withEmpty))
}

// TestCanonicalFloatSemantics pins the two float rules that must mirror `==`: -0 equals +0, and NaN
// is equal to nothing, including itself.
func TestCanonicalFloatSemantics(t *testing.T) {
	type floats struct {
		Value float64
	}
	positiveZero := floats{Value: 0}
	negativeZero := floats{Value: math.Copysign(0, -1)}
	require.True(t, reflect.DeepEqual(positiveZero, negativeZero))
	require.True(t, Equivalent(positiveZero, negativeZero))
	notANumber := floats{Value: math.NaN()}
	require.False(t, reflect.DeepEqual(notANumber, notANumber))
	require.False(t, Equivalent(notANumber, notANumber))
}

// TestCanonicalFuncIsNeverEqual mirrors DeepEqual's rule that non-nil functions are never equal,
// including to themselves.
func TestCanonicalFuncIsNeverEqual(t *testing.T) {
	type withFunc struct {
		Call func()
	}
	call := func() {}
	value := withFunc{Call: call}
	require.False(t, reflect.DeepEqual(value, value))
	require.False(t, Equivalent(value, value))
	require.True(t, reflect.DeepEqual(withFunc{}, withFunc{}))
	require.True(t, Equivalent(withFunc{}, withFunc{}))
}

// TestCanonicalCycleIsRefusedNotCrashed proves a self-referential value cannot hang or blow the
// stack: the encoder refuses it, and the provider's rule then rebuilds the node instead of reusing
// it. That is the safe direction - a false "different" costs a rebuild, a false "equal" would keep a
// stale object.
func TestCanonicalCycleIsRefusedNotCrashed(t *testing.T) {
	type cyclic struct {
		Name string
		Next *cyclic
	}
	value := &cyclic{Name: "root"}
	value.Next = value
	other := &cyclic{Name: "root"}
	other.Next = other
	_, err := Encode(value)
	require.ErrorIs(t, err, ErrUnsupported)
	require.False(t, Equivalent(value, other))
}

// TestCanonicalChannelIsConservative records the one intentional departure from DeepEqual: a record
// outlives its source, so a channel is encoded as a unique token. A channel-bearing option would
// rebuild on every refresh instead of risking a stale reuse; no option type in this tree carries a
// channel, so the difference is unreachable in production.
func TestCanonicalChannelIsConservative(t *testing.T) {
	type withChannel struct {
		Ch chan int
	}
	channel := make(chan int)
	value := withChannel{Ch: channel}
	require.True(t, reflect.DeepEqual(value, value))
	require.False(t, Equivalent(value, value))
}

// TestCanonicalRecordSize reports the size of a realistic node record. The number is evidence for the
// report, not an assertion about a particular byte count.
func TestCanonicalRecordSize(t *testing.T) {
	payload, err := Encode(trojanNode("provider/node-1", "example.com", []string{"h2", "http/1.1"}, false))
	require.NoError(t, err)
	t.Logf("MEASURED canonical trojan record bytes=%d", len(payload.Bytes))
	selectorPayload, err := Encode(selectorNode("provider/group", []string{"provider/node-1", "provider/node-2"}))
	require.NoError(t, err)
	t.Logf("MEASURED canonical selector record bytes=%d", len(selectorPayload.Bytes))
	dialerHeavy, err := Encode(edgeBase())
	require.NoError(t, err)
	t.Logf("MEASURED canonical edge record bytes=%d", len(dialerHeavy.Bytes))
	if len(payload.Bytes) >= 1024 {
		t.Fatalf("a trojan record of %d bytes is not a compact representation", len(payload.Bytes))
	}
}

// TestOpaqueNeverMatches proves the fallback record cannot be confused with a canonical record or
// with another fallback: it exists only to preserve tag ownership, never to prove equality.
func TestOpaqueNeverMatches(t *testing.T) {
	canonical, err := Encode(trojanNode("a", "example.com", []string{"h2"}, false))
	require.NoError(t, err)
	first := Opaque()
	second := Opaque()
	require.NotEqual(t, canonical.Bytes, first.Bytes)
	require.NotEqual(t, first.Bytes, second.Bytes)
	require.NotEqual(t, canonical.Hash, first.Hash)
	// The marker byte is outside the canonical kind range, so the two shapes cannot collide even if
	// the rest of the bytes were crafted.
	require.Equal(t, opaqueMarker, first.Bytes[1])
	require.Less(t, int(canonical.Bytes[1]), int(opaqueMarker))
	builder := NewBuilder(1)
	firstIndex, err := builder.Intern(first)
	require.NoError(t, err)
	secondIndex, err := builder.Intern(second)
	require.NoError(t, err)
	require.NotEqual(t, firstIndex, secondIndex)
	require.Equal(t, 2, builder.Records())
}
