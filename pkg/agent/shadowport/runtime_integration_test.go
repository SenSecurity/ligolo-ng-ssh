//go:build windows && amd64 && shadowport

package shadowport

import (
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/nicocha30/ligolo-ng/pkg/protocol"
)

func TestLiveShadowPort(t *testing.T) {
	if os.Getenv("LIGOLO_SHADOWPORT_INTEGRATION") != "1" {
		t.Skip("set LIGOLO_SHADOWPORT_INTEGRATION=1 to load the signed WinDivert driver")
	}
	publicAddress := os.Getenv("LIGOLO_SHADOWPORT_PUBLIC_ADDRESS")
	source := os.Getenv("LIGOLO_SHADOWPORT_SOURCE")
	readyFile := os.Getenv("LIGOLO_SHADOWPORT_READY_FILE")
	if publicAddress == "" || source == "" || readyFile == "" {
		t.Fatal("public address, source, and ready file environment variables are required")
	}

	SetAllowed(true)
	listener, err := NewListener(net.JoinHostPort(publicAddress, "445"), &protocol.ShadowPortConfig{
		InternalPort:   18445,
		AllowedSources: []string{source},
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	tcpListener, ok := listener.Listener.(*net.TCPListener)
	if !ok {
		t.Fatalf("listener type = %T, want *net.TCPListener", listener.Listener)
	}
	if err := tcpListener.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	connection, err := listener.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	request := make([]byte, len("SHADOWPORT-LIVE"))
	if _, err := io.ReadFull(connection, request); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if string(request) != "SHADOWPORT-LIVE" {
		t.Fatalf("request = %q", request)
	}
	if _, err := fmt.Fprint(connection, "SHADOWPORT-ECHO"); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func TestLiveRuntimeReferenceCounting(t *testing.T) {
	if os.Getenv("LIGOLO_SHADOWPORT_INTEGRATION") != "1" {
		t.Skip("set LIGOLO_SHADOWPORT_INTEGRATION=1 to inspect the embedded runtime lifecycle")
	}
	first, err := acquireRuntime()
	if err != nil {
		t.Fatalf("first acquireRuntime: %v", err)
	}
	second, err := acquireRuntime()
	if err != nil {
		_ = releaseRuntime()
		t.Fatalf("second acquireRuntime: %v", err)
	}
	if first != second {
		t.Fatal("runtime API was not shared")
	}

	sharedRuntime.Lock()
	runtimeDir := sharedRuntime.dir
	refs := sharedRuntime.refs
	sharedRuntime.Unlock()
	if refs != 2 {
		t.Fatalf("runtime refs = %d, want 2", refs)
	}
	if err := releaseRuntime(); err != nil {
		t.Fatalf("first releaseRuntime: %v", err)
	}
	if _, err := os.Stat(runtimeDir); err != nil {
		t.Fatalf("runtime removed while one reference remained: %v", err)
	}
	if err := releaseRuntime(); err != nil {
		t.Fatalf("second releaseRuntime: %v", err)
	}
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime directory still exists after final release: %v", err)
	}
}
