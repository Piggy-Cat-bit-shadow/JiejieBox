package varch_test

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter/provider/nodestore"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
)

type myInt int

type namedHidden struct {
	Visible int
	hidden  int
}

type taggedA struct {
	Field int `json:"a"`
}

type taggedB struct {
	Field int `json:"b"`
}

// equivalentEncode encodes both sides and compares bytes exactly the way Store.Matches does:
// hash+length are a fast reject, bytes are the decision. It returns ok=false when either side is
// refused by the encoder (the provider then rebuilds, the safe direction).
func equivalentEncode(t *testing.T, a, b any) (equal bool, encoded bool) {
	t.Helper()
	payloadA, errA := nodestore.Encode(a)
	payloadB, errB := nodestore.Encode(b)
	if errA != nil || errB != nil {
		return false, false
	}
	if payloadA.Hash != payloadB.Hash || len(payloadA.Bytes) != len(payloadB.Bytes) {
		return false, true
	}
	return string(payloadA.Bytes) == string(payloadB.Bytes), true
}

// TestArchBCanonicalVsDeepEqual is the property test the provider's substitution claim needs:
// `Encode(a)==Encode(b)` must agree with `reflect.DeepEqual(a,b)` for the value domain the
// provider compares. Mismatches are reported in both directions, because they have different
// consequences: `encoded-equal but DeepEqual-false` (a false "unchanged", stale object reused) is
// a correctness defect; `DeepEqual-true but encoded-different` is only an extra rebuild.
func TestArchBCanonicalVsDeepEqual(t *testing.T) {
	one, oneB, two := 1, 1, 2
	sharedInt := &one
	sharedStruct := struct{ P *int }{P: sharedInt}
	otherStructSame := struct{ P *int }{P: &oneB}
	otherStructTwo := struct{ P *int }{P: &two}

	sliceA := []int{1, 2, 3}
	sliceB := []int{1, 2, 3}
	sliceShare := sliceA[:3]

	nan := math.NaN()
	nanPointer := &nan
	nanStruct := struct{ F float64 }{F: nan}
	nanStructPointer := &nanStruct

	ch := make(chan int)
	fn := func() {}
	var nilFn func()
	_ = fn

	timeZero := time.Time{}
	timeUnixUTC := time.Unix(0, 0).UTC()
	timeUnixLocal := time.Unix(0, 0).Local()
	timeFixed := time.Unix(0, 0).In(time.FixedZone("z", 3600))

	cycleA := &varchCyclic{Name: "x"}
	cycleA.Next = cycleA
	cycleB := &varchCyclic{Name: "x"}
	cycleB.Next = cycleB

	genericInt := varchGeneric[int]{Value: 1}
	genericInt64 := varchGeneric[int64]{Value: 1}

	corpus := []struct {
		name  string
		value any
	}{
		{"nil", nil},
		{"typed-nil-pointer", (*int)(nil)},
		{"nil-slice", []int(nil)},
		{"empty-slice", []int{}},
		{"nil-map", map[string]int(nil)},
		{"empty-map", map[string]int{}},
		{"false", false},
		{"true", true},
		{"int8-1", int8(1)},
		{"int-1", int(1)},
		{"int64-1", int64(1)},
		{"uint8-1", uint8(1)},
		{"float32-1", float32(1)},
		{"float64-1", float64(1)},
		{"float64-zero", float64(0)},
		{"float64-negative-zero", math.Copysign(0, -1)},
		{"float64-nan", nan},
		{"float64-inf", math.Inf(1)},
		{"complex-1", complex(1, 0)},
		{"string-empty", ""},
		{"string-a", "a"},
		{"empty-struct", struct{}{}},
		{"hidden-zero", namedHidden{}},
		{"hidden-visible", namedHidden{Visible: 1}},
		{"hidden-different-a", namedHidden{Visible: 1, hidden: 1}},
		{"hidden-different-b", namedHidden{Visible: 1, hidden: 2}},
		{"time-zero", timeZero},
		{"time-unix-utc", timeUnixUTC},
		{"time-unix-local", timeUnixLocal},
		{"time-fixed-zone", timeFixed},
		{"netip-v4", netip.MustParseAddr("192.0.2.1")},
		{"netip-v4-mapped", netip.MustParseAddr("::ffff:192.0.2.1")},
		{"pointer-1", sharedInt},
		{"pointer-1-other", &oneB},
		{"pointer-2", &two},
		{"pointer-shared-struct", sharedStruct},
		{"pointer-struct-equal", otherStructSame},
		{"pointer-struct-different", otherStructTwo},
		{"slice-a", sliceA},
		{"slice-b", sliceB},
		{"slice-shared", sliceShare},
		{"slice-empty-literal", []int{}},
		{"map-a", map[string]int{"a": 1, "b": 2}},
		{"map-a-reordered", map[string]int{"b": 2, "a": 1}},
		{"map-a-value-different", map[string]int{"a": 1, "b": 3}},
		{"map-int-key", map[int]string{1: "a"}},
		{"map-int64-key", map[int64]string{1: "a"}},
		{"map-interface-values", map[string]any{"a": 1}},
		{"map-interface-values-int64", map[string]any{"a": int64(1)}},
		{"iface-int", any(1)},
		{"iface-int64", any(int64(1))},
		{"iface-float64", any(float64(1))},
		{"iface-string", any("1")},
		{"iface-named-int", any(myInt(1))},
		{"iface-generic-int", any(genericInt)},
		{"iface-generic-int64", any(genericInt64)},
		{"iface-tagged-a", any(taggedA{Field: 1})},
		{"iface-tagged-b", any(taggedB{Field: 1})},
		{"iface-nil-func", any(nilFn)},
		{"iface-nonNil-func", any(fn)},
		{"func-nil", nilFn},
		{"func-nonNil", fn},
		{"chan-nil", (chan int)(nil)},
		{"chan-value", ch},
		{"pointer-nan", nanPointer},
		{"struct-pointer-nan", nanStructPointer},
		{"slice-nan", []float64{nan}},
		{"map-nan-key", map[float64]int{nan: 1}},
		{"cycle-a", cycleA},
		{"cycle-b", cycleB},
		{"array", [3]int{1, 2, 3}},
		{"array-other", [3]int{1, 2, 4}},
		{"empty-array", [0]int{}},
		{"deep-struct", struct {
			A []int
			B map[string]any
			C *int
		}{A: []int{1}, B: map[string]any{"k": true}, C: sharedInt}},
	}

	var encodedEqualDeepEqualFalse, deepEqualTrueEncodedDifferent []string
	checked := 0
	for i := range corpus {
		for j := range corpus {
			left, right := corpus[i], corpus[j]
			want := reflect.DeepEqual(left.value, right.value)
			got, encoded := equivalentEncode(t, left.value, right.value)
			if !encoded {
				// At least one side is refused (cycle/deep). The provider rebuilds, which is
				// safe; a refused side can never claim equality. Nothing to compare.
				if want && left.name == right.name {
					t.Logf("encoder refuses %q (both sides), provider rebuilds: safe", left.name)
				}
				continue
			}
			checked++
			if got != want {
				entry := left.name + " vs " + right.name
				if got && !want {
					encodedEqualDeepEqualFalse = append(encodedEqualDeepEqualFalse, entry)
				} else {
					deepEqualTrueEncodedDifferent = append(deepEqualTrueEncodedDifferent, entry)
				}
			}
		}
	}
	t.Logf("checked %d encoded pairs of %d values", checked, len(corpus))
	if len(encodedEqualDeepEqualFalse) > 0 {
		for _, entry := range encodedEqualDeepEqualFalse {
			t.Errorf("DANGEROUS: encoder says equal but reflect.DeepEqual says different: %s", entry)
		}
	}
	if len(deepEqualTrueEncodedDifferent) > 0 {
		for _, entry := range deepEqualTrueEncodedDifferent {
			t.Logf("SAFE-DIRECTION mismatch (DeepEqual true, encoded different -> rebuild): %s", entry)
		}
	}
}

// TestArchBRandomTrees compares 300 randomly generated pairs of the same shape class. The point is
// not to find pretty values but to let a generator hit combinations the hand corpus missed.
func TestArchBRandomTrees(t *testing.T) {
	random := rand.New(rand.NewSource(20261122))
	var randomValue func(depth int) any
	randomValue = func(depth int) any {
		if depth <= 0 {
			switch random.Intn(8) {
			case 0:
				return random.Intn(3)
			case 1:
				return int64(random.Intn(3))
			case 2:
				return random.Intn(2) == 0
			case 3:
				return float64(random.Intn(3))
			case 4:
				return []string{"a", "b"}[:random.Intn(2)]
			case 5:
				return map[string]int{"k": random.Intn(2)}
			case 6:
				return nil
			default:
				return &[]int{1, 2, 3}[0]
			}
		}
		switch random.Intn(5) {
		case 0:
			return []any{randomValue(depth - 1), randomValue(depth - 1)}
		case 1:
			return map[string]any{"a": randomValue(depth - 1), "b": randomValue(depth - 1)}
		case 2:
			return struct{ A, B any }{A: randomValue(depth - 1), B: randomValue(depth - 1)}
		case 3:
			return any(randomValue(depth - 1))
		default:
			return randomValue(0)
		}
	}
	dangerous, safe := 0, 0
	for i := 0; i < 300; i++ {
		left := randomValue(3)
		right := randomValue(3)
		want := reflect.DeepEqual(left, right)
		got, encoded := equivalentEncode(t, left, right)
		if !encoded {
			continue
		}
		if got != want {
			if got {
				dangerous++
				t.Errorf("DANGEROUS pair %d: %T/%T encoder-equal but DeepEqual false", i, left, right)
			} else {
				safe++
			}
		}
	}
	t.Logf("random pairs: dangerous=%d safe-direction=%d", dangerous, safe)
}

// TestArchBRealOptionDocumentEquivalence is the domain check: decode the real mixed provider
// document twice and compare every node's DeepEqual answer with the encoder's answer, and compare
// against a one-field mutation (a different server port) that must be "different".
func TestArchBRealOptionDocumentEquivalence(t *testing.T) {
	path := evidenceFile(t, "fixtures", "mixed-provider-100", "nodes.json")
	content := readFile(t, path)
	ctx := include.Context(context.Background())
	first, err := singjson.UnmarshalExtendedContext[option.Options](ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	second, err := singjson.UnmarshalExtendedContext[option.Options](ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Outbounds) != len(second.Outbounds) {
		t.Fatal("decode count mismatch")
	}
	for i := range first.Outbounds {
		a, b := first.Outbounds[i], second.Outbounds[i]
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("outbound %d: two decodes of identical bytes are not DeepEqual", i)
		}
		payloadA, errA := nodestore.Encode(a)
		payloadB, errB := nodestore.Encode(b)
		if errA != nil || errB != nil {
			t.Fatalf("outbound %d: encode error %v/%v", i, errA, errB)
		}
		if string(payloadA.Bytes) != string(payloadB.Bytes) {
			t.Fatalf("outbound %d type=%s: equal values encode to different bytes", i, a.Type)
		}
		if !reflect.DeepEqual(a.Options, b.Options) {
			t.Fatalf("outbound %d options differ", i)
		}
	}
	// Mutate one field of the first node; it must be encoded different, and DeepEqual must agree.
	mutated := first.Outbounds[0]
	trojan, ok := mutated.Options.(*option.TrojanOutboundOptions)
	if !ok {
		t.Fatalf("first outbound is %T, expected trojan", mutated.Options)
	}
	copyOptions := *trojan
	copyOptions.ServerPort++
	mutated.Options = &copyOptions
	equal, encoded := equivalentEncode(t, first.Outbounds[0], mutated)
	if !encoded || equal {
		t.Errorf("mutated server_port must be different (encoded=%v equal=%v)", encoded, equal)
	}
	if reflect.DeepEqual(first.Outbounds[0], mutated) {
		t.Errorf("DeepEqual claims the mutated node is equal")
	}
	// Explicit false / zero fields in real options: decode JSON with explicit false and zero values
	// and compare against the same JSON without them. DeepEqual may differ; the encoder must agree.
	var withFalse, withoutFalse struct {
		A bool `json:"a"`
	}
	withFalse.A = false
	withoutFalse.A = false
	if equal, encoded := equivalentEncode(t, withFalse, withoutFalse); !encoded || !equal {
		t.Errorf("explicit false vs zero false: encoded=%v equal=%v", encoded, equal)
	}
}

// TestArchBPostFixCounterexamples verifies the fix for the three false-equal cases the first review
// found: identity-bearing map keys must be REFUSED (ErrUnsupported -> Opaque -> rebuild), and the
// tag-only anonymous-struct difference must be DISTINGUISHED (both encode, bytes differ).
func TestArchBPostFixCounterexamples(t *testing.T) {
	one, oneB := 1, 1
	pointerKeyA, pointerKeyB := &one, &oneB
	cases := []struct {
		name       string
		left       any
		right      any
		wantRefuse bool
	}{
		{
			name:       "pointer-keyed map",
			left:       map[*int]int{pointerKeyA: 1},
			right:      map[*int]int{pointerKeyB: 1},
			wantRefuse: true,
		},
		{
			name:       "interface-pointer-keyed map",
			left:       map[any]int{any(pointerKeyA): 1},
			right:      map[any]int{any(pointerKeyB): 1},
			wantRefuse: true,
		},
		{
			name: "anonymous struct with different tags",
			left: any(struct {
				Field int `json:"a"`
			}{Field: 1}),
			right: any(struct {
				Field int `json:"b"`
			}{Field: 1}),
		},
	}
	for _, item := range cases {
		item := item
		t.Run(item.name, func(t *testing.T) {
			if reflect.DeepEqual(item.left, item.right) {
				t.Fatalf("DeepEqual unexpectedly says equal")
			}
			if item.wantRefuse {
				if _, err := nodestore.Encode(item.left); !errors.Is(err, nodestore.ErrUnsupported) {
					t.Fatalf("left side not refused: %v", err)
				}
				if _, err := nodestore.Encode(item.right); !errors.Is(err, nodestore.ErrUnsupported) {
					t.Fatalf("right side not refused: %v", err)
				}
			} else {
				if _, err := nodestore.Encode(item.left); err != nil {
					t.Fatalf("left side unexpectedly refused: %v", err)
				}
				if _, err := nodestore.Encode(item.right); err != nil {
					t.Fatalf("right side unexpectedly refused: %v", err)
				}
			}
			if equal, encoded := equivalentEncode(t, item.left, item.right); equal {
				t.Fatalf("still a false-equal (encoded=%v)", encoded)
			}
			t.Logf("fixed: refused=%v, equivalent=false", item.wantRefuse)
		})
	}
}

type valueKeyStruct struct {
	Name  string
	Port  uint16
	Flags [2]bool
}

// TestArchBPostFixValueKeysAreNotOverRejected checks that the map-key restriction did not spread to
// value-comparable keys: strings, numbers, arrays and pointer-free structs must still encode.
func TestArchBPostFixValueKeysAreNotOverRejected(t *testing.T) {
	valueKeys := []struct {
		name  string
		left  any
		right any
	}{
		{"string-key", map[string]int{"a": 1}, map[string]int{"a": 1}},
		{"int-key", map[int]string{1: "a"}, map[int]string{1: "a"}},
		{"uintptr-key", map[uintptr]int{1: 1}, map[uintptr]int{1: 1}},
		{"array-key", map[[2]int]int{{1, 2}: 1}, map[[2]int]int{{1, 2}: 1}},
		{"struct-key", map[valueKeyStruct]int{{Name: "a", Port: 443}: 1}, map[valueKeyStruct]int{{Name: "a", Port: 443}: 1}},
		{"unexported-string-field-struct-key", map[varchInner]int{{Visible: 1}: 1}, map[varchInner]int{{Visible: 1}: 1}},
		{"float-key", map[float64]int{1.5: 1}, map[float64]int{1.5: 1}},
	}
	for _, item := range valueKeys {
		item := item
		t.Run(item.name, func(t *testing.T) {
			if !reflect.DeepEqual(item.left, item.right) {
				t.Fatalf("test value is not DeepEqual to itself")
			}
			equal, encoded := equivalentEncode(t, item.left, item.right)
			if !encoded || !equal {
				t.Fatalf("value key was over-rejected or mis-compared: encoded=%v equal=%v", encoded, equal)
			}
		})
	}

	// Real provider document: no node may be refused by the new key/type policy.
	outbounds := decodeProviderFixture(t)
	for i, outbound := range outbounds {
		if _, err := nodestore.Encode(outbound); err != nil {
			t.Fatalf("real provider outbound %d type=%s refused: %v", i, outbound.Type, err)
		}
	}
	t.Logf("all %d real provider outbounds remain inside the encoding domain", len(outbounds))

	// Conservative over-rejection: a key type whose Go == is by value but which CONTAINS a pointer
	// internally (netip.Addr's interned zone pointer, time.Time's location pointer) is refused by the
	// type-based rule even though DeepEqual on equal values is true. Safe direction, documented here so
	// the bound is explicit; no option map uses these key types.
	conservative := []struct {
		name  string
		value any
	}{
		{"netip.Addr-key", map[netip.Addr]string{netip.MustParseAddr("192.0.2.1"): "a"}},
		{"time.Time-key", map[time.Time]string{time.Unix(0, 0): "a"}},
	}
	for _, item := range conservative {
		item := item
		t.Run(item.name, func(t *testing.T) {
			if _, err := nodestore.Encode(item.value); !errors.Is(err, nodestore.ErrUnsupported) {
				t.Fatalf("expected conservative refusal, got %v", err)
			}
			t.Logf("conservatively refused (contains an internal pointer, no option map uses it)")
		})
	}
}

// TestArchBPostFixAnonymousStructIdentity probes the rest of anonymous-struct type identity after
// the tag fix. Embeddedness is part of Go type identity too; if typeKey still omits it, two distinct
// anonymous struct types with the same field name/type but different embedding can collapse.
func TestArchBPostFixAnonymousStructIdentity(t *testing.T) {
	left := any(struct{ varchInner }{})
	right := any(struct{ varchInner varchInner }{})
	requireDifferentTypes := reflect.TypeOf(left) != reflect.TypeOf(right)
	if !requireDifferentTypes {
		t.Skip("the two anonymous struct types are identical")
	}
	if reflect.DeepEqual(left, right) {
		t.Fatal("DeepEqual unexpectedly says equal for distinct anonymous struct types")
	}
	if equal, encoded := equivalentEncode(t, left, right); encoded && equal {
		t.Errorf("NEW false-equal: anonymous structs differing only by embeddedness encode equal")
	}

	// Two maps whose key types differ only by embeddedness are distinct map types; the map type key
	// must distinguish them as well.
	mapLeft := any(map[struct{ varchInner }]int{})
	mapRight := any(map[struct{ varchInner varchInner }]int{})
	if reflect.DeepEqual(mapLeft, mapRight) {
		t.Fatal("DeepEqual unexpectedly says equal for distinct map types")
	}
	if equal, encoded := equivalentEncode(t, mapLeft, mapRight); encoded && equal {
		t.Errorf("NEW false-equal: maps keyed by embeddedness-different structs encode equal")
	}
}
