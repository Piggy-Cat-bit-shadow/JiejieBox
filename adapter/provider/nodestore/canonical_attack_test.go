package nodestore

import (
	"encoding/json"
	"math"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/stretchr/testify/require"
)

// Some structs in this file carry unexported fields on purpose: the encoder must read them without
// reflect.Value.Interface, exactly as it must for netip.Addr, time.Time and regexp.Regexp.

type unexportedFields struct {
	visible string
	count   int
	child   *unexportedFields
}

type anyHolder struct {
	Any any
}

type funcHolder struct {
	Call func()
}

type chanHolder struct {
	Channel chan int
}

type pointerHolder struct {
	Pointer unsafe.Pointer
}

type cyclic struct {
	Name string
	Next *cyclic
}

func cyclicValue() *cyclic {
	value := &cyclic{Name: "root"}
	value.Next = value
	return value
}

func deepValue(levels int) any {
	var value any = 1
	for i := 0; i < levels; i++ {
		value = &value
	}
	return value
}

func compiledRegexp(t *testing.T) badoption.Regexp {
	t.Helper()
	var expression badoption.Regexp
	require.NoError(t, json.Unmarshal([]byte(`"a.*b"`), &expression))
	return expression
}

func trojanWithTransport() option.Outbound {
	return option.Outbound{
		Type: "trojan",
		Tag:  "trojan-with-headers",
		Options: &option.TrojanOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "example.com", ServerPort: 443},
			Password:      "secret",
			Transport: &option.V2RayTransportOptions{
				Type: "http",
				HTTPOptions: option.V2RayHTTPOptions{
					Host:    badoption.Listable[string]{"example.com"},
					Path:    "/",
					Method:  "GET",
					Headers: map[string]badoption.Listable[string]{"X-Test": {"one", "two"}},
				},
			},
		},
	}
}

type encodeCase struct {
	name string
	// value is encoded. A panic fails the test: Encode must never panic.
	value any
	// wantEncoded reports whether a canonical encoding is expected. A value the encoder refuses
	// must return an error wrapping ErrUnsupported, never a partial payload.
	wantEncoded bool
	// selfEqual overrides the expected `Equivalent(value, value)`; nil means "same as
	// reflect.DeepEqual". It is only set for the two documented conservative shapes (channel and
	// unsafe pointer), where a record outlives its source and the encoder refuses to prove equality.
	selfEqual *bool
	// nonDeterministic marks the shapes the encoder deliberately gives a fresh unique token on
	// every call (non-nil func, chan, unsafe pointer). They can never compare equal, so the token is
	// the point; it also means two encodings of the same value differ.
	nonDeterministic bool
}

func TestEncodeNeverPanics(t *testing.T) {
	nonNilFunc := func() {}
	channel := make(chan int)
	scratch := 1
	nonNilPointer := unsafe.Pointer(&scratch)
	falseValue := false
	validTime := time.Unix(1_700_000_000, 1234).UTC()
	unexported := &unexportedFields{visible: "x", count: 2, child: &unexportedFields{visible: "y"}}
	regexpValue := compiledRegexp(t)

	cases := []encodeCase{
		{name: "nil interface", value: nil, wantEncoded: true},
		{name: "int", value: 7, wantEncoded: true},
		{name: "empty string", value: "", wantEncoded: true},
		{name: "invalid utf8 string", value: "\xff\xfe\x00text", wantEncoded: true},
		{name: "nil byte slice", value: []byte(nil), wantEncoded: true},
		{name: "empty byte slice", value: []byte{}, wantEncoded: true},
		{name: "byte slice", value: []byte{1, 2, 3}, wantEncoded: true},
		{name: "nil map", value: map[string]string(nil), wantEncoded: true},
		{name: "empty map", value: map[string]string{}, wantEncoded: true},
		{name: "map", value: map[string]string{"a": "1", "b": "2"}, wantEncoded: true},
		{name: "empty struct", value: struct{}{}, wantEncoded: true},
		{name: "empty outbound envelope", value: option.Outbound{}, wantEncoded: true},
		{name: "outbound without options", value: option.Outbound{Type: "trojan", Tag: "a"}, wantEncoded: true},
		{name: "trojan options", value: option.Outbound{Type: "trojan", Tag: "a", Options: &option.TrojanOutboundOptions{}}, wantEncoded: true},
		{name: "trojan typed nil options", value: option.Outbound{Type: "trojan", Tag: "a", Options: (*option.TrojanOutboundOptions)(nil)}, wantEncoded: true},
		{name: "vless options", value: option.Outbound{Type: "vless", Tag: "v", Options: &option.VLESSOutboundOptions{}}, wantEncoded: true},
		{name: "vless typed nil options", value: option.Outbound{Type: "vless", Tag: "v", Options: (*option.VLESSOutboundOptions)(nil)}, wantEncoded: true},
		{name: "anytls options", value: option.Outbound{Type: "anytls", Tag: "a", Options: &option.AnyTLSOutboundOptions{}}, wantEncoded: true},
		{name: "hysteria2 options", value: option.Outbound{Type: "hysteria2", Tag: "h", Options: &option.Hysteria2OutboundOptions{}}, wantEncoded: true},
		{name: "tuic options", value: option.Outbound{Type: "tuic", Tag: "t", Options: &option.TUICOutboundOptions{}}, wantEncoded: true},
		{name: "vmess options", value: option.Outbound{Type: "vmess", Tag: "m", Options: &option.VMessOutboundOptions{}}, wantEncoded: true},
		{name: "shadowsocks options", value: option.Outbound{Type: "shadowsocks", Tag: "s", Options: &option.ShadowsocksOutboundOptions{}}, wantEncoded: true},
		{name: "http options", value: option.Outbound{Type: "http", Tag: "p", Options: &option.HTTPOutboundOptions{}}, wantEncoded: true},
		{name: "socks options", value: option.Outbound{Type: "socks", Tag: "k", Options: &option.SOCKSOutboundOptions{}}, wantEncoded: true},
		{name: "direct options", value: option.Outbound{Type: "direct", Tag: "d", Options: &option.DirectOutboundOptions{}}, wantEncoded: true},
		{name: "selector options", value: option.Outbound{Type: "selector", Tag: "g", Options: &option.SelectorOutboundOptions{Outbounds: []string{"a", "b"}}}, wantEncoded: true},
		{name: "urltest options with compiled regexp", value: option.Outbound{Type: "urltest", Tag: "u", Options: &option.URLTestOutboundOptions{Include: &regexpValue}}, wantEncoded: true},
		{name: "load balance options", value: option.Outbound{Type: "loadbalance", Tag: "l", Options: &option.LoadBalanceOutboundOptions{Outbounds: []string{"a"}}}, wantEncoded: true},
		{name: "stub options", value: option.Outbound{Type: "block", Tag: "b", Options: &option.StubOptions{}}, wantEncoded: true},
		{name: "trojan with transport headers", value: trojanWithTransport(), wantEncoded: true},
		{name: "typed nil regexp pointer", value: (*badoption.Regexp)(nil), wantEncoded: true},
		{name: "any holding nil", value: anyHolder{}, wantEncoded: true},
		{name: "any holding typed nil pointer", value: anyHolder{Any: (*int)(nil)}, wantEncoded: true},
		{name: "any holding string", value: anyHolder{Any: "text"}, wantEncoded: true},
		{name: "any holding int64", value: anyHolder{Any: int64(1)}, wantEncoded: true},
		{name: "invalid netip addr", value: netip.Addr{}, wantEncoded: true},
		{name: "netip v4 addr", value: netip.AddrFrom4([4]byte{198, 51, 100, 7}), wantEncoded: true},
		{name: "netip v6 zoned addr", value: netip.AddrFrom16([16]byte{0xfe, 0x80}).WithZone("eth0"), wantEncoded: true},
		{name: "zero time", value: time.Time{}, wantEncoded: true},
		{name: "time with monotonic and location", value: validTime, wantEncoded: true},
		{name: "unexported fields", value: unexported, wantEncoded: true},
		{name: "nil func field", value: funcHolder{}, wantEncoded: true},
		{name: "non-nil func field", value: funcHolder{Call: nonNilFunc}, wantEncoded: true, selfEqual: &falseValue, nonDeterministic: true},
		{name: "nil channel field", value: chanHolder{}, wantEncoded: true},
		{name: "non-nil channel field", value: chanHolder{Channel: channel}, wantEncoded: true, selfEqual: &falseValue, nonDeterministic: true},
		{name: "nil unsafe pointer field", value: pointerHolder{}, wantEncoded: true},
		{name: "non-nil unsafe pointer field", value: pointerHolder{Pointer: nonNilPointer}, wantEncoded: true, selfEqual: &falseValue, nonDeterministic: true},
		{name: "float nan", value: math.NaN(), wantEncoded: true, selfEqual: &falseValue, nonDeterministic: true},
		{name: "reflection value", value: reflect.Value{}, wantEncoded: true},
		{name: "slice of outbounds", value: []option.Outbound{{Type: "trojan", Tag: "a"}, {Type: "direct", Tag: "b", Options: &option.DirectOutboundOptions{}}}, wantEncoded: true},
		{name: "map of outbounds", value: map[string]option.Outbound{"a": {Type: "direct", Tag: "a"}}, wantEncoded: true},
		{name: "pointer to interface", value: new(any), wantEncoded: true},
		{name: "cyclic pointer", value: cyclicValue(), wantEncoded: false},
		{name: "deeper than limit", value: deepValue(maxDepth + 64), wantEncoded: false},
		{name: "huge string", value: strings.Repeat("x", maxRecordBytes+1), wantEncoded: false},
		{name: "huge zero-sized slice", value: make([]struct{}, maxRecordBytes+1), wantEncoded: false},
		{name: "huge map", value: make(map[int]struct{}, 1024), wantEncoded: true},
		{name: "value with boolean", value: struct{ Enabled bool }{Enabled: true}, wantEncoded: true},
	}
	require.GreaterOrEqual(t, len(cases), 30, "the property table must cover at least 30 shapes")

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// The call itself must not panic; a panic fails the subtest with the stack.
			payload, err := Encode(testCase.value)
			if !testCase.wantEncoded {
				require.ErrorIs(t, err, ErrUnsupported)
				require.Empty(t, payload.Bytes)
				require.Zero(t, payload.Hash)
				// An unsupported value can never be reported equal, not even to itself.
				require.False(t, Equivalent(testCase.value, testCase.value))
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, payload.Bytes)
			require.Equal(t, formatVersion, payload.Bytes[0])
			require.Equal(t, Hash(payload.Bytes), payload.Hash)

			// Encoding is deterministic for every encodable shape except the three unique-token
			// shapes (non-nil func/chan/unsafe pointer), which are never equal by design.
			if !testCase.nonDeterministic {
				second, err := Encode(testCase.value)
				require.NoError(t, err)
				require.Equal(t, payload.Bytes, second.Bytes)
				require.Equal(t, payload.Hash, second.Hash)
			}

			// An encoded value must compare consistently with the old rule; the only exceptions are
			// the two conservatively-unique shapes, which say "different" on purpose.
			expectedSelf := reflect.DeepEqual(testCase.value, testCase.value)
			if testCase.selfEqual != nil {
				expectedSelf = *testCase.selfEqual
			}
			require.Equal(t, expectedSelf, Equivalent(testCase.value, testCase.value))
		})
	}
}

// TestEncodeInterfaceFieldIsTheRegression is the minimal input of the production panic:
//
//	panic: reflect: Elem of invalid type interface {}
//
// The pre-fix encoder wrote the dynamic type of an interface value with
// `typeKey(v.Type().Elem())`. `reflect.Type.Elem` is not defined for interfaces, so the first
// `option.Outbound` with a non-nil `Options` - that is, every provider node - panicked inside
// UpdateNodes. The panic is unreachable now, and this test fails (panics) if the interface branch
// ever calls Type.Elem again.
func TestEncodeInterfaceFieldIsTheRegression(t *testing.T) {
	minimal := option.Outbound{
		Type:    "trojan",
		Tag:     "minimal",
		Options: &option.TrojanOutboundOptions{},
	}
	payload, err := Encode(minimal)
	require.NoError(t, err)
	require.NotEmpty(t, payload.Bytes)
	require.True(t, Equivalent(minimal, minimal))

	// A bare interface field, with no option type involved, is the smallest shape that hit it.
	held := anyHolder{Any: "text"}
	payload, err = Encode(held)
	require.NoError(t, err)
	require.NotEmpty(t, payload.Bytes)

	// The dynamic type is part of the identity: the same bytes under another type must differ.
	asInt := anyHolder{Any: int64(0)}
	asString := anyHolder{Any: ""}
	require.False(t, Equivalent(asInt, asString))
	require.True(t, Equivalent(asInt, anyHolder{Any: int64(0)}))
}
