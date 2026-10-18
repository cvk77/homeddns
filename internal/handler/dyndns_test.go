package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/markussiebert/homeddns/internal/clientip"
	"github.com/markussiebert/homeddns/internal/provider"
)

// fakeProvider is an in-memory provider keyed by "name/type".
type fakeProvider struct {
	mu      sync.Mutex
	records map[string]string
	updates []provider.DNSRecord
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{records: make(map[string]string)}
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) GetRecord(ctx context.Context, domain, hostname, recordType string) (*provider.DNSRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.records[hostname+"/"+recordType]
	if !ok {
		return nil, provider.ErrRecordNotFound
	}
	return &provider.DNSRecord{Name: hostname, Type: recordType, Value: value}, nil
}

func (f *fakeProvider) UpdateRecord(ctx context.Context, domain string, record *provider.DNSRecord) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := record.Name + "/" + record.Type
	if f.records[key] == record.Value {
		return false, nil
	}
	f.records[key] = record.Value
	f.updates = append(f.updates, *record)
	return true, nil
}

func (f *fakeProvider) DeleteRecord(ctx context.Context, domain string, record *provider.DNSRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := record.Name + "/" + record.Type
	if f.records[key] != record.Value {
		return provider.ErrRecordNotFound
	}
	delete(f.records, key)
	return nil
}

func (f *fakeProvider) Close(ctx context.Context) error { return nil }

func serve(h http.Handler, target string, configure func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "1.2.3.4:5555"
	if configure != nil {
		configure(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func body(rec *httptest.ResponseRecorder) string {
	return strings.TrimSpace(rec.Body.String())
}

func TestServeHTTP_NochgWhenUnchanged(t *testing.T) {
	fake := newFakeProvider()
	h := NewDynDNSHandler(Config{Provider: fake})

	rec := serve(h, "/nic/update?hostname=home.example.com&myip=1.2.3.4", nil)
	if got := body(rec); got != "good 1.2.3.4" {
		t.Fatalf("first update: got %q", got)
	}

	rec = serve(h, "/nic/update?hostname=home.example.com&myip=1.2.3.4", nil)
	if got := body(rec); got != "nochg 1.2.3.4" {
		t.Fatalf("second update: got %q", got)
	}
	if len(fake.updates) != 1 {
		t.Fatalf("expected 1 provider update, got %d", len(fake.updates))
	}
}

func TestServeHTTP_AddressSelection(t *testing.T) {
	trusted := clientip.Resolver{TrustedProxies: []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("fd00::/8"),
	}}

	testCases := []struct {
		name       string
		target     string
		remoteAddr string
		xff        string
		resolver   clientip.Resolver
		allowPriv  bool
		expected   string
		records    map[string]string
	}{
		{name: "myip IPv4", target: "/nic/update?hostname=home.example.com&myip=1.2.3.4",
			expected: "good 1.2.3.4", records: map[string]string{"home.example.com/A": "1.2.3.4"}},
		{name: "myip IPv6", target: "/nic/update?hostname=home.example.com&myip=2a01:4f8::1",
			expected: "good 2a01:4f8::1", records: map[string]string{"home.example.com/AAAA": "2a01:4f8::1"}},
		{name: "IPv4-mapped IPv6 becomes A", target: "/nic/update?hostname=home.example.com&myip=::ffff:1.2.3.4",
			expected: "good 1.2.3.4", records: map[string]string{"home.example.com/A": "1.2.3.4"}},
		{name: "IPv6 with zone ID rejected", target: "/nic/update?hostname=home.example.com&myip=2a01:4f8::1%25eth0",
			expected: "911"},
		{name: "invalid myip rejected", target: "/nic/update?hostname=home.example.com&myip=not-an-ip",
			expected: "911"},
		{name: "myip and myipv6", target: "/nic/update?hostname=home.example.com&myip=1.2.3.4&myipv6=2a01:4f8::1",
			expected: "good 1.2.3.4,2a01:4f8::1",
			records:  map[string]string{"home.example.com/A": "1.2.3.4", "home.example.com/AAAA": "2a01:4f8::1"}},
		{name: "myipv6 with IPv4 rejected", target: "/nic/update?hostname=home.example.com&myipv6=1.2.3.4",
			expected: "911"},
		{name: "two IPv6 addresses rejected", target: "/nic/update?hostname=home.example.com&myip=2a01:4f8::1&myipv6=2a01:4f8::2",
			expected: "911"},
		{name: "CGNAT rejected", target: "/nic/update?hostname=home.example.com&myip=100.64.1.2", expected: "911"},
		{name: "RFC 1918 rejected", target: "/nic/update?hostname=home.example.com&myip=192.168.1.10", expected: "911"},
		{name: "ULA rejected", target: "/nic/update?hostname=home.example.com&myip=fd12:3456::1", expected: "911"},
		{name: "link-local rejected", target: "/nic/update?hostname=home.example.com&myip=fe80::1", expected: "911"},
		{name: "loopback rejected", target: "/nic/update?hostname=home.example.com&myip=::1", expected: "911"},
		{name: "documentation rejected", target: "/nic/update?hostname=home.example.com&myip=2001:db8::1", expected: "911"},
		{name: "private allowed when enabled", target: "/nic/update?hostname=home.example.com&myip=192.168.1.10",
			allowPriv: true, expected: "good 192.168.1.10", records: map[string]string{"home.example.com/A": "192.168.1.10"}},
		{name: "RemoteAddr IPv6", target: "/nic/update?hostname=home.example.com", remoteAddr: "[2a01:4f8::1]:443",
			expected: "good 2a01:4f8::1", records: map[string]string{"home.example.com/AAAA": "2a01:4f8::1"}},
		{name: "RemoteAddr private rejected", target: "/nic/update?hostname=home.example.com", remoteAddr: "10.1.2.3:443",
			expected: "911"},
		{name: "XFF ignored from untrusted peer", target: "/nic/update?hostname=home.example.com",
			remoteAddr: "5.6.7.8:443", xff: "1.2.3.4",
			expected: "good 5.6.7.8", records: map[string]string{"home.example.com/A": "5.6.7.8"}},
		{name: "XFF from trusted proxy, multiple entries with IPv6", target: "/nic/update?hostname=home.example.com",
			remoteAddr: "10.0.0.1:443", xff: "9.9.9.9, [2a01:4f8::1]:1234, 10.0.0.2", resolver: trusted,
			expected: "good 2a01:4f8::1", records: map[string]string{"home.example.com/AAAA": "2a01:4f8::1"}},
		{name: "myip takes precedence over XFF", target: "/nic/update?hostname=home.example.com&myip=1.2.3.4",
			remoteAddr: "10.0.0.1:443", xff: "5.6.7.8", resolver: trusted,
			expected: "good 1.2.3.4", records: map[string]string{"home.example.com/A": "1.2.3.4"}},
		{name: "UniFi path format", target: "/home.example.com?myip=1.2.3.4",
			expected: "good", records: map[string]string{"home.example.com/A": "1.2.3.4"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeProvider()
			h := NewDynDNSHandler(Config{Provider: fake, ClientIP: tc.resolver, AllowPrivateIPs: tc.allowPriv})
			rec := serve(h, tc.target, func(r *http.Request) {
				if tc.remoteAddr != "" {
					r.RemoteAddr = tc.remoteAddr
				}
				if tc.xff != "" {
					r.Header.Set("X-Forwarded-For", tc.xff)
				}
			})
			if got := body(rec); got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
			if len(fake.records) != len(tc.records) {
				t.Fatalf("expected records %v, got %v", tc.records, fake.records)
			}
			for key, value := range tc.records {
				if fake.records[key] != value {
					t.Fatalf("expected records %v, got %v", tc.records, fake.records)
				}
			}
		})
	}
}

func TestServeHTTP_CleanupStaleRecord(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		fake := newFakeProvider()
		fake.records["home.example.com/A"] = "5.6.7.8"
		fake.records["other.example.com/A"] = "5.6.7.8"
		h := NewDynDNSHandler(Config{Provider: fake, CleanupStaleRecords: enabled})

		rec := serve(h, "/nic/update?hostname=home.example.com&myipv6=2a01:4f8::1", nil)
		if got := body(rec); got != "good 2a01:4f8::1" {
			t.Fatalf("enabled=%v: got %q", enabled, got)
		}
		_, hasA := fake.records["home.example.com/A"]
		if hasA == enabled {
			t.Fatalf("enabled=%v: stale A record present=%v", enabled, hasA)
		}
		if _, ok := fake.records["other.example.com/A"]; !ok {
			t.Fatalf("enabled=%v: unrelated record was deleted", enabled)
		}
	}
}

func TestServeHTTP_CleanupSkippedForDualUpdate(t *testing.T) {
	fake := newFakeProvider()
	h := NewDynDNSHandler(Config{Provider: fake, CleanupStaleRecords: true})

	serve(h, "/nic/update?hostname=home.example.com&myip=1.2.3.4&myipv6=2a01:4f8::1", nil)
	if len(fake.records) != 2 {
		t.Fatalf("expected both records, got %v", fake.records)
	}
}

func TestServeHTTP_AllowedHostnames(t *testing.T) {
	allowed := []string{"home.example.com", "*.dyn.example.com"}
	testCases := []struct {
		hostname string
		allowed  bool
	}{
		{"home.example.com", true},
		{"HOME.example.com", true},
		{"a.dyn.example.com", true},
		{"a.b.dyn.example.com", true},
		{"*.dyn.example.com", true},
		{"dyn.example.com", false},
		{"evil-dyn.example.com", false},
		{"other.example.com", false},
		{"home.example.com.evil.org", false},
	}

	for _, tc := range testCases {
		t.Run(tc.hostname, func(t *testing.T) {
			fake := newFakeProvider()
			h := NewDynDNSHandler(Config{Provider: fake, AllowedHostnames: allowed})
			rec := serve(h, "/nic/update?hostname="+tc.hostname+"&myip=1.2.3.4", nil)
			if tc.allowed {
				if rec.Code != http.StatusOK || body(rec) != "good 1.2.3.4" {
					t.Fatalf("expected update, got %d %q", rec.Code, body(rec))
				}
				return
			}
			if rec.Code != http.StatusForbidden || body(rec) != "nohost" {
				t.Fatalf("expected 403 nohost, got %d %q", rec.Code, body(rec))
			}
			if len(fake.updates) != 0 {
				t.Fatalf("unexpected provider update")
			}
		})
	}

	// UniFi path format is checked as well
	h := NewDynDNSHandler(Config{Provider: newFakeProvider(), AllowedHostnames: allowed})
	if rec := serve(h, "/other.example.com?myip=1.2.3.4", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for UniFi format, got %d", rec.Code)
	}
}

func TestSplitHostname(t *testing.T) {
	testCases := []struct {
		configured string
		hostname   string
		domain     string
		subdomain  string
	}{
		{"", "test.example.com", "example.com", "test"},
		{"", "example.com", "example.com", "@"},
		{"", "*.example.com", "example.com", "*"},
		{"example.co.uk", "home.example.co.uk", "example.co.uk", "home"},
		{"example.co.uk", "example.co.uk", "example.co.uk", "@"},
		{"example.co.uk", "home.other.org", "other.org", "home"},
		{"example.com", "a.b.example.com", "example.com", "a.b"},
	}
	for _, tc := range testCases {
		h := NewDynDNSHandler(Config{Domain: tc.configured})
		domain, subdomain := h.splitHostname(tc.hostname)
		if domain != tc.domain || subdomain != tc.subdomain {
			t.Errorf("splitHostname(%q) with DOMAIN=%q = %q, %q; expected %q, %q",
				tc.hostname, tc.configured, domain, subdomain, tc.domain, tc.subdomain)
		}
	}
}
