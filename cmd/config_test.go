package cmd

import (
	"testing"
)

func setBaseEnv(t *testing.T) {
	t.Setenv("ADDON_OPTIONS_PATH", "")
	t.Setenv("AUTH_USERNAME", "dyndns")
	t.Setenv("AUTH_PASSWORD", "secret")
	t.Setenv("DOMAIN", "")
	t.Setenv("DNS_PROVIDER", "")
	t.Setenv("ALLOWED_HOSTNAMES", "")
	t.Setenv("TRUSTED_PROXIES", "")
	t.Setenv("ALLOW_PRIVATE_IPS", "")
	t.Setenv("CLEANUP_STALE_RECORD", "")
	t.Setenv("SSL", "")
	t.Setenv("PORT", "")
}

func TestLoadConfig_DomainOptional(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("DNS_PROVIDER", "aws_route53")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Domain != "" {
		t.Fatalf("expected empty domain, got %q", config.Domain)
	}

	if err := RunUpdate("home.example.com", "A", config); err == nil {
		t.Fatalf("expected update command to require DOMAIN")
	}
}

func TestLoadConfig_Route53Alias(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("DNS_PROVIDER", "Route53")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Provider != "aws_route53" {
		t.Fatalf("expected aws_route53, got %q", config.Provider)
	}
}

func TestLoadConfig_IPOptions(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ALLOWED_HOSTNAMES", " Home.Example.com., *.dyn.example.com ,")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8,::1")
	t.Setenv("ALLOW_PRIVATE_IPS", "true")
	t.Setenv("CLEANUP_STALE_RECORD", "1")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(config.AllowedHostnames) != 2 || config.AllowedHostnames[0] != "home.example.com" || config.AllowedHostnames[1] != "*.dyn.example.com" {
		t.Fatalf("unexpected allowed hostnames: %v", config.AllowedHostnames)
	}
	if len(config.TrustedProxies) != 2 {
		t.Fatalf("unexpected trusted proxies: %v", config.TrustedProxies)
	}
	if !config.AllowPrivateIPs || !config.CleanupStaleRecords {
		t.Fatalf("expected boolean options to be enabled")
	}

	t.Setenv("TRUSTED_PROXIES", "not-a-cidr")
	if _, err := LoadConfig(); err == nil {
		t.Fatalf("expected error for invalid TRUSTED_PROXIES")
	}
}

func TestLoadConfig_Port(t *testing.T) {
	setBaseEnv(t)

	t.Setenv("PORT", "")
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Port != 8053 {
		t.Fatalf("expected default port 8053, got %d", config.Port)
	}

	t.Setenv("PORT", "9000")
	config, err = LoadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Port != 9000 {
		t.Fatalf("expected port 9000, got %d", config.Port)
	}

	for _, invalid := range []string{"abc", "0", "70000"} {
		t.Setenv("PORT", invalid)
		if _, err := LoadConfig(); err == nil {
			t.Fatalf("expected error for PORT=%s", invalid)
		}
	}
}
