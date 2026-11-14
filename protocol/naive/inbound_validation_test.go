package naive

import (
	"context"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
)

// TestNewInboundRejectsUnknownCongestionControl is the check-vs-run agreement:
// `sing-box check` constructs inbounds and never starts them, so the rejection
// must happen here, not in the HTTP/3 initialiser.
func TestNewInboundRejectsUnknownCongestionControl(t *testing.T) {
	_, err := NewInbound(context.Background(), nil, log.NewNOPFactory().Logger(), "naive-in",
		option.NaiveInboundOptions{Network: "tcp", QUICCongestionControl: "bogus"})
	if err == nil {
		t.Fatal("an unknown quic_congestion_control must be refused at construction")
	}
	if !strings.Contains(err.Error(), "unknown quic congestion control: bogus") {
		t.Fatalf("the error must name the value so it is actionable, got: %v", err)
	}
}

// TestNewInboundAcceptsValidCongestionControl keeps the early validation from
// becoming stricter than the listener: every value the resolver accepts must get
// past construction. The "missing users" error is the proof that construction
// proceeded past the congestion-control check.
func TestNewInboundAcceptsValidCongestionControl(t *testing.T) {
	for _, name := range []string{"", "default", "bbr", "cubic", "reno"} {
		t.Run("value="+name, func(t *testing.T) {
			_, err := NewInbound(context.Background(), nil, log.NewNOPFactory().Logger(), "naive-in",
				option.NaiveInboundOptions{Network: "tcp", QUICCongestionControl: name})
			if err == nil {
				t.Fatal("the fixture is expected to fail later on missing users; nil error means the check was skipped")
			}
			if strings.Contains(err.Error(), "unknown quic congestion control") {
				t.Fatalf("%q was refused by construction-time validation: %v", name, err)
			}
			if !strings.Contains(err.Error(), "missing users") {
				t.Fatalf("%q did not reach the next validation step: %v", name, err)
			}
		})
	}
}
