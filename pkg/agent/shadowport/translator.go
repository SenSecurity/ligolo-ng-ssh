package shadowport

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"
	"time"
)

const (
	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpACK = 0x10
)

type Decision uint8

const (
	Pass Decision = iota
	Rewritten
	CapacityExceeded
)

type TranslatorConfig struct {
	PublicAddress   netip.Addr
	PublicPort      uint16
	InternalPort    uint16
	AllowedSources  []netip.Prefix
	MaxFlows        int
	HalfOpenTimeout time.Duration
	IdleTimeout     time.Duration
	ClosingTimeout  time.Duration
}

type flowKey struct {
	remoteAddress netip.Addr
	remotePort    uint16
	localAddress  netip.Addr
}

type flowState struct {
	lastSeen time.Time
	halfOpen bool
	closing  bool
}

type Translator struct {
	config TranslatorConfig
	mu     sync.Mutex
	flows  map[flowKey]flowState
}

func NewTranslator(config TranslatorConfig) (*Translator, error) {
	if !config.PublicAddress.Is4() || config.PublicAddress.IsUnspecified() {
		return nil, errors.New("ShadowPort public address must be a specific IPv4 address")
	}
	if config.PublicPort == 0 || config.InternalPort == 0 {
		return nil, errors.New("ShadowPort ports must be non-zero")
	}
	if config.PublicPort == config.InternalPort {
		return nil, errors.New("ShadowPort public and internal ports must differ")
	}
	if len(config.AllowedSources) == 0 || len(config.AllowedSources) > 64 {
		return nil, errors.New("ShadowPort requires between one and 64 allowed source ranges")
	}
	for _, prefix := range config.AllowedSources {
		if !prefix.Addr().Is4() {
			return nil, errors.New("ShadowPort source ranges must be IPv4")
		}
	}
	if config.MaxFlows <= 0 {
		config.MaxFlows = 4096
	}
	if config.HalfOpenTimeout <= 0 {
		config.HalfOpenTimeout = 30 * time.Second
	}
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = 5 * time.Minute
	}
	if config.ClosingTimeout <= 0 {
		config.ClosingTimeout = 30 * time.Second
	}
	return &Translator{config: config, flows: make(map[flowKey]flowState)}, nil
}

func (t *Translator) Translate(packet []byte, outbound bool, now time.Time) Decision {
	parsed, ok := parseIPv4TCP(packet)
	if !ok {
		return Pass
	}
	if outbound {
		return t.translateOutbound(packet, parsed, now)
	}
	return t.translateInbound(packet, parsed, now)
}

func (t *Translator) translateInbound(packet []byte, parsed ipv4TCPPacket, now time.Time) Decision {
	if parsed.destinationAddress != t.config.PublicAddress ||
		parsed.destinationPort != t.config.PublicPort ||
		!t.sourceAllowed(parsed.sourceAddress) {
		return Pass
	}
	key := flowKey{parsed.sourceAddress, parsed.sourcePort, parsed.destinationAddress}

	t.mu.Lock()
	defer t.mu.Unlock()
	state, exists := t.flows[key]
	if exists && parsed.flags&tcpSYN != 0 && parsed.flags&tcpACK == 0 {
		state = flowState{halfOpen: true}
	}
	if !exists {
		if parsed.flags&tcpSYN == 0 || parsed.flags&tcpACK != 0 {
			return Pass
		}
		if len(t.flows) >= t.config.MaxFlows {
			return CapacityExceeded
		}
		state = flowState{halfOpen: true}
	}
	state.lastSeen = now
	if parsed.flags&tcpACK != 0 {
		state.halfOpen = false
	}
	if parsed.flags&tcpFIN != 0 {
		state.closing = true
	}
	t.flows[key] = state
	binary.BigEndian.PutUint16(packet[parsed.tcpOffset+2:parsed.tcpOffset+4], t.config.InternalPort)
	if parsed.flags&tcpRST != 0 {
		delete(t.flows, key)
	}
	return Rewritten
}

func (t *Translator) translateOutbound(packet []byte, parsed ipv4TCPPacket, now time.Time) Decision {
	if parsed.sourceAddress != t.config.PublicAddress || parsed.sourcePort != t.config.InternalPort {
		return Pass
	}
	key := flowKey{parsed.destinationAddress, parsed.destinationPort, parsed.sourceAddress}
	t.mu.Lock()
	defer t.mu.Unlock()
	state, exists := t.flows[key]
	if !exists {
		return Pass
	}
	state.lastSeen = now
	state.halfOpen = false
	if parsed.flags&tcpFIN != 0 {
		state.closing = true
	}
	t.flows[key] = state
	binary.BigEndian.PutUint16(packet[parsed.tcpOffset:parsed.tcpOffset+2], t.config.PublicPort)
	if parsed.flags&tcpRST != 0 {
		delete(t.flows, key)
	}
	return Rewritten
}

func (t *Translator) Expire(now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	removed := 0
	for key, state := range t.flows {
		timeout := t.config.IdleTimeout
		if state.halfOpen {
			timeout = t.config.HalfOpenTimeout
		} else if state.closing {
			timeout = t.config.ClosingTimeout
		}
		if now.Sub(state.lastSeen) > timeout {
			delete(t.flows, key)
			removed++
		}
	}
	return removed
}

func (t *Translator) sourceAllowed(address netip.Addr) bool {
	for _, prefix := range t.config.AllowedSources {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

type ipv4TCPPacket struct {
	sourceAddress      netip.Addr
	destinationAddress netip.Addr
	sourcePort         uint16
	destinationPort    uint16
	flags              byte
	tcpOffset          int
}

func parseIPv4TCP(packet []byte) (ipv4TCPPacket, bool) {
	if len(packet) < 40 || packet[0]>>4 != 4 || packet[9] != 6 {
		return ipv4TCPPacket{}, false
	}
	ipHeaderLength := int(packet[0]&0x0f) * 4
	if ipHeaderLength < 20 || len(packet) < ipHeaderLength+20 {
		return ipv4TCPPacket{}, false
	}
	totalLength := int(binary.BigEndian.Uint16(packet[2:4]))
	if totalLength < ipHeaderLength+20 || totalLength > len(packet) {
		return ipv4TCPPacket{}, false
	}
	sourceAddress, ok := netip.AddrFromSlice(packet[12:16])
	if !ok {
		return ipv4TCPPacket{}, false
	}
	destinationAddress, ok := netip.AddrFromSlice(packet[16:20])
	if !ok {
		return ipv4TCPPacket{}, false
	}
	return ipv4TCPPacket{
		sourceAddress:      sourceAddress.Unmap(),
		destinationAddress: destinationAddress.Unmap(),
		sourcePort:         binary.BigEndian.Uint16(packet[ipHeaderLength : ipHeaderLength+2]),
		destinationPort:    binary.BigEndian.Uint16(packet[ipHeaderLength+2 : ipHeaderLength+4]),
		flags:              packet[ipHeaderLength+13],
		tcpOffset:          ipHeaderLength,
	}, true
}
