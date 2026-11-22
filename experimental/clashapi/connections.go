package clashapi

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing/common"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/ws"
	"github.com/sagernet/ws/wsutil"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"
	"github.com/gofrs/uuid/v5"
)

func connectionRouter(ctx context.Context, network adapter.NetworkManager, trafficManager *trafficcontrol.Manager) http.Handler {
	r := chi.NewRouter()
	r.Get("/", getConnections(ctx, trafficManager))
	r.Delete("/", closeAllConnections(ctx, network, trafficManager))
	r.Delete("/{id}", closeConnection(trafficManager))
	return r
}

// WebSocket snapshot frame buffer retention policy.
//
// A connections stream encodes one JSON document per tick into a bytes.Buffer. bytes.Buffer.Reset
// only sets the length to zero: the backing array keeps the capacity of the largest frame the
// stream has ever produced. A dashboard opened while ten thousand connections were alive therefore
// keeps a multi-megabyte array resident for as long as the tab is open, even after the list shrinks
// to a handful of entries.
//
// The policy releases the array only when releasing it is both worthwhile and stable:
//
//   - wsFrameBufferFloor: buffers at or below this size are never released. Below it the array is
//     small enough that reallocating it is not worth the churn, and the common small-panel stream
//     would otherwise allocate a fresh buffer on every tick after a transient spike.
//   - wsFrameBufferShrinkFactor: the current frame must be less than a quarter of the retained
//     capacity. A stream that keeps producing large frames keeps its buffer; only a stream whose
//     frames have shrunk relative to the high-water mark releases it.
//   - wsFrameBufferShrinkTicks: the small-frame condition must hold for two consecutive ticks. A
//     single dip (one momentarily quiet second on a busy node) does not throw the buffer away and
//     force a full reallocation on the next tick.
//
// A released buffer is replaced with a fresh zero-capacity bytes.Buffer, so the next frame grows to
// exactly what that frame needs. The replacement happens after WriteServerText has returned, in the
// goroutine that owns the stream and is the only writer of the buffer, so it can never be swapped
// while a concurrent write is using it.
const (
	wsFrameBufferFloor        = 64 << 10
	wsFrameBufferShrinkFactor = 4
	wsFrameBufferShrinkTicks  = 2
)

// wsFrameBuffer owns the reusable encoding buffer of ONE connections WebSocket stream.
type wsFrameBuffer struct {
	buffer      bytes.Buffer
	smallFrames int
	releases    int
}

func (b *wsFrameBuffer) encode(value any) error {
	b.buffer.Reset()
	return json.NewEncoder(&b.buffer).Encode(value)
}

func (b *wsFrameBuffer) bytes() []byte {
	return b.buffer.Bytes()
}

func (b *wsFrameBuffer) capacity() int {
	return b.buffer.Cap()
}

// releaseHighWater drops the backing array when this frame shows that the stream no longer needs the
// capacity it once did. frameSize is the size of the frame that was just sent.
func (b *wsFrameBuffer) releaseHighWater(frameSize int) bool {
	if b.buffer.Cap() <= wsFrameBufferFloor || frameSize*wsFrameBufferShrinkFactor >= b.buffer.Cap() {
		b.smallFrames = 0
		return false
	}
	b.smallFrames++
	if b.smallFrames < wsFrameBufferShrinkTicks {
		return false
	}
	b.buffer = bytes.Buffer{}
	b.smallFrames = 0
	b.releases++
	return true
}

func connectionsSnapshot(trafficManager *trafficcontrol.Manager) render.M {
	uplinkTotal, downlinkTotal := trafficManager.Total()
	connections := common.Filter(trafficManager.Connections(), func(metadata *trafficcontrol.TrackerMetadata) bool {
		return metadata.OutboundType != C.TypeDNS
	})
	return render.M{
		"downloadTotal": downlinkTotal,
		"uploadTotal":   uplinkTotal,
		"connections": common.Map(connections, func(metadata *trafficcontrol.TrackerMetadata) connectionObject {
			return connectionObject(*metadata)
		}),
		"memory": inuseMemory(),
	}
}

type connectionObject trafficcontrol.TrackerMetadata

func (c connectionObject) MarshalJSON() ([]byte, error) {
	var inbound string
	if c.Metadata.Inbound != "" {
		inbound = c.Metadata.InboundType + "/" + c.Metadata.Inbound
	} else {
		inbound = c.Metadata.InboundType
	}
	var domain string
	if c.Metadata.Domain != "" {
		domain = c.Metadata.Domain
	} else {
		domain = c.Metadata.Destination.Fqdn
	}
	var processPath string
	if c.Metadata.ProcessInfo != nil {
		if len(c.Metadata.ProcessInfo.ProcessPaths) > 0 {
			processPath = c.Metadata.ProcessInfo.ProcessPaths[0]
		} else if len(c.Metadata.ProcessInfo.PackageNames) > 0 {
			processPath = c.Metadata.ProcessInfo.PackageNames[0]
		}
		if processPath == "" {
			if c.Metadata.ProcessInfo.UserId != -1 {
				processPath = F.ToString(c.Metadata.ProcessInfo.UserId)
			}
		} else if c.Metadata.ProcessInfo.UserName != "" {
			processPath = F.ToString(processPath, " (", c.Metadata.ProcessInfo.UserName, ")")
		} else if c.Metadata.ProcessInfo.UserId != -1 {
			processPath = F.ToString(processPath, " (", c.Metadata.ProcessInfo.UserId, ")")
		}
	}
	var rule string
	if c.Rule != nil {
		rule = F.ToString(c.Rule, " => ", c.Rule.Action())
	} else {
		rule = "final"
	}
	return json.Marshal(map[string]any{
		"id": c.ID,
		"metadata": map[string]any{
			"network":         c.Metadata.Network,
			"type":            inbound,
			"sourceIP":        c.Metadata.Source.Addr,
			"destinationIP":   c.Metadata.Destination.Addr,
			"sourcePort":      F.ToString(c.Metadata.Source.Port),
			"destinationPort": F.ToString(c.Metadata.Destination.Port),
			"host":            domain,
			"dnsMode":         "normal",
			"processPath":     processPath,
		},
		"upload":      c.Upload.Load(),
		"download":    c.Download.Load(),
		"start":       c.CreatedAt,
		"chains":      c.Chain,
		"rule":        rule,
		"rulePayload": "",
	})
}

func getConnections(ctx context.Context, trafficManager *trafficcontrol.Manager) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			render.JSON(w, r, connectionsSnapshot(trafficManager))
			return
		}

		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		defer conn.Close()

		intervalStr := r.URL.Query().Get("interval")
		interval := 1000
		if intervalStr != "" {
			t, err := strconv.Atoi(intervalStr)
			if err != nil {
				render.Status(r, http.StatusBadRequest)
				render.JSON(w, r, ErrBadRequest)
				return
			}

			interval = t
		}

		frameBuffer := &wsFrameBuffer{}
		sendSnapshot := func() error {
			encodeErr := frameBuffer.encode(connectionsSnapshot(trafficManager))
			if encodeErr != nil {
				return encodeErr
			}
			frame := frameBuffer.bytes()
			frameSize := len(frame)
			writeErr := wsutil.WriteServerText(conn, frame)
			if writeErr != nil {
				return writeErr
			}
			frameBuffer.releaseHighWater(frameSize)
			return nil
		}

		if err = sendSnapshot(); err != nil {
			return
		}

		tick := time.NewTicker(time.Millisecond * time.Duration(interval))
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			if err = sendSnapshot(); err != nil {
				break
			}
		}
	}
}

func closeConnection(trafficManager *trafficcontrol.Manager) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		id := uuid.FromStringOrNil(chi.URLParam(r, "id"))
		targetConnection := trafficManager.Connection(id)
		if targetConnection != nil {
			targetConnection.Close()
		}
		render.NoContent(w, r)
	}
}

func closeAllConnections(ctx context.Context, network adapter.NetworkManager, trafficManager *trafficcontrol.Manager) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		trafficManager.CloseAllConnections()
		network.ResetNetwork(ctx)
		render.NoContent(w, r)
	}
}
