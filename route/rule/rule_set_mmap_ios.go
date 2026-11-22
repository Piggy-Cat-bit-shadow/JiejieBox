//go:build ios

package rule

import (
	"context"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
)

// mmapRuleSetPlatform activates the compiled-artifact path. It is true only where the product
// already serves rule-sets from an mmap: the derived artifact is a persisted form of exactly the
// temp mapping this build makes, so enabling it here changes WHEN the conversion is paid, not HOW
// a rule-set is represented. On every other platform the rules are heap-resident, and introducing
// a mapping there would be a different product change (steady heap, page-cache and file lifetime on
// Android/desktop), not a reuse of an existing one.
//
// It is a variable rather than a constant so the Unix-only integration tests can exercise the real
// path on a host that can map; production assigns it in this file and never changes it.
var mmapRuleSetPlatform = true

// mmapRuleSet converts a heap rule-set into the transient mmap form this platform serves.
func mmapRuleSet(ctx context.Context, logger logger.Logger, tag string, ruleSet option.PlainRuleSetCompat) option.PlainRuleSetCompat {
	file, _, _, ok := writeRuleSetMmapFile(ctx, logger, tag, ruleSet)
	if !ok {
		return ruleSet
	}
	defer file.Close()
	return mapRuleSetMmapFile(logger, tag, file, ruleSet)
}
