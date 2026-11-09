package box_test

import (
	"context"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
)

// The Box-level wiring of the XHTTP upload budget, assembled by the real Box.New:
//
//   - two vless-XHTTP outbounds produce ONE shared budget with two clients bound to it;
//   - one XHTTP outbound still gets the budget (one aggregate, not zero);
//   - a Box with no XHTTP outbound constructs none and pays nothing.
//
// Nothing dials (no server exists): the budget is created when each client's transport is
// constructed, which is exactly the timing the wiring relies on - a client can only be
// bound before its first dial.
//
// The accounting itself (cross-client charging, isolation, late returns) is covered by
// the transport suite and the in-package Box tests; this file proves the production
// assembly path installs the provider and that every configured XHTTP outbound reaches
// it.

const xhttpOutboundTemplate = `{
  "log": {"disabled": true},
  "outbounds": [
    %s
  ]
}`

func newXHTTPOutbound(tag, path string) string {
	return `{
      "type": "vless",
      "tag": "` + tag + `",
      "server": "127.0.0.1",
      "server_port": 443,
      "uuid": "b831381d-6324-4d53-ad4f-8cda48b30811",
      "transport": {"type": "xhttp", "path": "` + path + `"}
    }`
}

func decodeBoxOptions(t *testing.T, config string) (context.Context, option.Options) {
	t.Helper()
	ctx := include.Context(context.Background())
	options, decodeErr := json.UnmarshalExtendedContext[option.Options](ctx, []byte(config))
	if decodeErr != nil {
		t.Fatalf("decode config: %v", decodeErr)
	}
	return ctx, options
}

func newBoxWithOutbounds(t *testing.T, outbounds ...string) *box.Box {
	t.Helper()
	joined := ""
	for index, outbound := range outbounds {
		if index > 0 {
			joined += ","
		}
		joined += outbound
	}
	config := sprintfConfig(joined)
	ctx, options := decodeBoxOptions(t, config)
	instance, newErr := box.New(box.Options{Context: ctx, Options: options})
	if newErr != nil {
		t.Fatalf("New: %v", newErr)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return instance
}

func sprintfConfig(outbounds string) string {
	return `{
  "log": {"disabled": true},
  "outbounds": [` + outbounds + `]
}`
}

func TestBoxNewSharesOneUploadBudgetAcrossXHTTPOutbounds(t *testing.T) {
	instance := newBoxWithOutbounds(t,
		newXHTTPOutbound("xhttp-one", "/one"),
		newXHTTPOutbound("xhttp-two", "/two"),
	)
	status := instance.XHTTPUploadBudgetStatus()
	if status.Clients != 2 {
		t.Fatalf("the shared budget was requested by %d clients, want one per XHTTP outbound", status.Clients)
	}
	if status.Snapshot.Capacity <= 0 {
		t.Fatalf("the shared budget has no pools: %+v", status.Snapshot)
	}
	if status.Snapshot.Control <= 0 || status.Snapshot.AllocCapacity <= 0 {
		t.Fatalf("the shared budget lost its control/allocation pools: %+v", status.Snapshot)
	}
	if status.Snapshot.Streams != 0 || status.Snapshot.Total() != 0 {
		t.Fatalf("a freshly assembled Box already holds traffic: %+v", status.Snapshot)
	}
}

func TestBoxNewKeepsALoneXHTTPOutboundOnTheSharedBudget(t *testing.T) {
	instance := newBoxWithOutbounds(t, newXHTTPOutbound("xhttp-only", "/only"))
	status := instance.XHTTPUploadBudgetStatus()
	if status.Clients != 1 {
		t.Fatalf("the shared budget was requested by %d clients, want 1", status.Clients)
	}
	if status.Snapshot.Capacity <= 0 {
		t.Fatalf("the single XHTTP client did not size the budget: %+v", status.Snapshot)
	}
}

func TestBoxWithoutXHTTPOutboundsHasNoUploadBudget(t *testing.T) {
	instance := newBoxWithOutbounds(t, `{"type": "direct", "tag": "direct"}`)
	status := instance.XHTTPUploadBudgetStatus()
	if status.Clients != 0 {
		t.Fatalf("a non-XHTTP Box requested an upload budget %d times", status.Clients)
	}
	if status.Snapshot.Capacity != 0 || status.Snapshot.Total() != 0 {
		t.Fatalf("a non-XHTTP Box created a budget: %+v", status.Snapshot)
	}
}
