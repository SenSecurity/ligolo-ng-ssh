package shadowport

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func TestTranslatorRewritesOnlyMappedFlows(t *testing.T) {
	now := time.Unix(1000, 0)
	translator := mustTranslator(t, 8)

	direct := tcpPacket("192.0.2.10", "198.51.100.20", 8445, 50000, tcpACK)
	if got := translator.Translate(direct, true, now); got != Pass {
		t.Fatalf("direct internal decision = %v, want Pass", got)
	}

	syn := tcpPacket("198.51.100.20", "192.0.2.10", 50000, 445, tcpSYN)
	if got := translator.Translate(syn, false, now); got != Rewritten {
		t.Fatalf("inbound SYN decision = %v, want Rewritten", got)
	}
	if port := binary.BigEndian.Uint16(syn[22:24]); port != 8445 {
		t.Fatalf("destination port = %d, want 8445", port)
	}

	reply := tcpPacket("192.0.2.10", "198.51.100.20", 8445, 50000, tcpACK)
	if got := translator.Translate(reply, true, now.Add(time.Second)); got != Rewritten {
		t.Fatalf("mapped reply decision = %v, want Rewritten", got)
	}
	if port := binary.BigEndian.Uint16(reply[20:22]); port != 445 {
		t.Fatalf("source port = %d, want 445", port)
	}
}

func TestTranslatorPassesDisallowedAndUntrackedPackets(t *testing.T) {
	translator := mustTranslator(t, 4)
	now := time.Unix(2000, 0)
	disallowed := tcpPacket("203.0.113.9", "192.0.2.10", 51000, 445, tcpSYN)
	if got := translator.Translate(disallowed, false, now); got != Pass {
		t.Fatalf("disallowed decision = %v, want Pass", got)
	}
	midstream := tcpPacket("198.51.100.20", "192.0.2.10", 51000, 445, tcpACK)
	if got := translator.Translate(midstream, false, now); got != Pass {
		t.Fatalf("midstream decision = %v, want Pass", got)
	}
}

func TestTranslatorBoundsAndExpiresFlows(t *testing.T) {
	translator := mustTranslator(t, 1)
	now := time.Unix(3000, 0)
	first := tcpPacket("198.51.100.20", "192.0.2.10", 52000, 445, tcpSYN)
	if got := translator.Translate(first, false, now); got != Rewritten {
		t.Fatalf("first decision = %v, want Rewritten", got)
	}
	second := tcpPacket("198.51.100.20", "192.0.2.10", 52001, 445, tcpSYN)
	if got := translator.Translate(second, false, now); got != CapacityExceeded {
		t.Fatalf("second decision = %v, want CapacityExceeded", got)
	}
	translator.Expire(now.Add(31 * time.Second))
	if got := translator.Translate(second, false, now.Add(31*time.Second)); got != Rewritten {
		t.Fatalf("post-expiry decision = %v, want Rewritten", got)
	}
}

func TestTranslatorResetsClosingStateWhenTupleIsReused(t *testing.T) {
	translator := mustTranslator(t, 1)
	now := time.Unix(4000, 0)

	syn := tcpPacket("198.51.100.20", "192.0.2.10", 53000, 445, tcpSYN)
	if got := translator.Translate(syn, false, now); got != Rewritten {
		t.Fatalf("initial SYN decision = %v, want Rewritten", got)
	}
	fin := tcpPacket("192.0.2.10", "198.51.100.20", 8445, 53000, tcpFIN|tcpACK)
	if got := translator.Translate(fin, true, now.Add(time.Second)); got != Rewritten {
		t.Fatalf("FIN decision = %v, want Rewritten", got)
	}

	reusedSYN := tcpPacket("198.51.100.20", "192.0.2.10", 53000, 445, tcpSYN)
	if got := translator.Translate(reusedSYN, false, now.Add(2*time.Second)); got != Rewritten {
		t.Fatalf("reused SYN decision = %v, want Rewritten", got)
	}
	synACK := tcpPacket("192.0.2.10", "198.51.100.20", 8445, 53000, tcpSYN|tcpACK)
	if got := translator.Translate(synACK, true, now.Add(3*time.Second)); got != Rewritten {
		t.Fatalf("reused SYN-ACK decision = %v, want Rewritten", got)
	}

	if removed := translator.Expire(now.Add(34 * time.Second)); removed != 0 {
		t.Fatalf("Expire removed %d active reused flows, want 0", removed)
	}
}

func TestTranslatorRejectsIPv6(t *testing.T) {
	_, err := NewTranslator(TranslatorConfig{
		PublicAddress:  netip.MustParseAddr("2001:db8::1"),
		PublicPort:     445,
		InternalPort:   8445,
		AllowedSources: []netip.Prefix{netip.MustParsePrefix("198.51.100.20/32")},
	})
	if err == nil {
		t.Fatal("expected IPv6 rejection")
	}
}

func TestSourceFilterExpressionExpandsCIDR(t *testing.T) {
	tests := map[string]string{
		"198.51.100.20/32": "ip.SrcAddr == 198.51.100.20",
		"198.51.100.0/24":  "(ip.SrcAddr >= 198.51.100.0 and ip.SrcAddr <= 198.51.100.255)",
		"0.0.0.0/0":        "(ip.SrcAddr >= 0.0.0.0 and ip.SrcAddr <= 255.255.255.255)",
	}
	for input, expected := range tests {
		if got := sourceFilterExpression(netip.MustParsePrefix(input)); got != expected {
			t.Errorf("sourceFilterExpression(%s) = %q, want %q", input, got, expected)
		}
	}
}

func mustTranslator(t *testing.T, maxFlows int) *Translator {
	t.Helper()
	translator, err := NewTranslator(TranslatorConfig{
		PublicAddress:   netip.MustParseAddr("192.0.2.10"),
		PublicPort:      445,
		InternalPort:    8445,
		AllowedSources:  []netip.Prefix{netip.MustParsePrefix("198.51.100.20/32")},
		MaxFlows:        maxFlows,
		HalfOpenTimeout: 30 * time.Second,
		IdleTimeout:     5 * time.Minute,
		ClosingTimeout:  30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewTranslator: %v", err)
	}
	return translator
}

func tcpPacket(srcIP, dstIP string, srcPort, dstPort uint16, flags byte) []byte {
	packet := make([]byte, 40)
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	packet[8] = 64
	packet[9] = 6
	copy(packet[12:16], netip.MustParseAddr(srcIP).AsSlice())
	copy(packet[16:20], netip.MustParseAddr(dstIP).AsSlice())
	binary.BigEndian.PutUint16(packet[20:22], srcPort)
	binary.BigEndian.PutUint16(packet[22:24], dstPort)
	packet[32] = 0x50
	packet[33] = flags
	return packet
}
