//go:build go1.27 && !http2legacy && !badlinkname

package http

// This is the pre-migration stub, kept because the Go 1.27 stdlib wrap needs both
// -checklinkname=0 and a private-layout reinterpretation. A build without badlinkname has
// neither, and before this file existed the equivalent stub said the same thing:
// extended CONNECT is unavailable here, so the tunnel falls back to HTTP/1 instead of
// attempting an HTTP/2 path this build cannot serve.
//
// The badlinkname builds that also carry the release flag are covered by
// extended_connect_legacy.go (the x/net transport) or, with the explicit
// tfogo_checklinkname0+http2stdlibwrap opt-in, by extended_connect_go127.go.
const extendedConnectAvailable = false
