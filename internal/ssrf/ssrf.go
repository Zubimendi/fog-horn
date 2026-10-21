// Package ssrf classifies IP addresses as ALLOWED or BLOCKED.
// The guard must run at dial time via net.Dialer.Control — never on the hostname.
package ssrf

import (
	"encoding/binary"
	"fmt"
	"net"
)

// Class is the result of classifying an address.
type Class int

const (
	// Allowed means the address may be dialed.
	Allowed Class = iota
	// Blocked means the address is private, reserved, or dangerous.
	Blocked
)

func (c Class) String() string {
	if c == Allowed {
		return "ALLOWED"
	}
	return "BLOCKED"
}

// blockedCIDRs lists every range that must never be dialed by a shared probe.
var blockedCIDRs []*net.IPNet

func init() {
	blocked := []string{
		// Loopback
		"127.0.0.0/8",
		"::1/128",
		// Unspecified
		"0.0.0.0/8",
		"::/128",
		// Link-local (includes cloud metadata 169.254.169.254)
		"169.254.0.0/16",
		"fe80::/10",
		// RFC 1918 private
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		// CGNAT — NOT covered by net.IP.IsPrivate
		"100.64.0.0/10",
		// IPv6 ULA
		"fc00::/7",
		// Multicast
		"224.0.0.0/4",
		"ff00::/8",
		// Documentation / benchmarking
		"192.0.2.0/24",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"2001:db8::/32",
		// Shared address space
		"192.0.0.0/24",
	}
	for _, cidr := range blocked {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("ssrf: bad CIDR %q: %v", cidr, err))
		}
		blockedCIDRs = append(blockedCIDRs, n)
	}
}

// Classify returns Blocked if ip is in any prohibited range, Allowed otherwise.
// It normalises IPv4-mapped IPv6 addresses to plain IPv4 before testing.
func Classify(ip net.IP) Class {
	ip = normalise(ip)
	if ip == nil {
		return Blocked
	}
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return Blocked
		}
	}
	return Allowed
}

// ClassifyAddr parses a "host:port" string (as received by net.Dialer.Control)
// and classifies the host portion. Returns Blocked on any parse error.
func ClassifyAddr(network, addr string) Class {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return Blocked
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return Blocked
	}
	return Classify(ip)
}

// normalise converts IPv4-mapped IPv6 (::ffff:a.b.c.d) to plain IPv4.
// This is required because Go's net package represents IPv4 addresses
// as 16-byte slices internally, and IPv4-mapped ::ffff:127.0.0.1 would
// otherwise bypass a loopback check against the 127.0.0.0/8 CIDR.
func normalise(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	// net.IP.To4 returns a 4-byte slice for both plain IPv4 and IPv4-mapped IPv6.
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return ip
}

// ParseDecimalIP converts alternate decimal encodings of IPv4 addresses
// (e.g. 2130706433 for 127.0.0.1) to a net.IP. Returns nil on failure.
func ParseDecimalIP(s string) net.IP {
	var n uint64
	// Only handle purely numeric strings of plausible IPv4 decimal length
	if len(s) == 0 || len(s) > 10 {
		return nil
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil
		}
		n = n*10 + uint64(c-'0')
	}
	if n > 0xFFFFFFFF {
		return nil
	}
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(n))
	return net.IP(b)
}
