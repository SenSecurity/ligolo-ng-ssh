// Ligolo-ng
// Copyright (C) 2025 Nicolas Chatelain (nicocha30)
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package sshtransport

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type testKeys struct {
	hostPrivate  string
	hostPublic   ssh.PublicKey
	agentPrivate string
	agentPublic  ssh.PublicKey
	wrongPrivate string
	authorized   string
}

func TestSSHTransportRoundTrip(t *testing.T) {
	listener, incoming, keys := startTestServer(t)

	clientConn, err := Dial(context.Background(), ClientOptions{
		Address:            listener.Addr().String(),
		User:               "ligolo",
		PrivateKeyPath:     keys.agentPrivate,
		HostKeyFingerprint: ssh.FingerprintSHA256(keys.hostPublic),
		ConnectTimeout:     2 * time.Second,
		KeepAliveInterval:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })

	var serverConn Conn
	select {
	case serverConn = <-incoming:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Ligolo SSH channel")
	}
	t.Cleanup(func() { _ = serverConn.Close() })

	assertStream(t, clientConn, serverConn, "agent-to-proxy")
	assertStream(t, serverConn, clientConn, "proxy-to-agent")
	if clientConn.RemoteAddr().String() != listener.Addr().String() {
		t.Fatalf("client remote address = %q, want %q", clientConn.RemoteAddr(), listener.Addr())
	}
}

func TestSSHServerPresentsRealBanner(t *testing.T) {
	listener, _, _ := startTestServer(t)

	conn, err := net.DialTimeout("tcp", listener.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("net.DialTimeout() error = %v", err)
	}
	defer conn.Close()

	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	banner, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read SSH banner: %v", err)
	}
	if banner != serverVersion+"\r\n" {
		t.Fatalf("SSH banner = %q, want %q", banner, serverVersion+"\\r\\n")
	}
}

func TestSSHAuthenticationRejectsWrongKeyAndUser(t *testing.T) {
	listener, _, keys := startTestServer(t)
	base := ClientOptions{
		Address:            listener.Addr().String(),
		User:               "ligolo",
		PrivateKeyPath:     keys.wrongPrivate,
		HostKeyFingerprint: ssh.FingerprintSHA256(keys.hostPublic),
		ConnectTimeout:     2 * time.Second,
	}

	if conn, err := Dial(context.Background(), base); err == nil {
		_ = conn.Close()
		t.Fatal("Dial() accepted an unauthorized private key")
	}

	base.PrivateKeyPath = keys.agentPrivate
	base.User = "wrong-user"
	if conn, err := Dial(context.Background(), base); err == nil {
		_ = conn.Close()
		t.Fatal("Dial() accepted an unauthorized SSH user")
	}
}

func TestSSHInsecureNoAuthRequiresExplicitServerAndClientOptIn(t *testing.T) {
	authenticatedListener, _, keys := startTestServer(t)
	noAuthClient := ClientOptions{
		Address:            authenticatedListener.Addr().String(),
		User:               "ligolo",
		HostKeyFingerprint: ssh.FingerprintSHA256(keys.hostPublic),
		InsecureNoAuth:     true,
		ConnectTimeout:     2 * time.Second,
	}
	if conn, err := Dial(context.Background(), noAuthClient); err == nil {
		_ = conn.Close()
		t.Fatal("authenticated SSH server accepted a no-auth client")
	}

	noAuthClient.PrivateKeyPath = keys.agentPrivate
	if conn, err := Dial(context.Background(), noAuthClient); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		if err == nil {
			_ = conn.Close()
		}
		t.Fatalf("Dial() no-auth with private key error = %v", err)
	}
}

func TestSSHInsecureNoAuthRoundTrip(t *testing.T) {
	keys := createTestKeys(t)
	config, err := NewServerConfig(ServerOptions{
		User:                "ligolo",
		HostKeyPath:         keys.hostPrivate,
		InsecureAllowNoAuth: true,
	})
	if err != nil {
		t.Fatalf("NewServerConfig() no-auth error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	incoming := make(chan Conn, 1)
	go func() {
		_ = Serve(listener, config, incoming, 2*time.Second)
	}()

	wrongUser := ClientOptions{
		Address:            listener.Addr().String(),
		User:               "wrong-user",
		HostKeyFingerprint: ssh.FingerprintSHA256(keys.hostPublic),
		InsecureNoAuth:     true,
		ConnectTimeout:     2 * time.Second,
	}
	if conn, err := Dial(context.Background(), wrongUser); err == nil {
		_ = conn.Close()
		t.Fatal("no-auth SSH server accepted an unauthorized username")
	}

	zeroFileClient, err := Dial(context.Background(), ClientOptions{
		Address:               listener.Addr().String(),
		User:                  "ligolo",
		InsecureIgnoreHostKey: true,
		InsecureNoAuth:        true,
		ConnectTimeout:        2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial() zero-file no-auth error = %v", err)
	}
	select {
	case serverConn := <-incoming:
		_ = serverConn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for zero-file no-auth SSH channel")
	}
	_ = zeroFileClient.Close()

	clientConn, err := Dial(context.Background(), ClientOptions{
		Address:            listener.Addr().String(),
		User:               "ligolo",
		HostKeyFingerprint: ssh.FingerprintSHA256(keys.hostPublic),
		InsecureNoAuth:     true,
		ConnectTimeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Dial() no-auth error = %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })

	select {
	case serverConn := <-incoming:
		t.Cleanup(func() { _ = serverConn.Close() })
		assertStream(t, clientConn, serverConn, "no-auth-agent-to-proxy")
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for no-auth Ligolo SSH channel")
	}
}

func TestSSHHostVerification(t *testing.T) {
	listener, incoming, keys := startTestServer(t)
	base := ClientOptions{
		Address:        listener.Addr().String(),
		User:           "ligolo",
		PrivateKeyPath: keys.agentPrivate,
		ConnectTimeout: 2 * time.Second,
	}

	if conn, err := Dial(context.Background(), base); !errors.Is(err, ErrNoHostVerification) {
		if err == nil {
			_ = conn.Close()
		}
		t.Fatalf("Dial() without host verification error = %v, want %v", err, ErrNoHostVerification)
	}

	base.HostKeyFingerprint = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if conn, err := Dial(context.Background(), base); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		if err == nil {
			_ = conn.Close()
		}
		t.Fatalf("Dial() wrong fingerprint error = %v", err)
	}

	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{listener.Addr().String()}, keys.hostPublic)
	if err := os.WriteFile(knownHostsPath, []byte(line+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	base.HostKeyFingerprint = ""
	base.KnownHostsPath = knownHostsPath
	conn, err := Dial(context.Background(), base)
	if err != nil {
		t.Fatalf("Dial() with valid known_hosts error = %v", err)
	}
	defer conn.Close()

	select {
	case serverConn := <-incoming:
		_ = serverConn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for known_hosts-verified connection")
	}
}

func assertStream(t *testing.T, writer io.Writer, reader io.Reader, payload string) {
	t.Helper()
	writeErr := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, payload)
		writeErr <- err
	}()

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(got) != payload {
		t.Fatalf("stream payload = %q, want %q", got, payload)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
}

func startTestServer(t *testing.T) (net.Listener, <-chan Conn, testKeys) {
	t.Helper()
	keys := createTestKeys(t)
	config, err := NewServerConfig(ServerOptions{
		User:               "ligolo",
		HostKeyPath:        keys.hostPrivate,
		AuthorizedKeysPath: keys.authorized,
	})
	if err != nil {
		t.Fatalf("NewServerConfig() error = %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	incoming := make(chan Conn, 8)
	go func() {
		_ = Serve(listener, config, incoming, 2*time.Second)
	}()
	return listener, incoming, keys
}

func createTestKeys(t *testing.T) testKeys {
	t.Helper()
	dir := t.TempDir()
	hostPrivate, hostPublic := createKeyPair(t, dir, "host")
	agentPrivate, agentPublic := createKeyPair(t, dir, "agent")
	wrongPrivate, _ := createKeyPair(t, dir, "wrong")
	authorized := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(authorized, ssh.MarshalAuthorizedKey(agentPublic), 0o600); err != nil {
		t.Fatalf("write authorized_keys: %v", err)
	}
	return testKeys{
		hostPrivate:  hostPrivate,
		hostPublic:   hostPublic,
		agentPrivate: agentPrivate,
		agentPublic:  agentPublic,
		wrongPrivate: wrongPrivate,
		authorized:   authorized,
	}
}

func createKeyPair(t *testing.T, dir, name string) (string, ssh.PublicKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey() error = %v", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatalf("x509.MarshalPKCS8PrivateKey() error = %v", err)
	}
	privatePath := filepath.Join(dir, name)
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	if err := os.WriteFile(privatePath, privatePEM, 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey() error = %v", err)
	}
	return privatePath, sshPublic
}
