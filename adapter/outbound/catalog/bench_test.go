//go:build !race

package catalog_test

import (
	"context"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/protocol/socks"
	"github.com/sagernet/sing-box/protocol/trojan"
)

// BenchmarkCatalogBuild measures the first-use record decode plus the protocol constructor; the
// eager benchmark is the constructor alone. The difference is the record's decode cost, which is the
// first-connection overhead the catalog adds and must be reported.
func BenchmarkCatalogBuildSOCKS(b *testing.B) {
	protocol := catalogProtocol(C.TypeSOCKS)
	payload, _, ok := protocol.Encode(context.Background(), socksOptionsAt(0))
	if !ok {
		b.Fatal("shape not admitted")
	}
	logger := log.NewNOPFactory().Logger()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		object, err := protocol.Build(context.Background(), nil, logger, "bench", payload)
		if err != nil {
			b.Fatal(err)
		}
		_ = object
	}
}

func BenchmarkEagerSOCKS(b *testing.B) {
	logger := log.NewNOPFactory().Logger()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		object, err := socks.NewOutbound(context.Background(), nil, logger, "bench", *socksOptionsAt(0))
		if err != nil {
			b.Fatal(err)
		}
		_ = object
	}
}

func BenchmarkCatalogBuildHTTP(b *testing.B) {
	protocol := catalogProtocol(C.TypeHTTP)
	payload, _, ok := protocol.Encode(context.Background(), httpOptionsAt(0))
	if !ok {
		b.Fatal("shape not admitted")
	}
	logger := log.NewNOPFactory().Logger()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		object, err := protocol.Build(context.Background(), nil, logger, "bench", payload)
		if err != nil {
			b.Fatal(err)
		}
		_ = object
	}
}

func BenchmarkEagerHTTP(b *testing.B) {
	logger := log.NewNOPFactory().Logger()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		object, err := http.NewOutbound(context.Background(), nil, logger, "bench", *httpOptionsAt(0))
		if err != nil {
			b.Fatal(err)
		}
		_ = object
	}
}

func BenchmarkCatalogBuildTrojan(b *testing.B) {
	protocol := catalogProtocol(C.TypeTrojan)
	payload, _, ok := protocol.Encode(context.Background(), trojanOptionsAt(0))
	if !ok {
		b.Fatal("shape not admitted")
	}
	logger := log.NewNOPFactory().Logger()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		object, err := protocol.Build(context.Background(), nil, logger, "bench", payload)
		if err != nil {
			b.Fatal(err)
		}
		_ = object
	}
}

func BenchmarkEagerTrojan(b *testing.B) {
	logger := log.NewNOPFactory().Logger()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		object, err := trojan.NewOutbound(context.Background(), nil, logger, "bench", *trojanOptionsAt(0))
		if err != nil {
			b.Fatal(err)
		}
		_ = object
	}
}
