package target

import "testing"

func patternBytes(n int, offset int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = PatternPage[(offset+i)%len(PatternPage)]
	}
	return out
}

func TestVerifyAcceptsAlignedAndUnalignedWindows(t *testing.T) {
	for _, offset := range []int64{0, 1, 4095, 4096, 8193, 12345} {
		chunk := patternBytes(20000, int(offset))
		if bad := Verify(chunk, offset); bad != 0 {
			t.Fatalf("offset %d: aligned pattern reported %d bad bytes", offset, bad)
		}
	}
}

func TestVerifyCountsCorruptedBytes(t *testing.T) {
	chunk := patternBytes(10000, 0)
	chunk[10] ^= 0xff
	chunk[5000] ^= 0xff
	if bad := Verify(chunk, 0); bad != 2 {
		t.Fatalf("corrupted pattern reported %d bad bytes, want 2", bad)
	}
}

func TestVerifyHandlesPartialPageAtTheEnd(t *testing.T) {
	chunk := patternBytes(4096+17, 0)
	chunk[4096+16] ^= 0x01
	if bad := Verify(chunk, 0); bad != 1 {
		t.Fatalf("tail corruption reported %d bad bytes, want 1", bad)
	}
}

func TestVerifyFindsTheWholeCorruptedPage(t *testing.T) {
	chunk := patternBytes(8192, 0)
	for i := 4096; i < 8192; i++ {
		chunk[i] ^= 0xff
	}
	if bad := Verify(chunk, 0); bad != 4096 {
		t.Fatalf("fully corrupted page reported %d bad bytes, want 4096", bad)
	}
}
