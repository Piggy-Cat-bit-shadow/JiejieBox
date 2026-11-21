package runtimeoverlay

import (
	"fmt"
	"os"
	"path/filepath"
)

// PrepareRequest is one libbox build's request for an overlay.
type PrepareRequest struct {
	// Root is the module root the build runs in. Used only as the working directory for
	// `go env`, so the probed toolchain is the one this module's GOTOOLCHAIN resolves.
	Root string
	// Mode is the requested mode. ModeBase prepares nothing and returns a nil Prepared.
	Mode Mode
	// BindTarget is the gomobile -target value.
	BindTarget string
	// GoBinary is the go executable gomobile itself will run. The shim delegates to exactly this
	// one, so the overlay cannot be applied by a different toolchain than the one probed.
	GoBinary string
	// Env is the environment gomobile will run with.
	Env []string
}

// Prepared is a written overlay plus the environment that makes gomobile use it.
type Prepared struct {
	*Overlay
	// Env is request.Env with the shim directory first on PATH.
	Env []string
	// Skipped explains why nothing was prepared, when ModeBase was resolved for a reason other
	// than the requested one (a macOS-only target, say).
	Skipped string
}

// Close removes the overlay's scratch directory. It is safe on a nil Prepared.
func (p *Prepared) Close() {
	if p == nil || p.Overlay == nil || p.Overlay.Dir == "" {
		return
	}
	_ = os.RemoveAll(p.Overlay.Dir)
}

// Prepare resolves the plan, writes the overlay and returns the environment gomobile must run
// with.
//
// It returns (nil, nil) when the plan compiles nothing, which is the honest representation of
// "this build keeps the official runtime": callers must not be able to forget to check a
// boolean, so there is nothing to use.
func Prepare(request PrepareRequest) (*Prepared, error) {
	plan, err := Resolve(request.Mode, request.BindTarget)
	if err != nil {
		return nil, err
	}
	if !plan.Enabled() {
		skipped := ""
		if request.Mode != ModeBase {
			skipped = fmt.Sprintf(
				"the gomobile target %q contains no iOS-family platform, so this build keeps the toolchain's runtime",
				request.BindTarget)
		}
		return &Prepared{Env: request.Env, Skipped: skipped}, nil
	}
	if err := CheckGoFlagsHasNoOverlay(request.Env); err != nil {
		return nil, err
	}
	if request.GoBinary == "" {
		return nil, fmt.Errorf("runtime overlay: no go binary was resolved, so the overlay has no toolchain to aim at")
	}
	toolchain, err := Probe(request.GoBinary, request.Root, request.Env)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "jiejiebox-runtime-overlay-")
	if err != nil {
		return nil, fmt.Errorf("runtime overlay: create a scratch directory: %w", err)
	}
	overlay, err := Generate(GenerationRequest{
		Root:      request.Root,
		Dir:       dir,
		Plan:      plan,
		Toolchain: toolchain,
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	shimDir, err := WriteGoShim(dir, request.GoBinary, overlay.JSONPath, overlay.injectedLog)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	overlay.SetShimDir(shimDir)
	return &Prepared{
		Overlay: overlay,
		Env:     WithShimPath(request.Env, shimDir),
	}, nil
}

// OverlayDirName is the scratch directory prefix, so an operator can find a leftover.
const OverlayDirName = "jiejiebox-runtime-overlay-"

// Describe is the one-line summary a build log prints.
func (o *Overlay) Describe() string {
	return fmt.Sprintf("runtime overlay: mode %s, Go %s, %d patched file(s), shim %s",
		o.Plan.Mode, o.Toolchain.GOVersion, len(o.Files), filepath.Join(o.ShimDir(), "go"))
}
