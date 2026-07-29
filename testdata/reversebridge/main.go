// Command reversebridge is a test-only two-ended TCP bridge. It allows the
// Linux agent to reach the Windows proxy when Windows Firewall blocks inbound
// WSL traffic, without adding any forwarding dependency to release binaries.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
)

func main() {
	mode := flag.String("mode", "", "broker or connector")
	agentListen := flag.String("agent-listen", "127.0.0.1:12224", "broker listener used by the Linux agent")
	connectorListen := flag.String("connector-listen", "0.0.0.0:20000", "broker listener used by the Windows connector")
	brokerAddress := flag.String("broker", "", "broker connector address")
	targetAddress := flag.String("target", "", "Windows proxy target address")
	flag.Parse()

	var err error
	switch *mode {
	case "broker":
		err = broker(*agentListen, *connectorListen)
	case "connector":
		err = connector(*brokerAddress, *targetAddress)
	default:
		err = fmt.Errorf("-mode must be broker or connector")
	}
	log.Fatal(err)
}

func broker(agentAddress, connectorAddress string) error {
	agentListener, err := net.Listen("tcp", agentAddress)
	if err != nil {
		return fmt.Errorf("agent listen: %w", err)
	}
	defer agentListener.Close()
	connectorListener, err := net.Listen("tcp", connectorAddress)
	if err != nil {
		return fmt.Errorf("connector listen: %w", err)
	}
	defer connectorListener.Close()
	log.Printf("broker listening for agent on %s and connector on %s", agentListener.Addr(), connectorListener.Addr())

	connectorConn, err := connectorListener.Accept()
	if err != nil {
		return fmt.Errorf("accept connector: %w", err)
	}
	defer connectorConn.Close()
	log.Printf("connector joined from %s", connectorConn.RemoteAddr())

	agentConn, err := agentListener.Accept()
	if err != nil {
		return fmt.Errorf("accept agent: %w", err)
	}
	defer agentConn.Close()
	log.Printf("agent joined from %s", agentConn.RemoteAddr())
	return relay(agentConn, connectorConn)
}

func connector(brokerAddress, targetAddress string) error {
	if brokerAddress == "" || targetAddress == "" {
		return fmt.Errorf("-broker and -target are required in connector mode")
	}
	brokerConn, err := net.Dial("tcp", brokerAddress)
	if err != nil {
		return fmt.Errorf("connect broker: %w", err)
	}
	defer brokerConn.Close()
	targetConn, err := net.Dial("tcp", targetAddress)
	if err != nil {
		return fmt.Errorf("connect target: %w", err)
	}
	defer targetConn.Close()
	log.Printf("bridging %s to %s", brokerAddress, targetAddress)
	return relay(brokerConn, targetConn)
}

func relay(left, right net.Conn) error {
	errors := make(chan error, 2)
	copySide := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		if tcp, ok := dst.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		errors <- err
	}
	go copySide(left, right)
	go copySide(right, left)
	return <-errors
}
