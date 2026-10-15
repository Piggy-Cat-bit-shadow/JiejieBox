package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// The camouflage TLS server for REALITY environments.
//
// A REALITY server forwards a handshake it does not authenticate to its `dest`,
// so the dest must be a real TLS endpoint that answers. This one answers with a
// minimal HTTP response and then holds the connection briefly: enough for the
// handshake to complete, and deliberately not enough to be mistaken for the
// tunnel's own path.
func runTLSCover(args []string) error {
	flags := flag.NewFlagSet("tlscover", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:9300", "listen address")
	certFile := flags.String("cert", "", "certificate file")
	keyFile := flags.String("key", "", "key file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *certFile == "" || *keyFile == "" {
		return fmt.Errorf("tlscover: -cert and -key are required")
	}
	certificate, err := loadCertificate(*certFile, *keyFile)
	if err != nil {
		return err
	}
	listener, err := tls.Listen("tcp", *listen, &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "tlscover: listening on %s\n", listener.Addr())
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if strings.Contains(acceptErr.Error(), "use of closed network connection") {
				return nil
			}
			continue
		}
		go serveCover(conn)
	}
}

func serveCover(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	buffer := make([]byte, 4096)
	if _, err := conn.Read(buffer); err != nil {
		return
	}
	body := "ok\n"
	response := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
	_, _ = conn.Write([]byte(response))
}
