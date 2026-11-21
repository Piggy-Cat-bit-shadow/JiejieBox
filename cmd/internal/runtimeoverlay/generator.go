package runtimeoverlay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The toolchain files the overlay replaces, module-relative inside GOROOT.
const (
	sourceMSpanSet = "src/runtime/mspanset.go"
	sourceMalloc   = "src/runtime/malloc.go"
	sourceProc     = "src/runtime/proc.go"
	// sourceASMArm64 is group B's assembly half. It is only replaced when group B is on: a mode
	// without B must not touch an assembly file at all.
	sourceASMArm64 = "src/runtime/asm_arm64.s"

	// The added files. Their names are chosen so nothing in GOROOT can already own them; the
	// generator refuses a toolchain that does.
	addedOnFile  = "jj_ios_runtime_overlay.go"
	addedOffFile = "jj_ios_runtime_overlay_off.go"
)

// edit is one literal replacement in one toolchain file.
type edit struct {
	// source is the GOROOT-relative file.
	source string
	// anchor is the literal the toolchain must contain EXACTLY once.
	anchor string
	// replacement is what the anchor becomes. It must not contain the anchor.
	replacement string
	// group names the plan field that requires this edit, for diagnostics.
	group string
}

// The literals below are the reviewed anchors in Go 1.27.4, whitespace included. They are
// written as raw strings with explicit tabs because gofmt runs on THIS file, not on the
// toolchain's, and a tab that quietly became a space would turn an exact match into a silent
// miss. ReviewReviewedToolchain asserts every one of them against the live GOROOT.

const (
	anchorMSpanSet = "\tspanSetBlockEntries = 512 // 4KB on 64-bit\n" +
		"\tspanSetInitSpineCap = 256 // Enough for 1GB heap on 64-bit\n"

	anchorStackCache = "\t_StackCacheSize = 32 * 1024\n"

	anchorGFPut = "\tif stksize != uintptr(startingStackSize) {\n" +
		"\t\t// non-standard stack size - free it.\n"

	anchorNeedAndBindM = "\tif _cgo_pthread_key_created != nil && *(*uintptr)(_cgo_pthread_key_created) != 0 {\n" +
		"\t\tcgoBindM()\n"

	anchorSchedInit = "func schedinit() {\n"

	anchorASMDropM = "\t// Skip dropm to reuse it in the next call, when a pthread key has been created.\n"
)

// The site note is appended to every edited line so a reader of a patched GOROOT can find out
// where the change came from without the overlay JSON.
const siteNote = " // jiejiebox ios runtime overlay; see cmd/internal/runtimeoverlay"

func editsFor(plan Plan) []edit {
	edits := make([]edit, 0, 6)
	edits = append(edits,
		edit{
			source:      sourceMSpanSet,
			anchor:      anchorMSpanSet,
			replacement: "\tspanSetBlockEntries = jjOverlaySpanSetBlockEntries" + siteNote + "\n\tspanSetInitSpineCap = jjOverlaySpanSetInitSpineCap\n",
			group:       "A1 span set",
		},
		edit{
			source:      sourceMalloc,
			anchor:      anchorStackCache,
			replacement: "\t_StackCacheSize = jjOverlayStackCacheSize" + siteNote + "\n",
			group:       "A2 stack cache",
		},
		edit{
			source: sourceProc,
			anchor: anchorGFPut,
			replacement: "\tif stksize != uintptr(startingStackSize) || jjOverlayFreeExitedStack ||\n" +
				"\t\t(jjOverlayFreeExitedStackBacklog > 0 && pp.gFree.size >= jjOverlayFreeExitedStackBacklog) {" + siteNote + "\n" +
				"\t\t// non-standard stack size - free it.\n",
			group: "A3 exited stacks",
		},
		// The marker call is not gated on a group: it must be in every overlay build, because it
		// is the only in-binary proof that this overlay was compiled at all.
		edit{
			source:      sourceProc,
			anchor:      anchorSchedInit,
			replacement: "func schedinit() {\n\tjjOverlayMarker()" + siteNote + "\n",
			group:       "marker",
		},
	)
	if plan.groupB() {
		edits = append(edits,
			edit{
				source:      sourceProc,
				anchor:      anchorNeedAndBindM,
				replacement: "\tif !jjOverlayDropExtraM && _cgo_pthread_key_created != nil && *(*uintptr)(_cgo_pthread_key_created) != 0 {" + siteNote + "\n\t\tcgoBindM()\n",
				group:       "B needAndBindM",
			},
			edit{
				source: sourceASMArm64,
				anchor: anchorASMDropM,
				replacement: "\t// jiejiebox ios runtime overlay: hand the m back after every call into Go,\n" +
					"\t// as the runtime did before Go 1.21, instead of keeping it bound to the thread\n" +
					"\t// until the thread exits. Group B is arm64-only; see cmd/internal/runtimeoverlay.\n" +
					"\tMOVBU\truntime·jjOverlayDropExtraM(SB), R6\n" +
					"\tCBNZ\tR6, dropm\n" +
					"\n" +
					"\t// Skip dropm to reuse it in the next call, when a pthread key has been created.\n",
				group: "B cgocallback",
			},
		)
	}
	for _, extra := range plan.extraEditTargets {
		edits = append(edits, edit{
			source:      extra,
			anchor:      anchorASMDropM,
			replacement: probeAssemblyInsertion(extra),
			group:       "probe " + filepath.Base(extra),
		})
	}
	return edits
}

// probeAssemblyInsertion is group B's insertion, spelled for the host architecture.
//
// It exists only so the host probe can execute the cgocallback epilogue change on a machine that
// is not an Apple device. The product edit is arm64-only and lives in the branch above; this one
// is selected by the file name so the instruction sequence matches the assembler that will read
// it, and it is never reached from a product plan.
func probeAssemblyInsertion(source string) string {
	header := "\t// jiejiebox ios runtime overlay PROBE: this edit exists only in\n" +
		"\t// cmd/internal/runtimeoverlay's host probe, never in a product overlay.\n"
	tail := "\n\t// Skip dropm to reuse it in the next call, when a pthread key has been created.\n"
	if strings.HasSuffix(source, "_amd64.s") {
		// AX is dead here on amd64: the next instruction loads _cgo_pthread_key_created into it.
		return header + "\tMOVBLZX\truntime·jjOverlayDropExtraM(SB), AX\n" +
			"\tTESTL\tAX, AX\n" +
			"\tJNE\tdropm\n" + tail
	}
	return header + "\tMOVBU\truntime·jjOverlayDropExtraM(SB), R6\n" +
		"\tCBNZ\tR6, dropm\n" + tail
}

// GeneratedFile is one file the overlay writes, with the digest recorded in provenance.
type GeneratedFile struct {
	// Role is "replace" for a toolchain file the overlay rewrites and "add" for a file the
	// overlay introduces.
	Role string `json:"role"`
	// Source is the GOROOT-relative path of the file being replaced, empty for an added file.
	Source string `json:"source,omitempty"`
	// Path is where the overlay's copy lives.
	Path string `json:"path"`
	// SHA256 is the digest of the generated bytes.
	SHA256 string `json:"sha256"`
}

// Overlay is a generated overlay plus everything needed to prove and record it.
type Overlay struct {
	Plan        Plan
	Dir         string
	JSONPath    string
	Toolchain   Toolchain
	Files       []GeneratedFile
	Marker      string
	shimDir     string
	injectedLog string
}

// GenerationRequest is everything Generate needs from the caller, so the function itself can
// be exercised without a gomobile build.
type GenerationRequest struct {
	// Root is the module root. It is only used to locate nothing but the toolchain: the
	// generated files live in Dir, never in the tree.
	Root string
	// Dir is the scratch directory the overlay is written into. It must already exist.
	Dir string
	// Plan is the resolved mode.
	Plan Plan
	// Toolchain is the probed toolchain the overlay will be compiled with.
	Toolchain Toolchain
}

// Generate writes the patched toolchain files, the two added files and overlay.json.
//
// It fails closed on every input it cannot prove: an un-reviewed toolchain version, a GOROOT
// whose files hash to something other than the reviewed bytes, an anchor that is missing or
// appears twice, and a GOROOT that already contains a file this overlay adds.
func Generate(request GenerationRequest) (*Overlay, error) {
	plan := request.Plan
	if !plan.Enabled() {
		return nil, fmt.Errorf("runtime overlay: mode %q compiles nothing; Generate must not be called for it", plan.Mode)
	}
	// Validated here as well as in Resolve, because a test or a future caller can build a Plan
	// directly, and the values that Validate refuses are the ones that compile and then throw at
	// startup on a target this host cannot run.
	if err := plan.Params.Validate(); err != nil {
		return nil, err
	}
	if err := request.Toolchain.Check(); err != nil {
		return nil, err
	}
	runtimeDir := filepath.Join(request.Toolchain.GOROOT, "src", "runtime")
	for _, name := range []string{addedOnFile, addedOffFile} {
		if _, err := os.Stat(filepath.Join(runtimeDir, name)); err == nil {
			return nil, fmt.Errorf(
				"runtime overlay: GOROOT %s already has src/runtime/%s, so the overlay would replace a file it means to add. "+
					"Either the toolchain ships that name now or a previous run left a patched GOROOT behind: inspect it before rebuilding",
				request.Toolchain.GOROOT, name)
		}
	}

	overlay := &Overlay{
		Plan:      plan,
		Dir:       request.Dir,
		Toolchain: request.Toolchain,
		Marker:    plan.Marker(),
	}

	// Group the edits by source file so each file is read once and its anchors are checked
	// against the text as it will actually be after the earlier edits in the same file.
	bySource := make(map[string][]edit)
	sources := make([]string, 0, 4)
	for _, current := range editsFor(plan) {
		if _, seen := bySource[current.source]; !seen {
			sources = append(sources, current.source)
		}
		bySource[current.source] = append(bySource[current.source], current)
	}
	sort.Strings(sources)

	outDir := filepath.Join(request.Dir, "runtime")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("runtime overlay: create %s: %w", outDir, err)
	}
	replace := make(map[string]string)

	for _, source := range sources {
		original := filepath.Join(request.Toolchain.GOROOT, filepath.FromSlash(source))
		data, err := os.ReadFile(original)
		if err != nil {
			return nil, fmt.Errorf("runtime overlay: read GOROOT %s: %w", source, err)
		}
		text := string(data)
		edited, err := applyEdits(source, text, bySource[source], request.Toolchain.GOVersion)
		if err != nil {
			return nil, err
		}
		text = edited
		name := strings.ReplaceAll(source, "/", "_")
		outPath := filepath.Join(outDir, name)
		if err := os.WriteFile(outPath, []byte(text), 0o644); err != nil {
			return nil, fmt.Errorf("runtime overlay: write %s: %w", outPath, err)
		}
		digest, err := digestOfBytes([]byte(text))
		if err != nil {
			return nil, err
		}
		overlay.Files = append(overlay.Files, GeneratedFile{Role: "replace", Source: source, Path: outPath, SHA256: digest})
		replace[original] = outPath
	}

	for _, added := range []struct {
		name    string
		content string
	}{
		{addedOnFile, plan.onFileSource()},
		{addedOffFile, plan.offFileSource()},
	} {
		// The generated file is parsed and formatted here rather than written as composed. Two
		// things fall out of that: the patched GOROOT is gofmt-clean, so a diff of it is readable
		// and a future formatting change cannot look like a semantic one; and a generator mistake
		// that produces something the compiler would reject fails at generation time, with the
		// generator's own message, instead of inside a gomobile build.
		formatted, err := format.Source([]byte(added.content))
		if err != nil {
			return nil, fmt.Errorf("runtime overlay: the generated %s does not parse: %w", added.name, err)
		}
		outPath := filepath.Join(outDir, added.name)
		if err := os.WriteFile(outPath, formatted, 0o644); err != nil {
			return nil, fmt.Errorf("runtime overlay: write %s: %w", outPath, err)
		}
		digest, err := digestOfBytes(formatted)
		if err != nil {
			return nil, err
		}
		overlay.Files = append(overlay.Files, GeneratedFile{Role: "add", Path: outPath, SHA256: digest})
		replace[filepath.Join(runtimeDir, added.name)] = outPath
	}

	payload, err := json.MarshalIndent(map[string]map[string]string{"Replace": replace}, "", "\t")
	if err != nil {
		return nil, fmt.Errorf("runtime overlay: encode overlay.json: %w", err)
	}
	overlay.JSONPath = filepath.Join(request.Dir, "overlay.json")
	if err := os.WriteFile(overlay.JSONPath, append(payload, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("runtime overlay: write %s: %w", overlay.JSONPath, err)
	}
	overlay.injectedLog = filepath.Join(request.Dir, "go-shim-injections.log")
	return overlay, nil
}

func digestOfBytes(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// applyEdits is the whole replacement engine: every anchor must occur exactly once, and the
// failure names the edit, the count and the toolchain version.
//
// It is a separate function because it is the part that must be provably safe: a count of zero
// means the toolchain moved and a count of two means the edit would have to choose, and a
// generator that chose would be a generator that silently patched the wrong function. It is
// also the part a test can drive with a synthetic toolchain, which is the only way to prove the
// refusal paths without breaking a real GOROOT.
func applyEdits(source, text string, edits []edit, goVersion string) (string, error) {
	for _, current := range edits {
		if count := strings.Count(text, current.anchor); count != 1 {
			return "", fmt.Errorf(
				"runtime overlay: %s contains the text the %s edit replaces %d times, not once, in %s. "+
					"The toolchain changed this code, so the edit's assumptions have to be re-read before it is applied again; "+
					"the overlay will not guess which occurrence is meant",
				source, current.group, count, goVersion)
		}
		text = strings.Replace(text, current.anchor, current.replacement, 1)
	}
	return text, nil
}

// onFileSource is the added file the plan's own build constraint selects. It carries the
// values and the marker; the off file carries upstream's values and no marker.
func (p Plan) onFileSource() string {
	var builder strings.Builder
	builder.WriteString("//go:build " + p.onConstraint() + "\n\n")
	builder.WriteString("// Generated by cmd/internal/runtimeoverlay. Do not edit, do not commit: the file is\n")
	builder.WriteString("// reconstructed for every libbox build from the mode recorded in libbox.provenance,\n")
	builder.WriteString("// and the build fails rather than guesses when the toolchain's runtime source is not\n")
	builder.WriteString("// the one this overlay was reviewed against.\n")
	builder.WriteString("//\n")
	builder.WriteString("// Values come from the plan, not from a patch: " + string(p.Mode) + " mode.\n\n")
	builder.WriteString("package runtime\n\n")
	if strings.Contains(p.dropExtraMExpression(), "goarch.") {
		builder.WriteString("import \"internal/goarch\"\n\n")
	} else if strings.Contains(strings.Join(p.extraEditTargets, ","), "amd64") && p.groupB() {
		// The probe's assembly edit is amd64, and no goarch term survives there, so nothing is
		// imported. Kept as an explicit branch so the reason is visible rather than implied.
		builder.WriteString("")
	}
	builder.WriteString("const (\n")
	builder.WriteString("\tjjOverlaySpanSetBlockEntries = " + itoa(p.Params.SpanSetBlockEntries) + "\n")
	builder.WriteString("\tjjOverlaySpanSetInitSpineCap = " + itoa(p.Params.SpanSetInitSpineCap) + "\n")
	builder.WriteString("\tjjOverlayStackCacheSize = " + itoa(p.Params.StackCacheSize) + "\n")
	builder.WriteString("\tjjOverlayFreeExitedStack = " + boolLiteral(p.Params.FreeExitedStack) + "\n")
	builder.WriteString("\tjjOverlayFreeExitedStackBacklog = " + itoa(p.Params.FreeExitedStackBacklog) + "\n")
	builder.WriteString(")\n\n")
	if p.groupB() {
		builder.WriteString("// jjOverlayDropExtraM is read by asm_arm64.s after every cgo callback, so it must be a\n")
		builder.WriteString("// variable and not a constant: the assembly loads its address.\n")
	} else {
		builder.WriteString("// jjOverlayDropExtraM is false because this mode does not enable group B; asm_arm64.s\n")
		builder.WriteString("// keeps upstream's path, which this value cannot reach.\n")
	}
	builder.WriteString("var jjOverlayDropExtraM = " + p.dropExtraMExpression() + "\n\n")
	builder.WriteString("const jjOverlayMarkerText = " + quote(p.Marker()) + "\n\n")
	builder.WriteString("// jjOverlayMarkerValue hands the marker out through a call rather than a package\n")
	builder.WriteString("// variable, and that is load-bearing: the runtime package's OWN variable initialisers have\n")
	builder.WriteString("// not run when schedinit calls jjOverlayMarker, so a variable initialised from this constant\n")
	builder.WriteString("// is still the zero value there and a check on it would fail on every launch. A constant\n")
	builder.WriteString("// compared with itself, on the other hand, folds to nothing and could take the string with\n")
	builder.WriteString("// it, which would leave the artifact unverifiable. The noinline call is what keeps both the\n")
	builder.WriteString("// comparison and the string alive.\n")
	builder.WriteString("//\n")
	builder.WriteString("//go:noinline\n")
	builder.WriteString("func jjOverlayMarkerValue() string {\n\treturn jjOverlayMarkerText\n}\n\n")
	builder.WriteString("// jjOverlayMarker is called from schedinit, which is what keeps the marker live.\n")
	builder.WriteString("//\n")
	builder.WriteString("//go:noinline\n")
	builder.WriteString("func jjOverlayMarker() {\n")
	builder.WriteString("\tif jjOverlayMarkerValue() == \"\" {\n")
	builder.WriteString("\t\tthrow(\"jiejiebox ios runtime overlay: marker was compiled empty\")\n")
	builder.WriteString("\t}\n")
	builder.WriteString("}\n")
	return builder.String()
}

// offFileSource is the added file every other target compiles. Its constants are upstream's
// values, so a slice that does not select the overlay behaves exactly as the official runtime,
// and it deliberately contains no marker string: a marker in a macOS slice is the constraint
// leaking, and verify.go fails on it.
func (p Plan) offFileSource() string {
	upstream := upstreamParams()
	var builder strings.Builder
	builder.WriteString("//go:build " + p.offConstraint() + "\n\n")
	builder.WriteString("// Generated by cmd/internal/runtimeoverlay. Every target outside " + p.onConstraint() + " compiles\n")
	builder.WriteString("// THIS file, which restates the toolchain's own values: the shared edits below are then\n")
	builder.WriteString("// inert on those slices, and no marker string exists for them to carry.\n\n")
	builder.WriteString("package runtime\n\n")
	builder.WriteString("const (\n")
	builder.WriteString("\tjjOverlaySpanSetBlockEntries = " + itoa(upstream.SpanSetBlockEntries) + " // 4KB on 64-bit\n")
	builder.WriteString("\tjjOverlaySpanSetInitSpineCap = " + itoa(upstream.SpanSetInitSpineCap) + " // Enough for 1GB heap on 64-bit\n")
	builder.WriteString("\tjjOverlayStackCacheSize = " + itoa(upstream.StackCacheSize) + "\n")
	builder.WriteString("\tjjOverlayFreeExitedStack = " + boolLiteral(upstream.FreeExitedStack) + "\n")
	builder.WriteString("\tjjOverlayFreeExitedStackBacklog = " + itoa(upstream.FreeExitedStackBacklog) + "\n")
	builder.WriteString(")\n\n")
	builder.WriteString("var jjOverlayDropExtraM = false\n\n")
	builder.WriteString("//go:noinline\n")
	builder.WriteString("func jjOverlayMarker() {}\n")
	return builder.String()
}

// InjectionCount reads how many go invocations the shim saw. Zero means the overlay never
// reached the compiler even though it was prepared, which is a build failure rather than a
// soft warning: the artifact would be an ordinary runtime with an extraordinary provenance
// record.
func (o *Overlay) InjectionCount() (int, error) {
	data, err := os.ReadFile(o.injectedLog)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count, nil
}

// ProvenanceLines are the libbox.provenance keys that record this overlay.
//
// They are separate keys rather than one composite so a reader (and a shell grep in the CI
// gate) can ask one question at a time, and so the file stays parseable by the existing
// `sed -n 's/^tags\.apple=//p'` idiom.
func (o *Overlay) ProvenanceLines() []string {
	lines := []string{
		"runtime.overlay=" + string(o.Plan.Mode),
		"runtime.overlay.go=" + o.Toolchain.GOVersion,
		"runtime.overlay.goroot=" + o.Toolchain.GOROOT,
		// The gomobile target the overlay was prepared for: it answers "which slices was this
		// aimed at" without inferring it from the mode, which is the question a reader has when a
		// framework has more slices than expected.
		"runtime.overlay.bind_target=" + o.Plan.BindTarget,
		"runtime.overlay.marker=" + o.Marker,
	}
	files := append([]GeneratedFile{}, o.Files...)
	sort.Slice(files, func(i, j int) bool {
		left, right := files[i].Path, files[j].Path
		if files[i].Source != "" {
			left = files[i].Source
		}
		if files[j].Source != "" {
			right = files[j].Source
		}
		return left < right
	})
	for _, file := range files {
		name := filepath.Base(file.Path)
		if file.Source != "" {
			name = strings.ReplaceAll(file.Source, "/", "_")
		}
		lines = append(lines, fmt.Sprintf("runtime.overlay.sha256.%s=%s", name, file.SHA256))
	}
	return lines
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}

func boolLiteral(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// quote renders a Go string literal for the generated source.
func quote(text string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text) + `"`
}
