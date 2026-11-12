package main

import (
	"context"
	"os"

	"github.com/sagernet/sing-box/daemon"
	E "github.com/sagernet/sing/common/exceptions"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ daemon.ManagedHandler = (*managedHandler)(nil)

type managedHandler struct {
	daemon *Daemon
}

func (h *managedHandler) ServiceStop() error {
	if h.daemon.closed {
		return os.ErrClosed
	}
	ownerUserID, err := loadOwner()
	if err != nil {
		return err
	}
	return h.daemon.stopServiceLocked(ownerUserID)
}

func (h *managedHandler) ServiceReload(ctx context.Context) error {
	_, err := h.serviceReload(ctx, "", "")
	return err
}

func (h *managedHandler) ServiceReloadWithRequest(ctx context.Context, requestID string, expectedInstanceID string) (*daemon.ReloadOutcome, error) {
	return h.serviceReload(ctx, requestID, expectedInstanceID)
}

// serviceReload is the one reload body: the legacy RPC and the request-identified RPC must not
// grow two different reload semantics. The identity half only observes - it reads the instance
// before and after the same call the old entry performs.
func (h *managedHandler) serviceReload(ctx context.Context, requestID string, expectedInstanceID string) (*daemon.ReloadOutcome, error) {
	if h.daemon.closed {
		return nil, os.ErrClosed
	}
	previousInstanceID, _ := h.daemon.startedService.ServiceIdentity()
	stale := expectedInstanceID != "" && expectedInstanceID != previousInstanceID
	ownerUserID, err := loadOwner()
	if err != nil {
		return nil, err
	}
	configContent, err := loadServiceConfig(ownerUserID)
	if err != nil {
		return nil, err
	}
	options, err := loadStartOptions(ownerUserID)
	if err != nil {
		return nil, err
	}
	err = h.daemon.startServiceLocked(ctx, ownerUserID, configContent, options)
	if err != nil {
		return nil, err
	}
	options.WasRunning = true
	err = saveStartOptions(ownerUserID, options)
	if err != nil {
		return nil, err
	}
	instanceID, activeRevision := h.daemon.startedService.ServiceIdentity()
	status := daemon.ReloadStatusStopped
	if instanceID != "" {
		status = daemon.ReloadStatusRestarted
	}
	return &daemon.ReloadOutcome{
		RequestID:      requestID,
		InstanceID:     instanceID,
		ActiveRevision: activeRevision,
		Stale:          stale,
		Status:         status,
	}, nil
}

func (h *managedHandler) SystemProxyStatus() (*daemon.SystemProxyStatus, error) {
	if h.daemon.platform == nil {
		return &daemon.SystemProxyStatus{}, nil
	}
	return h.daemon.platform.SystemProxyStatus()
}

func (h *managedHandler) SetSystemProxyEnabled(enabled bool) error {
	if h.daemon.platform == nil {
		if !enabled {
			return nil
		}
		return status.Error(codes.FailedPrecondition, "the system proxy is not available")
	}
	ownerUserID, err := loadOwner()
	if err != nil {
		return err
	}
	options, err := loadStartOptions(ownerUserID)
	if err != nil {
		return err
	}
	previousEnabled := options.systemProxyEnabled()
	err = h.daemon.platform.SetSystemProxyEnabled(enabled)
	if err != nil {
		return err
	}
	options.SystemProxyEnabled = &enabled
	err = saveStartOptions(ownerUserID, options)
	if err != nil {
		rollbackError := h.daemon.platform.SetSystemProxyEnabled(previousEnabled)
		return E.Errors(err, rollbackError)
	}
	return nil
}

func (h *managedHandler) TriggerNativeCrash() error {
	return E.New("native crash is not supported")
}
