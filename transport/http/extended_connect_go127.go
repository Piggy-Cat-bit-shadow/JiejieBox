//go:build go1.27 && !http2legacy && badlinkname && tfogo_checklinkname0 && http2stdlibwrap

package http

// Go 1.27 replaced the golang.org/x/net/http2 client with net/http's internal copy, so the
// extended-CONNECT switch moved to net/http/internal/http2.disableExtendedConnectProtocol.
//
// That variable is NOT pullable: it is a plain package-level variable in the standard library
// with no push-form //go:linkname, so cmd/link rejects a pull of it under its default policy
// ("invalid reference to net/http/internal/http2.disableExtendedConnectProtocol"). The previous
// revision of this file pulled it anyway, which made every profile-tagged BARE build fail:
//
//	go build -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" ./...
//
// That bare build is a checked contract (verify.yml, "tagged build of every package"), and the
// official artifact does not cover it: the release recipe passes -checklinkname=0 through
// release/LDFLAGS, so the break was invisible to the shipped binary and visible only to a reader
// who builds with the profile tags and without the linker flag.
//
// The pull is not needed here. In Go 1.27 that variable is read by the HTTP/2 SERVER only
// (net/http/internal/http2/server.go); the client decides whether to send :protocol from the
// peer's SETTINGS_ENABLE_CONNECT_PROTOCOL, which is what this package's tunnel code actually
// depends on. Forcing the server-side default from a client transport therefore changed nothing
// about the tunnel, and cost the bare-build contract.
//
// If an in-process net/http server ever needs extended CONNECT on Go 1.27, the supported lever is
// the http2xconnect GODEBUG, which that package's init reads from the environment. It is a
// process-start setting, so a library cannot apply it on the caller's behalf; that is a deployment
// contract, not something to fake with an unlinkable symbol pull.
const extendedConnectAvailable = true
