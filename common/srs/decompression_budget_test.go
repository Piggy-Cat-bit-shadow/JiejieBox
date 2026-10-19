package srs

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
	"runtime"
	"testing"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/stretchr/testify/require"
)

// withDecompressedLimit lowers the production budget for one test and restores it afterwards.
// The budget is a var, not a const, exactly so the adversarial cases can run at a realistic size
// instead of materialising 128 MiB of rule data in every `make test`.
func withDecompressedLimit(t *testing.T, limit int64) {
	t.Helper()
	previous := decompressedSizeLimit
	decompressedSizeLimit = limit
	t.Cleanup(func() { decompressedSizeLimit = previous })
}

func writeRuleSetWithKeywords(t *testing.T, keywords []string) []byte {
	t.Helper()
	ruleSet := option.PlainRuleSet{Rules: []option.HeadlessRule{{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultHeadlessRule{
			DomainKeyword: badoption.Listable[string](keywords),
		},
	}}}
	var buffer bytes.Buffer
	require.NoError(t, Write(&buffer, ruleSet, C.RuleSetVersionCurrent))
	return buffer.Bytes()
}

// appendCompressedTail decompresses the payload of a valid .srs, appends tailSize zero bytes
// inside the SAME zlib stream, and reassembles the file with its original 4-byte header.
//
// This is the shape of a decompression bomb: a completely valid rule list followed by bytes the
// parser never needs but the checksum verification must decompress to reach the trailer.
func appendCompressedTail(t *testing.T, valid []byte, tailSize int) []byte {
	t.Helper()
	decompressReader, err := zlib.NewReader(bytes.NewReader(valid[4:]))
	require.NoError(t, err)
	raw, err := io.ReadAll(decompressReader)
	require.NoError(t, err)
	require.NoError(t, decompressReader.Close())
	raw = append(raw, make([]byte, tailSize)...)
	var compressed bytes.Buffer
	compressWriter := zlib.NewWriter(&compressed)
	_, err = compressWriter.Write(raw)
	require.NoError(t, err)
	require.NoError(t, compressWriter.Close())
	reassembled := append([]byte{}, valid[:4]...)
	return append(reassembled, compressed.Bytes()...)
}

func repeatKeyword(size int, count int) []string {
	keyword := make([]byte, size)
	for i := range keyword {
		keyword[i] = byte('a' + i%26)
	}
	keywords := make([]string, count)
	for i := range keywords {
		keywords[i] = string(keyword)
	}
	return keywords
}

// A stream that expands far past the budget must fail promptly, without allocating its output and
// without being misreported as an integrity failure.
func TestReadRejectsADecompressionBomb(t *testing.T) {
	withDecompressedLimit(t, 1<<20)
	valid := writeRuleSetWithKeywords(t, []string{"bomb.example"})
	bomb := appendCompressedTail(t, valid, 8<<20)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := Read(bytes.NewReader(bomb), false)
	runtime.ReadMemStats(&after)

	require.ErrorIs(t, err, ErrDecompressedTooLarge)
	require.NotContains(t, err.Error(), "verify rule-set stream",
		"budget exhaustion is a refusal for work, not a checksum or truncation failure")
	allocated := after.TotalAlloc - before.TotalAlloc
	require.Less(t, allocated, uint64(4<<20),
		"refusing the bomb must not allocate its decompressed output")
}

// The budget must be enforced on declared rule data too, not only on the discarded tail, and the
// boundary must not reject a legal stream that is under it.
func TestReadEnforcesTheDecompressedBudgetAtTheBoundary(t *testing.T) {
	withDecompressedLimit(t, 1<<20)

	under := writeRuleSetWithKeywords(t, repeatKeyword(100, 8000))
	_, err := Read(bytes.NewReader(under), false)
	require.NoError(t, err, "a legal stream under the budget must still parse")

	over := writeRuleSetWithKeywords(t, repeatKeyword(100, 12000))
	_, err = Read(bytes.NewReader(over), false)
	require.True(t, errors.Is(err, ErrDecompressedTooLarge),
		"a legal-shaped stream past the budget must be refused with the dedicated error")
}

// A corrupt trailer and a budget refusal must stay distinguishable: the first is an integrity
// failure the operator must investigate, the second is a bounded refusal.
func TestReadKeepsBudgetRefusalDistinctFromChecksumFailure(t *testing.T) {
	withDecompressedLimit(t, 1<<20)
	valid := writeRuleSetWithKeywords(t, []string{"checksum.example"})
	require.Greater(t, len(valid), 8)

	corrupt := append([]byte{}, valid...)
	corrupt[len(corrupt)-1] ^= 0xFF
	_, err := Read(bytes.NewReader(corrupt), false)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrDecompressedTooLarge),
		"a corrupt Adler-32 must not be reported as a budget refusal")

	bomb := appendCompressedTail(t, writeRuleSetWithKeywords(t, []string{"bomb.example"}), 8<<20)
	_, err = Read(bytes.NewReader(bomb), false)
	require.ErrorIs(t, err, ErrDecompressedTooLarge)
}
