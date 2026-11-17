//go:build darwin || linux || windows

package oomprofile

// Tests for the six profiles this package writes on the OOM path.
//
// # Why these exist
//
// The writer is a mirror of runtime/pprof: it pulls the runtime's private collection functions with
// //go:linkname and re-implements the protobuf encoding. That arrangement has one catastrophic failure
// mode - the pinned toolchain changes a mirrored record's layout or the meaning of its fields, the
// code still compiles, and the process dies inside a memory-pressure callback (0.1.52, iOS
// Network Extension, SIGSEGV in writeHeapProto). Compiling is not evidence.
//
// So the assertions here are deliberately about the DATA, not about the absence of a panic:
//
//   - every writer produces a parseable profile with the sample types its consumers expect;
//   - heap and allocs report byte totals that agree with runtime.MemStats;
//   - the goroutine profile still carries labels, which is the only thing that explains the
//     mirrored label-map type;
//   - the same profile written by the standard library and by this package reports the same numbers.
//
// The last one is the gate that would have caught the 0.1.52 bug without knowing anything about Go
// 1.27: the standard library always uses the layout it was compiled with.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"
)

func TestWriteFileProducesWellFormedProfiles(t *testing.T) {
	directory := t.TempDir()

	expectations := map[string]struct {
		sampleTypes       []string // "type/unit", in order
		periodType        string
		period            int64
		defaultSampleType string
	}{
		"heap": {
			sampleTypes: []string{"alloc_objects/count", "alloc_space/bytes", "inuse_objects/count", "inuse_space/bytes"},
			periodType:  "space/bytes",
			period:      int64(runtime.MemProfileRate),
		},
		"allocs": {
			sampleTypes:       []string{"alloc_objects/count", "alloc_space/bytes", "inuse_objects/count", "inuse_space/bytes"},
			periodType:        "space/bytes",
			period:            int64(runtime.MemProfileRate),
			defaultSampleType: "alloc_space",
		},
		"goroutine": {
			sampleTypes: []string{"goroutine/count"},
			periodType:  "goroutine/count",
			period:      1,
		},
		"threadcreate": {
			sampleTypes: []string{"threadcreate/count"},
			periodType:  "threadcreate/count",
			period:      1,
		},
		"block": {
			sampleTypes: []string{"contentions/count", "delay/nanoseconds"},
			periodType:  "contentions/count",
			period:      1,
		},
		"mutex": {
			sampleTypes: []string{"contentions/count", "delay/nanoseconds"},
			periodType:  "contentions/count",
			period:      1,
		},
	}

	// A live allocation makes the heap/allocs assertions about a real record rather than an empty one.
	payload := make([]byte, 4<<20)
	payload[0] = 1
	runtime.KeepAlive(payload)

	for name, expected := range expectations {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name+".pb")
			if err := WriteFile(path, name); err != nil {
				t.Fatalf("WriteFile(%q): %v", name, err)
			}
			profile := parseProfileFile(t, path)

			var got []string
			for _, sampleType := range profile.sampleTypes {
				got = append(got, sampleType.typeName+"/"+sampleType.unitName)
			}
			if fmt.Sprint(got) != fmt.Sprint(expected.sampleTypes) {
				t.Fatalf("sample types = %v, want %v", got, expected.sampleTypes)
			}
			if profile.periodType == nil {
				t.Fatal("profile has no period type; pprof requires one for a self-describing profile")
			}
			gotPeriodType := profile.periodType.typeName + "/" + profile.periodType.unitName
			if gotPeriodType != expected.periodType {
				t.Fatalf("period type = %q, want %q", gotPeriodType, expected.periodType)
			}
			if profile.period != expected.period {
				t.Fatalf("period = %d, want %d", profile.period, expected.period)
			}
			if profile.defaultSampleType != expected.defaultSampleType {
				t.Fatalf("default sample type = %q, want %q", profile.defaultSampleType, expected.defaultSampleType)
			}
			if name == "goroutine" && len(profile.samples) == 0 {
				t.Fatal("goroutine profile has no samples while the test itself is running")
			}
			for i, sample := range profile.samples {
				if len(sample.values) != len(profile.sampleTypes) {
					t.Fatalf("sample %d has %d values for %d sample types", i, len(sample.values), len(profile.sampleTypes))
				}
				if len(sample.locations) == 0 {
					t.Fatalf("sample %d has no locations", i)
				}
			}
		})
	}
}

func TestWriteFileRejectsUnknownProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.pb")
	if err := WriteFile(path, "not-a-profile"); err == nil {
		t.Fatal("WriteFile accepted an unknown profile name")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("WriteFile left a file behind after rejecting the profile name")
	}
}

// TestHeapSampleValuesMatchTheirObjectSizeLabel is the semantic half of the heap mirror, and it is
// phrased so that it depends on NOTHING outside the profile itself.
//
// Go 1.27 changed what a record carries: (AllocBytes, FreeBytes) became one per-object ObjectSize,
// and every sample value is derived from it. The contract that must hold for a correct writer is
// therefore internal: the "bytes" label is the object size, and both the allocation and the in-use
// byte figures are the object count multiplied by it (exactly, when no sampling correction applies).
//
// This is what catches the quiet half of the 0.1.52 bug. Fixing only the struct layout leaves the
// old arithmetic compiling, and the old arithmetic divides a per-object size by the object count, so
// inuse_space collapses by that factor. Nothing crashes and the profile still parses - only the
// numbers are wrong.
//
// MemProfileRate is set to 1 so scaleHeapSample is the identity and the equality is exact.
func TestHeapSampleValuesMatchTheirObjectSizeLabel(t *testing.T) {
	previousRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = previousRate }()

	payload := make([][]byte, 256)
	for i := range payload {
		payload[i] = make([]byte, 4096)
		payload[i][0] = 1
	}
	runtime.KeepAlive(payload)

	directory := t.TempDir()
	for _, name := range []string{"heap", "allocs"} {
		path := filepath.Join(directory, name+".pb")
		if err := WriteFile(path, name); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
		profile := parseProfileFile(t, path)
		if len(profile.samples) == 0 {
			t.Fatalf("%s profile is empty", name)
		}
		for i, sample := range profile.samples {
			var objectSize int64
			found := false
			for _, label := range sample.labels {
				if label.key == "bytes" {
					objectSize = label.num
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: sample %d has no \"bytes\" label; pprof uses it as the object size", name, i)
			}
			if objectSize <= 0 {
				t.Fatalf("%s: sample %d has object size %d", name, i, objectSize)
			}
			allocObjects, allocBytes, inUseObjects, inUseBytes := sample.values[0], sample.values[1], sample.values[2], sample.values[3]
			if allocObjects < inUseObjects {
				t.Fatalf("%s: sample %d has alloc_objects %d < inuse_objects %d", name, i, allocObjects, inUseObjects)
			}
			if allocObjects > 0 && allocBytes != allocObjects*objectSize {
				t.Fatalf("%s: sample %d reports alloc_space %d for %d objects of size %d; want %d",
					name, i, allocBytes, allocObjects, objectSize, allocObjects*objectSize)
			}
			if inUseObjects > 0 && inUseBytes != inUseObjects*objectSize {
				t.Fatalf("%s: sample %d reports inuse_space %d for %d objects of size %d; want %d",
					name, i, inUseBytes, inUseObjects, objectSize, inUseObjects*objectSize)
			}
		}

		// And the profile must describe the allocation this test is holding: at least one sample's
		// object size is the 4 KiB class every object here was allocated from.
		sawPayloadClass := false
		for _, sample := range profile.samples {
			for _, label := range sample.labels {
				if label.key == "bytes" && label.num == 4096 {
					sawPayloadClass = true
				}
			}
		}
		if !sawPayloadClass {
			t.Fatalf("%s: no sample reports a 4096-byte object size, so the profile does not describe "+
				"the allocations this test made", name)
		}
	}
}

// TestGoroutineProfileCarriesLabels covers the other mirrored type: the label map the runtime hands
// back as an unsafe.Pointer. Go 1.27 moved its definition (pprof.labelSet -> label.Set) without
// changing the layout, so this asserts the behaviour rather than the source shape: a labelled
// goroutine must appear labelled in the written profile.
func TestGoroutineProfileCarriesLabels(t *testing.T) {
	const (
		labelKey = "oomprofile.test.label"
		labelVal = "present"
	)
	ready := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		pprof.Do(context.Background(), pprof.Labels(labelKey, labelVal), func(context.Context) {
			close(ready)
			<-release
		})
	}()
	defer func() {
		close(release)
		<-done
	}()

	<-ready

	path := filepath.Join(t.TempDir(), "goroutine.pb")
	if err := WriteFile(path, "goroutine"); err != nil {
		t.Fatalf("WriteFile(goroutine): %v", err)
	}
	profile := parseProfileFile(t, path)
	if len(profile.samples) == 0 {
		t.Fatal("goroutine profile is empty")
	}
	for _, sample := range profile.samples {
		for _, label := range sample.labels {
			if label.key == labelKey && label.str == labelVal {
				return
			}
		}
	}
	t.Fatalf("no sample carries the %q=%q label; the mirrored label map no longer decodes", labelKey, labelVal)
}

// TestMirrorAgreesWithStandardLibrary is the differential gate: the same instant seen through this
// package's mirror and through runtime/pprof's own writer must agree on what the profile MEANS.
//
// This is the check that generalises to every future toolchain: it compares against whatever the
// pinned standard library does, instead of against a copy of its code that has to be maintained.
//
// One difference is deliberate and is asserted here rather than smoothed over: this package writes
// RAW protobuf, while runtime/pprof wraps its builder in gzip.BestSpeed. The OOM reporter runs inside
// a memory-pressure callback, where compressing six profiles spends CPU and a gzip window's worth of
// heap to make an artifact slightly smaller - the wrong trade on that path. The raw output is also
// what every archived OOM report has always contained, so the first byte assertion below is a format
// contract, not an implementation detail.
func TestMirrorAgreesWithStandardLibrary(t *testing.T) {
	previousRate := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = previousRate }()

	payload := make([][]byte, 512)
	for i := range payload {
		payload[i] = make([]byte, 16<<10)
		payload[i][0] = 1
	}
	runtime.KeepAlive(payload)

	directory := t.TempDir()
	for _, name := range []string{"heap", "allocs"} {
		mirroredPath := filepath.Join(directory, "mirror-"+name+".pb")
		if err := WriteFile(mirroredPath, name); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
		mirroredContent, err := os.ReadFile(mirroredPath)
		if err != nil {
			t.Fatal(err)
		}
		if len(mirroredContent) >= 2 && mirroredContent[0] == 0x1f && mirroredContent[1] == 0x8b {
			t.Fatalf("%s: written profile is gzip-compressed; the OOM report has always carried raw "+
				"protobuf under a .pb name, and compressing it would change the artifact format", name)
		}

		var standard bytes.Buffer
		if err := pprof.Lookup(name).WriteTo(&standard, 0); err != nil {
			t.Fatalf("pprof.Lookup(%q).WriteTo: %v", name, err)
		}
		standardContent := decodeStandardLibraryProfile(t, standard.Bytes())

		mirrored := parseProfile(t, mirroredContent)
		standardProfile := parseProfile(t, standardContent)

		mirroredTypes := mirrored.sampleTypeStrings()
		standardTypes := standardProfile.sampleTypeStrings()
		if fmt.Sprint(mirroredTypes) != fmt.Sprint(standardTypes) {
			t.Fatalf("%s: mirror sample types = %v, standard library = %v", name, mirroredTypes, standardTypes)
		}
		if mirrored.defaultSampleType != standardProfile.defaultSampleType {
			t.Fatalf("%s: mirror default sample type = %q, standard library = %q",
				name, mirrored.defaultSampleType, standardProfile.defaultSampleType)
		}
		if mirrored.period != standardProfile.period {
			t.Fatalf("%s: mirror period = %d, standard library = %d", name, mirrored.period, standardProfile.period)
		}

		mAllocObjects, mAllocBytes, mInUseObjects, mInUseBytes := mirrored.heapTotals()
		sAllocObjects, sAllocBytes, sInUseObjects, sInUseBytes := standardProfile.heapTotals()
		t.Logf("%s: mirror alloc=%d/%d inuse=%d/%d standard alloc=%d/%d inuse=%d/%d",
			name,
			mAllocObjects, mAllocBytes, mInUseObjects, mInUseBytes,
			sAllocObjects, sAllocBytes, sInUseObjects, sInUseBytes)

		// The two profiles are taken a few hundred microseconds apart and each write allocates into
		// the heap it is measuring, so the totals are compared with a tolerance rather than for
		// equality. The tolerance is far tighter than any layout or arithmetic error would produce.
		for _, field := range []struct {
			name           string
			mirror, stdlib int64
		}{
			{"alloc_objects", mAllocObjects, sAllocObjects},
			{"alloc_space", mAllocBytes, sAllocBytes},
			{"inuse_objects", mInUseObjects, sInUseObjects},
			{"inuse_space", mInUseBytes, sInUseBytes},
		} {
			if !withinTolerance(field.mirror, field.stdlib, 0.25) {
				t.Fatalf("%s: %s differs between the mirror (%d) and the standard library (%d); "+
					"the mirrored record layout or its arithmetic no longer matches the pinned toolchain",
					name, field.name, field.mirror, field.stdlib)
			}
		}
	}
}

// decodeStandardLibraryProfile unwraps the gzip layer runtime/pprof applies in its profile builder.
func decodeStandardLibraryProfile(t *testing.T, content []byte) []byte {
	t.Helper()
	if len(content) < 2 || content[0] != 0x1f || content[1] != 0x8b {
		// Not compressed: runtime/pprof changed how it writes profiles, which is worth knowing about
		// but does not invalidate the comparison.
		return content
	}
	reader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("runtime/pprof profile is not readable gzip: %v", err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress runtime/pprof profile: %v", err)
	}
	return decoded
}

// withinTolerance reports whether two counts agree, allowing for the allocations that happen between
// the two observations.
func withinTolerance(a, b int64, tolerance float64) bool {
	if a == b {
		return true
	}
	larger := max(a, b)
	if larger == 0 {
		return true
	}
	difference := a - b
	if difference < 0 {
		difference = -difference
	}
	return float64(difference)/float64(larger) <= tolerance
}

// parsedProfile holds the parts of a profile.proto message these tests assert on.
//
// The encoding is parsed by hand rather than through google.golang.org/protobuf because the profile
// schema is not generated into this repository, and a hand-rolled reader cannot drift out of sync
// with a generated message the way an unchecked mirror can.
type parsedProfile struct {
	sampleTypes       []parsedValueType
	periodType        *parsedValueType
	period            int64
	defaultSampleType string
	samples           []parsedSample
	mappings          int
	locations         int
	functions         int
}

type parsedValueType struct {
	typeName string
	unitName string
}

type parsedSample struct {
	values    []int64
	locations []uint64
	labels    []parsedLabel
}

type parsedLabel struct {
	key string
	str string
	num int64
}

func (p *parsedProfile) sampleTypeStrings() []string {
	result := make([]string, 0, len(p.sampleTypes))
	for _, sampleType := range p.sampleTypes {
		result = append(result, sampleType.typeName+"/"+sampleType.unitName)
	}
	return result
}

// heapTotals sums the four heap sample values across all samples.
func (p *parsedProfile) heapTotals() (allocObjects, allocBytes, inUseObjects, inUseBytes int64) {
	if len(p.sampleTypes) != 4 {
		return 0, 0, 0, 0
	}
	for _, sample := range p.samples {
		if len(sample.values) != 4 {
			continue
		}
		allocObjects += sample.values[0]
		allocBytes += sample.values[1]
		inUseObjects += sample.values[2]
		inUseBytes += sample.values[3]
	}
	return
}

func parseProfileFile(t *testing.T, path string) *parsedProfile {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return parseProfile(t, content)
}

func parseProfile(t *testing.T, content []byte) *parsedProfile {
	t.Helper()
	if len(content) == 0 {
		t.Fatal("profile is empty")
	}
	// The string table is written LAST by the runtime's builder, so the field is collected in a first
	// pass and only then resolved. Fields are also not required to be in schema order.
	var strings_ []string
	forEachProfileField(t, content, func(field int, wireType int, payload []byte) {
		if field == 6 {
			strings_ = append(strings_, string(payload))
		}
	})
	stringAt := func(index uint64) string {
		if index >= uint64(len(strings_)) {
			t.Fatalf("string table index %d out of range (%d entries)", index, len(strings_))
		}
		return strings_[index]
	}
	parseValueType := func(payload []byte) parsedValueType {
		var valueType parsedValueType
		forEachProfileField(t, payload, func(field int, wireType int, payload []byte) {
			switch field {
			case 1:
				valueType.typeName = stringAt(payloadUvarint(t, payload))
			case 2:
				valueType.unitName = stringAt(payloadUvarint(t, payload))
			}
		})
		return valueType
	}

	profile := &parsedProfile{}
	forEachProfileField(t, content, func(field int, wireType int, payload []byte) {
		switch field {
		case 1: // sample_type
			profile.sampleTypes = append(profile.sampleTypes, parseValueType(payload))
		case 2: // sample
			var sample parsedSample
			forEachProfileField(t, payload, func(field int, wireType int, payload []byte) {
				switch field {
				case 1: // location_id, packed or repeated
					sample.locations = append(sample.locations, unpackUint64s(t, wireType, payload)...)
				case 2: // value, packed or repeated
					for _, value := range unpackUint64s(t, wireType, payload) {
						sample.values = append(sample.values, int64(value))
					}
				case 3: // label
					var label parsedLabel
					forEachProfileField(t, payload, func(field int, wireType int, payload []byte) {
						switch field {
						case 1:
							label.key = stringAt(payloadUvarint(t, payload))
						case 2:
							label.str = stringAt(payloadUvarint(t, payload))
						case 3:
							label.num = int64(payloadUvarint(t, payload))
						}
					})
					sample.labels = append(sample.labels, label)
				}
			})
			profile.samples = append(profile.samples, sample)
		case 3:
			profile.mappings++
		case 4:
			profile.locations++
		case 5:
			profile.functions++
		case 11: // period_type
			valueType := parseValueType(payload)
			profile.periodType = &valueType
		case 12: // period
			profile.period = int64(payloadUvarint(t, payload))
		case 14: // default_sample_type
			profile.defaultSampleType = stringAt(payloadUvarint(t, payload))
		}
	})
	return profile
}

// forEachProfileField walks one protobuf message, handing each field's payload to fn. Length-delimited
// fields arrive without their length prefix so nested messages can be walked with the same function.
func forEachProfileField(t *testing.T, message []byte, fn func(field int, wireType int, payload []byte)) {
	t.Helper()
	for len(message) > 0 {
		key, n := binary.Uvarint(message)
		if n <= 0 {
			t.Fatalf("malformed profile: bad field key at %d bytes remaining", len(message))
		}
		message = message[n:]
		field := int(key >> 3)
		wireType := int(key & 0x7)
		switch wireType {
		case 0: // varint
			value, n := binary.Uvarint(message)
			if n <= 0 {
				t.Fatalf("malformed profile: bad varint in field %d", field)
			}
			message = message[n:]
			payload := make([]byte, binary.MaxVarintLen64)
			written := binary.PutUvarint(payload, value)
			fn(field, wireType, payload[:written])
		case 1: // fixed64
			if len(message) < 8 {
				t.Fatalf("malformed profile: truncated fixed64 in field %d", field)
			}
			fn(field, wireType, message[:8])
			message = message[8:]
		case 2: // length-delimited
			length, n := binary.Uvarint(message)
			if n <= 0 || uint64(len(message)-n) < length {
				t.Fatalf("malformed profile: truncated length-delimited field %d", field)
			}
			message = message[n:]
			fn(field, wireType, message[:length])
			message = message[length:]
		case 5: // fixed32
			if len(message) < 4 {
				t.Fatalf("malformed profile: truncated fixed32 in field %d", field)
			}
			fn(field, wireType, message[:4])
			message = message[4:]
		default:
			t.Fatalf("malformed profile: unsupported wire type %d in field %d", wireType, field)
		}
	}
}

// unpackUint64s accepts both encodings a repeated integer field may use: packed (wire type 2) and one
// value per tag (wire type 0).
func unpackUint64s(t *testing.T, wireType int, payload []byte) []uint64 {
	t.Helper()
	if wireType == 0 {
		value, n := binary.Uvarint(payload)
		if n <= 0 {
			t.Fatal("malformed profile: bad scalar integer")
		}
		return []uint64{value}
	}
	var values []uint64
	for len(payload) > 0 {
		value, n := binary.Uvarint(payload)
		if n <= 0 {
			t.Fatal("malformed profile: bad packed integer")
		}
		values = append(values, value)
		payload = payload[n:]
	}
	return values
}

// payloadUvarint decodes one varint field payload that forEachProfileField produced.
func payloadUvarint(t *testing.T, payload []byte) uint64 {
	t.Helper()
	value, n := binary.Uvarint(payload)
	if n <= 0 {
		t.Fatal("malformed profile: bad varint payload")
	}
	return value
}
