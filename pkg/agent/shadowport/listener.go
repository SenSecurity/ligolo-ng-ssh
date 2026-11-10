package shadowport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/nicocha30/ligolo-ng/pkg/protocol"
)

var (
	allowed atomic.Bool
	scopes  = struct {
		sync.Mutex
		active map[*Listener]listenerScope
	}{active: make(map[*Listener]listenerScope)}
)

type listenerScope struct {
	publicAddress netip.Addr
	publicPort    uint16
	sources       []netip.Prefix
}

type Listener struct {
	net.Listener
	divert   packetDivert
	firewall packetDivert
	once     sync.Once
}

type packetDivert interface {
	Close() error
}

func SetAllowed(value bool) {
	allowed.Store(value)
}

func Enabled() bool {
	return supported() && allowed.Load()
}

func NewListener(publicEndpoint string, config *protocol.ShadowPortConfig) (*Listener, error) {
	if !supported() {
		return nil, errors.New("ShadowPort is unsupported by this agent build")
	}
	if !allowed.Load() {
		return nil, errors.New("ShadowPort is disabled; start agent-shadow with -allow-shadow-port")
	}
	if config == nil {
		return nil, errors.New("ShadowPort configuration is missing")
	}
	host, portText, err := net.SplitHostPort(publicEndpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid ShadowPort public endpoint: %w", err)
	}
	publicAddress, err := netip.ParseAddr(host)
	if err != nil || !publicAddress.Is4() || publicAddress.IsUnspecified() {
		return nil, errors.New("ShadowPort public endpoint must use a specific IPv4 address")
	}
	if !isLocalAddress(publicAddress) {
		return nil, fmt.Errorf("ShadowPort public address %s is not assigned to this agent", publicAddress)
	}
	publicPort, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || publicPort == 0 {
		return nil, errors.New("ShadowPort public port must be between 1 and 65535")
	}
	sourcePrefixes, err := parseSources(config.AllowedSources)
	if err != nil {
		return nil, err
	}
	translator, err := NewTranslator(TranslatorConfig{
		PublicAddress:  publicAddress,
		PublicPort:     uint16(publicPort),
		InternalPort:   config.InternalPort,
		AllowedSources: sourcePrefixes,
	})
	if err != nil {
		return nil, err
	}

	listener := &Listener{}
	scope := listenerScope{publicAddress, uint16(publicPort), sourcePrefixes}
	if err := reserveScope(listener, scope); err != nil {
		return nil, err
	}
	firewall, err := newFirewallPermit(scope, config.InternalPort)
	if err != nil {
		releaseScope(listener)
		return nil, err
	}
	internalEndpoint := net.JoinHostPort(publicAddress.String(), strconv.Itoa(int(config.InternalPort)))
	baseListener, err := net.Listen("tcp4", internalEndpoint)
	if err != nil {
		firewall.Close()
		releaseScope(listener)
		return nil, fmt.Errorf("bind ShadowPort internal listener %s: %w", internalEndpoint, err)
	}
	listener.Listener = baseListener
	divert, err := newPacketDivert(scope, config.InternalPort, translator)
	if err != nil {
		firewall.Close()
		releaseScope(listener)
		baseListener.Close()
		return nil, err
	}
	listener.firewall = firewall
	listener.divert = divert
	return listener, nil
}

func (l *Listener) Close() error {
	var closeErr error
	l.once.Do(func() {
		if l.divert != nil {
			closeErr = l.divert.Close()
		}
		if l.firewall != nil {
			if err := l.firewall.Close(); closeErr == nil {
				closeErr = err
			}
		}
		if err := l.Listener.Close(); closeErr == nil {
			closeErr = err
		}
		releaseScope(l)
	})
	return closeErr
}

func parseSources(values []string) ([]netip.Prefix, error) {
	if len(values) == 0 || len(values) > 64 {
		return nil, errors.New("ShadowPort requires between one and 64 allowed sources")
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil || !address.Is4() {
				return nil, fmt.Errorf("invalid ShadowPort IPv4 source %q", value)
			}
			prefix = netip.PrefixFrom(address, 32)
		}
		if !prefix.Addr().Is4() {
			return nil, fmt.Errorf("ShadowPort source %q is not IPv4", value)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func sourceFilterExpression(prefix netip.Prefix) string {
	prefix = prefix.Masked()
	if prefix.Bits() == 32 {
		return "ip.SrcAddr == " + prefix.Addr().String()
	}
	address := prefix.Addr().As4()
	first := binary.BigEndian.Uint32(address[:])
	var mask uint32
	if prefix.Bits() > 0 {
		mask = ^uint32(0) << (32 - prefix.Bits())
	}
	lastBytes := [4]byte{}
	binary.BigEndian.PutUint32(lastBytes[:], first|^mask)
	last := netip.AddrFrom4(lastBytes)
	return fmt.Sprintf("(ip.SrcAddr >= %s and ip.SrcAddr <= %s)", prefix.Addr(), last)
}

func reserveScope(listener *Listener, candidate listenerScope) error {
	scopes.Lock()
	defer scopes.Unlock()
	for _, active := range scopes.active {
		if active.publicAddress != candidate.publicAddress || active.publicPort != candidate.publicPort {
			continue
		}
		for _, left := range active.sources {
			for _, right := range candidate.sources {
				if left.Contains(right.Addr()) || right.Contains(left.Addr()) {
					return errors.New("ShadowPort scope overlaps an active listener")
				}
			}
		}
	}
	scopes.active[listener] = candidate
	return nil
}

func releaseScope(listener *Listener) {
	scopes.Lock()
	delete(scopes.active, listener)
	scopes.Unlock()
}

func isLocalAddress(target netip.Addr) bool {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.String())
		if err == nil && prefix.Addr().Unmap() == target {
			return true
		}
	}
	return false
}
