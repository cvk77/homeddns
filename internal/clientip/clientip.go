// Package clientip parses and validates the IP addresses used as DynDNS
// update targets and determines the address of the requesting client.
package clientip

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// nonPublicPrefixes are ranges that are not covered by the netip.Addr
// helpers used in IsPublic but cannot be a valid DynDNS target either.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT shared address space (RFC 6598)
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1 (RFC 5737)
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2 (RFC 5737)
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3 (RFC 5737)
	netip.MustParsePrefix("2001:db8::/32"),   // IPv6 documentation (RFC 3849)
	netip.MustParsePrefix("3fff::/20"),       // IPv6 documentation (RFC 9637)
}

// ParseAddr parses a single IP address as sent in a myip/myipv6 parameter.
// Surrounding brackets are accepted, zone IDs are rejected and
// IPv4-mapped IPv6 addresses are converted to plain IPv4.
func ParseAddr(value string) (netip.Addr, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = value[1 : len(value)-1]
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid IP address %q: %w", value, err)
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("IP address %q must not contain a zone ID", value)
	}
	return addr.Unmap(), nil
}

// parseHostAddr parses an address that may carry a port, as found in
// RemoteAddr or X-Forwarded-For ("1.2.3.4:80", "[2001:db8::1]:1234").
func parseHostAddr(value string) (netip.Addr, error) {
	value = strings.TrimSpace(value)
	if addrPort, err := netip.ParseAddrPort(value); err == nil {
		return ParseAddr(addrPort.Addr().String())
	}
	return ParseAddr(value)
}

// IsPublic reports whether addr is usable as a public DynDNS target.
// Loopback, unspecified, multicast, link-local, private (RFC 1918, ULA),
// CGNAT and documentation addresses are rejected.
func IsPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsUnspecified() || addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsPrivate() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// ParsePrefixes parses a comma-separated list of CIDR prefixes or plain IP
// addresses (treated as single-host prefixes).
func ParsePrefixes(list string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, entry := range strings.Split(list, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("invalid prefix %q: %w", entry, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := ParseAddr(entry)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// Resolver determines the client address of a request.
type Resolver struct {
	// TrustedProxies lists the peers whose X-Forwarded-For header is honored.
	TrustedProxies []netip.Prefix
}

// FromRequest returns the address of the client that sent the request.
// X-Forwarded-For is only evaluated if the direct peer is a trusted proxy;
// it is then read from right to left, skipping trusted proxies, so a client
// cannot spoof its address by prepending entries.
func (r Resolver) FromRequest(req *http.Request) (netip.Addr, error) {
	remote, err := parseHostAddr(req.RemoteAddr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("parse remote address: %w", err)
	}
	if !r.isTrusted(remote) {
		return remote, nil
	}

	var entries []string
	for _, header := range req.Header.Values("X-Forwarded-For") {
		entries = append(entries, strings.Split(header, ",")...)
	}
	if len(entries) == 0 {
		return remote, nil
	}

	var addr netip.Addr
	for i := len(entries) - 1; i >= 0; i-- {
		addr, err = parseHostAddr(entries[i])
		if err != nil {
			return netip.Addr{}, fmt.Errorf("parse X-Forwarded-For entry: %w", err)
		}
		if !r.isTrusted(addr) {
			return addr, nil
		}
	}
	// Every hop is a trusted proxy; the leftmost entry is the origin.
	return addr, nil
}

func (r Resolver) isTrusted(addr netip.Addr) bool {
	for _, prefix := range r.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
