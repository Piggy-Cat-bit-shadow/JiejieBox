package remote

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	singjson "github.com/sagernet/sing/common/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/option"

	"github.com/stretchr/testify/require"
)

func benchmarkOutbounds(nodes int) []option.Outbound {
	outbounds := make([]option.Outbound, 0, nodes)
	for index := 0; index < nodes; index++ {
		outbounds = append(outbounds, option.Outbound{
			Type: "trojan",
			Tag:  "node-" + strconv.Itoa(index),
			Options: &option.TrojanOutboundOptions{
				ServerOptions: option.ServerOptions{Server: "198.51.100.1", ServerPort: 443},
				Password:      "secret",
			},
		})
	}
	return outbounds
}

func BenchmarkCacheDocumentBytes(b *testing.B) {
	for _, nodes := range []int{1000, 10000} {
		b.Run(strconv.Itoa(nodes), func(b *testing.B) {
			provider := &ProviderRemote{lastOutOpts: benchmarkOutbounds(nodes)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := provider.cacheDocumentBytes(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCacheDocumentDoubleMarshal(b *testing.B) {
	for _, nodes := range []int{1000, 10000} {
		b.Run(strconv.Itoa(nodes), func(b *testing.B) {
			outbounds := benchmarkOutbounds(nodes)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				first, err := singjson.Marshal(option.Options{Outbounds: outbounds})
				if err != nil {
					b.Fatal(err)
				}
				second, err := singjson.Marshal(option.Options{Outbounds: outbounds})
				if err != nil {
					b.Fatal(err)
				}
				runtime.KeepAlive(first)
				runtime.KeepAlive(second)
			}
		})
	}
}

func TestSaveCacheFileWritesAtomicDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider.json")
	provider := &ProviderRemote{ctx: context.Background(), path: path, lastOutOpts: benchmarkOutbounds(2)}
	content, err := provider.cacheDocumentBytes()
	require.NoError(t, err)
	info := adapter.SubscriptionInfo{Upload: 1, Download: 2, Total: 3, Expire: 4}
	require.NoError(t, provider.saveCacheFile(true, info, content))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(got), "# upload=1; download=2; total=3; expire=4;\n")
	firstNewline := bytes.IndexByte(got, '\n')
	require.Greater(t, firstNewline, 0)
	require.True(t, stdjson.Valid(got[firstNewline+1:]))
	require.NoFileExists(t, path+".tmp", "the successful write must rename the temp file away")
}
