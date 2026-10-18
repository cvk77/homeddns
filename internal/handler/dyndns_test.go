package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
