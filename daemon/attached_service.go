package daemon

import (
	"context"
	"time"
)

const defaultAttachedLogMaxLines = 3000

// StartOrReloadService and CloseService must not be called on an attached service.
func NewAttachedService(ctx context.Context) *StartedService {
	instance := attachInstance(ctx)
	s := NewStartedService(ServiceOptions{
		Context:     ctx,
		LogMaxLines: defaultAttachedLogMaxLines,
	})
	// `setInstanceLocked` is the ONE writer of the pair (instance, instanceID): a stream captures
	// both together, and a batch that names no instance is a batch a consumer cannot attribute.
	// Assigning s.instance directly here left instanceID empty, so every connection-event
	// subscription on this path - the Native API service of a standalone `run`, which is what the
	// macOS headless smoke test and the dashboard's connection list use - was refused with
	// "service is not running" while status, logs and outbounds streamed normally.
	s.serviceAccess.Lock()
	s.setInstanceLocked(instance)
	s.serviceAccess.Unlock()
	s.serviceStatus = &ServiceStatus{Status: ServiceStatus_STARTED}
	s.startedAt = time.Now()
	instance.urlTestHistoryStorage.AddUpdateHook(s.urlTestSubscriber)
	if instance.clashMode != nil {
		instance.clashMode.AddUpdateHook(s.clashModeSubscriber)
	}
	instance.logFactory.AttachPlatformWriter(s)
	return s
}
