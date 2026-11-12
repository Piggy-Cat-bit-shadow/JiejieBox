package daemon

import "context"

type PlatformHandler interface {
	WriteDebugMessage(message string)
	ConnectSSHAgent() (int32, error)
}

type ManagedHandler interface {
	ServiceStop() error
	ServiceReload(ctx context.Context) error
	SystemProxyStatus() (*SystemProxyStatus, error)
	SetSystemProxyEnabled(enabled bool) error
	TriggerNativeCrash() error
}

// ReloadOutcome is what a request-identified reload reports. It is the server half of the
// ReloadServiceWithRequest contract: the request identity is echoed, and the instance and
// revision that are running afterwards are read from the service rather than from the request.
type ReloadOutcome struct {
	// RequestID is the identity the caller supplied for this attempt.
	RequestID string
	// InstanceID is the instance running after the attempt, or empty when none is running.
	InstanceID string
	// ActiveRevision is the profile revision serving after the attempt, or empty when nothing
	// is running.
	ActiveRevision string
	// Stale says the caller's expectedInstanceId did not match the instance that was running
	// when the request arrived. The reload is still performed.
	Stale bool
	// Status is ReloadStatusRestarted or ReloadStatusStopped.
	Status string
}

const (
	// ReloadStatusRestarted says a (possibly new) instance is running after the attempt.
	ReloadStatusRestarted = "restarted"
	// ReloadStatusStopped says no instance is running after the attempt.
	ReloadStatusStopped = "stopped"
)

// RequestAwareManagedHandler is the optional half of ManagedHandler. A handler that implements
// it can answer ReloadServiceWithRequest; a handler that does not leaves the RPC at
// codes.Unimplemented, which is exactly how an older daemon answers it and what makes the
// client fall back to the legacy ReloadService.
type RequestAwareManagedHandler interface {
	ServiceReloadWithRequest(ctx context.Context, requestID string, expectedInstanceID string) (*ReloadOutcome, error)
}
