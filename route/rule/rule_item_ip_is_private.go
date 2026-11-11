package rule

import (
	"github.com/sagernet/sing-box/adapter"
	N "github.com/sagernet/sing/common/network"
)

var _ RuleItem = (*IPIsPrivateItem)(nil)

type IPIsPrivateItem struct {
	isSource bool
}

func NewIPIsPrivateItem(isSource bool) *IPIsPrivateItem {
	return &IPIsPrivateItem{isSource}
}

func (r *IPIsPrivateItem) Match(metadata *adapter.InboundContext) bool {
	if r.isSource {
		return !N.IsPublicAddr(metadata.Source.Addr)
	}
	if metadata.DestinationAddressMatchFromResponse {
		for _, destinationAddress := range metadata.DNSResponseAddressesForMatch() {
			if !N.IsPublicAddr(destinationAddress) {
				return true
			}
		}
		return false
	}
	exact, candidates := destinationMatchSource(metadata)
	if exact.IsValid() {
		return !N.IsPublicAddr(exact)
	}
	for _, destinationAddress := range candidates {
		if !N.IsPublicAddr(destinationAddress) {
			return true
		}
	}
	return false
}

func (r *IPIsPrivateItem) String() string {
	if r.isSource {
		return "source_ip_is_private=true"
	} else {
		return "ip_is_private=true"
	}
}
