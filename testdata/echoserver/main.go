// Command echoserver is a small TCP/UDP target used by the SSH transport
// runtime test. It is not included in release artifacts.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
)

func main() {
	tcpAddress := flag.String("tcp", "0.0.0.0:19090", "TCP echo listen address")
	udpAddress := flag.String("udp", "0.0.0.0:19091", "UDP echo listen address")
	flag.Parse()

	errors := make(chan error, 2)
	go func() { errors <- serveTCP(*tcpAddress) }()
	go func() { errors <- serveUDP(*udpAddress) }()
	log.Fatal(<-errors)
}

func serveTCP(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("TCP listen: %w", err)
	}
	log.Printf("TCP echo listening on %s", listener.Addr())
	for {
		conn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("TCP accept: %w", err)
		}
		go func() {
			defer conn.Close()
			if _, err := io.Copy(conn, conn); err != nil {
				log.Printf("TCP echo: %v", err)
			}
		}()
	}
}

func serveUDP(address string) error {
	conn, err := net.ListenPacket("udp", address)
	if err != nil {
		return fmt.Errorf("UDP listen: %w", err)
	}
	defer conn.Close()
	log.Printf("UDP echo listening on %s", conn.LocalAddr())

	buffer := make([]byte, 64*1024)
	for {
		n, remote, err := conn.ReadFrom(buffer)
		if err != nil {
			return fmt.Errorf("UDP read: %w", err)
		}
		if _, err := conn.WriteTo(buffer[:n], remote); err != nil {
			log.Printf("UDP echo: %v", err)
		}
	}
}
