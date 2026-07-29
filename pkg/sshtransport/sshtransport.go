// Ligolo-ng
// Copyright (C) 2025 Nicolas Chatelain (nicocha30)
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Package sshtransport carries the existing Ligolo yamux stream over a real
// SSH connection. SSH is responsible for authentication, host verification,
// encryption, and the outer keepalive; Ligolo's protocol remains unchanged.
package sshtransport

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	// ChannelType is deliberately not a session or port-forwarding channel.
	// It makes packet captures and server logs unambiguously identify the
	// application stream as SSH while keeping Ligolo multiplexing unchanged.
	ChannelType = "ligolo"

	clientVersion = "SSH-2.0-LigoloSSH_Agent"
	serverVersion = "SSH-2.0-LigoloSSH_Proxy"
)

var (
	ErrNoHostVerification = errors.New("SSH host verification is required: set known_hosts or a host-key fingerprint")
	ErrNoAuthorizedKeys   = errors.New("SSH authorized_keys contains no usable public keys")
)

// Conn is the subset of net.Conn needed by Ligolo and yamux. An SSH channel
// does not support deadlines, so this interface intentionally omits them.
type Conn interface {
	io.ReadWriteCloser
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
}

// ClientOptions configures an outbound SSH connection made by an agent.
type ClientOptions struct {
	Address               string
	User                  string
	PrivateKeyPath        string
	PrivateKeyPassphrase  []byte
	KnownHostsPath        string
	HostKeyFingerprint    string
	InsecureIgnoreHostKey bool
	InsecureNoAuth        bool
	ConnectTimeout        time.Duration
	KeepAliveInterval     time.Duration
}

// ServerOptions configures the embedded SSH server in the proxy.
type ServerOptions struct {
	User                string
	HostKeyPath         string
	HostKeyPassphrase   []byte
	AuthorizedKeysPath  string
	InsecureAllowNoAuth bool
	HandshakeTimeout    time.Duration
}

// ChannelConn adapts an SSH channel to the address-aware connection interface
// expected by the proxy. Closing it also closes the owning SSH connection.
type ChannelConn struct {
	ssh.Channel
	localAddr  net.Addr
	remoteAddr net.Addr
	closeFn    func() error
	closeOnce  sync.Once
	closeErr   error
}

func (c *ChannelConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *ChannelConn) RemoteAddr() net.Addr { return c.remoteAddr }

func (c *ChannelConn) Close() error {
	c.closeOnce.Do(func() {
		channelErr := c.Channel.Close()
		if c.closeFn != nil {
			c.closeErr = c.closeFn()
		}
		if channelErr != nil && c.closeErr == nil {
			c.closeErr = channelErr
		}
	})
	return c.closeErr
}

// LoadSigner reads an OpenSSH/PEM private key, optionally encrypted.
func LoadSigner(path string, passphrase []byte) (ssh.Signer, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SSH private key %q: %w", path, err)
	}
	var signer ssh.Signer
	if len(passphrase) > 0 {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
	} else {
		signer, err = ssh.ParsePrivateKey(key)
	}
	if err != nil {
		return nil, fmt.Errorf("parse SSH private key %q: %w", path, err)
	}
	return signer, nil
}

// PrivateKeyFingerprint returns the SHA256 fingerprint operators pin on agents.
func PrivateKeyFingerprint(path string, passphrase []byte) (string, error) {
	signer, err := LoadSigner(path, passphrase)
	if err != nil {
		return "", err
	}
	return ssh.FingerprintSHA256(signer.PublicKey()), nil
}

// LoadAuthorizedKeys reads all public keys accepted by the proxy.
func LoadAuthorizedKeys(path string) ([]ssh.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SSH authorized_keys %q: %w", path, err)
	}
	var keys []ssh.PublicKey
	for len(bytes.TrimSpace(data)) > 0 {
		key, _, _, rest, parseErr := ssh.ParseAuthorizedKey(data)
		if parseErr != nil {
			return nil, fmt.Errorf("parse SSH authorized_keys %q: %w", path, parseErr)
		}
		keys = append(keys, key)
		data = rest
	}
	if len(keys) == 0 {
		return nil, ErrNoAuthorizedKeys
	}
	return keys, nil
}

// NewServerConfig builds a public-key-only SSH server configuration.
func NewServerConfig(options ServerOptions) (*ssh.ServerConfig, error) {
	if strings.TrimSpace(options.User) == "" {
		return nil, errors.New("SSH user cannot be empty")
	}
	hostSigner, err := LoadSigner(options.HostKeyPath, options.HostKeyPassphrase)
	if err != nil {
		return nil, err
	}

	config := &ssh.ServerConfig{
		NoClientAuth:  options.InsecureAllowNoAuth,
		ServerVersion: serverVersion,
	}
	if options.InsecureAllowNoAuth {
		config.NoClientAuthCallback = func(metadata ssh.ConnMetadata) (*ssh.Permissions, error) {
			if subtle.ConstantTimeCompare([]byte(metadata.User()), []byte(options.User)) != 1 {
				return nil, fmt.Errorf("SSH user %q is not authorized", metadata.User())
			}
			return nil, nil
		}
	} else {
		authorizedKeys, loadErr := LoadAuthorizedKeys(options.AuthorizedKeysPath)
		if loadErr != nil {
			return nil, loadErr
		}
		config.PublicKeyCallback = func(metadata ssh.ConnMetadata, candidate ssh.PublicKey) (*ssh.Permissions, error) {
			if subtle.ConstantTimeCompare([]byte(metadata.User()), []byte(options.User)) != 1 {
				return nil, fmt.Errorf("SSH user %q is not authorized", metadata.User())
			}
			for _, authorized := range authorizedKeys {
				if subtle.ConstantTimeCompare(candidate.Marshal(), authorized.Marshal()) == 1 {
					return &ssh.Permissions{
						Extensions: map[string]string{
							"ligolo-key-fingerprint": ssh.FingerprintSHA256(candidate),
						},
					}, nil
				}
			}
			return nil, fmt.Errorf("SSH public key %s is not authorized", ssh.FingerprintSHA256(candidate))
		}
	}
	config.AddHostKey(hostSigner)
	return config, nil
}

// Serve accepts SSH handshakes from listener and emits one Ligolo channel per
// authenticated SSH connection. It returns only when the listener fails.
func Serve(listener net.Listener, config *ssh.ServerConfig, incoming chan<- Conn, handshakeTimeout time.Duration) error {
	for {
		rawConn, err := listener.Accept()
		if err != nil {
			return err
		}
		go serveConnection(rawConn, config, incoming, handshakeTimeout)
	}
}

func serveConnection(rawConn net.Conn, config *ssh.ServerConfig, incoming chan<- Conn, handshakeTimeout time.Duration) {
	if handshakeTimeout > 0 {
		_ = rawConn.SetDeadline(time.Now().Add(handshakeTimeout))
	}
	serverConn, channels, requests, err := ssh.NewServerConn(rawConn, config)
	if err != nil {
		_ = rawConn.Close()
		return
	}

	go handleGlobalRequests(requests)

	accepted := false
	for newChannel := range channels {
		if newChannel.ChannelType() != ChannelType {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only the ligolo channel is supported")
			continue
		}
		if accepted {
			_ = newChannel.Reject(ssh.ResourceShortage, "one ligolo channel is allowed per SSH connection")
			continue
		}

		channel, channelRequests, acceptErr := newChannel.Accept()
		if acceptErr != nil {
			_ = serverConn.Close()
			return
		}
		_ = rawConn.SetDeadline(time.Time{})
		accepted = true
		go ssh.DiscardRequests(channelRequests)

		incoming <- &ChannelConn{
			Channel:    channel,
			localAddr:  serverConn.LocalAddr(),
			remoteAddr: serverConn.RemoteAddr(),
			closeFn:    serverConn.Close,
		}
	}
	if !accepted {
		_ = serverConn.Close()
	}
}

func handleGlobalRequests(requests <-chan *ssh.Request) {
	for request := range requests {
		ok := request.Type == "keepalive@openssh.com" || request.Type == "keepalive@ligolo"
		if request.WantReply {
			_ = request.Reply(ok, nil)
		}
	}
}

// Dial creates a verified, public-key-authenticated SSH connection and opens
// the dedicated Ligolo channel used as yamux's underlying stream.
func Dial(ctx context.Context, options ClientOptions) (Conn, error) {
	if strings.TrimSpace(options.User) == "" {
		return nil, errors.New("SSH user cannot be empty")
	}
	var authMethods []ssh.AuthMethod
	if options.InsecureNoAuth {
		if strings.TrimSpace(options.PrivateKeyPath) != "" {
			return nil, errors.New("SSH private key cannot be combined with insecure no-auth mode")
		}
	} else {
		if strings.TrimSpace(options.PrivateKeyPath) == "" {
			return nil, errors.New("SSH private key path is required unless insecure no-auth mode is enabled")
		}
		signer, err := LoadSigner(options.PrivateKeyPath, options.PrivateKeyPassphrase)
		if err != nil {
			return nil, err
		}
		authMethods = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	hostKeyCallback, err := hostKeyCallback(options)
	if err != nil {
		return nil, err
	}

	clientConfig := &ssh.ClientConfig{
		User:            options.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		ClientVersion:   clientVersion,
		Timeout:         options.ConnectTimeout,
	}

	dialer := &net.Dialer{Timeout: options.ConnectTimeout, KeepAlive: options.KeepAliveInterval}
	rawConn, err := dialer.DialContext(ctx, "tcp", options.Address)
	if err != nil {
		return nil, fmt.Errorf("dial SSH proxy %s: %w", options.Address, err)
	}
	if options.ConnectTimeout > 0 {
		_ = rawConn.SetDeadline(time.Now().Add(options.ConnectTimeout))
	}

	clientConn, channels, requests, err := ssh.NewClientConn(rawConn, options.Address, clientConfig)
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("SSH handshake with %s: %w", options.Address, err)
	}
	_ = rawConn.SetDeadline(time.Time{})
	client := ssh.NewClient(clientConn, channels, requests)

	channel, channelRequests, err := client.OpenChannel(ChannelType, nil)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("open SSH %q channel: %w", ChannelType, err)
	}
	go ssh.DiscardRequests(channelRequests)

	stopKeepAlive := make(chan struct{})
	if options.KeepAliveInterval > 0 {
		go keepAlive(client, options.KeepAliveInterval, stopKeepAlive)
	}

	var stopOnce sync.Once
	return &ChannelConn{
		Channel:    channel,
		localAddr:  rawConn.LocalAddr(),
		remoteAddr: rawConn.RemoteAddr(),
		closeFn: func() error {
			stopOnce.Do(func() { close(stopKeepAlive) })
			return client.Close()
		},
	}, nil
}

func keepAlive(client *ssh.Client, interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_, _, err := client.SendRequest("keepalive@ligolo", true, nil)
			if err != nil {
				_ = client.Close()
				return
			}
		case <-stop:
			return
		}
	}
}

func hostKeyCallback(options ClientOptions) (ssh.HostKeyCallback, error) {
	if options.InsecureIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	var callbacks []ssh.HostKeyCallback
	if options.KnownHostsPath != "" {
		callback, err := knownhosts.New(options.KnownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("load SSH known_hosts %q: %w", options.KnownHostsPath, err)
		}
		callbacks = append(callbacks, callback)
	}
	if options.HostKeyFingerprint != "" {
		expected := strings.TrimSpace(options.HostKeyFingerprint)
		if !strings.HasPrefix(expected, "SHA256:") {
			return nil, errors.New("SSH host-key fingerprint must use SHA256:<base64> format")
		}
		callbacks = append(callbacks, func(_ string, _ net.Addr, key ssh.PublicKey) error {
			actual := ssh.FingerprintSHA256(key)
			if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
				return fmt.Errorf("SSH host-key fingerprint mismatch: got %s, want %s", actual, expected)
			}
			return nil
		})
	}
	if len(callbacks) == 0 {
		return nil, ErrNoHostVerification
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		for _, callback := range callbacks {
			if err := callback(hostname, remote, key); err != nil {
				return err
			}
		}
		return nil
	}, nil
}
