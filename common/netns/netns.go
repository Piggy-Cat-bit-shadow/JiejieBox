package netns

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/sagernet/sing-box/adapter"
)

var _ adapter.NetworkNamespaceManager = (*Manager)(nil)

func Hold() {
	// The holder's job is to keep the namespace alive until the parent closes stdin. A signal sent
	// to the process group (a terminal Ctrl-C, a service manager's stop) must not kill it early and
	// tear down the namespace other processes are using; the parent's pipe close is the exit
	// condition (upstream 9c24112d12).
	signal.Ignore(os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	buffer := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(buffer)
		if err != nil {
			os.Exit(0)
		}
	}
}
