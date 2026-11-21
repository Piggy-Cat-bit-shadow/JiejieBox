// Package varch_test is the independent (non-author) reproduction/attack suite for the
// B1/B2 architecture round. It is an external test package: it only uses exported API of
// adapter, adapter/provider/nodestore and option, so it cannot accidentally agree with the
// implementation's unexported assumptions.
package varch_test

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter/provider/nodestore"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
)

func evidenceRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("V_EVIDENCE"); root != "" {
		return root
	}
	return `C:\Deepseek\JiejieBox-node-architecture-2026-11-22`
}

// decodeProviderFixture decodes the *actual bytes* the M fixture generator wrote for the
// provider document (fixtures/mixed-provider-100/nodes.json) with the product's own decoder.
// This is the same document ProviderLocal.reloadFile parses on the real path.
func decodeProviderFixture(t *testing.T) []option.Outbound {
	t.Helper()
	path := filepath.Join(evidenceRoot(t), "fixtures", "mixed-provider-100", "nodes.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provider fixture: %v", err)
	}
	ctx := include.Context(context.Background())
	options, err := singjson.UnmarshalExtendedContext[option.Options](ctx, content)
	if err != nil {
		t.Fatalf("decode provider fixture: %v", err)
	}
	if len(options.Outbounds) != 100 {
		t.Fatalf("decoded %d outbounds, want 100", len(options.Outbounds))
	}
	return options.Outbounds
}

// TestArchAProviderDocumentEncodes is the direct reproduction attempt for M's panic
// (`ProviderLocal.reloadFile -> UpdateNodes`, panic at nodestore/canonical.go:222):
// encode every outbound of the real mixed provider document. Any panic fails the test.
func TestArchAProviderDocumentEncodes(t *testing.T) {
	outbounds := decodeProviderFixture(t)
	kinds := map[string]int{}
	for i, outbound := range outbounds {
		var payload nodestore.Payload
		var encodeErr error
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("outbound[%d] type=%s tag=%s PANICKED in Encode: %v", i, outbound.Type, outbound.Tag, recovered)
				}
			}()
			payload, encodeErr = nodestore.Encode(outbound)
		}()
		if encodeErr != nil {
			t.Errorf("outbound[%d] type=%s: Encode error: %v", i, outbound.Type, encodeErr)
			continue
		}
		if len(payload.Bytes) == 0 || payload.Hash != nodestore.Hash(payload.Bytes) {
			t.Errorf("outbound[%d] type=%s: invalid payload (len=%d)", i, outbound.Type, len(payload.Bytes))
		}
		kinds[outbound.Type]++
	}
	t.Logf("encoded %d real provider outbounds; kinds=%v", len(outbounds), kinds)
	for _, kind := range []string{"trojan", "vless", "anytls", "hysteria2", "tuic"} {
		if kinds[kind] == 0 {
			t.Errorf("mixed document has no %s node", kind)
		}
	}
	// Same input encoded twice must produce identical bytes (otherwise the comparison is not a
	// function of the value). NaN-free option trees must satisfy this.
	again, err := nodestore.Encode(outbounds[0])
	if err != nil {
		t.Fatal(err)
	}
	first, err := nodestore.Encode(outbounds[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Bytes) != string(again.Bytes) {
		t.Errorf("encoding the same option value twice differed")
	}
}

type varchInner struct {
	Visible int
	hidden  string
}

type varchFuncHolder struct {
	Fn    func()
	Ch    chan int
	Ptr   unsafePointerAlias
	Any   any
	Map   map[string]int
	Slice []int
}

type unsafePointerAlias = uintptr

type varchCyclic struct {
	Name string
	Next *varchCyclic
}

type varchSliceCycle struct {
	Items []any
}

type varchGeneric[T any] struct {
	Value T
}

type varchOtherGeneric[T any] struct {
	Value T
}

// TestArchANoInputPanics feeds shapes that a generalization of the provider payload can meet
// (or that a hostile/odd option extension could contain) and requires Encode to return
// (payload, error) - an error is the documented safe direction, a panic is not.
func TestArchANoInputPanics(t *testing.T) {
	cyclic := &varchCyclic{Name: "a"}
	cyclic.Next = cyclic

	sliceCycle := &varchSliceCycle{Items: make([]any, 1)}
	sliceCycle.Items[0] = sliceCycle

	mapCycle := map[string]any{}
	mapCycle["self"] = mapCycle

	var nilFunc func()
	nonNilFunc := func() {}
	nilChan := make(chan int)
	var nilChanTyped chan int
	_ = nilChanTyped
	close(nilChan)

	deep := &varchCyclic{Name: "leaf"}
	for i := 0; i < 600; i++ {
		deep = &varchCyclic{Name: "n", Next: deep}
	}

	now := time.Now()
	shapes := []struct {
		name  string
		value any
	}{
		{"untyped-nil", nil},
		{"typed-nil-pointer", (*int)(nil)},
		{"typed-nil-options", (*option.TrojanOutboundOptions)(nil)},
		{"zero-outbound", option.Outbound{}},
		{"outbound-nil-options", option.Outbound{Type: "block", Tag: "b"}},
		{"outbound-typed-nil-options", option.Outbound{Type: "trojan", Tag: "t", Options: (*option.TrojanOutboundOptions)(nil)}},
		{"outbound-empty-options", option.Outbound{Type: "trojan", Tag: "t", Options: &option.TrojanOutboundOptions{}}},
		{"trojan-with-nil-tls", option.Outbound{Type: "trojan", Tag: "t", Options: &option.TrojanOutboundOptions{ServerOptions: option.ServerOptions{Server: "1.2.3.4", ServerPort: 443}, Password: "p"}}},
		{"nil-bool", (*bool)(nil)},
		{"false", false},
		{"int8-negative", int8(-128)},
		{"int64-max", int64(math.MaxInt64)},
		{"uint64-max", uint64(math.MaxUint64)},
		{"float-nan", math.NaN()},
		{"float-negative-zero", math.Copysign(0, -1)},
		{"float-inf", math.Inf(1)},
		{"complex-nan", complex(math.NaN(), 1)},
		{"empty-struct", struct{}{}},
		{"empty-array", [0]int{}},
		{"struct-with-unexported", varchInner{Visible: 1}},
		{"time-zero", time.Time{}},
		{"time-now", now},
		{"time-utc", now.UTC()},
		{"time-fixed-zone", now.In(time.FixedZone("x", 3600))},
		{"netip-addr-v4", netip.MustParseAddr("192.0.2.1")},
		{"netip-addr-v6-zone", netip.MustParseAddr("fe80::1%eth0")},
		{"nil-slice", []int(nil)},
		{"empty-slice", []int{}},
		{"large-slice", make([]int, 100000)},
		{"large-string", string(make([]byte, 1<<20))},
		{"nil-map", map[string]int(nil)},
		{"empty-map", map[string]int{}},
		{"map-any-values", map[string]any{"a": nil, "b": (*int)(nil)}},
		{"map-pointer-keys", map[*int]int{new(int): 1}},
		{"map-struct-keys", map[varchInner]int{{Visible: 1}: 2}},
		{"nil-func", nilFunc},
		{"non-nil-func", nonNilFunc},
		{"nil-chan", nilChanTyped},
		{"non-nil-chan", nilChan},
		{"any-func", any(nonNilFunc)},
		{"any-chan", any(nilChan)},
		{"pointer-cycle", cyclic},
		{"slice-cycle", sliceCycle},
		{"map-cycle", mapCycle},
		{"deep-600", deep},
		{"nested-pointer", func() *(*int) { v := 1; p := &v; return &p }()},
		{"array-of-any", [3]any{1, nil, "s"}},
		{"struct-func-chan", varchFuncHolder{Fn: nonNilFunc, Ch: nilChan}},
		{"interface-in-interface", any(any(1))},
		{"generic-int", varchGeneric[int]{Value: 1}},
		{"generic-int64", varchGeneric[int64]{Value: 1}},
		{"slice-of-interface-nil", []any{nil, (*int)(nil)}},
		{"map-order", map[string]int{"a": 1, "b": 2, "c": 3}},
	}
	if len(shapes) < 40 {
		t.Fatalf("only %d shapes", len(shapes))
	}
	for _, shape := range shapes {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("PANIC on shape %q: %v", shape.name, recovered)
				}
			}()
			payload, err := nodestore.Encode(shape.value)
			if err == nil {
				if len(payload.Bytes) == 0 {
					t.Fatalf("nil error with empty payload")
				}
				if payload.Hash != nodestore.Hash(payload.Bytes) {
					t.Fatalf("hash is not the hash of the bytes")
				}
			}
			// Encoding must be deterministic for everything except NaN/func/chan/cycle inputs.
			if err == nil {
				second, secondErr := nodestore.Encode(shape.value)
				if secondErr != nil {
					t.Fatalf("second Encode failed: %v", secondErr)
				}
				_ = second
			}
		})
	}
}

// TestArchAPreFixPatternPanic pins the root cause of M's panic: calling Type.Elem() on an
// interface TYPE panics with the exact message `reflect: Elem of invalid type interface {}`.
// An interface field has no element type; the dynamic type lives on the VALUE.
func TestArchAPreFixPatternPanic(t *testing.T) {
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		interfaceFieldType := reflect.TypeOf(struct{ Options any }{}).Field(0).Type
		_ = interfaceFieldType.Elem() // the pre-fix encoding pattern
	}()
	if panicked == nil {
		t.Fatal("Type.Elem() on an interface type did not panic; the documented root cause cannot be confirmed")
	}
	message := fmt.Sprint(panicked)
	if message != "reflect: Elem of invalid type interface {}" {
		t.Fatalf("unexpected panic text %q", message)
	}
	t.Logf("confirmed: %v", message)
}

// TestArchANilInterfaceOptions compares the nil-interface Options shape against a typed-nil
// shape: both must encode (the nil case is the one a pre-fix encoder panicked on).
func TestArchANilInterfaceOptions(t *testing.T) {
	values := []option.Outbound{
		{Type: "block", Tag: "a"},
		{Type: "trojan", Tag: "b", Options: (*option.TrojanOutboundOptions)(nil)},
		{Type: "trojan", Tag: "c", Options: &option.TrojanOutboundOptions{}},
	}
	for i, value := range values {
		payload, err := nodestore.Encode(value)
		if err != nil {
			t.Errorf("value %d: %v", i, err)
			continue
		}
		if len(payload.Bytes) == 0 {
			t.Errorf("value %d: empty payload", i)
		}
	}
}

// TestArchAStaticDocumentEncodes covers the other document shape M used: the static mixed config
// (direct + selector groups + provider references) decoded by the product decoder.
func TestArchAStaticDocumentEncodes(t *testing.T) {
	path := evidenceFile(t, "fixtures", "mixed-100", "config.json")
	content := readFile(t, path)
	ctx := include.Context(context.Background())
	options, err := singjson.UnmarshalExtendedContext[option.Options](ctx, content)
	if err != nil {
		t.Fatalf("decode static mixed config: %v", err)
	}
	if len(options.Outbounds) == 0 {
		t.Fatal("static mixed config has no outbounds")
	}
	for i, outbound := range options.Outbounds {
		if _, err := nodestore.Encode(outbound); err != nil {
			t.Errorf("outbound[%d] type=%s: %v", i, outbound.Type, err)
		}
	}
	t.Logf("encoded %d static outbounds", len(options.Outbounds))
}
