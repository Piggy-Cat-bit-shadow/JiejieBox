package route

// Repository test for G03's reference-walk coverage. The independent reviewer's probe is checked
// in so the walk contract has a normal CI home: a real protocol/http.Outbound is wrapped only to
// count calls, registered in the manager the reference walk reads, and Start + TrimIdleResources is
// driven exactly as the product does.

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/power"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	phttp "github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing/service"
)

type catalogHTTPKeeper struct {
	*phttp.Outbound
	setCalls   atomic.Int32
	closeCalls atomic.Int32
}

func (k *catalogHTTPKeeper) SetKeepIdleConnections(keep bool) {
	k.setCalls.Add(1)
	k.Outbound.SetKeepIdleConnections(keep)
}

func (k *catalogHTTPKeeper) CloseIdleConnections() {
	k.closeCalls.Add(1)
	k.Outbound.CloseIdleConnections()
}

func TestReferenceWalkSeesHTTPKeeper(t *testing.T) {
	httpOutbound, err := phttp.NewOutbound(context.Background(), nil, log.NewNOPFactory().Logger(), "http-out",
		option.HTTPOutboundOptions{
			ServerOptions: option.ServerOptions{Server: "192.0.2.1", ServerPort: 8080},
			Version:       1,
		})
	if err != nil {
		t.Fatal(err)
	}
	keeper := &catalogHTTPKeeper{Outbound: httpOutbound.(*phttp.Outbound)}

	governor := power.NewGovernor(power.DefaultPolicy())
	ctx := service.ContextWith[*power.Governor](context.Background(), governor)
	outboundManager := &reuseTestOutboundManager{outbounds: []adapter.Outbound{keeper}}
	ctx = service.ContextWith[adapter.OutboundManager](ctx, outboundManager)
	ctx = service.ContextWith[adapter.EndpointManager](ctx, &reuseTestEndpointManager{})
	ctx = service.ContextWith[adapter.InboundManager](ctx, &reuseTestInboundManager{})
	ctx = service.ContextWith[adapter.ServiceManager](ctx, &reuseTestServiceManager{})
	ctx = service.ContextWith[adapter.DNSTransportManager](ctx, &reuseTestDNSTransportManager{})
	ctx = service.ContextWith[adapter.NetworkManager](ctx, &reuseTestNetworkManager{})
	manager := NewReferenceManager(ctx, log.NewNOPFactory().NewLogger("reference"), option.Options{})
	scope := adapter.NewScope(ctx, log.NewNOPFactory().Logger())
	if err = manager.Start(adapter.StartStateStarted, scope); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = scope.Close()
		governor.Close()
	}()
	manager.TrimIdleResources()
	fmt.Printf("HTTPWALK set_calls=%d close_calls=%d\n", keeper.setCalls.Load(), keeper.closeCalls.Load())
	if keeper.setCalls.Load() == 0 {
		t.Fatal("the eligibility pass never called SetKeepIdleConnections on the HTTP keeper")
	}
	if keeper.closeCalls.Load() == 0 {
		t.Fatal("the retire walk never called CloseIdleConnections on the HTTP keeper")
	}
}
