package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestHandler(now func() time.Time) http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	limiter := newFailureLimiter(now)
	return middleware(Config{Username: "dyndns", Password: "secret"}, limiter)(ok)
}

func request(h http.Handler, remoteAddr, username, password string, withAuth bool) int {
	req := httptest.NewRequest(http.MethodGet, "/nic/update", nil)
	req.RemoteAddr = remoteAddr
	if withAuth {
		req.SetBasicAuth(username, password)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestMiddleware_Credentials(t *testing.T) {
	h := newTestHandler(time.Now)
	testCases := []struct {
		name     string
		username string
		password string
		withAuth bool
		expected int
	}{
		{"valid", "dyndns", "secret", true, http.StatusOK},
		{"wrong password", "dyndns", "wrong", true, http.StatusUnauthorized},
		{"wrong username", "admin", "secret", true, http.StatusUnauthorized},
		{"password prefix", "dyndns", "secre", true, http.StatusUnauthorized},
		{"password with colon", "dyndns", "secret:extra", true, http.StatusUnauthorized},
		{"empty", "", "", true, http.StatusUnauthorized},
		{"missing header", "", "", false, http.StatusUnauthorized},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := request(h, "1.2.3.4:5555", tc.username, tc.password, tc.withAuth); got != tc.expected {
				t.Fatalf("expected %d, got %d", tc.expected, got)
			}
		})
	}
}

func TestMiddleware_RateLimitsFailedLogins(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := newTestHandler(func() time.Time { return now })

	for i := 0; i < maxFailedAttempts; i++ {
		if got := request(h, "1.2.3.4:5555", "dyndns", "wrong", true); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, got)
		}
	}

	// Blocked, even with valid credentials
	if got := request(h, "1.2.3.4:5555", "dyndns", "secret", true); got != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", got)
	}
	// Other addresses are not affected
	if got := request(h, "[2a01:4f8::1]:5555", "dyndns", "secret", true); got != http.StatusOK {
		t.Fatalf("expected 200 for other address, got %d", got)
	}

	// Window expires
	now = now.Add(failureWindow)
	if got := request(h, "1.2.3.4:5555", "dyndns", "secret", true); got != http.StatusOK {
		t.Fatalf("expected 200 after window, got %d", got)
	}
}

func TestMiddleware_SuccessResetsFailures(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h := newTestHandler(func() time.Time { return now })

	for i := 0; i < maxFailedAttempts-1; i++ {
		request(h, "1.2.3.4:5555", "dyndns", "wrong", true)
	}
	request(h, "1.2.3.4:5555", "dyndns", "secret", true)
	if got := request(h, "1.2.3.4:5555", "dyndns", "wrong", true); got != http.StatusUnauthorized {
		t.Fatalf("expected 401 after reset, got %d", got)
	}
	if got := request(h, "1.2.3.4:5555", "dyndns", "secret", true); got != http.StatusOK {
		t.Fatalf("expected 200, got %d", got)
	}
}
