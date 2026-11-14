package naive

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/common/listener"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/logger"
)

// TestNewInboundWarnsWhenQUICCongestionControlIsUnset pins the construction-time
// warning for the one configuration shape that needs it: UDP served, QUIC support
// present, and the field unset.
//
// `sing-box check` constructs inbounds and never starts them, so a warning emitted
// only from the HTTP/3 listener initialiser is invisible to it. Info is additionally
// filtered by the production log level of warn. Reporting the finding here, at Warn,
// is what makes it visible to both audiences.
func TestNewInboundWarnsWhenQUICCongestionControlIsUnset(t *testing.T) {
	previousFunc := ConfigureHTTP3ListenerFunc
	previousAvailable := HTTP3ListenerAvailable
	ConfigureHTTP3ListenerFunc = func(context.Context, logger.Logger, *listener.Listener, http.Handler, tls.ServerConfig, option.NaiveInboundOptions) (io.Closer, error) {
		return nil, nil
	}
	HTTP3ListenerAvailable = true
	t.Cleanup(func() {
		ConfigureHTTP3ListenerFunc = previousFunc
		HTTP3ListenerAvailable = previousAvailable
	})

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

	for _, testCase := range []struct {
		name    string
		network string
		control string
		want    bool
	}{
		{name: "udp unset warns", network: "udp", control: "", want: true},
		{name: "udp explicit is quiet", network: "udp", control: "bbr", want: false},
		{name: "tcp never reads the field", network: "tcp", control: "", want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			logs.Reset()
			_, err := NewInbound(context.Background(), nil, logFactory.Logger(), "naive-in",
				option.NaiveInboundOptions{
					Network:               option.NetworkList(testCase.network),
					Users:                 []auth.User{{Username: "user", Password: "password"}},
					QUICCongestionControl: testCase.control,
					InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
						TLS: &option.InboundTLSOptions{Enabled: true, Insecure: true},
					},
				})
			if err != nil {
				t.Fatalf("construction failed: %v", err)
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

// TestNewInboundDoesNotWarnAboutCongestionControlWithoutQUIC keeps the warning
// precise: without a QUIC listener to tune, the field is unread and a CUBIC warning
// would describe a path that does not exist in this build.
func TestNewInboundDoesNotWarnAboutCongestionControlWithoutQUIC(t *testing.T) {
	previousFunc := ConfigureHTTP3ListenerFunc
	previousAvailable := HTTP3ListenerAvailable
	// The no-QUIC build is not "no constructor": it installs a stub, exactly as
	// include/quic_stub.go does, while leaving availability false. The warning must key
	// on availability, not on the function being non-nil.
	ConfigureHTTP3ListenerFunc = func(context.Context, logger.Logger, *listener.Listener, http.Handler, tls.ServerConfig, option.NaiveInboundOptions) (io.Closer, error) {
		return nil, nil
	}
	HTTP3ListenerAvailable = false
	t.Cleanup(func() {
		ConfigureHTTP3ListenerFunc = previousFunc
		HTTP3ListenerAvailable = previousAvailable
	})

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

	_, err = NewInbound(context.Background(), nil, logFactory.Logger(), "naive-in",
		option.NaiveInboundOptions{
			Network: "udp",
			Users:   []auth.User{{Username: "user", Password: "password"}},
			InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
				TLS: &option.InboundTLSOptions{Enabled: true, Insecure: true},
			},
		})
	if err != nil {
		t.Fatalf("construction failed: %v", err)
	}
	if strings.Contains(logs.String(), "quic_congestion_control is unset") {
		t.Fatalf("a build without QUIC must not warn about a QUIC-only field; logs:\n%s", logs.String())
	}
}
