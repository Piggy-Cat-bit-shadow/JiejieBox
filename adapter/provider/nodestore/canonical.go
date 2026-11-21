// Package nodestore keeps the provider's configuration records in a compact, immutable,
// generation-tagged form.
//
// # What a record is, and what it is not
//
// A record is the canonical byte encoding of one `option.Outbound` value: the exact input the
// provider used for change detection before this package existed. It is NOT a second copy of the
// decoded options tree. The provider compares a freshly decoded node against the stored bytes, so
// the decoded tree it received from the document can be collected after the apply - which is the
// whole point: the map of `option.Outbound` retained one decoded tree per node for the lifetime of
// the published generation.
//
// # Why bytes instead of a digest
//
// A digest alone cannot answer "is this the same configuration" without a complete equality
// decision. The canonical encoding is that decision, but it is a BOUNDED one: equal bytes mean
// `reflect.DeepEqual` would have reported true only inside the encoding's equivalence domain. The
// domain excludes values whose Go equality is identity rather than value: a map key type that
// contains a pointer, interface, channel, func or unsafe pointer is refused (DeepEqual matches map
// keys with `==`, not with its own deep walk), and anonymous structs carry their tags and unexported
// field package paths in the type key so two syntactically different struct types can never collapse.
// Outside the domain Encode returns ErrUnsupported; the provider turns that into an Opaque record and
// rebuilds the node, so the excluded shapes are never silently reported equal. The 64-bit hash stored
// next to each record is only a fast reject and is always confirmed by a byte comparison, so a
// collision can never make two different configurations share a record.
//
// # Why not re-marshal JSON
//
// JSON is not the identity of an options value. It drops `nil` versus empty distinctions, it
// applies defaults on decode, it loses integer/float precision at the edges, and a custom
// `UnmarshalJSON` may normalise a field. The encoder below walks the Go value reflectively and
// mirrors the semantics of `reflect.DeepEqual`, which is the rule the provider actually promised.
package nodestore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	// formatVersion prefixes every canonical payload so a payload from a future or foreign store
	// can never be compared against this one.
	formatVersion byte = 1
	// opaqueMarker marks a payload that is deliberately stable-unique rather than canonical. It is
	// outside the kind range, so an opaque payload can never be equal to a canonical one.
	opaqueMarker byte = 0xff
	// maxDepth bounds recursion. The option tree is shallow; a value deeper than this is treated as
	// unsupported (the provider rebuilds the node, which is the safe direction).
	maxDepth = 512
	// maxRecordBytes bounds one record. Real nodes are kilobytes; the cap only exists so a hostile
	// or corrupt document cannot make change detection allocate without bound. A value above the
	// cap is not an error the provider surfaces at start: it becomes an Opaque record, which can
	// never compare equal, so the node is rebuilt on every refresh but still works.
	maxRecordBytes = 16 << 20
)

var (
	// ErrUnsupported is returned when a value cannot be encoded without risking a false "equal".
	// Callers must treat it as "different": a rebuild is always semantically safe, a stale reuse is
	// not.
	ErrUnsupported = errors.New("nodestore: value cannot be canonically encoded")
)

// Payload is one canonical configuration record.
type Payload struct {
	Bytes []byte
	Hash  uint64
}

// Encode returns the canonical encoding of value.
//
// # It never panics
//
// The value comes from a parsed provider document, so it is untrusted input as far as this encoder
// is concerned: undocumented reflection corners (a not-in-heap pointer, a reflect.Value with an
// invalid kind, a structure the walker does not anticipate) must not be able to abort a provider
// refresh. The deferred recover turns any such corner into ErrUnsupported, which the provider treats
// as "different" and records as an Opaque record - the node is rebuilt, the provider still starts,
// and no configuration is ever silently reused.
func Encode(value any) (payload Payload, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			payload = Payload{}
			err = fmt.Errorf("%w: encoder panic: %v", ErrUnsupported, recovered)
		}
	}()
	encoder := encoder{visiting: make(map[visit]bool)}
	encoder.buf = append(encoder.buf, formatVersion)
	// The root type is written once. Every nested type is implied by the root type and the
	// interface values the encoding meets, so repeating a type key per struct would only inflate
	// the record.
	encoder.buf = appendString(encoder.buf, typeKey(reflect.TypeOf(value)))
	encoder.value(reflect.ValueOf(value), 0)
	if encoder.err != nil {
		return Payload{}, encoder.err
	}
	payload = Payload{Bytes: encoder.buf}
	payload.Hash = Hash(payload.Bytes)
	return payload, nil
}

// Equivalent reports whether a and b are equal under the provider's rule, which must match
// `reflect.DeepEqual` on the values the provider compared before - within the equivalence domain
// documented on the package. It is exposed for the differential test that holds the two rules
// against each other; production compares a payload against a stored record instead of encoding
// both sides. A value outside the domain (identity-bearing map key, cyclic structure, func/chan
// marker, over-budget value) makes Encode fail, and Equivalent then reports false - never a
// silent "equal".
func Equivalent(a, b any) bool {
	encodedA, err := Encode(a)
	if err != nil {
		return false
	}
	encodedB, err := Encode(b)
	if err != nil {
		return false
	}
	if encodedA.Hash != encodedB.Hash || len(encodedA.Bytes) != len(encodedB.Bytes) {
		return false
	}
	return string(encodedA.Bytes) == string(encodedB.Bytes)
}

// Opaque returns a payload that can never be equal to a canonical payload or to another opaque
// payload. It is the record of a configuration the encoder refused: change detection then always
// rebuilds that node, while the provider still owns its tag. Losing the tag would make the next
// document look like a foreign outbound and be refused, so ownership is preserved even when the
// comparison is not available.
func Opaque() Payload {
	payload := Payload{Bytes: make([]byte, 2+8)}
	payload.Bytes[0] = formatVersion
	payload.Bytes[1] = opaqueMarker
	binary.LittleEndian.PutUint64(payload.Bytes[2:], opaqueSequence.Add(1))
	payload.Hash = Hash(payload.Bytes)
	return payload
}

var opaqueSequence atomic.Uint64

// Hash is FNV-1a 64 over the canonical bytes. It is a fast reject only; every hit is confirmed by
// an exact byte comparison.
func Hash(payload []byte) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	hash := uint64(offset64)
	for _, b := range payload {
		hash ^= uint64(b)
		hash *= prime64
	}
	return hash
}

type visit struct {
	typ reflect.Type
	ptr uintptr
}

type encoder struct {
	buf      []byte
	err      error
	visiting map[visit]bool
}

func (e *encoder) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

func (e *encoder) value(v reflect.Value, depth int) {
	if e.err != nil {
		return
	}
	if !v.IsValid() {
		e.buf = append(e.buf, kindInvalid)
		return
	}
	if depth > maxDepth {
		e.fail(fmt.Errorf("%w: nesting deeper than %d", ErrUnsupported, maxDepth))
		return
	}
	if len(e.buf) > maxRecordBytes {
		e.fail(fmt.Errorf("%w: record exceeds %d bytes", ErrUnsupported, maxRecordBytes))
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		e.buf = append(e.buf, kindBool)
		if v.Bool() {
			e.buf = append(e.buf, 1)
		} else {
			e.buf = append(e.buf, 0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// `reflect.DeepEqual` compares integers by value. The width only matters for the value it
		// can hold, so one zig-zag encoding covers every width.
		e.buf = append(e.buf, kindInt)
		e.buf = binary.AppendVarint(e.buf, v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.buf = append(e.buf, kindUint)
		e.buf = binary.AppendUvarint(e.buf, v.Uint())
	case reflect.Float32, reflect.Float64:
		// `==` on floats says -0 == +0 and NaN != NaN. The encoding must agree: normalise -0, and
		// give every NaN its own token so it can never compare equal to anything, itself included.
		e.buf = append(e.buf, kindFloat)
		value := v.Float()
		if math.IsNaN(value) {
			e.buf = binary.LittleEndian.AppendUint64(e.buf, nextUniqueToken())
			return
		}
		if value == 0 {
			value = 0
		}
		e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(value))
	case reflect.Complex64, reflect.Complex128:
		e.buf = append(e.buf, kindComplex)
		value := v.Complex()
		realPart, imagPart := real(value), imag(value)
		if math.IsNaN(realPart) || math.IsNaN(imagPart) {
			e.buf = binary.LittleEndian.AppendUint64(e.buf, nextUniqueToken())
			return
		}
		if realPart == 0 {
			realPart = 0
		}
		if imagPart == 0 {
			imagPart = 0
		}
		e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(realPart))
		e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(imagPart))
	case reflect.String:
		encodedString := v.String()
		if len(encodedString) > maxRecordBytes-len(e.buf) {
			e.fail(fmt.Errorf("%w: record exceeds %d bytes", ErrUnsupported, maxRecordBytes))
			return
		}
		e.buf = append(e.buf, kindString)
		e.buf = binary.AppendUvarint(e.buf, uint64(len(encodedString)))
		e.buf = append(e.buf, encodedString...)
	case reflect.Pointer:
		if v.IsNil() {
			e.buf = append(e.buf, kindNilPointer)
			return
		}
		if e.enter(v, depth) {
			return
		}
		e.buf = append(e.buf, kindPointer)
		e.value(v.Elem(), depth+1)
		e.leave(v)
	case reflect.Interface:
		if v.IsNil() {
			e.buf = append(e.buf, kindNilInterface)
			return
		}
		e.buf = append(e.buf, kindInterface)
		e.buf = appendString(e.buf, typeKey(v.Elem().Type()))
		e.value(v.Elem(), depth+1)
	case reflect.Struct:
		e.structValue(v, depth)
	case reflect.Slice:
		if v.IsNil() {
			e.buf = append(e.buf, kindNilSlice)
			return
		}
		// An empty non-nil slice is a different value from a nil slice under DeepEqual, so the nil
		// case above and this branch must never collapse into each other.
		if v.Len() > maxRecordBytes {
			// Every element occupies at least one marker byte, except zero-sized elements, which
			// would otherwise let a huge length spin the walker without growing the record.
			e.fail(fmt.Errorf("%w: slice length %d exceeds the record budget", ErrUnsupported, v.Len()))
			return
		}
		if v.Len() > 0 && e.enter(v, depth) {
			return
		}
		e.buf = append(e.buf, kindSlice)
		e.buf = binary.AppendUvarint(e.buf, uint64(v.Len()))
		for i := 0; i < v.Len(); i++ {
			e.value(v.Index(i), depth+1)
		}
		if v.Len() > 0 {
			e.leave(v)
		}
	case reflect.Array:
		e.buf = append(e.buf, kindArray)
		for i := 0; i < v.Len(); i++ {
			e.value(v.Index(i), depth+1)
		}
	case reflect.Map:
		if v.IsNil() {
			e.buf = append(e.buf, kindNilMap)
			return
		}
		if v.Len() > maxRecordBytes {
			e.fail(fmt.Errorf("%w: map length %d exceeds the record budget", ErrUnsupported, v.Len()))
			return
		}
		if e.enter(v, depth) {
			return
		}
		e.mapValue(v, depth)
		e.leave(v)
	case reflect.Func:
		// `reflect.DeepEqual` says func values are only equal when both are nil. A non-nil func is
		// therefore never equal to anything, including itself.
		if v.IsNil() {
			e.buf = append(e.buf, kindNilFunc)
		} else {
			e.buf = append(e.buf, kindFunc)
			e.buf = binary.LittleEndian.AppendUint64(e.buf, nextUniqueToken())
		}
	case reflect.Chan:
		// DeepEqual compares channels with ==, but a stored record outlives the channel it came
		// from, so an address could be reused after GC and produce a false "equal". Unique tokens
		// make such a value always rebuild instead - conservative, never stale.
		e.buf = append(e.buf, kindUnique)
		if v.IsNil() {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.buf = binary.LittleEndian.AppendUint64(e.buf, nextUniqueToken())
		}
	case reflect.UnsafePointer:
		e.buf = append(e.buf, kindUnique)
		if v.IsNil() {
			e.buf = append(e.buf, 0)
		} else {
			e.buf = append(e.buf, 1)
			e.buf = binary.LittleEndian.AppendUint64(e.buf, nextUniqueToken())
		}
	default:
		e.fail(fmt.Errorf("%w: kind %s", ErrUnsupported, v.Kind()))
	}
}

// structValue encodes a struct as a field count and a presence bitmap over the fields that are not
// the zero value. The struct's type is not repeated here: it is fixed by the root type or by the
// enclosing interface value, which is where the type key is written.
//
// Skipping zero fields is exactly equality-preserving: a zero field compares equal to a zero field
// under DeepEqual, so leaving both out cannot make two different values look the same. It is also
// what makes the record smaller than the decoded tree: option structs have many fields and almost
// all of them are zero for any one node.
func (e *encoder) structValue(v reflect.Value, depth int) {
	fieldCount := v.NumField()
	e.buf = append(e.buf, kindStruct)
	e.buf = binary.AppendUvarint(e.buf, uint64(fieldCount))
	bitmapStart := len(e.buf)
	bitmapLength := (fieldCount + 7) / 8
	e.buf = append(e.buf, make([]byte, bitmapLength)...)
	for i := 0; i < fieldCount; i++ {
		field := v.Field(i)
		// Field(i) is readable for unexported fields for every operation used here (IsZero and the
		// kind accessors); Interface() is deliberately never called on it.
		if field.IsZero() {
			continue
		}
		e.buf[bitmapStart+i/8] |= 1 << (i % 8)
		e.value(field, depth+1)
	}
}

// mapValue encodes map entries sorted by canonical key bytes, because map iteration order is
// random while DeepEqual's answer is not. Entries are encoded into scratch buffers so they can be
// sorted before being appended.
//
// # Why map keys are restricted
//
// `reflect.DeepEqual` matches map entries with Go `==` on the keys, not with its own deep
// comparison. A struct or array key is compared field by field, and a pointer or interface key is
// compared by identity. Encoding a pointer key by its pointee would therefore report two maps with
// distinct-but-equal key pointers as equal, which DeepEqual calls different. The safe direction is
// to refuse the whole map: the provider records an Opaque record and rebuilds that node instead of
// trusting a comparison that is not exact. Value types can still be keys, including named types,
// arrays and structs of value types.
func (e *encoder) mapValue(v reflect.Value, depth int) {
	keyType := v.Type().Key()
	if !canonicalMapKeyType(keyType) {
		e.fail(fmt.Errorf("%w: map key type %s compares by identity or interface, so its canonical bytes cannot preserve map-key equality", ErrUnsupported, keyType))
		return
	}
	type entry struct {
		key   []byte
		value []byte
	}
	entries := make([]entry, 0, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		key := e.scratch(iter.Key(), depth+1)
		if e.err != nil {
			return
		}
		value := e.scratch(iter.Value(), depth+1)
		if e.err != nil {
			return
		}
		entries = append(entries, entry{key: key, value: value})
	}
	sort.Slice(entries, func(i, j int) bool {
		return string(entries[i].key) < string(entries[j].key)
	})
	e.buf = append(e.buf, kindMap)
	e.buf = binary.AppendUvarint(e.buf, uint64(len(entries)))
	for _, item := range entries {
		e.buf = binary.AppendUvarint(e.buf, uint64(len(item.key)))
		e.buf = append(e.buf, item.key...)
		e.buf = append(e.buf, item.value...)
	}
}

// scratch encodes v into a fresh buffer, sharing the cycle-detection state with the main encoder.
func (e *encoder) scratch(v reflect.Value, depth int) []byte {
	saved := e.buf
	e.buf = nil
	e.value(v, depth)
	out := e.buf
	e.buf = saved
	return out
}

func (e *encoder) enter(v reflect.Value, depth int) bool {
	key := visit{typ: v.Type(), ptr: v.Pointer()}
	if e.visiting[key] {
		// `reflect.DeepEqual` has a pointer shortcut for the self-comparison case and otherwise
		// tracks visited pairs; a genuine cycle cannot be linearised into bytes without a more
		// expensive pair encoding. Refusing the comparison is the safe direction: the provider
		// rebuilds the node.
		e.fail(fmt.Errorf("%w: cyclic value at %s", ErrUnsupported, v.Type()))
		return true
	}
	e.visiting[key] = true
	_ = depth
	return false
}

func (e *encoder) leave(v reflect.Value) {
	delete(e.visiting, visit{typ: v.Type(), ptr: v.Pointer()})
}

func appendString(buf []byte, value string) []byte {
	buf = binary.AppendUvarint(buf, uint64(len(value)))
	return append(buf, value...)
}

var uniqueToken atomic.Uint64

func nextUniqueToken() uint64 {
	return uniqueToken.Add(1)
}

// Kind markers. Values are never persisted across a process, so only the decoder (the comparison)
// and this encoder need to agree.
const (
	kindInvalid byte = iota
	kindBool
	kindInt
	kindUint
	kindFloat
	kindComplex
	kindString
	kindNilPointer
	kindPointer
	kindNilInterface
	kindInterface
	kindStruct
	kindNilSlice
	kindSlice
	kindArray
	kindNilMap
	kindMap
	kindNilFunc
	kindFunc
	kindUnique
)

// canonicalMapKeyType reports whether Go `==` on this key type is injective on the canonical
// encoding of the value. Value kinds are; pointers, interfaces, channels, funcs and unsafe
// pointers have identity semantics that the encoding deliberately drops (a pointer key is encoded
// by what it points at), so a map keyed by any of them is outside the equivalence domain.
func canonicalMapKeyType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.String:
		return true
	case reflect.Array:
		return canonicalMapKeyType(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if !canonicalMapKeyType(t.Field(i).Type) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// typeKey returns a stable identity for a type. `reflect.Type.String()` alone is not enough: two
// packages with the same package name would print the same qualified name. Named types therefore
// carry their package path, and composite types are built recursively from their elements.
//
// Anonymous structs need more than their fields: the struct tag is part of the type identity, an
// embedded field is not the same field as a named one with the same name/type/tag, and unexported
// field names are package-qualified. Two anonymous structs that differ only by a tag or only by
// embeddedness are DIFFERENT types (DeepEqual on interfaces holding them is false, and they are not
// interchangeable map keys). Every part is length-prefixed so a field name, tag or nested type key
// cannot splice into another type's key.
func typeKey(t reflect.Type) string {
	if t == nil {
		return ""
	}
	if t.Name() != "" {
		return t.PkgPath() + "." + t.Name()
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + typeKey(t.Elem())
	case reflect.Slice:
		return "[]" + typeKey(t.Elem())
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), typeKey(t.Elem()))
	case reflect.Map:
		return "map[" + typeKey(t.Key()) + "]" + typeKey(t.Elem())
	case reflect.Chan:
		return "chan " + typeKey(t.Elem())
	case reflect.Struct:
		var builder strings.Builder
		builder.WriteString("struct{")
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			builder.WriteString(identityPart(field.Name))
			builder.WriteString(identityPart(field.PkgPath))
			builder.WriteString(identityPart(typeKey(field.Type)))
			builder.WriteString(identityPart(string(field.Tag)))
			// Embeddedness is part of Go type identity: `struct{ T }` (embedded field named T) and
			// `struct{ T T }` (named field named T) have the same field name, type and tag, but are
			// different types and are never deeply equal.
			builder.WriteString(identityPart(strconv.FormatBool(field.Anonymous)))
		}
		builder.WriteString("}")
		return builder.String()
	case reflect.Interface:
		return t.String()
	default:
		return t.String()
	}
}

func identityPart(value string) string {
	return strconv.Itoa(len(value)) + ":" + value
}
