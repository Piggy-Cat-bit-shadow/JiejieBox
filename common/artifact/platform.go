package artifact

import "runtime"

// currentPlatform and currentABI are variables so a test can present another platform's
// manifest without a build tag.
var (
	currentPlatform = func() string { return runtime.GOOS }
	currentABI      = func() string { return runtime.GOOS + "/" + runtime.GOARCH }
)
