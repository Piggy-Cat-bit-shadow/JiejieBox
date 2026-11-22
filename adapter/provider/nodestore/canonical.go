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
// Two narrower rules keep the domain exact where Go's own comparison is not a deep walk:
//
//   - A NaN is equal to nothing, including itself, and a NaN in either component makes the whole
//     complex value unequal to everything. Non-finite floats therefore use their own kind marker and
//     an explicit flag byte instead of the finite float data space, and a NaN's unique counter lives
//     inside that dedicated space. A finite float can never reproduce a non-finite record from the
//     counter's bits.
//   - The type key of a channel includes its direction, because `chan int`, `<-chan int` and
//     `chan<- int` are different Go types and DeepEqual is false between nil values of different
//     directions. Two non-nil channels of the same type still never compare equal: a record outlives
//     the channel it came from, so identity cannot be proven (the safe direction is a rebuild).
//
// # Why the budget is charged, not measured
//
// One cumulative budget bounds the whole encoding. Every byte is charged before it is copied into any
// buffer, and a map's key/value sorting scratch shares that same budget, so individually-in-budget
// pieces cannot assemble into an over-budget record. Measuring the finished buffer instead would run
// after the allocation the limit exists to prevent.
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
	// maxRecordBytes bounds one record: the output length and the peak of the encoding. Every write
	// is charged against one cumulative budget before the bytes are copied (scratch included), so no
	// intermediate buffer and no assembled record can pass the cap. Real nodes are kilobytes; the cap
	// only exists so a hostile or corrupt document cannot make change detection allocate without
	// bound. A value above the cap is not an error the provider surfaces at start: it becomes an
	// Opaque record, which can never compare equal, so the node is rebuilt on every refresh but still
	// works.
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
	encoder := newEncoder()
	encoder.encodeRoot(value)
	if encoder.err != nil {
		return Payload{}, encoder.err
	}
	// The per-write budget already guarantees this; the check is a cheap second line of defence
	// against an accounting mistake in a future write path.
	if encoder.spent > maxRecordBytes || len(encoder.buf) > maxRecordBytes {
		return Payload{}, fmt.Errorf("%w: record exceeds %d bytes", ErrUnsupported, maxRecordBytes)
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
	limit    int
	spent    int
	visiting map[visit]bool
}

func newEncoder() *encoder {
	return &encoder{limit: maxRecordBytes, visiting: make(map[visit]bool)}
}

// encodeRoot writes the version, the root type key and the value. The root type is written once.
// Every nested type is implied by the root type and the interface values the encoding meets, so
// repeating a type key per struct would only inflate the record.
func (e *encoder) encodeRoot(value any) {
	e.writeByte(formatVersion)
	e.writeLengthPrefixed(typeKey(reflect.TypeOf(value)))
	e.value(reflect.ValueOf(value), 0)
}

func (e *encoder) fail(err error) {
	if e.err == nil {
		e.err = err
	}
}

// reserve charges n bytes against the record budget. It is called before every allocation and copy,
// so the budget bounds the peak of the encoding rather than only the finished length: a write that
// would push the record past the limit fails before its bytes are materialised.
func (e *encoder) reserve(n int) bool {
	if e.err != nil {
		return false
	}
	if n < 0 || n > e.limit-e.spent {
		e.fail(fmt.Errorf("%w: record exceeds %d bytes", ErrUnsupported, e.limit))
		return false
	}
	e.spent += n
	return true
}

// grow makes room for n more bytes without letting a buffer's capacity pass the record limit, so a
// large write cannot allocate far beyond the budget it was charged for.
func (e *encoder) grow(n int) {
	need := len(e.buf) + n
	if cap(e.buf) >= need {
		return
	}
	newCapacity := cap(e.buf) * 2
	if newCapacity < need {
		newCapacity = need
	}
	if newCapacity > e.limit {
		newCapacity = need
	}
	grown := make([]byte, len(e.buf), newCapacity)
	copy(grown, e.buf)
	e.buf = grown
}

func (e *encoder) writeByte(value byte) {
	if !e.reserve(1) {
		return
	}
	e.grow(1)
	e.buf = append(e.buf, value)
}

// writeBytes copies value into the current buffer. It is for data that has not been charged yet.
func (e *encoder) writeBytes(value []byte) {
	if !e.reserve(len(value)) {
		return
	}
	e.grow(len(value))
	e.buf = append(e.buf, value...)
}

func (e *encoder) writeZeros(count int) {
	if !e.reserve(count) {
		return
	}
	e.grow(count)
	e.buf = append(e.buf, make([]byte, count)...)
}

func (e *encoder) writeUvarint(value uint64) {
	var scratch [binary.MaxVarintLen64]byte
	e.writeBytes(scratch[:binary.PutUvarint(scratch[:], value)])
}

func (e *encoder) writeVarint(value int64) {
	var scratch [binary.MaxVarintLen64]byte
	e.writeBytes(scratch[:binary.PutVarint(scratch[:], value)])
}

func (e *encoder) writeUint64(value uint64) {
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], value)
	e.writeBytes(scratch[:])
}

func (e *encoder) writeString(value string) {
	if !e.reserve(len(value)) {
		return
	}
	e.grow(len(value))
	e.buf = append(e.buf, value...)
}

// writeLengthPrefixed writes a uvarint byte length followed by the string's bytes.
func (e *encoder) writeLengthPrefixed(value string) {
	e.writeUvarint(uint64(len(value)))
	e.writeString(value)
}

// writeCharged appends bytes that were already charged by the scratch buffer that produced them.
// The transfer is deliberately not charged again: every byte is charged exactly once, so `spent`
// equals the finished record length, and a map's key/value scratch shares the one record budget
// instead of passing a local check each and then assembling into an over-budget record.
func (e *encoder) writeCharged(value []byte) {
	if e.err != nil {
		return
	}
	e.grow(len(value))
	e.buf = append(e.buf, value...)
}

func (e *encoder) value(v reflect.Value, depth int) {
	if e.err != nil {
		return
	}
	if !v.IsValid() {
		e.writeByte(kindInvalid)
		return
	}
	if depth > maxDepth {
		e.fail(fmt.Errorf("%w: nesting deeper than %d", ErrUnsupported, maxDepth))
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		e.writeByte(kindBool)
		if v.Bool() {
			e.writeByte(1)
		} else {
			e.writeByte(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// `reflect.DeepEqual` compares integers by value. The width only matters for the value it
		// can hold, so one zig-zag encoding covers every width.
		e.writeByte(kindInt)
		e.writeVarint(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.writeByte(kindUint)
		e.writeUvarint(v.Uint())
	case reflect.Float32, reflect.Float64:
		e.floatValue(v.Float())
	case reflect.Complex64, reflect.Complex128:
		// `==` on a complex value compares both components as floats, so each component goes through
		// the same float space - including the non-finite space for a NaN or infinity component.
		e.writeByte(kindComplex)
		value := v.Complex()
		e.floatValue(real(value))
		e.floatValue(imag(value))
	case reflect.String:
		encodedString := v.String()
		e.writeByte(kindString)
		e.writeUvarint(uint64(len(encodedString)))
		e.writeString(encodedString)
	case reflect.Pointer:
		if v.IsNil() {
			e.writeByte(kindNilPointer)
			return
		}
		if e.enter(v, depth) {
			return
		}
		e.writeByte(kindPointer)
		e.value(v.Elem(), depth+1)
		e.leave(v)
	case reflect.Interface:
		if v.IsNil() {
			e.writeByte(kindNilInterface)
			return
		}
		e.writeByte(kindInterface)
		e.writeLengthPrefixed(typeKey(v.Elem().Type()))
		e.value(v.Elem(), depth+1)
	case reflect.Struct:
		e.structValue(v, depth)
	case reflect.Slice:
		if v.IsNil() {
			e.writeByte(kindNilSlice)
			return
		}
		// An empty non-nil slice is a different value from a nil slice under DeepEqual, so the nil
		// case above and this branch must never collapse into each other.
		if v.Len() > e.limit {
			// Every element occupies at least one marker byte, except zero-sized elements, which
			// would otherwise let a huge length spin the walker without growing the record.
			e.fail(fmt.Errorf("%w: slice length %d exceeds the record budget", ErrUnsupported, v.Len()))
			return
		}
		if v.Len() > 0 && e.enter(v, depth) {
			return
		}
		e.writeByte(kindSlice)
		e.writeUvarint(uint64(v.Len()))
		for i := 0; i < v.Len(); i++ {
			e.value(v.Index(i), depth+1)
		}
		if v.Len() > 0 {
			e.leave(v)
		}
	case reflect.Array:
		e.writeByte(kindArray)
		for i := 0; i < v.Len(); i++ {
			e.value(v.Index(i), depth+1)
		}
	case reflect.Map:
		if v.IsNil() {
			e.writeByte(kindNilMap)
			return
		}
		if v.Len() > e.limit {
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
			e.writeByte(kindNilFunc)
		} else {
			e.writeByte(kindFunc)
			e.writeUint64(nextUniqueToken())
		}
	case reflect.Chan:
		// DeepEqual compares channels with ==, but a stored record outlives the channel it came
		// from, so an address could be reused after GC and produce a false "equal". Unique tokens
		// make such a value always rebuild instead - conservative, never stale. The channel's type
		// (including direction) is part of the enclosing type key.
		e.writeByte(kindUnique)
		if v.IsNil() {
			e.writeByte(0)
		} else {
			e.writeByte(1)
			e.writeUint64(nextUniqueToken())
		}
	case reflect.UnsafePointer:
		e.writeByte(kindUnique)
		if v.IsNil() {
			e.writeByte(0)
		} else {
			e.writeByte(1)
			e.writeUint64(nextUniqueToken())
		}
	default:
		e.fail(fmt.Errorf("%w: kind %s", ErrUnsupported, v.Kind()))
	}
}

// floatValue encodes one float component. A finite value keeps the compact `kindFloat` + IEEE bit
// pattern form; the two infinities and NaN move to the dedicated non-finite kind.
//
// The separation is the fix for the review's F3 counterexample: the old encoding wrote a NaN's
// unique counter into the same eight bytes a finite float's bit pattern occupies, so a finite float
// whose bits equalled the counter produced a byte-identical record. A NaN now carries its counter
// inside `kindNonFiniteFloat`, a space no finite value ever writes.
func (e *encoder) floatValue(value float64) {
	switch {
	case math.IsNaN(value):
		// `==` says NaN is equal to nothing, including itself, so every NaN record is unique.
		e.writeByte(kindNonFiniteFloat)
		e.writeByte(nonFiniteNaN)
		e.writeUint64(nextUniqueToken())
	case math.IsInf(value, 1):
		// `==` says +Inf equals +Inf and nothing else, so the flag alone is the identity.
		e.writeByte(kindNonFiniteFloat)
		e.writeByte(nonFinitePositiveInfinity)
	case math.IsInf(value, -1):
		e.writeByte(kindNonFiniteFloat)
		e.writeByte(nonFiniteNegativeInfinity)
	default:
		// `==` on floats says -0 == +0, so both zeros must encode to the same bytes.
		if value == 0 {
			value = 0
		}
		e.writeByte(kindFloat)
		e.writeUint64(math.Float64bits(value))
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
	e.writeByte(kindStruct)
	e.writeUvarint(uint64(fieldCount))
	bitmapLength := (fieldCount + 7) / 8
	bitmapStart := len(e.buf)
	e.writeZeros(bitmapLength)
	if e.err != nil {
		return
	}
	for i := 0; i < fieldCount; i++ {
		field := v.Field(i)
		// Field(i) is readable for unexported fields for every operation used here (IsZero and the
		// kind accessors); Interface() is deliberately never called on it.
		if field.IsZero() {
			continue
		}
		e.buf[bitmapStart+i/8] |= 1 << (i % 8)
		e.value(field, depth+1)
		if e.err != nil {
			return
		}
	}
}

// mapValue encodes map entries sorted by canonical key bytes, because map iteration order is
// random while DeepEqual's answer is not. Entries are encoded into scratch buffers so they can be
// sorted before being appended; the scratch bytes are charged against the same record budget as
// every other byte, so the assembled record cannot outgrow the limit even when each entry fits on
// its own.
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
	e.writeByte(kindMap)
	e.writeUvarint(uint64(len(entries)))
	for _, item := range entries {
		e.writeUvarint(uint64(len(item.key)))
		e.writeCharged(item.key)
		e.writeCharged(item.value)
		if e.err != nil {
			return
		}
	}
}

// scratch encodes v into a fresh buffer, sharing the cycle-detection state and the cumulative record
// budget with the main encoder. The budget is deliberately not swapped: bytes a scratch writes are
// charged as they are produced, which is what makes map keys and values share one limit.
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
	// kindNonFiniteFloat marks a NaN or an infinity. It is its own kind so a non-finite value can
	// never share the byte space of a finite float's IEEE bit pattern; the next byte is the
	// non-finite flag below, and a NaN adds one unique counter inside this dedicated space.
	kindNonFiniteFloat
)

// Non-finite float flags, written immediately after kindNonFiniteFloat. NaN is the zero flag because
// it is the only non-finite value that needs the extra unique counter.
const (
	nonFiniteNaN byte = iota
	nonFinitePositiveInfinity
	nonFiniteNegativeInfinity
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
//
// A channel's direction is part of its type identity: `chan int`, `<-chan int` and `chan<- int` are
// three different types, so nil values of different directions are not deeply equal. Leaving the
// direction out (the review's F3 counterexample) made them share a record.
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
		switch t.ChanDir() {
		case reflect.RecvDir:
			return "<-chan " + typeKey(t.Elem())
		case reflect.SendDir:
			return "chan<- " + typeKey(t.Elem())
		default:
			return "chan " + typeKey(t.Elem())
		}
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
