package rule

import (
	"strings"

	"github.com/sagernet/sing-box/adapter"
)

var _ RuleItem = (*DomainKeywordItem)(nil)

type DomainKeywordItem struct {
	keywords []string
	// matcher is the compiled large-set form, or nil for the low-cost loop (small sets, or a set
	// past the compile bounds). Both paths preserve the same semantics; see domain_keyword_matcher.go.
	matcher *domainKeywordMatcher
}

func NewDomainKeywordItem(keywords []string) *DomainKeywordItem {
	return &DomainKeywordItem{keywords: keywords, matcher: newDomainKeywordMatcher(keywords)}
}

func (r *DomainKeywordItem) Match(metadata *adapter.InboundContext) bool {
	var domainHost string
	if metadata.Domain != "" {
		domainHost = metadata.Domain
	} else {
		domainHost = metadata.Destination.Fqdn
	}
	if domainHost == "" {
		return false
	}
	domainHost = strings.ToLower(domainHost)
	if r.matcher != nil {
		return r.matcher.matchLower(domainHost)
	}
	for _, keyword := range r.keywords {
		if strings.Contains(domainHost, keyword) {
			return true
		}
	}
	return false
}

func (r *DomainKeywordItem) String() string {
	kLen := len(r.keywords)
	if kLen == 1 {
		return "domain_keyword=" + r.keywords[0]
	} else if kLen > 3 {
		return "domain_keyword=[" + strings.Join(r.keywords[:3], " ") + "...]"
	} else {
		return "domain_keyword=[" + strings.Join(r.keywords, " ") + "]"
	}
}
