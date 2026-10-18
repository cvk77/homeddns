package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/markussiebert/homeddns/internal/clientip"
	"github.com/markussiebert/homeddns/internal/logger"
)

const (
	// maxFailedAttempts is the number of failed logins per client address
	// within failureWindow after which further requests are rejected.
	maxFailedAttempts = 10
	failureWindow     = 15 * time.Minute
	// pruneThreshold is the number of tracked addresses above which expired
	// entries are removed.
	pruneThreshold = 1024
)

// Config represents authentication configuration
type Config struct {
	Username string
	Password string
	// ClientIP determines the address used for rate limiting failed logins.
	ClientIP clientip.Resolver
}

// Middleware creates a basic auth middleware
func Middleware(config Config) func(http.Handler) http.Handler {
	limiter := newFailureLimiter(time.Now)
	return middleware(config, limiter)
}

func middleware(config Config, limiter *failureLimiter) func(http.Handler) http.Handler {
	expectedUsername := sha256.Sum256([]byte(config.Username))
	expectedPassword := sha256.Sum256([]byte(config.Password))

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// An unparseable address falls back to the zero address, which
			// then acts as a shared bucket.
			client, _ := config.ClientIP.FromRequest(r)

			if limiter.blocked(client) {
				logger.Warn("Too many failed login attempts from %s, rejecting request", client)
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(failureWindow.Seconds())))
				http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
				return
			}

			username, password, ok := parseBasicAuth(r.Header.Get("Authorization"))
			if !ok {
				unauthorized(w)
				return
			}

			// Compare fixed-length digests in constant time, and evaluate
			// both comparisons to not reveal which one failed.
			usernameDigest := sha256.Sum256([]byte(username))
			passwordDigest := sha256.Sum256([]byte(password))
			usernameMatch := subtle.ConstantTimeCompare(usernameDigest[:], expectedUsername[:])
			passwordMatch := subtle.ConstantTimeCompare(passwordDigest[:], expectedPassword[:])
			if usernameMatch&passwordMatch != 1 {
				limiter.recordFailure(client)
				logger.Warn("Failed login attempt from %s", client)
				unauthorized(w)
				return
			}

			// Authentication successful
			limiter.reset(client)
			next.ServeHTTP(w, r)
		})
	}
}

// parseBasicAuth extracts the credentials from a "Basic <base64>" header
func parseBasicAuth(header string) (username, password string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return "", "", false
	}

	decoded, err := base64.StdEncoding.DecodeString(header[len(prefix):])
	if err != nil {
		return "", "", false
	}

	// Split username:password
	return strings.Cut(string(decoded), ":")
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="DynDNS"`)
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte("401 Unauthorized\n"))
}

// failureLimiter counts failed logins per client address in fixed windows.
type failureLimiter struct {
	mu       sync.Mutex
	now      func() time.Time
	failures map[netip.Addr]*failureCount
}

type failureCount struct {
	count       int
	windowStart time.Time
}

func newFailureLimiter(now func() time.Time) *failureLimiter {
	return &failureLimiter{now: now, failures: make(map[netip.Addr]*failureCount)}
}

// blocked reports whether addr exceeded the failed attempts in the current window.
func (l *failureLimiter) blocked(addr netip.Addr) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.failures[addr]
	if !ok {
		return false
	}
	if l.now().Sub(entry.windowStart) >= failureWindow {
		delete(l.failures, addr)
		return false
	}
	return entry.count >= maxFailedAttempts
}

func (l *failureLimiter) recordFailure(addr netip.Addr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	if len(l.failures) > pruneThreshold {
		for key, entry := range l.failures {
			if now.Sub(entry.windowStart) >= failureWindow {
				delete(l.failures, key)
			}
		}
	}

	entry, ok := l.failures[addr]
	if !ok || now.Sub(entry.windowStart) >= failureWindow {
		l.failures[addr] = &failureCount{count: 1, windowStart: now}
		return
	}
	entry.count++
}

func (l *failureLimiter) reset(addr netip.Addr) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, addr)
}
