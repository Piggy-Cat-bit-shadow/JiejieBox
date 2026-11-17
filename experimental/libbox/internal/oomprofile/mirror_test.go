//go:build darwin || linux || windows

package oomprofile

// This file is the upgrade gate for the mirrored runtime types in this package.
//
// # The failure it exists to prevent
//
// oomprofile does not define these types, it MIRRORS them: //go:linkname hands the runtime a slice of
// THIS package's struct, and the runtime writes into it using the layout of
// internal/profilerecord.MemProfileRecord. Nothing in the type system connects the two. When the
// toolchain moves a field, the build stays green, and the mismatch surfaces as garbage slice headers
// on a memory-pressure callback at run time - which is how 0.1.52 died (SIGSEGV in writeHeapProto,
// iOS Network Extension, Go 1.27.4).
//
// # Why it reads the toolchain, not a constant
//
// A test that asserts `unsafe.Sizeof(memProfileRecord{}) == 48` only proves the mirror equals a
// number somebody typed. The question that matters at upgrade time is "does the mirror still equal
// the TOOLCHAIN", so this test parses the pinned GOROOT's own source: the field names, their order
// and their types, plus the body of InUseBytes that defines what the fields MEAN.
//
// It fails rather than skips when the source cannot be read, because a skip would turn the only
// detector of this class of defect into a silent pass.

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// toolchainStructField is one declaration inside a struct, with the names it declares and its type.
type toolchainStructField struct {
	names []string
	typ   string
}

func goRuntimeSourceFile(t *testing.T, path ...string) string {
	t.Helper()
	goroot := runtime.GOROOT()
	if goroot == "" {
		t.Fatal("runtime.GOROOT() is empty; the compatibility gate for the mirrored profile records " +
			"cannot read the pinned toolchain's source, so it cannot be evaluated")
	}
	file := filepath.Join(append([]string{goroot, "src"}, path...)...)
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v\n"+
			"The mirrored record types in this package are only correct relative to the toolchain's own "+
			"definitions, so this gate must be re-derived (not deleted) when the standard library moves them.", file, err)
	}
	return string(content)
}

// parseToolchainStruct returns the fields of one struct declaration, in source order.
func parseToolchainStruct(t *testing.T, source string, structName string) []toolchainStructField {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "toolchain.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse toolchain source: %v", err)
	}
	var found *ast.StructType
	ast.Inspect(parsed, func(node ast.Node) bool {
		typeSpec, ok := node.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != structName {
			return true
		}
		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			t.Fatalf("%s is declared in the toolchain source but is no longer a struct", structName)
		}
		found = structType
		return false
	})
	if found == nil {
		t.Fatalf("the pinned toolchain no longer declares %s; the mirror in this package is stale", structName)
	}
	var fields []toolchainStructField
	for _, field := range found.Fields.List {
		names := make([]string, 0, len(field.Names))
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
		if len(names) == 0 {
			// An embedded field. The mirrored types have none, and one appearing means the layout
			// rules changed in a way this mirror was not written for.
			t.Fatalf("%s in the toolchain gained an embedded field; this mirror must be re-derived", structName)
		}
		fields = append(fields, toolchainStructField{names: names, typ: renderNode(t, fileSet, field.Type)})
	}
	return fields
}

func renderNode(t *testing.T, fileSet *token.FileSet, node ast.Node) string {
	t.Helper()
	var builder strings.Builder
	if err := printer.Fprint(&builder, fileSet, node); err != nil {
		t.Fatalf("render toolchain source node: %v", err)
	}
	return builder.String()
}

// mirrorFields reads how this package ACTUALLY declares the record, through reflect.
//
// It must not be a hand-written copy of the declaration: a second copy is one more thing that can
// drift, and it would let the test agree with itself while the struct it is meant to guard changed.
// reflect reports the fields in declaration order with their real types, which is exactly the shape
// the runtime's own writes depend on.
func mirrorFields(t *testing.T, value any) []toolchainStructField {
	t.Helper()
	structType := reflect.TypeOf(value)
	if structType.Kind() != reflect.Struct {
		t.Fatalf("%v is not a struct", structType)
	}
	fields := make([]toolchainStructField, 0, structType.NumField())
	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		if len(field.Index) != 1 {
			t.Fatalf("%v has an embedded field %q; the mirror must stay a flat struct", structType, field.Name)
		}
		fields = append(fields, toolchainStructField{names: []string{field.Name}, typ: field.Type.String()})
	}
	return fields
}

func TestToolchainProfileRecordsStillMatchTheMirrors(t *testing.T) {
	source := goRuntimeSourceFile(t, "internal", "profilerecord", "profilerecord.go")
	mirrors := map[string]any{
		"MemProfileRecord":   memProfileRecord{},
		"BlockProfileRecord": blockProfileRecord{},
		"StackRecord":        stackRecord{},
	}
	for _, structName := range []string{"MemProfileRecord", "BlockProfileRecord", "StackRecord"} {
		t.Run(structName, func(t *testing.T) {
			toolchain := flattenFields(parseToolchainStruct(t, source, structName))
			mirror := mirrorFields(t, mirrors[structName])
			if len(toolchain) != len(mirror) {
				t.Fatalf("%s has %d fields in the pinned toolchain and %d in this mirror:\n"+
					"  toolchain: %s\n  mirror:    %s\n"+
					"The runtime writes into a slice of THIS type using the toolchain's layout. Re-derive "+
					"internal/oomprofile's %s before shipping.",
					structName, len(toolchain), len(mirror),
					formatFields(toolchain), formatFields(mirror), structName)
			}
			for i := range toolchain {
				if formatFields(toolchain[i:i+1]) != formatFields(mirror[i:i+1]) {
					t.Fatalf("%s field %d differs:\n  toolchain: %s\n  mirror:    %s\n"+
						"A field that changed name, order or type moves every offset after it, and the "+
						"runtime will write the old layout into the new struct.",
						structName, i, formatFields(toolchain[i:i+1]), formatFields(mirror[i:i+1]))
				}
			}
		})
	}

	// The meaning of the fields matters as much as their shape: Go 1.27 redefined InUseBytes as
	// objects multiplied by the per-object size. A mirror that copies the fields but keeps the old
	// derivation produces a profile that parses and lies.
	t.Run("MemProfileRecord.InUseBytes", func(t *testing.T) {
		body := functionBody(t, source, "InUseBytes")
		if !strings.Contains(body, "InUseObjects()") || !strings.Contains(body, "ObjectSize") {
			t.Fatalf("MemProfileRecord.InUseBytes no longer derives the in-use size from InUseObjects() and "+
				"ObjectSize:\n  %s\n"+
				"internal/oomprofile mirrors this derivation; update memProfileRecord.InUseBytes "+
				"and writeHeapProto together with it.", body)
		}
	})
}

// TestLabelMapStillMatchesTheMirror covers the second mirrored type: the label map the runtime hands
// back as an unsafe.Pointer for goroutine profiles. Go 1.27 moved its declaration from
// runtime/pprof.labelSet to internal/runtime/pprof/label.Set while keeping the layout, so the gate
// asserts the layout shape rather than the package it lives in.
func TestLabelMapStillMatchesTheMirror(t *testing.T) {
	source := goRuntimeSourceFile(t, "internal", "runtime", "pprof", "label", "labelset.go")
	labelFields := parseToolchainStruct(t, source, "Label")
	expectedLabel := []toolchainStructField{
		{names: []string{"Key"}, typ: "string"},
		{names: []string{"Value"}, typ: "string"},
	}
	if formatFields(labelFields) != formatFields(expectedLabel) {
		t.Fatalf("label.Label is now %s, but internal/oomprofile's label mirror assumes %s",
			formatFields(labelFields), formatFields(expectedLabel))
	}
	setFields := parseToolchainStruct(t, source, "Set")
	expectedSet := []toolchainStructField{{names: []string{"List"}, typ: "[]Label"}}
	if formatFields(setFields) != formatFields(expectedSet) {
		t.Fatalf("label.Set is now %s, but internal/oomprofile's label mirror assumes %s",
			formatFields(setFields), formatFields(expectedSet))
	}
}

// TestMemProfileRecordLayoutMatchesGo1274 pins the concrete numbers that the 0.1.52 crash violated.
// The source gate above is the primary detector; this one states the consequence in the terms the
// crash report used, and it is the assertion that fails first if the mirror is edited by hand.
func TestMemProfileRecordLayoutMatchesGo1274(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("the pinned layout numbers are the 64-bit ones; every platform this package ships on is 64-bit")
	}
	if got := unsafe.Sizeof(memProfileRecord{}); got != 48 {
		t.Fatalf("sizeof(memProfileRecord) = %d, want 48 (Go 1.27.4's 64-bit layout). "+
			"An earlier version of this file had 56 here, which is the layout of (AllocBytes, FreeBytes) "+
			"and the cause of the 0.1.52 Network Extension SIGSEGV", got)
	}
	if got := unsafe.Offsetof(memProfileRecord{}.Stack); got != 24 {
		t.Fatalf("offsetof(memProfileRecord.Stack) = %d, want 24 (Go 1.27.4's 64-bit layout); "+
			"the runtime computes this offset itself when it writes a record", got)
	}
	if got := unsafe.Sizeof(labelMap{}); got != unsafe.Sizeof(labelSet{}) {
		t.Fatalf("labelMap is %d bytes but its embedded labelSet is %d; the runtime hands back a pointer "+
			"to its own label map and this package reinterprets it", got, unsafe.Sizeof(labelSet{}))
	}
	if got := unsafe.Sizeof(label{}); got != 4*unsafe.Sizeof(uintptr(0)) {
		t.Fatalf("sizeof(label) = %d, want %d (two strings); the label decode depends on it",
			got, 4*unsafe.Sizeof(uintptr(0)))
	}
}

func formatFields(fields []toolchainStructField) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, strings.Join(field.names, ", ")+" "+field.typ)
	}
	return strings.Join(parts, "; ")
}

// flattenFields expands a declaration that names several fields at once ("A, B int64") into one entry
// per field, so it can be compared against reflect's field-by-field view.
func flattenFields(fields []toolchainStructField) []toolchainStructField {
	flattened := make([]toolchainStructField, 0, len(fields))
	for _, field := range fields {
		for _, name := range field.names {
			flattened = append(flattened, toolchainStructField{names: []string{name}, typ: field.typ})
		}
	}
	return flattened
}

// functionBody renders one method or function body from the toolchain source.
func functionBody(t *testing.T, source string, name string) string {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, "toolchain.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse toolchain source: %v", err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name || function.Body == nil {
			continue
		}
		return renderNode(t, fileSet, function.Body)
	}
	t.Fatalf("the pinned toolchain no longer declares %s", name)
	return ""
}
