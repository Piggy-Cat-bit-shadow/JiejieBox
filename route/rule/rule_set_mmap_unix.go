//go:build unix

package rule

import (
	"context"
	"io"
	"os"

	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	"github.com/sagernet/sing/service/filemanager"
)

// writeRuleSetMmapFile writes one compiled rule-set to a transient file and rewinds it, ready to be
// mapped by mapRuleSetMmapFile. The caller owns the file and must close it.
//
// It is the first half of the iOS temp-mmap path, split out so a caller can ALSO publish the exact
// bytes as a derived artifact before mapping them - one write of the SRSM bytes serves both.
func writeRuleSetMmapFile(ctx context.Context, logger logger.Logger, tag string, ruleSet option.PlainRuleSetCompat) (*os.File, bool) {
	file, err := filemanager.CreateTemp(ctx, "rule-set-*.mmap")
	if err != nil {
		logger.Warn(E.Cause(err, "create rule-set mmap file ", tag))
		return nil, false
	}
	if err = srs.WriteMmap(file, ruleSet); err != nil {
		_ = file.Close()
		_ = filemanager.Remove(ctx, file.Name())
		logger.Warn(E.Cause(err, "mmap rule-set ", tag))
		return nil, false
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		_ = filemanager.Remove(ctx, file.Name())
		logger.Warn(E.Cause(err, "mmap rule-set ", tag))
		return nil, false
	}
	return file, true
}

// mapRuleSetMmapFile maps the transient file and returns the fallback on any failure, so the caller
// keeps its previous behaviour: a platform or filesystem that cannot map simply loads on the heap.
// On success srs.ReadMmap unlinks the name; the mapping keeps the file alive.
func mapRuleSetMmapFile(logger logger.Logger, tag string, file *os.File, fallback option.PlainRuleSetCompat) option.PlainRuleSetCompat {
	mapped, err := srs.ReadMmap(file)
	if err != nil {
		logger.Warn(E.Cause(err, "mmap rule-set ", tag))
		return fallback
	}
	logger.Debug("mmap rule-set ", tag)
	return mapped
}
