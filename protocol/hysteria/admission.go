package hysteria

import (
	"context"

	"github.com/sagernet/sing-box/common/resourceguard"
	qtls "github.com/sagernet/sing-quic"
)

// admitCreation bounds one real Hysteria connection creation with the process admission policy.
//
// The hook is called by the sing-quic client once per shared creation - never per waiting flow and
// never for a new stream on an established connection - so this is where the QUIC handshake is
// charged. A nil guard keeps the SDK on its no-hook path.
func admitCreation(guard *resourceguard.Guard, key string) qtls.CreationAdmission {
	if guard == nil {
		return nil
	}
	return func(ctx context.Context, create func(context.Context) error) error {
		return guard.AdmitCreation(ctx, key, create)
	}
}
