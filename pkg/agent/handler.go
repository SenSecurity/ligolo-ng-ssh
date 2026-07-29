// Ligolo-ng
// Copyright (C) 2025 Nicolas Chatelain (nicocha30)

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/nicocha30/ligolo-ng/pkg/agent/neterror"
	"github.com/nicocha30/ligolo-ng/pkg/agent/shadowport"
	"github.com/nicocha30/ligolo-ng/pkg/agent/smartping"
	"github.com/nicocha30/ligolo-ng/pkg/protocol"
	"github.com/nicocha30/ligolo-ng/pkg/relay"
	"github.com/sirupsen/logrus"
)

var listenerConntrack map[int32]net.Conn
var listenerMap map[int32]io.Closer
var connTrackID int32
var listenerID int32
var sessionID string
var listenerMutex sync.Mutex

func init() {
	listenerConntrack = make(map[int32]net.Conn)
	listenerMap = make(map[int32]io.Closer)
	sessionID = hex.EncodeToString(uuid.NodeID())
}

// Listener is the base class implementing listener sockets for Ligolo
type Listener struct {
	net.Listener
}

// NewListener register a new listener
func NewListener(network string, addr string) (Listener, error) {
	lis, err := net.Listen(network, addr)
	if err != nil {
		return Listener{}, err
	}
	return Listener{lis}, nil
}

func newTCPListener(request *protocol.ListenerRequestPacket) (net.Listener, error) {
	if request.ShadowPort != nil {
		return shadowport.NewListener(request.Address, request.ShadowPort)
	}
	listener, err := NewListener(request.Network, request.Address)
	if err != nil {
		return nil, err
	}
	return listener.Listener, nil
}

// ListenAndServe fill new listener connections to a channel
func (s *Listener) ListenAndServe(connTrackChan chan int32) error {
	for {
		conn, err := s.Accept()
		if err != nil {
			return err
		}
		connTrackID++
		connTrackChan <- connTrackID
		listenerConntrack[connTrackID] = conn
	}
}

// Close request the main listener to exit
func (s *Listener) Close() error {
	return s.Listener.Close()
}

// UDPListener is the base class implementing UDP listeners for Ligolo
type UDPListener struct {
	*net.UDPConn
}

// NewUDPListener register a new UDP listener
func NewUDPListener(network string, addr string) (UDPListener, error) {
	udpaddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return UDPListener{}, nil
	}

	udplis, err := net.ListenUDP(network, udpaddr)
	if err != nil {
		return UDPListener{}, err
	}
	return UDPListener{udplis}, err
}

func HandleConn(conn net.Conn) {
	decoder := protocol.NewDecoder(conn)
	if err := decoder.Decode(); err != nil {
		logrus.Error(err)
		return
	}

	e := decoder
	switch decoder.Payload.(type) {

	case *protocol.ConnectRequestPacket:
		connRequest := e.Payload.(*protocol.ConnectRequestPacket)
		encoder := protocol.NewEncoder(conn)

		logrus.Debugf("Got connect request to %s:%d", connRequest.Address, connRequest.Port)
		var network string
		if connRequest.Transport == protocol.TransportTCP {
			network = "tcp"
		} else {
			network = "udp"
		}
		if connRequest.Net == protocol.Networkv4 {
			network += "4"
		} else {
			network += "6"
		}

		var d net.Dialer
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		targetConn, err := d.DialContext(ctx, network, fmt.Sprintf("%s:%d", connRequest.Address, connRequest.Port))
		defer cancel()

		var connectPacket protocol.ConnectResponsePacket
		if err != nil {

			var serr syscall.Errno
			if errors.As(err, &serr) {
				// Magic trick ! If the error syscall indicate that the system responded, send back a RST packet!
				if neterror.HostResponded(serr) {
					connectPacket.Reset = true
				}
			}

			connectPacket.Established = false
		} else {
			connectPacket.Established = true
			connectPacket.FramedUDP = connRequest.Transport == protocol.TransportUDP && connRequest.FramedUDP
		}
		if err := encoder.Encode(connectPacket); err != nil {
			logrus.Error(err)
			return
		}
		if connectPacket.Established {
			if connectPacket.FramedUDP {
				relay.StartFramedPacketRelay(conn, targetConn, func(err error) relay.PacketRelayError {
					if neterror.ConnectionRefused(err) {
						return relay.PacketRelayPortUnreachable
					}
					return relay.PacketRelayNoError
				}, nil)
			} else {
				relay.StartRelay(targetConn, conn)
			}
		}
	case *protocol.HostPingRequestPacket:
		pingRequest := e.Payload.(*protocol.HostPingRequestPacket)
		encoder := protocol.NewEncoder(conn)

		pingResponse := protocol.HostPingResponsePacket{Alive: smartping.TryResolve(pingRequest.Address)}

		if err := encoder.Encode(pingResponse); err != nil {
			logrus.Error(err)
			return
		}
	case *protocol.InfoRequestPacket:
		var username string
		encoder := protocol.NewEncoder(conn)
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "UNKNOWN"
		}

		userinfo, err := user.Current()
		if err != nil {
			username = "Unknown"
		} else {
			username = userinfo.Username
		}

		netifaces, err := net.Interfaces()
		if err != nil {
			logrus.Error("could not get network interfaces")
			return
		}
		infoResponse := protocol.InfoReplyPacket{
			Name:       fmt.Sprintf("%s@%s", username, hostname),
			Interfaces: protocol.NewNetInterfaces(netifaces),
			SessionID:  sessionID,
		}
		if shadowport.Enabled() {
			infoResponse.Capabilities = []string{protocol.CapabilityShadowPort}
		}

		if err := encoder.Encode(infoResponse); err != nil {
			logrus.Error(err)
			return
		}
	case *protocol.ListenerCloseRequestPacket:
		// Request to close a listener
		closeRequest := e.Payload.(*protocol.ListenerCloseRequestPacket)
		encoder := protocol.NewEncoder(conn)

		var err error
		listenerMutex.Lock()
		lis, ok := listenerMap[closeRequest.ListenerID]
		if ok {
			delete(listenerMap, closeRequest.ListenerID)
		}
		listenerMutex.Unlock()
		if ok {
			lis.Close()
		} else {
			err = errors.New("invalid listener id")
		}

		listenerResponse := protocol.ListenerCloseResponsePacket{
			Err: err != nil,
		}
		if err != nil {
			listenerResponse.ErrString = err.Error()
		}

		if err := encoder.Encode(listenerResponse); err != nil {
			logrus.Error(err)
		}

	case *protocol.ListenerRequestPacket:
		listenRequest := e.Payload.(*protocol.ListenerRequestPacket)
		encoder := protocol.NewEncoder(conn)
		connTrackChan := make(chan int32)
		stopChan := make(chan error, 1)

		if listenRequest.Network == "tcp" {
			listener, err := newTCPListener(listenRequest)
			if err != nil {
				listenerResponse := protocol.ListenerResponsePacket{
					ListenerID: 0,
					Err:        true,
					ErrString:  err.Error(),
				}
				if err := encoder.Encode(listenerResponse); err != nil {
					logrus.Error(err)
				}
				return
			}
			listenerMutex.Lock()
			currentListenerID := listenerID
			listenerID++
			listenerMap[currentListenerID] = listener
			listenerMutex.Unlock()
			listenerResponse := protocol.ListenerResponsePacket{
				ListenerID: currentListenerID,
				Err:        false,
				ErrString:  "",
			}
			if err := encoder.Encode(listenerResponse); err != nil {
				logrus.Error(err)
			}
			go func() {
				for {
					accepted, err := listener.Accept()
					if err != nil {
						stopChan <- err
						return
					}
					listenerMutex.Lock()
					connTrackID++
					currentConnTrackID := connTrackID
					listenerConntrack[currentConnTrackID] = accepted
					listenerMutex.Unlock()
					connTrackChan <- currentConnTrackID
				}
			}()
			defer func() {
				listenerMutex.Lock()
				delete(listenerMap, currentListenerID)
				listenerMutex.Unlock()
				listener.Close()
			}()

		} else if listenRequest.Network == "udp" {
			udplistener, err := NewUDPListener(listenRequest.Network, listenRequest.Address)
			if err != nil {
				listenerResponse := protocol.ListenerResponsePacket{
					ListenerID: 0,
					Err:        true,
					ErrString:  err.Error(),
				}
				if err := encoder.Encode(listenerResponse); err != nil {
					logrus.Error(err)
				}
				return
			}
			listenerMutex.Lock()
			currentListenerID := listenerID
			listenerID++
			listenerMap[currentListenerID] = udplistener.UDPConn
			listenerMutex.Unlock()
			listenerResponse := protocol.ListenerResponsePacket{
				ListenerID: currentListenerID,
				Err:        false,
				ErrString:  "",
			}
			if err := encoder.Encode(listenerResponse); err != nil {
				logrus.Error(err)
			}
			go func() {
				err := relay.StartUDPListenerRelay(conn, udplistener.UDPConn)
				if err != nil {
					logrus.Error(err)
				}
			}()
		}

		if listenRequest.Network == "tcp" {
			for {
				var bindResponse protocol.ListenerBindReponse
				select {
				case err := <-stopChan:
					logrus.Error(err)
					bindResponse = protocol.ListenerBindReponse{
						SockID:    0,
						Err:       true,
						ErrString: err.Error(),
					}
				case connTrackID := <-connTrackChan:
					bindResponse = protocol.ListenerBindReponse{
						SockID: connTrackID,
						Err:    false,
					}
				}
				if err := encoder.Encode(bindResponse); err != nil {
					logrus.Error(err)
					return
				}

				if bindResponse.Err {
					break
				}

			}
		}
	case *protocol.ListenerSockRequestPacket:
		sockRequest := e.Payload.(*protocol.ListenerSockRequestPacket)
		socketEncDec := protocol.NewEncoderDecoder(conn)

		var sockResponse protocol.ListenerSockResponsePacket
		listenerMutex.Lock()
		netConn, ok := listenerConntrack[sockRequest.SockID]
		listenerMutex.Unlock()
		if !ok {
			// Handle error
			sockResponse.ErrString = "invalid or unexistant SockID"
			sockResponse.Err = true
		}

		if err := socketEncDec.Encode(sockResponse); err != nil {
			logrus.Error(err)
			return
		}

		if sockResponse.Err {
			return
		}

		if err := socketEncDec.Decode(); err != nil {
			logrus.Error(err)
			return
		}
		if err := socketEncDec.Payload.(*protocol.ListenerSocketConnectionReady).Err; err != false {
			logrus.Debug("Socket relay session failed: error from proxy")
			netConn.Close()
			listenerMutex.Lock()
			delete(listenerConntrack, sockRequest.SockID)
			listenerMutex.Unlock()
			return
		}

		// Start bidirectional relay; when it returns, close and clean up
		if err := relay.StartRelay(netConn, conn); err != nil {
			logrus.Error(err)
		}
		netConn.Close()
		listenerMutex.Lock()
		delete(listenerConntrack, sockRequest.SockID)
		listenerMutex.Unlock()

	case *protocol.AgentKillRequestPacket:
		os.Exit(0)

	}
}
