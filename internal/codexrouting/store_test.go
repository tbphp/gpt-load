package codexrouting

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/testutil/encryptiontest"
)

func TestInjectCapturePinsAndDetectsRotation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir(), encryptiontest.Service(t, "codex-routing-test-master-key"), Config{
		Enabled:       true,
		RefreshBefore: 5 * time.Minute,
		EventLimit:    20,
		Models:        []string{"gpt-6-astra"},
	})
	store.now = func() time.Time { return now }

	probeHeaders := make(http.Header)
	probeHeaders.Add("Set-Cookie", "__oailb="+testOaiLB("chat.gateway.unified-122.api.openai.com", now.Add(time.Hour))+"; Path=/; Secure")
	probeHeaders.Set("Cf-Ray", "abc123-LAX")
	store.Capture(WithDiscovery(t.Context()), 7, "gpt-6-astra", probeHeaders, 200)

	request := make(http.Header)
	store.Inject(t.Context(), 7, "gpt-6-astra", request)
	if !strings.Contains(request.Get("Cookie"), "__oailb=") {
		t.Fatalf("Cookie = %q", request.Get("Cookie"))
	}

	reuse := make(http.Header)
	reuse.Set("X-Codex-Safety-Buffering-Enabled", "false")
	store.Capture(t.Context(), 7, "gpt-6-astra", reuse, 200)
	status := store.Status([]AccountRef{{CredentialID: 7, GroupName: "codex"}})
	if len(status.Credentials) != 1 || status.Credentials[0].Verdict != string(VerdictPinned) {
		t.Fatalf("status = %#v", status)
	}
	if status.Credentials[0].Region != "unified-122" || !status.Credentials[0].InjectSuccess {
		t.Fatalf("credential = %#v", status.Credentials[0])
	}

	rotated := make(http.Header)
	rotated.Add("Set-Cookie", "__oailb="+testOaiLB("chat.gateway.unified-9.api.openai.com", now.Add(time.Hour))+"; Path=/")
	store.Inject(t.Context(), 7, "gpt-6-sol", make(http.Header))
	store.Capture(t.Context(), 7, "gpt-6-sol", rotated, 200)
	status = store.Status([]AccountRef{{CredentialID: 7}})
	if status.Credentials[0].Verdict != string(VerdictRotated) || status.Credentials[0].Region != "unified-9" {
		t.Fatalf("rotated = %#v", status.Credentials[0])
	}
}

func TestInjectSetsRelayKeyWithoutPin(t *testing.T) {
	t.Parallel()
	store := NewStore("", nil, Config{Enabled: true, RelayURL: "https://relay.example", RelayKey: "relay-secret"})
	headers := make(http.Header)
	store.Inject(t.Context(), 4, "gpt-6-astra", headers)
	if headers.Get("X-Relay-Key") != "relay-secret" {
		t.Fatalf("X-Relay-Key = %q", headers.Get("X-Relay-Key"))
	}
	if headers.Get("Cookie") != "" {
		t.Fatalf("Cookie = %q", headers.Get("Cookie"))
	}
}

func TestStatusOmitsCookieValues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	secret := "SECRET_COOKIE_VALUE_XYZ"
	store := NewStore(t.TempDir(), encryptiontest.Service(t, "codex-routing-test-master-key"), Config{
		Enabled:    true,
		ProbeProxy: "http://user:super-secret-proxy-pass@127.0.0.1:5000",
		EventLimit: 10,
	})
	store.now = func() time.Time { return now }
	header := make(http.Header)
	header.Add("Set-Cookie", "__oailb="+secret+"; Path=/")
	store.Capture(WithDiscovery(t.Context()), 3, "gpt-6-astra", header, 200)
	raw, err := json.Marshal(store.Status([]AccountRef{{CredentialID: 3, GroupName: "lab"}}))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(body, secret) {
		t.Fatalf("status leaked cookie value: %s", body)
	}
	if strings.Contains(body, "super-secret-proxy-pass") {
		t.Fatalf("status leaked proxy: %s", body)
	}
	events, err := json.Marshal(store.Events())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(events), secret) {
		t.Fatalf("events leaked cookie value: %s", events)
	}
}

func TestJarRoundTripPersistsRegion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	enc := encryptiontest.Service(t, "codex-routing-test-master-key")
	now := time.Now().UTC().Truncate(time.Second)
	first := NewStore(dir, enc, Config{Enabled: true, EventLimit: 4})
	first.now = func() time.Time { return now }
	header := make(http.Header)
	header.Add("Set-Cookie", "__oailb="+testOaiLB("chat.gateway.unified-94.api.openai.com", now.Add(30*time.Minute))+"; Path=/")
	first.Capture(WithDiscovery(t.Context()), 11, "gpt-6-sol", header, 200)

	second := NewStore(dir, enc, Config{Enabled: true, EventLimit: 4})
	status := second.Status([]AccountRef{{CredentialID: 11}})
	if len(status.Credentials) != 1 || status.Credentials[0].Region != "unified-94" {
		t.Fatalf("reloaded = %#v", status)
	}
}
