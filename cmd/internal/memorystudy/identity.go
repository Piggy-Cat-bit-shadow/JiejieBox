package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"time"
)

// captureIdentity records what this process is and what policy is actually in force.
//
// # Why every field is read and not passed in
//
// The task this instrument serves is to decide whether a build-time or startup-time change made a
// difference. A runner that records the flags it INTENDED to pass cannot tell the difference between
// "the change worked" and "the change was never applied": a lost linker flag, an environment
// variable the shell did not export, a GODEBUG string that a later entry overwrote, all look
// identical in a log of intentions. So the source revision comes from the binary's own VCS stamp
// (cross-checked against the working tree), the metric rate comes from runtime.MemProfileRate, and
// GOMAXPROCS comes from the runtime rather than from the flag that set it.
func captureIdentity(sampler *sampler) identity {
	build := captureBuildIdentity()
	source := captureSourceIdentity(build)
	policy := capturePolicyIdentity(sampler)
	overlay := captureOverlayIdentity(build)
	device := captureDeviceIdentity()

	notes := platformStaticNotes()
	for key, value := range sampler.static {
		notes["metric:"+key] = value
	}
	// Static statements about what this instrument cannot see, once per run, so that a null in a
	// reading always has a nearby explanation.
	notes["policy.adaptivestackstart"] = policy.AdaptiveStackReason
	notes["device.phys_footprint_bytes"] = device.Reason

	return identity{
		Source:      source,
		Build:       build,
		Policy:      policy,
		Overlay:     overlay,
		Device:      device,
		Unsupported: notes,
	}
}

func hostPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

func captureBuildIdentity() buildIdentity {
	build := buildIdentity{
		GoVersion:  runtime.Version(),
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		CGOEnabled: "unknown",
		Settings:   make(map[string]string),
	}
	executable, err := os.Executable()
	if err == nil {
		build.BinaryPath = executable
		if info, statErr := os.Stat(executable); statErr == nil {
			build.BinaryBytes = info.Size()
		}
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return build
	}
	build.TagsRaw = build.Settings["-tags"]
	for _, setting := range info.Settings {
		build.Settings[setting.Key] = setting.Value
		switch setting.Key {
		case "-tags":
			build.TagsRaw = setting.Value
			build.Tags = strings.Fields(setting.Value)
		case "GOOS":
			build.GOOS = setting.Value
		case "GOARCH":
			build.GOARCH = setting.Value
		case "GOAMD64":
			build.GOAMD64 = setting.Value
		case "GOEXPERIMENT":
			build.GOEXPERIMENT = setting.Value
		case "CGO_ENABLED":
			build.CGOEnabled = setting.Value
		case "-ldflags":
			build.LDFlags = setting.Value
		case "-buildmode":
			build.BuildMode = setting.Value
		case "-compiler":
			build.Compiler = setting.Value
		}
	}
	if build.Tags == nil && build.TagsRaw != "" {
		build.Tags = strings.Fields(build.TagsRaw)
	}
	if build.Tags == nil {
		build.Tags = []string{}
	}
	return build
}

// captureSourceIdentity prefers the working tree over the linker stamp when git is available,
// because the two answer different questions: vcs.revision is what the BINARY contains, and a
// reviewer comparing two runs needs to know the tree those runs were launched from. Both are kept,
// and a disagreement stays visible instead of being resolved silently.
func captureSourceIdentity(build buildIdentity) sourceIdentity {
	source := sourceIdentity{
		DetectedBy:    "none",
		ModuleVersion: moduleVersion(),
	}
	if cwd, err := os.Getwd(); err == nil {
		source.Root = cwd
	}
	if revision := build.Settings["vcs.revision"]; revision != "" {
		source.BuildSHA = revision
		source.SHA = revision
		source.Short = shortSHA(revision)
		source.DetectedBy = "buildinfo"
	}
	if modified, present := build.Settings["vcs.modified"]; present {
		dirty := modified == "true"
		source.BuildDirty = &dirty
		source.Dirty = dirty
	}

	gitSHA, gitBranch, gitDirty, dirtyPaths, ok := gitProbe()
	if ok {
		source.GitSHA = gitSHA
		source.GitDirty = gitDirty
		source.Branch = gitBranch
		source.DirtyPaths = dirtyPaths
		if gitSHA != "" {
			source.SHA = gitSHA
			source.Short = shortSHA(gitSHA)
			source.DetectedBy = "git-working-tree"
		}
		if gitDirty != nil {
			source.Dirty = *gitDirty
		}
	}

	// A DECLARED identity wins over both, and says so.
	//
	// The case this exists for: a baseline built from `git archive HEAD` in a directory that is not
	// a checkout. Without a declared revision that binary can report no source identity at all, and
	// a ledger with an empty sha is not evidence. The declaration is recorded as such - detected_by
	// is "declared", and declared_origin carries the command that produced the tree - so it is
	// auditable rather than indistinguishable from a measured value.
	if declared := os.Getenv("MEMORYSTUDY_SOURCE_SHA"); declared != "" {
		source.SHA = declared
		source.Short = shortSHA(declared)
		source.DetectedBy = "declared"
		source.DeclaredOrigin = os.Getenv("MEMORYSTUDY_SOURCE_ORIGIN")
		switch os.Getenv("MEMORYSTUDY_SOURCE_DIRTY") {
		case "0":
			dirty := false
			source.Dirty = dirty
		case "1":
			dirty := true
			source.Dirty = dirty
		}
	}
	return source
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" {
			return info.Main.Version
		}
	}
	return ""
}

// gitProbe reports the checked-out revision, the branch and whether the working tree has changes.
//
// The timeout is short and a failure is not fatal: a binary copied out of its checkout is still a
// valid measurement, it just has to say that its source identity came from the linker stamp.
func gitProbe() (sha string, branch string, dirty *bool, dirtyPaths []string, ok bool) {
	run := func(arguments ...string) (string, bool) {
		command := exec.Command("git", arguments...)
		command.Stdin = nil
		done := make(chan struct{})
		var output []byte
		var err error
		go func() {
			output, err = command.Output()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
			return "", false
		}
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(output)), true
	}

	revision, ok := run("rev-parse", "HEAD")
	if !ok || revision == "" {
		return "", "", nil, nil, false
	}
	branch, _ = run("rev-parse", "--abbrev-ref", "HEAD")
	status, ok := run("status", "--porcelain")
	if !ok {
		return revision, branch, nil, nil, true
	}
	isDirty := status != ""
	return revision, branch, &isDirty, parseStatusPaths(status), true
}

// parseStatusPaths turns porcelain output into the list of changed paths.
//
// The list is part of the identity, not decoration: a baseline captured on a tree with three other
// agents' work in progress is only interpretable if the ledger says WHICH files were not the
// committed revision. It is capped so that a pathological tree cannot turn every JSONL line into a
// megabyte.
func parseStatusPaths(status string) []string {
	const limit = 200
	var paths []string
	for _, line := range strings.Split(status, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// A rename reports "old -> new"; the new name is the one that exists.
		if arrow := strings.Index(path, " -> "); arrow >= 0 {
			path = path[arrow+4:]
		}
		paths = append(paths, path)
		if len(paths) == limit {
			paths = append(paths, "... (truncated)")
			break
		}
	}
	return paths
}

// capturePolicyIdentity reads the policy the process is running under.
func capturePolicyIdentity(sampler *sampler) policyIdentity {
	policy := policyIdentity{
		GOMAXPROCS:            runtime.GOMAXPROCS(0),
		GOMAXPROCSOrigin:      "runtime default (Go decides from the CPU count)",
		GODEBUGEnv:            os.Getenv("GODEBUG"),
		AdaptiveStackStart:    nil,
		AdaptiveStackStartObs: false,
		// adaptivestackstart has no exported accessor. /gc/stack/starting-size:bytes is the RESULT
		// of the setting and starts at the same value on a fresh process either way, so a single
		// reading cannot distinguish "off" from "on but not yet adapted". Saying it is observable
		// would be a guess dressed as a reading; the differential experiment belongs to the
		// runtime-strategy task, which trains stacks and compares starting-size after a GC.
		AdaptiveStackReason: "no exported runtime accessor and /gc/stack/starting-size:bytes has the same " +
			"initial value under both settings; a single reading cannot observe it",
	}
	if value, present := os.LookupEnv("GOMAXPROCS"); present && value != "" {
		policy.GOMAXPROCSOrigin = "environment GOMAXPROCS=" + value
		policy.GOMAXPROCSEnv = value
	}

	// runtime.MemProfileRate is the rate the profiler is using right now. 0 means no profiling at
	// all, which is the value the build-time experiment is trying to verify.
	rate := int64(runtime.MemProfileRate)
	policy.MemProfileRateBytes = &rate
	switch {
	case godedebugSetting(policy.GODEBUGEnv, "memprofilerate") != "":
		policy.MemProfileRateOrigin = "environment GODEBUG=memprofilerate"
	case linkedGODEBUGDefault(captureBuildIdentity()) != "":
		policy.MemProfileRateOrigin = "linked runtime.godebugDefault (build-time default)"
	default:
		policy.MemProfileRateOrigin = "Go default (sampling enabled at 512 KiB unless the binary says otherwise)"
	}

	sampler.read()
	policy.GOMEMLIMITBytes = sampler.uintValue("/gc/gomemlimit:bytes")
	policy.GOGCPercent = sampler.uintValue("/gc/gogc:percent")
	return policy
}

// linkedGODEBUGDefault extracts the value of a linker-stamped runtime.godebugDefault, if any.
//
// This is how a build-time GODEBUG default reaches a binary: `-X runtime.godebugDefault=...`. It is
// a whole-string REPLACEMENT of the Go version's default, not an addition, so reading it is the only
// way to know what a build actually shipped.
func linkedGODEBUGDefault(build buildIdentity) string {
	const marker = "runtime.godebugDefault="
	for _, field := range strings.Fields(build.LDFlags) {
		if strings.HasPrefix(field, marker) {
			return strings.TrimPrefix(field, marker)
		}
	}
	if position := strings.Index(build.LDFlags, marker); position >= 0 {
		value := build.LDFlags[position+len(marker):]
		if space := strings.IndexAny(value, " \t"); space >= 0 {
			value = value[:space]
		}
		return value
	}
	return ""
}

// godedebugSetting extracts one setting out of a GODEBUG string of "key=value,key2=value2".
func godedebugSetting(godebug string, key string) string {
	if godebug == "" {
		return ""
	}
	for _, part := range strings.Split(godebug, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if found && name == key {
			return value
		}
	}
	return ""
}

func captureOverlayIdentity(build buildIdentity) overlayIdentity {
	linked := linkedGODEBUGDefault(build)
	overlay := overlayIdentity{
		Mode:                 "none",
		LinkedGODEBUGDefault: linked,
	}
	switch {
	case linked != "":
		overlay.Mode = "linked-godebug-default"
		overlay.Detail = "the binary was linked with runtime.godebugDefault=" + linked +
			"; memorystudy itself applies no product runtime overlay, so this is inherited policy"
	default:
		overlay.Detail = "plain Go binary: no product runtime overlay is linked and no GODEBUG is set " +
			"by the runner unless -godebug or -gomaxprocs says so; the measured policy is whatever " +
			"the build and the environment produced, recorded above"
	}
	return overlay
}

func captureDeviceIdentity() deviceIdentity {
	return deviceIdentity{
		Supported:                  false,
		PhysFootprintBytes:         nil,
		PeakFootprintBytes:         nil,
		HostProcessAreNotFootprint: true,
		Reason: "no Apple device is attached: phys_footprint and its peak need an iOS/tvOS Network " +
			"Extension and are not obtainable from a host process. The host counters in " +
			"readings.process are analogues only; they are not this number",
	}
}

// readGODebugEvents reports every GODEBUG setting that has actually fired with a non-default
// behaviour, and how many times. It is the runtime's own answer to "did that setting do anything",
// which is stronger than the presence of a string in the binary.
//
// The metric names are discovered rather than listed: the set changes with the Go version, and a
// hardcoded list would silently stop covering new settings.
func readGODebugEvents() map[string]uint64 {
	const prefix = "/godebug/non-default-behavior/"
	var names []string
	for _, descriptor := range metrics.All() {
		if strings.HasPrefix(descriptor.Name, prefix) && descriptor.Kind == metrics.KindUint64 {
			names = append(names, descriptor.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	samples := make([]metrics.Sample, len(names))
	for index, name := range names {
		samples[index] = metrics.Sample{Name: name}
	}
	metrics.Read(samples)
	events := make(map[string]uint64, len(samples))
	for _, sample := range samples {
		if sample.Value.Kind() != metrics.KindUint64 {
			continue
		}
		count := sample.Value.Uint64()
		if count == 0 {
			continue
		}
		events[strings.TrimPrefix(sample.Name, prefix)] = count
	}
	if len(events) == 0 {
		return nil
	}
	return events
}

// resolveExecutable returns the path and size of the running binary, used for provenance lines.
func resolveExecutable() (string, int64) {
	executable, err := os.Executable()
	if err != nil {
		return "", 0
	}
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return executable, 0
	}
	if info, statErr := os.Stat(absolute); statErr == nil {
		return absolute, info.Size()
	}
	return absolute, 0
}
