package shadowport

import (
	"net/netip"
	"testing"
)

func TestParseSourcesNormalizesAddressesAndCIDRs(t *testing.T) {
	prefixes, err := parseSources([]string{"198.51.100.20", "203.0.113.17/24"})
	if err != nil {
		t.Fatal(err)
	}
	if got := prefixes[0].String(); got != "198.51.100.20/32" {
		t.Fatalf("address prefix = %s", got)
	}
	if got := prefixes[1].String(); got != "203.0.113.0/24" {
		t.Fatalf("masked prefix = %s", got)
	}
}

func TestReserveScopeRejectsOverlappingSources(t *testing.T) {
	first := &Listener{}
	second := &Listener{}
	address := netip.MustParseAddr("192.0.2.10")
	if err := reserveScope(first, listenerScope{
		publicAddress: address,
		publicPort:    445,
		sources:       []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
	}); err != nil {
		t.Fatal(err)
	}
	defer releaseScope(first)

	err := reserveScope(second, listenerScope{
		publicAddress: address,
		publicPort:    445,
		sources:       []netip.Prefix{netip.MustParsePrefix("198.51.100.20/32")},
	})
	if err == nil {
		releaseScope(second)
		t.Fatal("expected overlapping scope rejection")
	}
}

func TestReserveScopeAllowsDisjointSources(t *testing.T) {
	first := &Listener{}
	second := &Listener{}
	address := netip.MustParseAddr("192.0.2.10")
	if err := reserveScope(first, listenerScope{
		publicAddress: address,
		publicPort:    445,
		sources:       []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
	}); err != nil {
		t.Fatal(err)
	}
	defer releaseScope(first)

	if err := reserveScope(second, listenerScope{
		publicAddress: address,
		publicPort:    445,
		sources:       []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")},
	}); err != nil {
		t.Fatal(err)
	}
	releaseScope(second)
}
