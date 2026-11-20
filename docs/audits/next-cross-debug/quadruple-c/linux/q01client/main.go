// Command q01client drives the fake-IP/TUN capture path from inside the test namespace.
//
//   q01client echo <address>        accept connections and count them, printing one line per accept
//   q01client resolve <server> <domain>   resolve through the given DNS server, print the addresses
//   q01client dial <address>        connect, write a greeting, read the reply
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: q01client echo|resolve|dial ...")
	}
	switch os.Args[1] {
	case "echo":
		runEcho(os.Args[2])
	case "resolve":
		runResolve(os.Args[2], os.Args[3])
	case "dial":
		runDial(os.Args[2])
	default:
		fail("unknown subcommand %q", os.Args[1])
	}
}

func runEcho(address string) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fail("echo listen: %v", err)
	}
	fmt.Printf("echo listening on %s\n", listener.Addr())
	accepted := 0
	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			fail("echo accept: %v", acceptErr)
		}
		accepted++
		fmt.Printf("echo accept #%d from %s\n", accepted, conn.RemoteAddr())
		_, _ = conn.Write([]byte("echo\n"))
		_ = conn.Close()
	}
}

func runResolve(server, domain string) {
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(ctx, "udp", server)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	addresses, err := resolver.LookupIPAddr(ctx, domain)
	if err != nil {
		fail("resolve %s: %v", domain, err)
	}
	for _, address := range addresses {
		fmt.Println(address.IP.String())
	}
}

func runDial(address string) {
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		fail("dial %s: %v", address, err)
	}
	defer conn.Close()
	if _, err = conn.Write([]byte("hello\n")); err != nil {
		fail("write: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reply, err := io.ReadAll(conn)
	if err != nil {
		fail("read: %v", err)
	}
	fmt.Printf("dial %s: reply %q\n", address, string(reply))
}

func fail(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
