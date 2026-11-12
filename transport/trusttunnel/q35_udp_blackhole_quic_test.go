//go:build with_quic

package trusttunnel

// The HTTP/3 leg of the Q35 matrix exists only under with_quic; the matrix test reads this instead
// of silently dropping the protocol when the tag is absent.
const q35HasQUIC = true
