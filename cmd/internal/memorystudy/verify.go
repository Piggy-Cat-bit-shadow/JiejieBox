package main

// Identity and provenance verification for the gate.
//
// # The two questions this file answers
//
//  1. Is the CANDIDATE ledger the checkout it claims to be? A gate that certifies a ledger measured
//     from an unknown tree is a report about an unknown tree. The candidate's revision must equal
//     this checkout's HEAD (on CI that is the commit under test), and its toolchain, tags, platform
//     and fixture must be the ones the budget's thresholds were derived for.
//  2. Is the BUDGET a reference, or was it re-derived from the candidate it is asked to judge? The
//     latter passes every rule by construction, so it is refused unless the caller explicitly asks
//     to self-check the reference ledger against itself.
//
// The baseline revision is deliberately NOT required to equal the candidate's: a normal code change
// has a new candidate SHA and an older reference SHA. The two are compared only to warn when they
// are the same, which means the budget cannot see a regression introduced at that revision.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ledgerSummary is what one ledger says about the world it was measured in. Everything here is read
// back from the JSONL, not from the process that is checking it.
//
// The source block is carried in FULL, not just the revision: a ledger that claims its revision came
// from the git working tree has to have matching git_sha/git_dirty fields, and a ledger that claims
// the build stamp has to be checkable against this binary's own stamp. Without those fields the
// claim "measured at X" is a self-declaration, and a self-declaration is what a washout needs.
type ledgerSummary struct {
	Runs           []string          `json:"runs"`
	SHA            string            `json:"source_sha"`
	BuildSHA       string            `json:"build_sha"`
	Dirty          bool              `json:"source_dirty"`
	DetectedBy     string            `json:"detected_by"`
	GitSHA         string            `json:"git_sha,omitempty"`
	GitDirty       *bool             `json:"git_dirty,omitempty"`
	BuildDirty     *bool             `json:"build_dirty,omitempty"`
	DirtyPaths     []string          `json:"dirty_paths,omitempty"`
	DeclaredOrigin string            `json:"declared_origin,omitempty"`
	GoVersion      string            `json:"go_version"`
	GOOS           string            `json:"goos"`
	GOARCH         string            `json:"goarch"`
	Tags           []string          `json:"tags"`
	FixtureVersion string            `json:"fixture_version"`
	FixtureDigests map[string]string `json:"fixture_digests,omitempty"`
}

func stringAt(record recordMap, path string) string {
	value, ok := record.resolve(path)
	if !ok || value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}

func boolAt(record recordMap, path string) (bool, bool) {
	value, ok := record.resolve(path)
	if !ok || value == nil {
		return false, false
	}
	flag, isBool := value.(bool)
	return flag, isBool
}

// stringListAt reads a JSON array of strings at a dotted path.
func stringListAt(record recordMap, path string) []string {
	value, ok := record.resolve(path)
	if !ok || value == nil {
		return nil
	}
	list, isList := value.([]any)
	if !isList {
		if texts, isStrings := value.([]string); isStrings {
			return texts
		}
		return nil
	}
	texts := make([]string, 0, len(list))
	for _, entry := range list {
		if text, isText := entry.(string); isText {
			texts = append(texts, text)
		}
	}
	return texts
}

// summariseLedger reads one ledger's identity.
//
// A ledger that mixes two revisions, two toolchains or two fixtures is refused rather than resolved
// to the first value: it is two measurements under one name, and every aggregate computed from it
// would describe neither.
func summariseLedger(records []recordMap) (ledgerSummary, error) {
	summary := ledgerSummary{FixtureDigests: make(map[string]string)}
	seen := make(map[string]map[string]bool)
	note := func(field, value string) {
		if value == "" {
			return
		}
		if seen[field] == nil {
			seen[field] = make(map[string]bool)
		}
		seen[field][value] = true
	}
	perRunDigest := make(map[string]string)
	perRunSpec := make(map[string]string)
	var dirtyPaths []string
	for _, record := range records {
		if record.recordKind() != recordStage && record.recordKind() != recordChildResult {
			continue
		}
		note("source.sha", stringAt(record, "identity.source.sha"))
		note("source.build_sha", stringAt(record, "identity.source.build_sha"))
		note("source.detected_by", stringAt(record, "identity.source.detected_by"))
		note("source.git_sha", stringAt(record, "identity.source.git_sha"))
		note("source.declared_origin", stringAt(record, "identity.source.declared_origin"))
		note("build.go_version", stringAt(record, "identity.build.go_version"))
		note("build.goos", stringAt(record, "identity.build.goos"))
		note("build.goarch", stringAt(record, "identity.build.goarch"))
		note("fixture.version", stringAt(record, "identity.fixture.version"))
		note("build.tags", strings.Join(stringListAt(record, "identity.build.tags"), ","))
		// Dirty is noted as a VALUE, not OR'd: a ledger whose first half says clean and whose second
		// half says dirty is two measurements under one name, and picking either answer would hide
		// that.
		if dirty, present := boolAt(record, "identity.source.dirty"); present {
			note("source.dirty", fmt.Sprint(dirty))
		}
		if dirty, present := boolAt(record, "identity.source.git_dirty"); present {
			note("source.git_dirty", fmt.Sprint(dirty))
		}
		if dirty, present := boolAt(record, "identity.source.build_dirty"); present {
			note("source.build_dirty", fmt.Sprint(dirty))
		}
		if len(dirtyPaths) == 0 {
			dirtyPaths = stringListAt(record, "identity.source.dirty_paths")
		}
		id := record.runID()
		if id == "" {
			continue
		}
		if _, exists := perRunSpec[id]; !exists {
			perRunSpec[id] = recordSpecSignature(record)
		}
		if digest := stringAt(record, "identity.fixture.document_sha256"); digest != "" {
			if existing, exists := perRunDigest[id]; exists && existing != digest {
				return summary, fmt.Errorf("run %s carries two fixture digests (%s and %s)", id, existing, digest)
			}
			perRunDigest[id] = digest
		}
	}
	for field, values := range seen {
		if len(values) > 1 {
			names := make([]string, 0, len(values))
			for value := range values {
				names = append(names, value)
			}
			sort.Strings(names)
			return summary, fmt.Errorf("the ledger mixes %s values (%s); one ledger is one measurement",
				field, strings.Join(names, ", "))
		}
	}
	first := func(field string) string {
		for value := range seen[field] {
			return value
		}
		return ""
	}
	summary.SHA = first("source.sha")
	summary.BuildSHA = first("source.build_sha")
	summary.DetectedBy = first("source.detected_by")
	summary.GitSHA = first("source.git_sha")
	summary.DeclaredOrigin = first("source.declared_origin")
	if raw := first("source.dirty"); raw != "" {
		summary.Dirty = raw == "true"
	}
	if raw := first("source.git_dirty"); raw != "" {
		value := raw == "true"
		summary.GitDirty = &value
	}
	if raw := first("source.build_dirty"); raw != "" {
		value := raw == "true"
		summary.BuildDirty = &value
	}
	summary.DirtyPaths = dirtyPaths
	summary.GoVersion = first("build.go_version")
	summary.GOOS = first("build.goos")
	summary.GOARCH = first("build.goarch")
	summary.FixtureVersion = first("fixture.version")
	if raw := first("build.tags"); raw != "" {
		summary.Tags = strings.Split(raw, ",")
	}
	summary.Runs = runIDs(records)
	// The digests are keyed by SPEC, not by run id: the run id is random per session, so a map
	// keyed by it could never match a different session's ledger, and the comparison would refuse
	// every real gate instead of the incompatible fixture it exists to catch.
	for id, digest := range perRunDigest {
		signature := perRunSpec[id]
		if existing, exists := summary.FixtureDigests[signature]; exists && existing != digest {
			return summary, fmt.Errorf("two runs of the spec %q produced different fixture documents "+
				"(%s and %s); the fixture is not deterministic", signature, existing, digest)
		}
		summary.FixtureDigests[signature] = digest
	}
	if len(summary.Runs) == 0 {
		return summary, fmt.Errorf("the ledger names no run; there is nothing to verify")
	}
	return summary, nil
}

// tagSet canonicalises a build-tag list. The linker setting is one comma-joined string, but a
// hand-written -tags argument can also carry spaces, and the ORDER of build tags is irrelevant to
// what was built. Comparing raw strings would fail a gate for a cosmetic difference and, worse,
// would hide a real tag change behind a reordering.
func tagSet(tags []string) []string {
	seen := make(map[string]bool)
	set := make([]string, 0, len(tags))
	for _, entry := range tags {
		for _, part := range strings.FieldsFunc(entry, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		}) {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			set = append(set, part)
		}
	}
	sort.Strings(set)
	return set
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// The sources a ledger's revision may come from. "declared" is the escape hatch for a tree that is
// not a checkout at all (an archive); it can never be verified, so it needs the caller to assert the
// revision explicitly.
const (
	detectedByGit       = "git-working-tree"
	detectedByBuildInfo = "buildinfo"
	detectedByDeclared  = "declared"
	liveOriginGit       = "git-working-tree"
	liveOriginBuildInfo = "buildinfo"
	liveOriginNone      = "none"
)

// liveIdentity is what THIS process can observe about the checkout it runs in, as opposed to what a
// ledger claims. The two are cross-checked field by field; neither side is trusted on its own.
//
// # Why the ledger alone is not enough
//
// Every source field in the ledger is written by the measured process. A ledger that says
// dirty=false on a dirty tree - one character changed - satisfied the first version of this check,
// which only compared the revision, and the gate then certified uncommitted code as the commit. The
// live `git status --porcelain` and the binary's own build stamp are the only values the gate can
// obtain without asking the evidence to describe itself.
type liveIdentity struct {
	SHA        string
	Origin     string // liveOriginGit | liveOriginBuildInfo | liveOriginNone
	Dirty      *bool
	DirtyPaths []string
	BuildSHA   string
	BuildDirty *bool
	BuildInfo  bool
}

// probeLiveIdentity reads the live sources: the git checkout first, the binary's VCS stamp second.
func probeLiveIdentity() liveIdentity {
	live := liveIdentity{Origin: liveOriginNone}
	build := captureBuildIdentity()
	live.BuildSHA = build.Settings["vcs.revision"]
	if modified, present := build.Settings["vcs.modified"]; present {
		dirty := modified == "true"
		live.BuildDirty = &dirty
	}
	live.BuildInfo = live.BuildSHA != ""
	if sha, _, dirty, paths, ok := gitProbe(); ok && sha != "" {
		live.SHA = sha
		live.Origin = liveOriginGit
		live.Dirty = dirty
		live.DirtyPaths = paths
		return live
	}
	if live.BuildInfo {
		live.SHA = live.BuildSHA
		live.Origin = liveOriginBuildInfo
		live.Dirty = live.BuildDirty
	}
	return live
}

// requireCandidateIdentity checks the candidate ledger against the checkout it must describe.
//
// The rule is "the candidate names this checkout", not "the candidate names the baseline": a change
// under test has a new SHA by definition, and requiring the two to match would make the gate fail
// for every normal commit.
//
// It checks three layers, because each one alone was a demonstrated false pass:
//  1. the ledger's own source block must be internally consistent and name a source this tool can
//     verify (`detected_by`); a ledger that does not say how it knows its revision is refused, and an
//     unknown source is refused rather than trusted;
//  2. the ledger's revision and dirty flag must equal what the LIVE checkout says;
//  3. when neither git nor the build stamp is available, the caller must assert the revision with
//     -source-sha, and the assertion is recorded as an assertion, not as a measurement.
func requireCandidateIdentity(summary ledgerSummary, live liveIdentity, explicitSourceSHA string) error {
	if summary.SHA == "" {
		return fmt.Errorf("the candidate ledger carries no source revision: a ledger without an " +
			"identity cannot be certified")
	}
	if live.BuildInfo && live.BuildSHA != "" && summary.BuildSHA != "" && !strings.EqualFold(summary.BuildSHA, live.BuildSHA) {
		// The binary in this tree was built from a different revision than the one the ledger
		// recorded: the ledger is not this binary's evidence.
		return fmt.Errorf("the ledger's binary was stamped at %s but this binary was built from %s; "+
			"the evidence and the checker are different builds", summary.BuildSHA, live.BuildSHA)
	}
	// 1. The claimed source of the revision must be one this tool can check.
	switch summary.DetectedBy {
	case detectedByGit:
		if !strings.EqualFold(summary.GitSHA, summary.SHA) {
			return fmt.Errorf("the ledger says its revision came from the git working tree, but its "+
				"git_sha (%q) disagrees with source.sha (%q); the source block is incoherent",
				summary.GitSHA, summary.SHA)
		}
		if summary.GitDirty == nil {
			return fmt.Errorf("the ledger says its revision came from the git working tree but records " +
				"no git_dirty flag; the working-tree state cannot be checked")
		}
		if *summary.GitDirty != summary.Dirty {
			return fmt.Errorf("the ledger's git_dirty=%t disagrees with its source.dirty=%t",
				*summary.GitDirty, summary.Dirty)
		}
	case detectedByBuildInfo:
		if !strings.EqualFold(summary.BuildSHA, summary.SHA) {
			return fmt.Errorf("the ledger says its revision came from the build stamp, but its "+
				"build_sha (%q) disagrees with source.sha (%q)", summary.BuildSHA, summary.SHA)
		}
		if !live.BuildInfo {
			return fmt.Errorf("the ledger says its revision came from the build stamp " +
				"(detected_by=buildinfo), but this binary carries no VCS information (buildvcs was not " +
				"stamped), so that claim cannot be checked; rebuild with buildvcs, or re-measure in a " +
				"checkout")
		}
		if !strings.EqualFold(live.BuildSHA, summary.SHA) {
			return fmt.Errorf("the ledger's build stamp says %s but this binary's says %s",
				summary.SHA, live.BuildSHA)
		}
		if live.BuildDirty != nil && summary.BuildDirty != nil && *live.BuildDirty != *summary.BuildDirty {
			return fmt.Errorf("the ledger's build_dirty=%t disagrees with this binary's %t",
				*summary.BuildDirty, *live.BuildDirty)
		}
	case detectedByDeclared:
		if explicitSourceSHA == "" {
			return fmt.Errorf("the ledger's revision was DECLARED, not measured (%s); pass "+
				"-source-sha with the revision this run is supposed to certify", summary.DeclaredOrigin)
		}
	case "":
		return fmt.Errorf("the candidate ledger does not say how its source revision was determined " +
			"(detected_by is empty); an unattributed revision cannot be verified")
	default:
		return fmt.Errorf("the candidate ledger names %q as the source of its revision; this tool "+
			"only accepts %q, %q or %q", summary.DetectedBy, detectedByGit, detectedByBuildInfo,
			detectedByDeclared)
	}

	// 2. The live checkout is authoritative when it is observable.
	if live.SHA == "" {
		if explicitSourceSHA == "" {
			return fmt.Errorf("cannot determine this checkout's revision (git is unavailable and the "+
				"binary carries no VCS stamp), and the candidate ledger claims %s; there is nothing to "+
				"check that claim against. Run the gate from a checkout or pass -source-sha %s "+
				"explicitly", summary.SHA, summary.SHA)
		}
		if !strings.EqualFold(explicitSourceSHA, summary.SHA) {
			return fmt.Errorf("-source-sha says %s but the candidate ledger was measured at %s",
				explicitSourceSHA, summary.SHA)
		}
	} else {
		if !strings.EqualFold(summary.SHA, live.SHA) {
			return fmt.Errorf("the candidate ledger was measured at %s but this checkout is %s "+
				"(read from %s); a gate must judge the commit under test, not a different tree",
				summary.SHA, live.SHA, live.Origin)
		}
		if explicitSourceSHA != "" && !strings.EqualFold(explicitSourceSHA, live.SHA) {
			return fmt.Errorf("-source-sha says %s but this checkout is %s; the assertion contradicts "+
				"the live source", explicitSourceSHA, live.SHA)
		}
	}

	// 3. The dirty flag is part of the identity: uncommitted code is not the commit.
	if live.Dirty != nil {
		if summary.Dirty != *live.Dirty {
			return fmt.Errorf("the candidate ledger says the working tree was dirty=%t, but this "+
				"checkout is dirty=%t (%d changed path(s)); the ledger does not describe this tree, so "+
				"its numbers cannot be attributed to %s", summary.Dirty, *live.Dirty,
				len(live.DirtyPaths), live.SHA)
		}
		if !*live.Dirty && len(summary.DirtyPaths) > 0 {
			return fmt.Errorf("the candidate ledger claims a clean tree but lists %d changed path(s); "+
				"the source block is incoherent", len(summary.DirtyPaths))
		}
	} else if explicitSourceSHA == "" {
		return fmt.Errorf("the working tree's state is not observable here (no git and no VCS stamp "+
			"with a modified flag), so the candidate's dirty=%t claim cannot be checked; pass "+
			"-source-sha to assert the revision explicitly", summary.Dirty)
	}

	if summary.BuildSHA != "" && live.SHA != "" && !strings.EqualFold(summary.BuildSHA, live.SHA) {
		return fmt.Errorf("the measured binary was built from %s, not from this checkout's %s; "+
			"rebuild the study binary in this tree before gating", summary.BuildSHA, live.SHA)
	}
	return nil
}

// requireBudgetComparable refuses a threshold set that was derived for a different world.
//
// Each field is checked for the same reason: a Go patch release changes the runtime's allocator and
// scheduler, a build tag selects a different protocol set (and so a different number of registered
// outbounds at the same node count), another platform has different page and thread accounting, and
// another fixture shape changes the document the heap numbers describe. None of these is noise the
// budget's headroom can absorb, so none of them is "close enough".
func requireBudgetComparable(budget budgetFile, summary ledgerSummary) (warnings []string, err error) {
	baseline := budget.Baseline
	if baseline.GOOS != "" && (baseline.GOOS != summary.GOOS || baseline.GOARCH != summary.GOARCH) {
		return warnings, fmt.Errorf("incompatible platform: the budget was derived on %s/%s and the "+
			"candidate was measured on %s/%s; thresholds are platform-specific", baseline.GOOS,
			baseline.GOARCH, summary.GOOS, summary.GOARCH)
	}
	if baseline.GOOS != "" && baseline.GOOS != runtime.GOOS {
		return warnings, fmt.Errorf("incompatible platform: the budget is for %s/%s but this gate runs "+
			"on %s/%s", baseline.GOOS, baseline.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if baseline.GoVersion != "" && summary.GoVersion != "" && baseline.GoVersion != summary.GoVersion {
		return warnings, fmt.Errorf("incompatible toolchain: the budget was derived with %s and the "+
			"candidate was measured with %s; a runtime upgrade changes the numbers these thresholds "+
			"describe", baseline.GoVersion, summary.GoVersion)
	}
	if baseline.GoVersion == "" || summary.GoVersion == "" {
		warnings = append(warnings, "the toolchain could not be read from the budget or the ledger; "+
			"the comparison assumes both used the same Go version")
	}
	if len(baseline.Tags) > 0 {
		want, got := tagSet(baseline.Tags), tagSet(summary.Tags)
		if !sameStrings(want, got) {
			return warnings, fmt.Errorf("incompatible build tags: the budget was derived with [%s] and "+
				"the candidate was measured with [%s]; the tag set decides which protocols are linked "+
				"in, and that changes what the same configuration costs", strings.Join(want, ","),
				strings.Join(got, ","))
		}
	}
	if baseline.FixtureVersion != "" && summary.FixtureVersion != "" &&
		baseline.FixtureVersion != summary.FixtureVersion {
		return warnings, fmt.Errorf("incompatible fixture: the budget describes %s and the candidate "+
			"measured %s", baseline.FixtureVersion, summary.FixtureVersion)
	}
	if baseline.FixtureVersion == "" {
		warnings = append(warnings, "the budget predates the recorded fixture version; its thresholds "+
			"may describe a different document")
	}
	if budget.Provenance != nil && len(budget.Provenance.FixtureDigests) > 0 {
		bySpec := make(map[string]string, len(budget.Provenance.FixtureDigests))
		for spec, digest := range budget.Provenance.FixtureDigests {
			bySpec[spec] = digest
		}
		seenSpecs := make(map[string]bool, len(bySpec))
		for spec := range bySpec {
			seenSpecs[spec] = true
		}
		for spec, digest := range summary.FixtureDigests {
			want, declared := bySpec[spec]
			switch {
			case !declared:
				return warnings, fmt.Errorf("incompatible fixture: the candidate measured the spec %q, "+
					"which the budget's reference never measured; re-derive the reference", spec)
			case want != digest:
				return warnings, fmt.Errorf("incompatible fixture: spec %q produced document %s in the "+
					"candidate and %s in the reference; the thresholds describe the other document",
					spec, digest, want)
			}
			delete(seenSpecs, spec)
		}
		for spec := range seenSpecs {
			return warnings, fmt.Errorf("incompatible fixture: the budget has a digest for spec %q but "+
				"the candidate has no run of it", spec)
		}
	}
	if summary.Dirty {
		// Reported here so the value is visible even on a run that fails a later check; the
		// authoritative comparison against the live tree is in requireCandidateIdentity.
		warnings = append(warnings, "the candidate working tree is dirty: the ledger describes "+
			"uncommitted code, so its SHA is the commit it was based on, not the code that ran")
	}
	return warnings, nil
}

// requireReferenceProvenance refuses a budget that cannot certify a candidate.
//
// Two independent refusals, because they catch different mistakes:
//   - a DERIVED budget came from `-write-budget -from`, i.e. from some ledger. It is fine for
//     inspecting what a session would have cost, but it is not a reference: anybody can derive one
//     from the candidate and pass. Only the capture path (which writes a manifest) or an archived
//     manifest makes a reference.
//   - a budget whose provenance ledger IS the candidate ledger is circular even if it is marked as
//     a reference: every rule then passes because the thresholds are the observation. The run ids
//     make that visible, because they are random per session and identical only for the same
//     evidence.
func requireReferenceProvenance(budget budgetFile, summary ledgerSummary, selfCheck bool) error {
	if budget.legacy || budget.Provenance == nil {
		// A legacy budget is evaluated for compatibility (the CI canary is one), but it cannot be
		// the reference this gate certifies against.
		return fmt.Errorf("this budget is %s or carries no provenance; only a reference budget "+
			"(captured with a manifest) may certify a candidate", describeBudgetVersion(budget))
	}
	if budget.Provenance.Kind != provenanceReference {
		return fmt.Errorf("this budget's provenance is %q, not %q: it was derived from a ledger "+
			"(-write-budget -from) rather than captured as a reference. A budget derived from the "+
			"candidate passes every rule by construction, so it cannot be the gate",
			budget.Provenance.Kind, provenanceReference)
	}
	if len(budget.Baseline.Runs) == 0 {
		return fmt.Errorf("the reference budget names no baseline run; its provenance cannot be checked")
	}
	if sameStringSet(budget.Baseline.Runs, summary.Runs) {
		if !selfCheck {
			return fmt.Errorf("circular comparison: the budget was derived from the very ledger it is "+
				"asked to certify (all %d run id(s) match). Its thresholds are this session's own "+
				"observations, so every rule must pass; re-capture the reference on a tree without the "+
				"change, or pass -self-check to verify the reference ledger against itself explicitly",
				len(summary.Runs))
		}
		return nil
	}
	return nil
}

func describeBudgetVersion(budget budgetFile) string {
	if budget.legacy {
		return "the legacy schema/1 contract"
	}
	if budget.Provenance == nil {
		return "an unprovenanced schema/2 file"
	}
	return budget.SchemaVersion
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftSorted := append([]string(nil), left...)
	rightSorted := append([]string(nil), right...)
	sort.Strings(leftSorted)
	sort.Strings(rightSorted)
	for index := range leftSorted {
		if leftSorted[index] != rightSorted[index] {
			return false
		}
	}
	return true
}

// gateVerification is the audit trail the gate writes next to its verdict. It is part of the
// report on purpose: "the gate passed" without the identities it compared is not reproducible.
type gateVerification struct {
	CandidateSHA        string   `json:"candidate_sha"`
	CandidateDetectedBy string   `json:"candidate_detected_by"`
	CandidateDirty      bool     `json:"candidate_dirty"`
	CandidateDirtyPaths int      `json:"candidate_dirty_paths"`
	CheckoutSHA         string   `json:"checkout_sha"`
	CheckoutSource      string   `json:"checkout_source"`
	CheckoutDirty       *bool    `json:"checkout_dirty,omitempty"`
	CheckoutDirtyPaths  int      `json:"checkout_dirty_paths"`
	IdentityVerified    bool     `json:"identity_verified"`
	SourceSHAExplicit   string   `json:"source_sha_explicit,omitempty"`
	BaselineSHA         string   `json:"baseline_sha"`
	BaselineDirty       bool     `json:"baseline_dirty"`
	BaselineIsCandidate bool     `json:"baseline_is_candidate"`
	SelfCheck           bool     `json:"self_check"`
	ReferenceKind       string   `json:"reference_kind,omitempty"`
	ReferenceLedger     string   `json:"reference_ledger_sha256,omitempty"`
	FixtureVersion      string   `json:"fixture_version,omitempty"`
	GoVersion           string   `json:"go_version"`
	GOOS                string   `json:"goos"`
	GOARCH              string   `json:"goarch"`
	Tags                []string `json:"tags"`
	LegacyBudget        bool     `json:"legacy_budget,omitempty"`
	Warnings            []string `json:"warnings,omitempty"`
}

// verifyGateInputs runs every identity check the gate needs and returns the audit block.
//
// The checks are ordered so that the message names the MOST fundamental problem first: a ledger
// that is not this checkout cannot be compared to anything, and a budget that is not a reference
// cannot certify anything.
func verifyGateInputs(budget budgetFile, summary ledgerSummary, selfCheck bool, explicitSourceSHA string) (gateVerification, error) {
	return verifyGateInputsAgainst(budget, summary, selfCheck, probeLiveIdentity(), explicitSourceSHA)
}

// verifyGateInputsAgainst is verifyGateInputs with the live sources passed in, so a test can exercise
// every comparison without depending on the working tree it happens to run in.
func verifyGateInputsAgainst(budget budgetFile, summary ledgerSummary, selfCheck bool, live liveIdentity, explicitSourceSHA string) (gateVerification, error) {
	verification := gateVerification{
		CandidateSHA:        summary.SHA,
		CandidateDetectedBy: summary.DetectedBy,
		CandidateDirty:      summary.Dirty,
		CandidateDirtyPaths: len(summary.DirtyPaths),
		CheckoutSHA:         live.SHA,
		CheckoutSource:      live.Origin,
		CheckoutDirty:       live.Dirty,
		CheckoutDirtyPaths:  len(live.DirtyPaths),
		IdentityVerified:    live.SHA != "" && strings.EqualFold(live.SHA, summary.SHA),
		SourceSHAExplicit:   explicitSourceSHA,
		BaselineSHA:         budget.Baseline.SourceSHA,
		BaselineDirty:       budget.Baseline.SourceDirty,
		SelfCheck:           selfCheck,
		FixtureVersion:      summary.FixtureVersion,
		GoVersion:           summary.GoVersion,
		GOOS:                summary.GOOS,
		GOARCH:              summary.GOARCH,
		Tags:                summary.Tags,
		LegacyBudget:        budget.legacy,
	}
	if budget.Provenance != nil {
		verification.ReferenceKind = budget.Provenance.Kind
		verification.ReferenceLedger = budget.Provenance.LedgerSHA256
	}
	if err := requireCandidateIdentity(summary, live, explicitSourceSHA); err != nil {
		return verification, err
	}
	if budget.Baseline.SourceDirty {
		verification.Warnings = append(verification.Warnings,
			fmt.Sprintf("the reference budget was captured on a DIRTY tree (source_dirty=true, "+
				"revision %s): its thresholds describe uncommitted code, not that commit",
				shortOrNone(budget.Baseline.SourceSHA)))
	}
	if budget.Baseline.SourceSHA != "" && strings.EqualFold(budget.Baseline.SourceSHA, summary.SHA) {
		verification.BaselineIsCandidate = true
		verification.Warnings = append(verification.Warnings,
			"the budget's baseline revision equals the candidate's: this reference cannot detect a "+
				"regression introduced at this revision, only drift from it")
	}
	warnings, err := requireBudgetComparable(budget, summary)
	verification.Warnings = append(verification.Warnings, warnings...)
	if err != nil {
		return verification, err
	}
	if err = requireReferenceProvenance(budget, summary, selfCheck); err != nil {
		return verification, err
	}
	return verification, nil
}

// referenceManifest is the archived record of a reference capture.
//
// It is the trust anchor the derived-budget path lacks: a manifest is written only by the capture
// workflow, it names the ledger's SHA-256 and the revision it was captured at, and it is archived
// with the ledger in the evidence root. -write-budget -from needs one to mint a reference budget,
// so a candidate ledger cannot be promoted to a reference without producing an artefact a reviewer
// can see (and which names the candidate revision in its source_sha).
type referenceManifest struct {
	SchemaVersion  string            `json:"schema_version"`
	Kind           string            `json:"kind"`
	CapturedAt     string            `json:"captured_at"`
	SourceSHA      string            `json:"source_sha"`
	SourceDirty    bool              `json:"source_dirty"`
	GoVersion      string            `json:"go_version"`
	GOOS           string            `json:"goos"`
	GOARCH         string            `json:"goarch"`
	Tags           []string          `json:"tags"`
	FixtureVersion string            `json:"fixture_version"`
	Repetitions    int               `json:"repetitions"`
	Specs          []string          `json:"specs"`
	Runs           []string          `json:"runs"`
	LedgerPath     string            `json:"ledger_path"`
	LedgerSHA256   string            `json:"ledger_sha256"`
	LedgerRecords  int               `json:"ledger_records"`
	FixtureDigests map[string]string `json:"fixture_digests,omitempty"`
}

// manifestFromLedger builds the archived record for a ledger that was just measured.
func manifestFromLedger(summary ledgerSummary, records int, ledgerPath, ledgerSHA string, repetitions int, planned []runSpec) referenceManifest {
	specs := make([]string, 0, len(planned))
	for _, spec := range planned {
		specs = append(specs, specSignature(spec))
	}
	return referenceManifest{
		SchemaVersion:  referenceSchemaVersion,
		Kind:           provenanceReference,
		CapturedAt:     nowStamp(),
		SourceSHA:      summary.SHA,
		SourceDirty:    summary.Dirty,
		GoVersion:      summary.GoVersion,
		GOOS:           summary.GOOS,
		GOARCH:         summary.GOARCH,
		Tags:           summary.Tags,
		FixtureVersion: summary.FixtureVersion,
		Repetitions:    repetitions,
		Specs:          specs,
		Runs:           summary.Runs,
		LedgerPath:     ledgerPath,
		LedgerSHA256:   ledgerSHA,
		LedgerRecords:  records,
		FixtureDigests: summary.FixtureDigests,
	}
}

func writeManifest(path string, manifest referenceManifest) error {
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(content, '\n'), 0o644)
}

func readManifest(path string) (referenceManifest, error) {
	var manifest referenceManifest
	content, err := os.ReadFile(path)
	if err != nil {
		return manifest, fmt.Errorf("read the reference manifest: %w", err)
	}
	if err = json.Unmarshal(content, &manifest); err != nil {
		return manifest, fmt.Errorf("decode %s: %w", path, err)
	}
	if manifest.SchemaVersion != referenceSchemaVersion {
		return manifest, fmt.Errorf("%s is %q, want %q", path, manifest.SchemaVersion, referenceSchemaVersion)
	}
	if manifest.Kind != provenanceReference {
		return manifest, fmt.Errorf("%s declares kind %q; only a reference capture may mint a "+
			"reference budget", path, manifest.Kind)
	}
	if manifest.LedgerSHA256 == "" {
		return manifest, fmt.Errorf("%s does not record the ledger's SHA-256", path)
	}
	return manifest, nil
}

// sha256File hashes the exact bytes of a file: a ledger is evidence, and evidence is bound to its
// bytes, not to a path that can be overwritten.
func sha256File(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}

func nowStamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
