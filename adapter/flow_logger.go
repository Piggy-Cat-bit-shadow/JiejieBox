package adapter

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/byteformats"
	N "github.com/sagernet/sing/common/network"
)

var _ tun.FlowTracker = (*FlowLogger)(nil)

// FlowLogger records one dispatched flow: the five-tuple it was judged for, and the bytes it moved.
//
// It lives here rather than in route because two different judges return it: the route's pre-match
// path, which knows the outbound, and JudgeHostFlow, which knows only that the local stack serves
// the flow. A tracker in route could not be constructed by the second one.
type FlowLogger struct {
	ctx         context.Context
	logger      log.ContextLogger
	network     string
	source      string
	destination string
	outbound    Outbound
	createdAt   time.Time
	upload      atomic.Int64
	download    atomic.Int64
}

func NewFlowLogger(ctx context.Context, logger log.ContextLogger, metadata InboundContext, outbound Outbound) *FlowLogger {
	var source, destination string
	if metadata.Network == N.NetworkICMP {
		source = metadata.Source.AddrString()
		destination = metadata.Destination.AddrString()
	} else {
		source = metadata.Source.String()
		destination = metadata.Destination.String()
	}
	return &FlowLogger{
		ctx:         ctx,
		logger:      logger,
		network:     metadata.Network,
		source:      source,
		destination: destination,
		outbound:    outbound,
	}
}

func (l *FlowLogger) AttachFlow(handle tun.FlowHandle) {
	l.createdAt = time.Now()
}

func (l *FlowLogger) CountForward(n int) {
	l.upload.Add(int64(n))
}

func (l *FlowLogger) CountReverse(n int) {
	l.download.Add(int64(n))
}

func (l *FlowLogger) FlowEstablished() {
}

func (l *FlowLogger) CloseFlow(reason tun.FlowCloseReason) {
	l.logger.DebugContext(l.ctx, "flow closed: ", reason,
		", upload ", byteformats.FormatBytes(uint64(l.upload.Load())), ", download ", byteformats.FormatBytes(uint64(l.download.Load())))
}
