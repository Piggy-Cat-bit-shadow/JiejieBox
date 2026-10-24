//go:build linux

package libbox

import (
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/logger"

	"github.com/stretchr/testify/require"
)

// Every JudgeFlow is a synchronous binder transaction into the app process and occupies one of its
// ~15 binder threads. The service handler bounds that at autoRedirectJudgeConcurrency so a burst of
// new auto-redirect flows cannot exhaust the pool that also serves the system's calls into the app.

type blockingAutoRedirectHandler struct {
	entered    chan string
	release    chan struct{}
	concurrent atomic.Int32
	peak       atomic.Int32
	fail       bool
}

func (h *blockingAutoRedirectHandler) JudgeFlow(ipProtocol int32, sourceAddress string, sourcePort int32, destinationAddress string, destinationPort int32, firstPacket []byte) (int32, error) {
	current := h.concurrent.Add(1)
	for {
		peak := h.peak.Load()
		if current <= peak || h.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	h.entered <- sourceAddress
	<-h.release
	h.concurrent.Add(-1)
	if h.fail {
		return 0, os.ErrInvalid
	}
	return int32(tun.ActionAccept), nil
}

func (h *blockingAutoRedirectHandler) RedirectListenerFileDescriptor() (int32, error) {
	return -1, nil
}

func (h *blockingAutoRedirectHandler) RouteAddressSetFileDescriptor() (int32, error) {
	return -1, nil
}

func (h *blockingAutoRedirectHandler) WriteLog(level int32, message string) {}

func testJudgeFlow(handler *autoRedirectServiceHandler, sourcePort uint16) tun.FlowVerdict {
	return handler.JudgeFlow(
		uint8(17),
		netip.AddrPortFrom(netip.MustParseAddr("10.0.0.2"), sourcePort),
		netip.MustParseAddrPort("93.184.216.34:443"),
		nil,
	)
}

func TestAutoRedirectJudgeFlowIsBounded(t *testing.T) {
	binderHandler := &blockingAutoRedirectHandler{
		entered: make(chan string, autoRedirectJudgeConcurrency+4),
		release: make(chan struct{}, autoRedirectJudgeConcurrency+4),
	}
	service := &autoRedirectServiceHandler{
		handler:    binderHandler,
		logger:     logger.NOP(),
		judgeSlots: make(chan struct{}, autoRedirectJudgeConcurrency),
	}

	var calls sync.WaitGroup
	start := func(port uint16) {
		calls.Add(1)
		go func() {
			defer calls.Done()
			testJudgeFlow(service, port)
		}()
	}
	for index := range autoRedirectJudgeConcurrency {
		start(uint16(40000 + index))
	}
	deadline := time.Now().Add(5 * time.Second)
	for binderHandler.concurrent.Load() != autoRedirectJudgeConcurrency {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d JudgeFlow calls reached the binder", binderHandler.concurrent.Load(), autoRedirectJudgeConcurrency)
		}
		time.Sleep(time.Millisecond)
	}

	// One more call must wait outside the binder instead of taking a ninth thread.
	start(50000)
	time.Sleep(150 * time.Millisecond)
	require.Equal(t, int32(autoRedirectJudgeConcurrency), binderHandler.concurrent.Load(),
		"a ninth call reached the binder while every slot was taken")
	require.Len(t, service.judgeSlots, autoRedirectJudgeConcurrency)

	// Releasing the binder calls lets all waiters through, one slot at a time.
	for range autoRedirectJudgeConcurrency + 1 {
		binderHandler.release <- struct{}{}
	}
	calls.Wait()
	require.Zero(t, len(service.judgeSlots), "every slot must be released")
	require.Equal(t, int32(autoRedirectJudgeConcurrency), binderHandler.peak.Load(),
		"concurrent binder calls exceeded the bound")
}

func TestAutoRedirectJudgeFlowReleasesSlotOnError(t *testing.T) {
	binderHandler := &blockingAutoRedirectHandler{
		entered: make(chan string, 4),
		release: make(chan struct{}, 4),
		fail:    true,
	}
	service := &autoRedirectServiceHandler{
		handler:    binderHandler,
		logger:     logger.NOP(),
		judgeSlots: make(chan struct{}, autoRedirectJudgeConcurrency),
	}
	done := make(chan tun.FlowVerdict, 1)
	go func() { done <- testJudgeFlow(service, 40001) }()
	<-binderHandler.entered
	binderHandler.release <- struct{}{}
	verdict := <-done
	require.Equal(t, tun.ActionAccept, verdict.Action)
	require.Zero(t, len(service.judgeSlots), "an error must still release the slot")
	require.Equal(t, autoRedirectJudgeConcurrency, cap(service.judgeSlots))
}
