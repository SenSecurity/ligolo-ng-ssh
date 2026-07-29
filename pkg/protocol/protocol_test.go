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

package protocol

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	var buffer bytes.Buffer

	baseEnvelope := InfoReplyPacket{Name: "hello"}
	enc := NewEncoder(&buffer)
	if err := enc.Encode(baseEnvelope); err != nil {
		t.Fatal(err)
	}

	fmt.Printf("Envelope created: %+v\n", buffer)

	dec := NewDecoder(&buffer)
	if err := dec.Decode(); err != nil {
		if err != io.EOF {
			t.Fatal(err)
		}
	}

	fmt.Printf("Envelope: %+v\n", dec.Payload)

	if dec.Payload.(*InfoReplyPacket).Name != "hello" {
		t.Fatal("invalid packet decoded")
	}

}

func TestConnectPacketsPreserveFramedUDPNegotiation(t *testing.T) {
	tests := []struct {
		name   string
		packet interface{}
		framed func(interface{}) bool
	}{
		{
			name:   "request",
			packet: ConnectRequestPacket{Transport: TransportUDP, FramedUDP: true},
			framed: func(payload interface{}) bool {
				return payload.(*ConnectRequestPacket).FramedUDP
			},
		},
		{
			name:   "response",
			packet: ConnectResponsePacket{Established: true, FramedUDP: true},
			framed: func(payload interface{}) bool {
				return payload.(*ConnectResponsePacket).FramedUDP
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buffer bytes.Buffer
			encoder := NewEncoder(&buffer)
			if err := encoder.Encode(tt.packet); err != nil {
				t.Fatalf("Encode: %v", err)
			}

			decoder := NewDecoder(&buffer)
			if err := decoder.Decode(); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !tt.framed(decoder.Payload) {
				t.Fatal("FramedUDP negotiation flag was lost")
			}
		})
	}
}

func TestWireFieldNamesAreExplicitAndBackwardCompatible(t *testing.T) {
	packetTypes := []interface{}{
		Envelope{},
		InfoReplyPacket{},
		ListenerSockRequestPacket{},
		ListenerSockResponsePacket{},
		ListenerSocketConnectionReady{},
		ListenerRequestPacket{},
		ListenerResponsePacket{},
		ListenerBindPacket{},
		ListenerBindReponse{},
		ListenerCloseRequestPacket{},
		ListenerCloseResponsePacket{},
		NetInterface{},
		ConnectRequestPacket{},
		ConnectResponsePacket{},
		HostPingRequestPacket{},
		HostPingResponsePacket{},
	}

	for _, packet := range packetTypes {
		packetType := reflect.TypeOf(packet)
		for i := 0; i < packetType.NumField(); i++ {
			field := packetType.Field(i)
			if field.PkgPath != "" {
				continue
			}
			if got := field.Tag.Get("msgpack"); got != field.Name {
				t.Errorf("%s.%s msgpack tag = %q, want legacy wire name %q", packetType.Name(), field.Name, got, field.Name)
			}
		}
	}
}

func BenchmarkEncodeDecode(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var buffer bytes.Buffer
		baseEnvelope := InfoReplyPacket{Name: "hello"}
		enc := NewEncoder(&buffer)
		if err := enc.Encode(baseEnvelope); err != nil {
			b.Fatal(err)
		}

		dec := NewDecoder(&buffer)
		if err := dec.Decode(); err != nil {
			if err != io.EOF {
				b.Fatal(err)
			}
		}

		if dec.Payload.(*InfoReplyPacket).Name != "hello" {
			b.Fatal("invalid packet decoded")
		}
	}
}
