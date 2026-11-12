//go:build with_naive_outbound

package naive

import (
	"runtime/debug"
	"sync"

	"github.com/sagernet/cronet-go"
)

// The cronet Go module this build imports. It is the module path in go.mod (upstream by design);
// the FACT about which bytes it resolved to is the replace directive, which is what
// release/source-lock.json records and what scripts/ci/verify-cronet-provenance.sh ties to the
// native archives.
const cronetGoModulePath = "github.com/sagernet/cronet-go"

// NativeContract is what this BUILD can state about the native artifact it is using (Q34/Q43).
//
// # Why a build fact and a runtime fact are kept apart
//
// The Go binding generation is knowable from the build (module version), while the native
// artifact's own version string only exists inside the loaded library and can therefore only be
// read from the engine after it started. Reporting them separately is what keeps the audit's rule:
// a module version is NOT a Chromium version, and the only honest source for the native version is
// the artifact itself. The link between the two is
// release/cronet-provenance*.txt, which records the sha256 of the archives a release build links
// for the pinned module version.
type NativeContract struct {
	// ModuleVersion is the resolved cronet-go module version, with the replace honored.
	ModuleVersion string
	// ModuleReplaced reports whether the build resolved the module through a replace directive,
	// i.e. whether it is the fork rather than upstream.
	ModuleReplaced bool
	// EngineVersion is the loaded engine's own version string, or empty before Start.
	EngineVersion string
	// CreationCallback reports whether this build reaches the engine's native creation callback.
	// It is a compile-time fact: the binding used by native_creation.go would not compile without
	// it, so a build that reports false here cannot exist.
	CreationCallback bool
	// Unknown is set when the build metadata could not be read at all (for example a test binary
	// built without module info). A reader must treat that as "not recorded", never as "upstream".
	Unknown bool
}

var (
	cronetModuleAccess   sync.Mutex
	cronetModuleCached   string
	cronetModuleReplaced bool
	cronetModuleRead     bool
)

// moduleVersionFromBuildInfo extracts the cronet-go binding generation from build metadata.
//
// It returns the REPLACEMENT's version when the module was replaced: the replace target is what the
// linker actually used, and the module path alone (upstream by design) would name the wrong bytes.
// A build that resolved upstream is reported as not replaced rather than as an error, because that
// is a fact a reader needs to see.
func moduleVersionFromBuildInfo(info *debug.BuildInfo) (version string, replaced bool, found bool) {
	if info == nil {
		return "", false, false
	}
	for _, dependency := range info.Deps {
		if dependency.Path != cronetGoModulePath {
			continue
		}
		if dependency.Replace != nil {
			return dependency.Replace.Version, true, true
		}
		return dependency.Version, false, true
	}
	return "", false, false
}

func readCronetModuleVersion() (string, bool) {
	cronetModuleAccess.Lock()
	defer cronetModuleAccess.Unlock()
	if !cronetModuleRead {
		cronetModuleRead = true
		if info, ok := debug.ReadBuildInfo(); ok {
			cronetModuleCached, cronetModuleReplaced, _ = moduleVersionFromBuildInfo(info)
		}
	}
	return cronetModuleCached, cronetModuleReplaced
}

// moduleContract fills the build-derived half of a NativeContract.
func moduleContract() NativeContract {
	version, replaced := readCronetModuleVersion()
	return NativeContract{
		ModuleVersion:    version,
		ModuleReplaced:   replaced,
		CreationCallback: true,
		Unknown:          version == "",
	}
}

// NativeContract reports the build and runtime identity of the native artifact in use.
//
// EngineVersion is filled by Start, once the native engine exists: reading an engine's version
// before it started would call into an uninitialised handle, and a record that can crash the
// process is worse than one that is empty until the fact exists.
func (h *Outbound) NativeContract() NativeContract {
	contract := moduleContract()
	contract.EngineVersion = h.engineVersion
	return contract
}

// The compile-time half of the ABI contract, in the production build.
//
// # Why these assertions live here and not only in a test
//
// native_creation.go's seam is only real while the pinned binding exposes the dialer callbacks and
// the engine setters that install them. Asserting the shapes in the production package means a pin
// that drops or renames them breaks the BUILD of the code that depends on them, at the moment the
// pin moves, instead of silently turning every counter into a zero. What these assertions do NOT
// prove is that the native archive implements the symbols - that is
// scripts/ci/verify-cronet-provenance.sh's job, and no Go assertion can replace it.
var (
	_ cronet.Dialer    = func(address string, port uint16) int { return -1 }
	_ cronet.UDPDialer = func(address string, port uint16) (int, string, uint16, func()) { return -1, "", 0, nil }
	_                  = cronet.Engine.SetDialer
	_                  = cronet.Engine.SetUDPDialer
)
