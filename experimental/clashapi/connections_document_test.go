package clashapi

import (
	"encoding/json"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/gofrs/uuid/v5"
	"github.com/stretchr/testify/require"
)

// The connections document used to be two map[string]any graphs. encoding/json writes a
// map with sorted keys and a struct in declaration order, so replacing the maps with
// structs must not change a single byte: panels diff these documents and a key-order
// change would show up as a changed connection. This pins the wire shape for one fully
// populated item, including a rule payload and a process path.
func TestConnectionsDocumentKeepsTheMapByteOrder(t *testing.T) {
	id := uuid.Must(uuid.FromString("00112233-4455-6677-8899-aabbccddeeff"))
	createdAt := time.Date(2026, 11, 28, 3, 0, 0, 0, time.UTC)
	upload, download := new(atomic.Int64), new(atomic.Int64)
	upload.Store(11)
	download.Store(22)

	var item connectionObject
	item.ID = id
	item.CreatedAt = createdAt
	item.Upload, item.Download = upload, download
	item.Chain = []string{"proxy", "direct"}
	item.Metadata = adapter.InboundContext{
		InboundType: "tun",
		Inbound:     "tun-in",
		Network:     "tcp",
		Source:      M.SocksaddrFrom(netip.MustParseAddr("10.0.0.2"), 50123),
		Destination: M.SocksaddrFrom(netip.MustParseAddr("93.184.216.34"), 443),
		Domain:      "example.com",
	}

	encoded, err := json.Marshal(item)
	require.NoError(t, err)

	// Byte-for-byte what the map-shaped encoder produced: outer keys alphabetically
	// (chains, download, id, metadata, rule, rulePayload, start, upload) and metadata
	// keys alphabetically (destinationIP, destinationPort, dnsMode, host, network,
	// processPath, sourceIP, sourcePort, type).
	require.Equal(t, `{"chains":["proxy","direct"],"download":22,"id":"00112233-4455-6677-8899-aabbccddeeff","metadata":{"destinationIP":"93.184.216.34","destinationPort":"443","dnsMode":"normal","host":"example.com","network":"tcp","processPath":"","sourceIP":"10.0.0.2","sourcePort":"50123","type":"tun/tun-in"},"rule":"final","rulePayload":"","start":"2026-11-28T03:00:00Z","upload":11}`, string(encoded))

	_ = trafficcontrol.TrackerMetadata{}
}
