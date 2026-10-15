package main

import "bytes"

// The deterministic payload.
//
// A benchmark that only counts bytes cannot tell a correct tunnel from one that
// duplicates, truncates or reorders a stream — and a transport bug that loses
// data usually also reports a higher throughput, which is exactly the wrong
// verdict. So every payload byte is a function of its absolute stream offset,
// and both ends can reconstruct it independently.
//
// The block is 1 MiB and position dependent; verification is memcmp against the
// same block, which is vectorized and costs a fraction of a byte per byte. The
// generator is a multiply-shift over the offset, computed once at startup rather
// than per byte on the wire.
const (
	patternBlockSize = 1 << 20
	patternMask      = patternBlockSize - 1
)

var patternBlock = buildPattern()

func buildPattern() []byte {
	block := make([]byte, patternBlockSize)
	for i := range block {
		v := uint64(i) * 2654435761
		block[i] = byte(v >> 24)
	}
	return block
}

// patternFill writes the pattern bytes that belong at absolute offset `offset`
// into dst, wrapping at the block boundary.
func patternFill(dst []byte, offset uint64) {
	position := int(offset & patternMask)
	for len(dst) > 0 {
		n := copy(dst, patternBlock[position:])
		dst = dst[n:]
		position = (position + n) & patternMask
	}
}

// patternVerify returns how many bytes of dst disagree with the pattern at
// absolute offset `offset`. Zero means byte-exact.
func patternVerify(dst []byte, offset uint64) int {
	mismatches := 0
	position := int(offset & patternMask)
	for len(dst) > 0 {
		n := patternBlockSize - position
		if n > len(dst) {
			n = len(dst)
		}
		expected := patternBlock[position : position+n]
		actual := dst[:n]
		if !bytes.Equal(actual, expected) {
			for i := range actual {
				if actual[i] != expected[i] {
					mismatches++
				}
			}
		}
		dst = dst[n:]
		position = (position + n) & patternMask
	}
	return mismatches
}
