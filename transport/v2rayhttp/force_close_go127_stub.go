//go:build go1.27 && !http2legacy && !(badlinkname && tfogo_checklinkname0 && http2stdlibwrap)

package v2rayhttp

import "golang.org/x/net/http2"

func closeHTTP2Connections(transport *http2.Transport) {
	transport.CloseIdleConnections()
}
