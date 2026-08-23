package outboundproxy

import (
	"errors"
	"strings"
	"testing"
)

func TestEnvironmentDetectsOnlyHTTPProxyVariables(t *testing.T) {
	for _, key := range []string{
		"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("ALL_PROXY", "socks5://proxy.example.com:1080")
	if got := Environment(); got != nil {
		t.Fatalf("Environment() with only ALL_PROXY = %#v, want nil", got)
	}

	t.Setenv("ALL_PROXY", "")
	t.Setenv("HTTP_PROXY", "http://proxy.example.com:8080")
	if got := Environment(); got == nil || got.Mode != ModeEnvironment {
		t.Fatalf("Environment() with HTTP_PROXY = %#v, want environment mode", got)
	}
}

func TestNormalizeSupportsProductProxySchemes(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"http://proxy.example.com",
		"http://proxy.example.com:8080",
		"http://[::1]:8080",
		"socks5://user:password@proxy.example.com:1080",
		"socks5://[2001:db8::1]:1080",
	} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()

			got, err := Normalize(Config{Mode: ModeCustom, URL: endpoint})
			if err != nil {
				t.Fatalf("Normalize() error = %v", err)
			}
			if got.URL != endpoint {
				t.Fatalf("Normalize().URL = %q, want %q", got.URL, endpoint)
			}
		})
	}
}

func TestNormalizeRejectsInvalidPoliciesWithoutLeakingEndpoint(t *testing.T) {
	t.Parallel()

	tests := []Config{
		{Mode: ModeInherit, URL: "http://secret:password@proxy.example.com"},
		{Mode: ModeDirect, URL: "http://secret:password@proxy.example.com"},
		{Mode: ModeCustom},
		{Mode: ModeCustom, URL: "https://secret:password@proxy.example.com"},
		{Mode: ModeCustom, URL: "ftp://secret:password@proxy.example.com"},
		{Mode: ModeCustom, URL: "http://:8080"},
		{Mode: ModeCustom, URL: "http://proxy.example.com:"},
		{Mode: ModeCustom, URL: "http://proxy.example.com:0"},
		{Mode: ModeCustom, URL: "http://proxy.example.com:65536"},
		{Mode: ModeCustom, URL: "socks5://proxy.example.com"},
		{Mode: ModeCustom, URL: "http://secret:password@proxy.example.com/path"},
		{Mode: ModeCustom, URL: "http://secret:password@proxy.example.com?token=secret"},
		{Mode: ModeCustom, URL: "http://:password@proxy.example.com"},
		{Mode: "unknown"},
	}
	for _, input := range tests {
		_, err := Normalize(input)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("Normalize(%#v) error = %v, want ErrInvalidConfig", input, err)
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "password") {
			t.Fatalf("Normalize(%#v) leaked endpoint in error %q", input, err)
		}
	}
}

func TestDisplayMasksPassword(t *testing.T) {
	t.Parallel()

	display, hasAuth, err := Display(Config{
		Mode: ModeCustom,
		URL:  "http://proxy-user:proxy-password@proxy.example.com:8080",
	})
	if err != nil {
		t.Fatalf("Display() error = %v", err)
	}
	if display != "http://proxy-user:******@proxy.example.com:8080" {
		t.Fatalf("Display() = %q", display)
	}
	if !hasAuth {
		t.Fatal("Display() hasAuth = false, want true")
	}
}

func TestResolveUsesCredentialGroupGlobalEnvironmentOrder(t *testing.T) {
	t.Parallel()

	credential := Config{Mode: ModeDirect}
	group := Config{Mode: ModeCustom, URL: "http://group.example.com"}
	global := Config{Mode: ModeCustom, URL: "http://global.example.com"}
	environment := Config{Mode: ModeCustom, URL: "http://environment.example.com"}

	tests := []struct {
		name       string
		credential *Config
		group      *Config
		global     *Config
		env        *Config
		wantSource Source
		wantMode   Mode
	}{
		{name: "credential", credential: &credential, group: &group, global: &global, env: &environment, wantSource: SourceCredential, wantMode: ModeDirect},
		{name: "group", group: &group, global: &global, env: &environment, wantSource: SourceGroup, wantMode: ModeCustom},
		{name: "global", global: &global, env: &environment, wantSource: SourceGlobal, wantMode: ModeCustom},
		{name: "environment", env: &environment, wantSource: SourceEnvironment, wantMode: ModeCustom},
		{name: "default", wantSource: SourceDefault, wantMode: ModeDirect},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Resolve(test.credential, test.group, test.global, test.env)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.Source != test.wantSource || got.Config.Mode != test.wantMode {
				t.Fatalf("Resolve() = %#v, want source=%q mode=%q", got, test.wantSource, test.wantMode)
			}
		})
	}
}

func TestNewViewShowsConfiguredAndEffectiveStateWithoutPassword(t *testing.T) {
	t.Parallel()

	configured := Config{Mode: ModeInherit}
	effective := Effective{
		Config: Config{Mode: ModeCustom, URL: "http://user:password@proxy.example.com:8080"},
		Source: SourceGroup,
	}
	got, err := NewView(&configured, effective)
	if err != nil {
		t.Fatalf("NewView() error = %v", err)
	}
	if got.ConfiguredMode != ModeInherit || got.EffectiveMode != ModeCustom || got.EffectiveSource != SourceGroup {
		t.Fatalf("NewView() = %#v", got)
	}
	if got.DisplayURL != "http://user:******@proxy.example.com:8080" || !got.HasAuth {
		t.Fatalf("NewView() display = %#v", got)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	want := Config{Mode: ModeCustom, URL: "socks5://user:password@proxy.example.com:1080"}
	encoded, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	got, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got != want {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}
