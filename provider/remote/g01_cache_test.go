package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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

func BenchmarkCacheDocumentStream(b *testing.B) {
	for _, nodes := range []int{1000, 10000} {
		b.Run(strconv.Itoa(nodes), func(b *testing.B) {
			provider := &ProviderRemote{lastOutOpts: benchmarkOutbounds(nodes)}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := provider.writeCacheDocument(io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCacheDocumentMarshal(b *testing.B) {
	for _, nodes := range []int{1000, 10000} {
		b.Run(strconv.Itoa(nodes), func(b *testing.B) {
			outbounds := benchmarkOutbounds(nodes)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := json.Marshal(option.Options{Outbounds: outbounds}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestSaveCacheFileStreamsWithInfoAndCleansUpOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "provider.json")
	provider := &ProviderRemote{
		ctx:         context.Background(),
		path:        path,
		lastOutOpts: benchmarkOutbounds(2),
	}
	info := adapter.SubscriptionInfo{Upload: 1, Download: 2, Total: 3, Expire: 4}
	require.NoError(t, provider.saveCacheFile(true, info, provider.writeCacheDocument))
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), "# upload=1; download=2; total=3; expire=4;\n")
	firstNewline := bytes.IndexByte(content, '\n')
	require.Greater(t, firstNewline, 0)
	require.True(t, json.Valid(content[firstNewline+1:]), "the document after the info line must be valid JSON")

	original := content
	encodeErr := errors.New("injected encode failure")
	require.ErrorIs(t, provider.saveCacheFile(false, info, func(io.Writer) error { return encodeErr }), encodeErr)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, after, "a failed stream must keep the previous cache file")
	require.NoFileExists(t, path+".tmp", "a failed stream must not leave the temp file behind")
}
