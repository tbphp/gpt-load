package config

import "testing"

func TestLoadClientIPConfiguration(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("AUTH_KEY", "test-auth-key")
	t.Setenv("CLIENT_IP_HEADER", "CF-Connecting-IP")
	t.Setenv("TRUSTED_PROXIES", "192.0.2.1,2001:db8::/32")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientIPHeader != "CF-Connecting-IP" || len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[1] != "2001:db8::/32" {
		t.Fatalf("IP configuration = %q, %v", cfg.ClientIPHeader, cfg.TrustedProxies)
	}
}

func TestLoadRejectsInvalidClientIPConfiguration(t *testing.T) {
	for _, test := range []struct{ key, value string }{
		{"CLIENT_IP_HEADER", "bad header"},
		{"TRUSTED_PROXIES", "192.0.2.1,not-an-ip"},
		{"TRUSTED_PROXIES", "192.0.2.1,"},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv(test.key, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid IP configuration was accepted")
			}
		})
	}
}
