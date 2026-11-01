// Package netx holds the small socket policy shared by listeners that are not
// product inbounds.
package netx

import (
	"context"
	"net"
)

// ListenTCP opens a plain-TCP listener.
//
// Go 1.24+ enables MPTCP for Linux listeners by default. A vulnerable Linux
// kernel's MPTCP listener is remotely exploitable (the reason Xray v26.10.31
// disabled it), and these API/debug endpoints have no tcp_multi_path option to
// opt back in. Product inbounds use common/listener, which owns that option.
func ListenTCP(network, address string) (net.Listener, error) {
	var listenConfig net.ListenConfig
	listenConfig.SetMultipathTCP(false)
	return listenConfig.Listen(context.Background(), network, address)
}
