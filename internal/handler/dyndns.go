package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/markussiebert/homeddns/internal/clientip"
	"github.com/markussiebert/homeddns/internal/logger"
	"github.com/markussiebert/homeddns/internal/provider"
)

// Config represents the DynDNS handler configuration
type Config struct {
	Provider   provider.Provider
	DefaultTTL int
	// ClientIP determines the client address when no myip/myipv6 is given.
	ClientIP clientip.Resolver
	// AllowPrivateIPs disables the rejection of non-public addresses.
	AllowPrivateIPs bool
	// CleanupStaleRecords removes the record of the other address family
	// (A when setting AAAA and vice versa) if only one family is updated.
	CleanupStaleRecords bool
}

// DynDNSHandler handles DynDNS update requests
type DynDNSHandler struct {
	config Config
}

// NewDynDNSHandler creates a new DynDNS handler
func NewDynDNSHandler(config Config) *DynDNSHandler {
	if config.DefaultTTL == 0 {
		config.DefaultTTL = 60
	}
	return &DynDNSHandler{config: config}
}

// ServeHTTP handles HTTP requests
func (h *DynDNSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger.Debug("Received %s request: %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
	logger.Debug("Query params: %s", r.URL.RawQuery)

	// Only allow GET requests
	if r.Method != http.MethodGet {
		logger.Warn("Method not allowed: %s from %s", r.Method, r.RemoteAddr)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Determine format based on path
	isStandardFormat := strings.HasPrefix(r.URL.Path, "/nic/update")
	logger.Debug("Using %s format", map[bool]string{true: "standard", false: "UniFi"}[isStandardFormat])

	// Extract hostname
	hostname := h.extractHostname(r)
	if hostname == "" {
		logger.Warn("No valid hostname found in request from %s", r.RemoteAddr)
		h.respond(w, "notfqdn", "", isStandardFormat)
		return
	}

	logger.Debug("Extracted hostname: %s", hostname)

	// Validate and parse hostname
	hostname = h.normalizeHostname(hostname)
	logger.Debug("Normalized hostname: %s", hostname)

	domain, subdomain := h.splitHostname(hostname)
	if domain == "" {
		logger.Warn("Failed to split hostname '%s' into domain and subdomain", hostname)
		h.respond(w, "notfqdn", "", isStandardFormat)
		return
	}
	logger.Debug("Split hostname: domain=%s, subdomain=%s", domain, subdomain)

	// Get IP addresses
	addresses, err := h.extractIPs(r)
	if err != nil {
		logger.Warn("Rejecting update for %s from %s: %v", hostname, r.RemoteAddr, err)
		h.respond(w, "911", "", isStandardFormat)
		return
	}
	logger.Debug("Extracted IP addresses: %v", addresses)

	// Update DNS records
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	anyChanged := false
	ipStrings := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		ipStrings = append(ipStrings, addr.String())
	}
	ipList := strings.Join(ipStrings, ",")

	for _, addr := range addresses {
		logger.Debug("Updating DNS: hostname=%s, domain=%s, subdomain=%s, ip=%s", hostname, domain, subdomain, addr)
		changed, err := h.updateDNS(ctx, domain, subdomain, addr)
		if err != nil {
			logger.Error("Error updating DNS for %s: %v", hostname, err)
			h.respond(w, "911", ipList, isStandardFormat)
			return
		}
		anyChanged = anyChanged || changed
	}

	if h.config.CleanupStaleRecords && len(addresses) == 1 {
		h.cleanupStaleRecord(ctx, domain, h.buildHostname(subdomain, domain), recordType(addresses[0]))
	}

	if !anyChanged {
		logger.Info("%s already points to %s, no update needed", hostname, ipList)
		h.respond(w, "nochg", ipList, isStandardFormat)
		return
	}

	logger.Info("Successfully updated %s to %s", hostname, ipList)
	h.respond(w, "good", ipList, isStandardFormat)
}

// extractHostname extracts the hostname from the request
func (h *DynDNSHandler) extractHostname(r *http.Request) string {
	// Standard format: /nic/update?hostname=example.com
	if strings.HasPrefix(r.URL.Path, "/nic/update") {
		return r.URL.Query().Get("hostname")
	}

	// UniFi format: /example.com
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path != "" {
		// URL decode in case of encoded wildcards
		if decoded, err := url.PathUnescape(path); err == nil {
			return decoded
		}
		return path
	}

	return ""
}

// normalizeHostname normalizes the hostname (handle wildcards)
func (h *DynDNSHandler) normalizeHostname(hostname string) string {
	// Replace URL-encoded asterisk
	hostname = strings.ReplaceAll(hostname, "%2a", "*")
	hostname = strings.ReplaceAll(hostname, "%2A", "*")
	return strings.ToLower(hostname)
}

// splitHostname splits a hostname into domain and subdomain
// Example: "test.example.com" -> domain="example.com", subdomain="test"
// Example: "*.example.com" -> domain="example.com", subdomain="*"
func (h *DynDNSHandler) splitHostname(hostname string) (domain, subdomain string) {
	parts := strings.Split(hostname, ".")
	if len(parts) < 2 {
		return "", ""
	}

	// Domain is the last two parts (e.g., "example.com")
	domain = strings.Join(parts[len(parts)-2:], ".")

	// Subdomain is everything before the domain
	if len(parts) > 2 {
		subdomain = strings.Join(parts[:len(parts)-2], ".")
	} else {
		// No subdomain, use "@" for apex
		subdomain = "@"
	}

	return domain, subdomain
}

// extractIPs returns the addresses to set, at most one per address family.
// Sources in order of precedence: myip/myipv6 query parameters, then the
// client address (X-Forwarded-For from trusted proxies, else RemoteAddr).
func (h *DynDNSHandler) extractIPs(r *http.Request) ([]netip.Addr, error) {
	query := r.URL.Query()
	var ipv4, ipv6 netip.Addr

	for _, param := range []string{"myip", "myipv6"} {
		value := query.Get(param)
		if value == "" {
			continue
		}
		addr, err := clientip.ParseAddr(value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", param, err)
		}
		if param == "myipv6" && addr.Is4() {
			return nil, fmt.Errorf("myipv6: %s is not an IPv6 address", addr)
		}
		target := &ipv6
		if addr.Is4() {
			target = &ipv4
		}
		if target.IsValid() {
			return nil, fmt.Errorf("%s: more than one address of the same family", param)
		}
		*target = addr
	}

	if !ipv4.IsValid() && !ipv6.IsValid() {
		addr, err := h.config.ClientIP.FromRequest(r)
		if err != nil {
			return nil, fmt.Errorf("determine client address: %w", err)
		}
		if addr.Is4() {
			ipv4 = addr
		} else {
			ipv6 = addr
		}
	}

	var addresses []netip.Addr
	for _, addr := range []netip.Addr{ipv4, ipv6} {
		if !addr.IsValid() {
			continue
		}
		if !h.config.AllowPrivateIPs && !clientip.IsPublic(addr) {
			return nil, fmt.Errorf("%s is not a public address (set ALLOW_PRIVATE_IPS=true to allow)", addr)
		}
		addresses = append(addresses, addr)
	}
	return addresses, nil
}

// recordType returns the DNS record type for an address.
func recordType(addr netip.Addr) string {
	if addr.Is4() {
		return "A"
	}
	return "AAAA"
}

// updateDNS updates the DNS record via the configured provider
func (h *DynDNSHandler) updateDNS(ctx context.Context, domain, subdomain string, addr netip.Addr) (bool, error) {
	// Build full hostname
	hostname := h.buildHostname(subdomain, domain)

	// Prepare DNS record
	record := &provider.DNSRecord{
		Name:  hostname,
		Type:  recordType(addr),
		Value: addr.String(),
		TTL:   h.config.DefaultTTL,
	}

	// Update the record via provider
	changed, err := h.config.Provider.UpdateRecord(ctx, domain, record)
	if err != nil {
		return false, fmt.Errorf("update DNS record: %w", err)
	}

	return changed, nil
}

// cleanupStaleRecord deletes the record of the other address family for
// hostname, e.g. an outdated A record after switching to IPv6-only updates.
// Failures are logged and do not fail the update.
func (h *DynDNSHandler) cleanupStaleRecord(ctx context.Context, domain, hostname, keepType string) {
	staleType := "AAAA"
	if keepType == "AAAA" {
		staleType = "A"
	}

	existing, err := h.config.Provider.GetRecord(ctx, domain, hostname, staleType)
	if errors.Is(err, provider.ErrRecordNotFound) {
		return
	}
	if err != nil {
		logger.Warn("Could not check for stale %s record of %s: %v", staleType, hostname, err)
		return
	}

	if err := h.config.Provider.DeleteRecord(ctx, domain, existing); err != nil {
		logger.Warn("Could not delete stale %s record of %s: %v", staleType, hostname, err)
		return
	}
	logger.Info("Deleted stale %s record of %s (was %s)", staleType, hostname, existing.Value)
}

// buildHostname builds a full hostname from subdomain and domain
func (h *DynDNSHandler) buildHostname(subdomain, domain string) string {
	if subdomain == "@" || subdomain == "" {
		return domain
	}
	return subdomain + "." + domain
}

// respond sends a DynDNS response
func (h *DynDNSHandler) respond(w http.ResponseWriter, status, ip string, standardFormat bool) {
	w.Header().Set("Content-Type", "text/plain")

	if standardFormat {
		// Standard format includes IP
		if ip != "" {
			fmt.Fprintf(w, "%s %s\n", status, ip)
		} else {
			fmt.Fprintf(w, "%s\n", status)
		}
	} else {
		// Simple format is just the status
		fmt.Fprintf(w, "%s\n", status)
	}
}
