//go:build !windows && !linux && !darwin

package route

import "github.com/sagernet/sing-box/common/netcap"

// globalIPv6RouteObservation is the honest answer on a platform with no reader: nothing was observed.
//
// This is not a stub to be filled in later so much as the correct value for the platforms that reach
// it. RouteUnobserved contributes nothing to the capability, so on Android and iOS the verdict stays
// exactly what it was - derived from addressing alone - rather than gaining a hard negative nobody
// measured. A wrong negative here defaults an entire family off on a platform nobody tested, which is
// worse than the missed fast-fail it would buy.
func globalIPv6RouteObservation(ownInterfaceNames []string, ownInterfaceIndices []int) RouteObservationResult {
	return RouteObservationResult{Observation: netcap.RouteUnobserved}
}
