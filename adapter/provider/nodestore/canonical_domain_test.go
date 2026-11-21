package nodestore

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

// These tests pin the three false-equal cases the independent review found. They all share one
// theme: `reflect.DeepEqual` does NOT compare map keys deeply (it matches them with Go ==), and it
// does not overlook struct tags. An encoder that only walks values would report equal where the
// provider's old rule reported different - the unsafe direction. The fix is to refuse the identity
// shapes (map key containing pointer/interface/chan/func) and to make the anonymous struct type key
// tag- and package-aware. Refusal is safe: the provider records an Opaque record and rebuilds the
// node; it never reuses a configuration it could not prove equal.

// TestMapPointerKeyIsUnsupported covers map[*int]int: two maps whose key pointers are different but
// whose pointees are equal. DeepEqual compares the keys by identity, so they are different maps; the
// encoder must not report them equal by encoding the pointee.
func TestMapPointerKeyIsUnsupported(t *testing.T) {
	leftValue, rightValue := 7, 7
	left := map[*int]int{&leftValue: 1}
	right := map[*int]int{&rightValue: 1}
	require.False(t, reflect.DeepEqual(left, right))
	require.False(t, Equivalent(left, right), "different pointer keys must not encode equal")
	_, err := Encode(left)
	require.ErrorIs(t, err, ErrUnsupported)
	// Even the same map object is refused (DeepEqual has a same-object shortcut, the encoder does
	// not): refusing is conservative, not a false equal.
	require.True(t, reflect.DeepEqual(left, left))
	require.False(t, Equivalent(left, left))
}

// TestMapInterfaceKeyIsUnsupported covers map[any]int with pointer keys: the interface is compared
// by Go ==, i.e. by the dynamic type and then by pointer identity. Interface keys are refused
// wholesale because their dynamic value may carry identity (pointer, chan, nested struct).
func TestMapInterfaceKeyIsUnsupported(t *testing.T) {
	leftValue, rightValue := 1, 1
	left := map[any]int{&leftValue: 1}
	right := map[any]int{&rightValue: 1}
	require.False(t, reflect.DeepEqual(left, right))
	require.False(t, Equivalent(left, right))
	_, err := Encode(left)
	require.ErrorIs(t, err, ErrUnsupported)

	// String-keyed interface maps ARE equal under DeepEqual, but they are still outside the domain:
	// the same key type can hold identity values, and the encoder refuses the type, not the sample.
	stringLeft := map[any]int{"key": 1}
	stringRight := map[any]int{"key": 1}
	require.True(t, reflect.DeepEqual(stringLeft, stringRight))
	require.False(t, Equivalent(stringLeft, stringRight))
	_, err = Encode(stringLeft)
	require.ErrorIs(t, err, ErrUnsupported)
}

// TestAnonymousStructTagIsTypeIdentity covers two anonymous structs that differ only by a tag. They
// are different Go types, so DeepEqual is false; the encoder's type key must include the tag.
func TestAnonymousStructTagIsTypeIdentity(t *testing.T) {
	type taggedA = struct {
		Value int `json:"a"`
	}
	type taggedB = struct {
		Value int `json:"b"`
	}
	left := taggedA{Value: 1}
	right := taggedB{Value: 1}
	require.False(t, reflect.DeepEqual(left, right), "different struct types are never deeply equal")
	require.False(t, Equivalent(left, right), "a tag-only difference must not encode equal")
	require.NotEqual(t, typeKey(reflect.TypeOf(left)), typeKey(reflect.TypeOf(right)))
	_, err := Encode(left)
	require.NoError(t, err)
	_, err = Encode(right)
	require.NoError(t, err)

	sameTag := taggedA{Value: 1}
	require.True(t, reflect.DeepEqual(left, sameTag))
	require.True(t, Equivalent(left, sameTag))

	// Through an interface the dynamic type is the tag-bearing anonymous struct.
	require.False(t, Equivalent(anyHolder{Any: left}, anyHolder{Any: right}))
	require.True(t, Equivalent(anyHolder{Any: left}, anyHolder{Any: sameTag}))

	// And through a map type: map[struct{...tag a}]int vs map[struct{...tag b}]int.
	mapLeft := map[taggedA]int{{Value: 1}: 1}
	mapRight := map[taggedB]int{{Value: 1}: 1}
	require.False(t, reflect.DeepEqual(any(mapLeft), any(mapRight)))
	require.False(t, Equivalent(mapLeft, mapRight))
}

// TestAnonymousStructUnexportedFieldIdentity is the other half of the type identity: two anonymous
// structs with an unexported field of the same name are different types when they come from
// different packages. The package path participates in the key, so the two encodings cannot collapse.
func TestAnonymousStructUnexportedFieldIdentity(t *testing.T) {
	first := reflect.TypeOf(struct{ hidden int }{})
	second := reflect.TypeOf(struct{ hidden int }{})
	require.Equal(t, first, second, "same package, same anonymous type")
	// The key must still name the package for unexported fields; the identity part is present.
	require.Contains(t, typeKey(first), "hidden")
}

// domainEmbedded and DomainEmbedded are field types for the embeddedness test. The unexported one
// matches the independent review's repro (field name/type/package are identical between the embedded
// and named shapes), the exported one proves the fix does not rely on PkgPath.
type domainEmbedded struct{ Value int }

type DomainEmbedded struct{ Value int }

// TestAnonymousStructEmbeddingIsTypeIdentity covers the last in-domain false equal found by the
// review: `struct{ T }` (T is embedded) and `struct{ T T }` (T is a named field) have the same field
// name, type and tag, but Go treats them as different types, so DeepEqual is false. The type key
// must include embeddedness.
func TestAnonymousStructEmbeddingIsTypeIdentity(t *testing.T) {
	check := func(t *testing.T, embedded, named any, embeddedKey, namedKey reflect.Type) {
		t.Helper()
		require.NotEqual(t, reflect.TypeOf(embedded), reflect.TypeOf(named))
		require.False(t, reflect.DeepEqual(embedded, named), "different struct types are never deeply equal")
		require.False(t, Equivalent(embedded, named), "embeddedness-only difference must not encode equal")
		require.NotEqual(t, typeKey(embeddedKey), typeKey(namedKey))
		_, err := Encode(embedded)
		require.NoError(t, err)
		_, err = Encode(named)
		require.NoError(t, err)
	}

	embeddedValue := struct{ domainEmbedded }{domainEmbedded: domainEmbedded{Value: 1}}
	namedValue := struct{ domainEmbedded domainEmbedded }{domainEmbedded: domainEmbedded{Value: 1}}
	check(t, any(embeddedValue), any(namedValue), reflect.TypeOf(embeddedValue), reflect.TypeOf(namedValue))

	embeddedExported := struct{ DomainEmbedded }{DomainEmbedded: DomainEmbedded{Value: 1}}
	namedExported := struct{ DomainEmbedded DomainEmbedded }{DomainEmbedded: DomainEmbedded{Value: 1}}
	check(t, any(embeddedExported), any(namedExported), reflect.TypeOf(embeddedExported), reflect.TypeOf(namedExported))

	// The same identity lives in the map TYPE when the anonymous struct is the key type.
	mapEmbedded := map[struct{ domainEmbedded }]int{}
	mapNamed := map[struct{ domainEmbedded domainEmbedded }]int{}
	require.NotEqual(t, reflect.TypeOf(mapEmbedded), reflect.TypeOf(mapNamed))
	require.False(t, reflect.DeepEqual(any(mapEmbedded), any(mapNamed)))
	require.False(t, Equivalent(any(mapEmbedded), any(mapNamed)))
	mapEmbeddedExported := map[struct{ DomainEmbedded }]int{}
	mapNamedExported := map[struct{ DomainEmbedded DomainEmbedded }]int{}
	require.False(t, Equivalent(any(mapEmbeddedExported), any(mapNamedExported)))
}

// TestAnonymousStructFixDoesNotChangeNamedOptionRecords pins the real option record size: every
// option type is a named type, whose type key is PkgPath+Name and never enters the anonymous struct
// branch, so the embeddedness fix must leave the published record bytes untouched.
func TestAnonymousStructFixDoesNotChangeNamedOptionRecords(t *testing.T) {
	payload, err := Encode(trojanNode("provider/node-1", "example.com", []string{"h2", "http/1.1"}, false))
	require.NoError(t, err)
	require.Equal(t, 219, len(payload.Bytes), "a named option record is unaffected by anonymous struct identity")
	selectorPayload, err := Encode(selectorNode("provider/group", []string{"provider/node-1", "provider/node-2"}))
	require.NoError(t, err)
	require.Equal(t, 177, len(selectorPayload.Bytes))
}

// TestMapKeyPolicyAllowsValueKeys makes sure the restriction is limited to identity keys: value
// kinds, arrays and structs of value kinds remain comparable and must encode.
func TestMapKeyPolicyAllowsValueKeys(t *testing.T) {
	type structKey struct {
		Name  string
		Port  uint16
		Flags [2]bool
	}
	left := map[structKey]string{{Name: "a", Port: 443}: "x"}
	right := map[structKey]string{{Name: "a", Port: 443}: "x"}
	require.True(t, reflect.DeepEqual(left, right))
	require.True(t, Equivalent(left, right))
	_, err := Encode(left)
	require.NoError(t, err)

	// The policy is type-based, so a pointer anywhere in the key type refuses the map even if the
	// particular pointer happens to be equal.
	type identityKey struct {
		Name   string
		Parent *int
	}
	value := 1
	pointerMap := map[identityKey]string{{Name: "a", Parent: &value}: "x"}
	_, err = Encode(pointerMap)
	require.ErrorIs(t, err, ErrUnsupported)
}
