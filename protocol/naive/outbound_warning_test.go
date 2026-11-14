//go:build with_naive_outbound

package naive

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
)

// TestNewOutboundWarnsOnlyWhenQUICUsesUnsetCongestionControl pins both halves of the
// outbound warning: it must be emitted when the engine will carry HTTP/3, and it must
// stay quiet when the engine is HTTP/2-only and quic_congestion_control is never read.
//
// Construction may stop later because the native Cronet library is not present in the
// test environment. The warning is emitted before the native client is created, so the
// assertion does not depend on that library: a test that required a successful native
// construction would be `BLOCKED_BY_ENVIRONMENT` on most machines.
func TestNewOutboundWarnsOnlyWhenQUICUsesUnsetCongestionControl(t *testing.T) {
	for _, testCase := range []struct {
		name string
		quic bool
		want bool
	}{
		{name: "quic warns", quic: true, want: true},
		{name: "h2 is quiet", quic: false, want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var logs bytes.Buffer
			logFactory, err := log.New(log.Options{
				Context:       context.Background(),
				Options:       option.LogOptions{Level: "warn", Timestamp: false, DisableColor: true},
				DefaultWriter: &logs,
			})
			if err != nil {
				t.Fatalf("create log factory: %v", err)
			}
			if err := logFactory.Start(); err != nil {
				t.Fatalf("start log factory: %v", err)
			}
			t.Cleanup(func() { _ = logFactory.Close() })

			_, err = NewOutbound(context.Background(), nil, logFactory.Logger(), "naive-out",
				option.NaiveOutboundOptions{
					ServerOptions: option.ServerOptions{Server: "127.0.0.1", ServerPort: 443},
					Username:      "user",
					Password:      "password",
					QUIC:          testCase.quic,
					OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{
						TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "example.com"},
					},
				})
			if err != nil {
				t.Logf("construction stopped after the warning point: %v", err)
			}

			got := strings.Contains(logs.String(), "quic_congestion_control is unset")
			if got != testCase.want {
				t.Fatalf("warning present = %v, want %v; logs:\n%s", got, testCase.want, logs.String())
			}
			if testCase.want && !strings.Contains(logs.String(), "WARN") {
				t.Fatalf("the warning must be emitted at WARN; logs:\n%s", logs.String())
			}
		})
	}
}
