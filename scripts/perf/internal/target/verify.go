package target

import "bytes"

// PatternPageLen is the period of the payload pattern.
const PatternPageLen = 4096

// Verify compares chunk against the pattern at the stream's own offset and
// returns the number of bytes that do not match.
//
// The fast path compares whole pages with memcmp after skipping the unaligned
// head; read boundaries do not fall on page boundaries, so a verifier that only
// handled aligned buffers would silently fall back to a per-byte comparison for
// most of the stream and become the bottleneck the measurement is trying to see
// past. The slow path exists only to count the bad bytes in the page that
// actually differs.
func Verify(chunk []byte, offset int64) int64 {
	pos := 0
	var bad int64
	for pos < len(chunk) {
		index := (offset + int64(pos)) % PatternPageLen
		if index != 0 {
			if chunk[pos] != PatternPage[index] {
				bad++
			}
			pos++
			continue
		}
		remaining := len(chunk) - pos
		if remaining >= PatternPageLen {
			pages := remaining / PatternPageLen
			for page := 0; page < pages; page++ {
				start := pos + page*PatternPageLen
				if !bytes.Equal(chunk[start:start+PatternPageLen], PatternPage) {
					bad += countBadPage(chunk[start : start+PatternPageLen])
				}
			}
			pos += pages * PatternPageLen
			continue
		}
		if !bytes.Equal(chunk[pos:], PatternPage[:remaining]) {
			bad += countBadPage(chunk[pos:])
		}
		pos += remaining
	}
	return bad
}

func countBadPage(page []byte) int64 {
	var bad int64
	for i := 0; i < len(page); i++ {
		if page[i] != PatternPage[i] {
			bad++
		}
	}
	return bad
}
