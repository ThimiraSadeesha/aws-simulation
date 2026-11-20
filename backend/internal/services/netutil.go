package services

import (
	"net/netip"

	"cloudsim/backend/internal/apierr"
)

// ParseCIDR parses and canonicalizes an IPv4 CIDR (host bits must be zero).
func ParseCIDR(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return netip.Prefix{}, apierr.Validation("invalid IPv4 CIDR block %q", s)
	}
	if p.Masked() != p {
		return netip.Prefix{}, apierr.Validation("CIDR %q has host bits set; did you mean %s?", s, p.Masked())
	}
	return p, nil
}

// Contains reports whether outer fully contains inner.
func Contains(outer, inner netip.Prefix) bool {
	return outer.Bits() <= inner.Bits() && outer.Contains(inner.Addr())
}

func Overlaps(a, b netip.Prefix) bool { return a.Overlaps(b) }

func prefixSize(p netip.Prefix) int { return 1 << (32 - p.Bits()) }

func addrToInt(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func intToAddr(n uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

// AllocateIP returns the lowest free host address in the subnet. Like AWS, the first four
// addresses (network, router, DNS, reserved) and the broadcast address are reserved.
func AllocateIP(subnet netip.Prefix, used []string) (string, error) {
	taken := make(map[string]bool, len(used))
	for _, u := range used {
		taken[u] = true
	}
	base := addrToInt(subnet.Addr())
	size := uint32(prefixSize(subnet))
	for off := uint32(4); off < size-1; off++ {
		a := intToAddr(base + off).String()
		if !taken[a] {
			return a, nil
		}
	}
	return "", apierr.New(409, "InsufficientFreeAddressesInSubnet", "subnet %s has no free IP addresses", subnet)
}

// UsableIPs is the number of assignable addresses in a subnet (size - 5 reserved).
func UsableIPs(p netip.Prefix) int {
	n := prefixSize(p) - 5
	if n < 0 {
		return 0
	}
	return n
}

// PublicIP maps a sequence number into 198.18.0.0/15 (RFC 2544 benchmarking range), so simulated
// "public" addresses are unique per platform and can never collide with real internet hosts.
func PublicIP(seq uint64) string {
	base := addrToInt(netip.MustParseAddr("198.18.0.0"))
	return intToAddr(base + uint32(1+seq%131070)).String()
}
