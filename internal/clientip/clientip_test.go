package clientip

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestParseAddr(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{input: "1.2.3.4", expected: "1.2.3.4"},
		{input: " 2a01:4f8::1 ", expected: "2a01:4f8::1"},
		{input: "[2a01:4f8::1]", expected: "2a01:4f8::1"},
		{input: "::ffff:1.2.3.4", expected: "1.2.3.4"},
		{input: "fe80::1%eth0", wantErr: true},
		{input: "1.2.3.4:80", wantErr: true},
		{input: "example.com", wantErr: true},
		{input: "", wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			addr, err := ParseAddr(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s", addr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if addr.String() != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, addr)
			}
		})
	}
}

func TestIsPublic(t *testing.T) {
	testCases := map[string]bool{
		"1.2.3.4":           true,
		"2a01:4f8::1":       true,
		"::ffff:1.2.3.4":    true,
		"127.0.0.1":         false,
		"::1":               false,
		"0.0.0.0":           false,
		"::":                false,
		"224.0.0.1":         false,
		"ff02::1":           false,
		"169.254.1.1":       false,
		"fe80::1":           false,
		"10.1.2.3":          false,
		"172.16.0.1":        false,
		"192.168.1.1":       false,
		"100.64.0.1":        false,
		"100.127.255.255":   false,
		"100.128.0.1":       true,
		"fc00::1":           false,
		"fd12:3456::1":      false,
		"192.0.2.1":         false,
		"198.51.100.1":      false,
		"203.0.113.1":       false,
		"2001:db8::1":       false,
		"3fff::1":           false,
		"::ffff:10.0.0.1":   false,
		"::ffff:100.64.0.1": false,
	}
	for input, expected := range testCases {
		if got := IsPublic(netip.MustParseAddr(input)); got != expected {
			t.Errorf("IsPublic(%s) = %v, expected %v", input, got, expected)
		}
	}
}

func TestParsePrefixes(t *testing.T) {
	prefixes, err := ParsePrefixes("10.0.0.0/8, 192.168.1.5 ,fd00::/8,::1,")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"10.0.0.0/8", "192.168.1.5/32", "fd00::/8", "::1/128"}
	if len(prefixes) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, prefixes)
	}
	for i, prefix := range prefixes {
		if prefix.String() != expected[i] {
			t.Fatalf("expected %v, got %v", expected, prefixes)
		}
	}

	if _, err := ParsePrefixes("10.0.0.0/33"); err == nil {
		t.Fatalf("expected error for invalid prefix")
	}
	if _, err := ParsePrefixes("not-an-ip"); err == nil {
		t.Fatalf("expected error for invalid address")
	}
}

func TestResolverFromRequest(t *testing.T) {
	resolver := Resolver{TrustedProxies: []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("fd00::/8"),
	}}

	testCases := []struct {
		name       string
		resolver   Resolver
		remoteAddr string
		xff        []string
		expected   string
		wantErr    bool
	}{
		{name: "remote IPv4", remoteAddr: "1.2.3.4:5555", expected: "1.2.3.4"},
		{name: "remote IPv6", remoteAddr: "[2a01:4f8::1]:5555", expected: "2a01:4f8::1"},
		{name: "remote IPv4-mapped", remoteAddr: "[::ffff:1.2.3.4]:5555", expected: "1.2.3.4"},
		{name: "XFF ignored without trusted proxies", remoteAddr: "1.2.3.4:5555", xff: []string{"5.6.7.8"}, expected: "1.2.3.4"},
		{name: "XFF ignored from untrusted peer", resolver: resolver, remoteAddr: "1.2.3.4:5555", xff: []string{"5.6.7.8"}, expected: "1.2.3.4"},
		{name: "XFF from trusted peer", resolver: resolver, remoteAddr: "10.0.0.1:5555", xff: []string{"5.6.7.8"}, expected: "5.6.7.8"},
		{name: "XFF rightmost untrusted wins", resolver: resolver, remoteAddr: "10.0.0.1:5555", xff: []string{"6.6.6.6, 5.6.7.8, 10.0.0.2"}, expected: "5.6.7.8"},
		{name: "XFF IPv6 with port and brackets", resolver: resolver, remoteAddr: "[fd00::1]:5555", xff: []string{"[2a01:4f8::1]:1234"}, expected: "2a01:4f8::1"},
		{name: "XFF bare IPv6", resolver: resolver, remoteAddr: "10.0.0.1:5555", xff: []string{"2a01:4f8::1"}, expected: "2a01:4f8::1"},
		{name: "XFF IPv4 with port", resolver: resolver, remoteAddr: "10.0.0.1:5555", xff: []string{"5.6.7.8:80"}, expected: "5.6.7.8"},
		{name: "XFF multiple headers", resolver: resolver, remoteAddr: "10.0.0.1:5555", xff: []string{"5.6.7.8", "10.0.0.3"}, expected: "5.6.7.8"},
		{name: "XFF all trusted", resolver: resolver, remoteAddr: "10.0.0.1:5555", xff: []string{"10.0.0.5, 10.0.0.6"}, expected: "10.0.0.5"},
		{name: "XFF invalid entry", resolver: resolver, remoteAddr: "10.0.0.1:5555", xff: []string{"garbage"}, wantErr: true},
		{name: "XFF zone ID", resolver: resolver, remoteAddr: "10.0.0.1:5555", xff: []string{"fe80::1%eth0"}, wantErr: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remoteAddr
			for _, value := range tc.xff {
				req.Header.Add("X-Forwarded-For", value)
			}
			addr, err := tc.resolver.FromRequest(req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s", addr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if addr.String() != tc.expected {
				t.Fatalf("expected %s, got %s", tc.expected, addr)
			}
		})
	}
}
