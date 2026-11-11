package target

import (
	"crypto/tls"
	"net"
	"sync"
	"time"
)

// TLSCover is the camouflage endpoint a REALITY server forwards
// unauthenticated handshakes to.
//
// It is not a web server: it completes a real TLS handshake with a real
// certificate and answers one HTTP request, which is exactly what REALITY needs
// in order to look like a live site to a prober and what the client skips when
// its authentication succeeds. It exists so the REALITY cell does not depend on
// a public site being reachable.
type TLSCover struct {
	listener net.Listener
	wg       sync.WaitGroup
}

// StartTLSCover serves TLS on addr with the given certificate.
func StartTLSCover(addr, certPath, keyPath string) (*TLSCover, error) {
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	listener, err := tls.Listen("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	})
	if err != nil {
		return nil, err
	}
	cover := &TLSCover{listener: listener}
	cover.wg.Add(1)
	go cover.serve()
	return cover, nil
}

// Addr is the bound address.
func (c *TLSCover) Addr() string { return c.listener.Addr().String() }

// Close stops the cover.
func (c *TLSCover) Close() error {
	err := c.listener.Close()
	c.wg.Wait()
	return err
}

func (c *TLSCover) serve() {
	defer c.wg.Done()
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			return
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			buffer := make([]byte, 4096)
			_, _ = conn.Read(buffer)
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\nok"))
		}()
	}
}
