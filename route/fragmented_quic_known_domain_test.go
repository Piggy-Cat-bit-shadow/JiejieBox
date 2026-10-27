package route

import (
	"errors"
	"io"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing-box/route/rule"
)

// TestPacketSniffKnownFakeIPDomainDecision pins the branch that stops a fragmented-QUIC sniff from
// waiting on a flow whose FakeIP reverse mapping already named it. The production path can only
// reach the branch on sniff.ErrNeedMoreData; this test pins the three extra preconditions so a
// future edit cannot widen it to an explicit QUIC sniffer or to a flow with no domain.
func TestPacketSniffKnownFakeIPDomainDecision(t *testing.T) {
	domainMetadata := &adapter.InboundContext{Domain: "rr1---sn-a5meknsy.googlevideo.com"}
	emptyAction := &rule.RuleActionSniff{}
	explicitAction := &rule.RuleActionSniff{PacketSniffers: []sniff.PacketSniffer{sniff.QUICClientHello}}

	testCases := []struct {
		name     string
		metadata *adapter.InboundContext
		action   *rule.RuleActionSniff
		err      error
		expected bool
	}{
		{"known domain, default sniffers, fragmented", domainMetadata, emptyAction, sniff.ErrNeedMoreData, true},
		{"known domain, explicit sniffer keeps its contract", domainMetadata, explicitAction, sniff.ErrNeedMoreData, false},
		{"no domain, default sniffers", &adapter.InboundContext{}, emptyAction, sniff.ErrNeedMoreData, false},
		{"known domain, non-fragmented error", domainMetadata, emptyAction, io.EOF, false},
		{"known domain, nil error", domainMetadata, emptyAction, nil, false},
		{"wrapped need-more-data still counts", domainMetadata, emptyAction, errors.Join(errors.New("quic"), sniff.ErrNeedMoreData), true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := packetSniffUsesKnownFakeIPDomain(testCase.metadata, testCase.action, testCase.err); got != testCase.expected {
				t.Fatalf("got %v, want %v", got, testCase.expected)
			}
		})
	}
}
