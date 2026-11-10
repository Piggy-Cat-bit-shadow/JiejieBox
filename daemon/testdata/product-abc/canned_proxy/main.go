// canned_proxy answers every HTTP-proxy request that reaches it with a fixed marker body.
//
// It is the stand-in for the provider nodes' egress in the product A/B/C fixture: A.json points its
// provider node at 18081, B.json at 18082, and the body a request receives names the node that
// carried it. Run it next to the product client:
//
//	go run canned_proxy.go -listen "18081=node-A,18082=node-B"
//
// The HTTP outbound sends CONNECT + a tunnelled request; a plain request is answered too.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
)

func serve(port int, marker string) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	log.Printf("marker %q on 127.0.0.1:%d", marker, port)
	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return acceptErr
		}
		go func(conn net.Conn) {
			defer conn.Close()
			reader := bufio.NewReader(conn)
			firstLine, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			for {
				header, headerErr := reader.ReadString('\n')
				if headerErr != nil {
					return
				}
				if header == "\r\n" {
					break
				}
			}
			if strings.HasPrefix(firstLine, "CONNECT ") {
				_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
				for {
					header, headerErr := reader.ReadString('\n')
					if headerErr != nil {
						return
					}
					if header == "\r\n" {
						break
					}
				}
			}
			_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n%s", len(marker), marker)
		}(connection)
	}
}

func main() {
	listen := flag.String("listen", "18081=node-A,18082=node-B", "port=marker pairs")
	flag.Parse()
	for _, item := range strings.Split(*listen, ",") {
		parts := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(parts) != 2 {
			log.Fatalf("bad -listen item %q", item)
		}
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			log.Fatalf("bad port in %q: %v", item, err)
		}
		go func(port int, marker string) {
			if err := serve(port, marker); err != nil {
				log.Fatalf("serve %d: %v", port, err)
			}
		}(port, parts[1])
	}
	select {}
}
