package main

import (
	"os"
	"os/exec"

	"github.com/sagernet/sing-box/cmd/internal/runtimeoverlay"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
)

// runtimeOverlayMode is the -runtime-overlay flag: which of the four local build modes this
// Apple build compiles.
//
// It is a PRIVATE builder flag, not a product switch. The shipped artifact uses
// runtimeoverlay.DefaultMode unless an operator overrides it here or with JJ_RUNTIME_OVERLAY, and
// the override exists for local A/B attribution: one `both`, one `small`, one `base` from the
// same commit is how an effect is attributed to a group. Nothing in the client, the workflow or
// the config schema learns about it, which is deliberate - a user-facing switch would be a second
// product configuration surface for a decision that belongs to the build.
var runtimeOverlayMode string

// appleRuntimeOverlay is the overlay this run prepared, or nil when the build compiles the
// toolchain's runtime untouched. It is package-level because writeProvenance runs after the
// builder has finished and the artifact's record has to name the plan that produced it.
var appleRuntimeOverlay *runtimeoverlay.Prepared

// resolvedRuntimeOverlayMode is the mode this run RESOLVED, which is not always the mode that was
// asked for: a target with no iOS-family slice resolves to base. It is recorded so an artifact's
// provenance says what the build did rather than what it was told.
var resolvedRuntimeOverlayMode = runtimeoverlay.DefaultMode

// runtimeOverlayModeFromEnvironment applies the flag/environment/default precedence. The refusal
// names the input the value actually came from: a message that named the flag for a value read out
// of JJ_RUNTIME_OVERLAY sent the operator to the wrong variable.
func runtimeOverlayModeFromEnvironment() (runtimeoverlay.Mode, error) {
	requested := runtimeOverlayMode
	source := "-runtime-overlay"
	if requested == "" {
		requested = os.Getenv("JJ_RUNTIME_OVERLAY")
		source = "JJ_RUNTIME_OVERLAY"
	}
	if requested == "" {
		return runtimeoverlay.DefaultMode, nil
	}
	mode, err := runtimeoverlay.ParseMode(requested)
	if err != nil {
		return "", E.Cause(err, "resolve "+source)
	}
	return mode, nil
}

// prepareRuntimeOverlay resolves and writes the overlay for one Apple build.
//
// A nil result with a nil error means "compile the toolchain's runtime", which is the case for
// mode base and for a target that has no iOS-family slice. The caller must then leave the
// gomobile environment alone: a shim on PATH with nothing to inject is not harmlessly redundant,
// it is a build whose record would claim an overlay.
func prepareRuntimeOverlay(bindTarget string) error {
	mode, err := runtimeOverlayModeFromEnvironment()
	if err != nil {
		return err
	}
	// The runtime the inner gomobile build compiles is found through PATH, exactly as it will be
	// found there. Resolving the same binary here is what keeps the shim delegating to the
	// toolchain that was probed.
	goBinary, err := exec.LookPath("go")
	if err != nil {
		return E.Cause(err, "locate the go toolchain for the runtime overlay")
	}
	prepared, err := runtimeoverlay.Prepare(runtimeoverlay.PrepareRequest{
		Root:       rootDir,
		Mode:       mode,
		BindTarget: bindTarget,
		GoBinary:   goBinary,
		Env:        os.Environ(),
	})
	if err != nil {
		return err
	}
	if prepared.Skipped != "" {
		log.Info("runtime overlay: skipped - ", prepared.Skipped)
	}
	if prepared.Overlay == nil {
		resolvedRuntimeOverlayMode = runtimeoverlay.ModeBase
		appleRuntimeOverlay = nil
		log.Info("runtime overlay: mode ", runtimeoverlay.ModeBase, " - the toolchain's runtime is compiled unchanged")
		return nil
	}
	resolvedRuntimeOverlayMode = prepared.Plan.Mode
	appleRuntimeOverlay = prepared
	log.Info(prepared.Describe())
	return nil
}

// verifyRuntimeOverlay proves the prepared plan reached the artifact.
//
// Two separate claims, both required:
//
//   - the shim was actually used, which the shim's own injection log answers. An overlay that
//     was written and never consulted produces an ordinary runtime with an extraordinary
//     provenance record, and that is the failure this check exists for.
//   - the slices carry the marker, which the artifact's own bytes answer. This is what an `nm`
//     lookup cannot do here: release libbox links with -s -w, so the symbol table is gone by the
//     time the framework exists.
func verifyRuntimeOverlay(prepared *runtimeoverlay.Prepared, frameworkPath string) error {
	injections, err := prepared.InjectionCount()
	if err != nil {
		return E.Cause(err, "read the overlay shim's injection log")
	}
	if injections == 0 {
		return E.New("the runtime overlay was prepared but the go shim was never used, so the framework was compiled from the toolchain's runtime: ",
			"gomobile did not resolve `go` through the PATH this build set, and the artifact would otherwise be recorded as mode ", prepared.Plan.Mode)
	}
	log.Info("runtime overlay: the go shim injected -overlay into ", injections, " go invocation(s)")
	checks, err := runtimeoverlay.RequireVerifiedXCframework(frameworkPath, prepared.Marker)
	if err != nil {
		return E.Cause(err, "verify the runtime overlay in ", frameworkPath)
	}
	for _, check := range checks {
		state := "absent"
		if check.AnyMarker {
			state = "present"
		}
		log.Info("runtime overlay: slice ", check.Slice, " (marker ", state, ", ", ternary(check.IOSFamily, "iOS family", "other"), ")")
	}
	return nil
}

func ternary(condition bool, left, right string) string {
	if condition {
		return left
	}
	return right
}

// runtimeOverlayProvenanceLines are appended to libbox.provenance so the artifact records the
// plan that produced it, the Go version it was compiled with, and the digest of every file the
// overlay wrote. A reader can then answer "was this framework built past the overlay" without
// rebuilding it.
//
// The mode line is written even when nothing was overlaid: "base" is a fact about the artifact,
// and a missing key would be indistinguishable from an older build that knew nothing about this.
func runtimeOverlayProvenanceLines() []string {
	if target != "apple" {
		return nil
	}
	if appleRuntimeOverlay == nil {
		return []string{"runtime.overlay=" + string(resolvedRuntimeOverlayMode)}
	}
	return appleRuntimeOverlay.ProvenanceLines()
}
