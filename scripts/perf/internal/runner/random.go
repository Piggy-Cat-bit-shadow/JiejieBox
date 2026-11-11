package runner

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// randomUUID returns a RFC 4122 version 4 UUID, which is what the VLESS-family
// protocols take as a user id.
func randomUUID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("read random bytes: %v", err))
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buffer[0:4], buffer[4:6], buffer[6:8], buffer[8:10], buffer[10:16])
}

// randomHex returns n random bytes as lowercase hex.
func randomHex(n int) string {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("read random bytes: %v", err))
	}
	return hex.EncodeToString(buffer)
}
