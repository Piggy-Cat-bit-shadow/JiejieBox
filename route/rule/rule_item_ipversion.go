package rule

import (
	"github.com/sagernet/sing-box/adapter"
)

var _ RuleItem = (*IPVersionItem)(nil)

type IPVersionItem struct {
	isIPv6 bool
}

func NewIPVersionItem(isIPv6 bool) *IPVersionItem {
	return &IPVersionItem{isIPv6}
}

func (r *IPVersionItem) Match(metadata *adapter.InboundContext) bool {
	destinationIsIPv6 := metadata.Destination.Addr.Is6() && !metadata.Destination.Addr.Is4In6()
	return metadata.IPVersion != 0 && metadata.IPVersion == 6 == r.isIPv6 ||
		metadata.Destination.IsIP() && destinationIsIPv6 == r.isIPv6
}

func (r *IPVersionItem) String() string {
	var versionStr string
	if r.isIPv6 {
		versionStr = "6"
	} else {
		versionStr = "4"
	}
	return "ip_version=" + versionStr
}
