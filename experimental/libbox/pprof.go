package libbox

import (
	"net/http"
	_ "net/http/pprof"
	"strconv"

	"github.com/sagernet/sing-box/common/netx"
)

type PProfServer struct {
	server *http.Server
}

func NewPProfServer(port int) *PProfServer {
	return &PProfServer{
		&http.Server{
			Addr: ":" + strconv.Itoa(port),
		},
	}
}

func (s *PProfServer) Start() error {
	ln, err := netx.ListenTCP("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	go s.server.Serve(ln)
	return nil
}

func (s *PProfServer) Close() error {
	return s.server.Close()
}
