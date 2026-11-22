//go:build !ios

package rule

import (
	"context"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
)

// mmapRuleSetPlatform is false where the product does not serve rule-sets from a mapping. See the
// iOS file for why the compiled-artifact path follows this flag instead of "mmap is supported".
var mmapRuleSetPlatform = false

// mmapRuleSet is a no-op here: the rule-set is served from the heap.
func mmapRuleSet(ctx context.Context, logger logger.Logger, tag string, ruleSet option.PlainRuleSetCompat) option.PlainRuleSetCompat {
	return ruleSet
}
