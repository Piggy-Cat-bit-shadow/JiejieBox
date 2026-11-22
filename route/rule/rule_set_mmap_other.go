//go:build !unix

package rule

import (
	"context"
	"os"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
)

// writeRuleSetMmapFile is unavailable where srs cannot map. The derived cache is not active there
// either (see mmapRuleSetPlatform), so this is only a compile-time stub.
func writeRuleSetMmapFile(ctx context.Context, logger logger.Logger, tag string, ruleSet option.PlainRuleSetCompat) (*os.File, bool) {
	return nil, false
}

func mapRuleSetMmapFile(logger logger.Logger, tag string, file *os.File, fallback option.PlainRuleSetCompat) option.PlainRuleSetCompat {
	return fallback
}
