package mirasim

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefreshModelsAndLimits(t *testing.T) {
	key := testDevicePEM(t)
	var seenSignature string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSignature = r.Header.Get("x-mirasim-sig")
		switch r.URL.Path {
		case "/v1/device/session":
			http.NotFound(w, r)
		case "/v1/models":
			_, _ = io.WriteString(w, `{"data":[{"id":"claude-sonnet-5"},{"id":"claude-sonnet-5-20250901"},{"id":"*"},{"id":"vendor/hidden"},{"id":"gpt-5.6"},{"id":"other-model"}]}`)
		case "/v1/limits":
			_, _ = io.WriteString(w, `{"paid":true,"windows":[{"name":"weekly","budget":100,"used":20,"model_scoped":false},{"name":"7d_fable","budget":10,"used":10,"model_scoped":true}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer relay.Close()
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/refresh" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"next-access","refresh_token":"next-refresh","expires_in":3600}`)
	}))
	defer admin.Close()

	credential, err := ParseCredentialJSON([]byte(`{"type":"mirasim","access_token":"access-secret","refresh_token":"refresh-secret","device_private_key":` + jsonString(key) + `,"relay_url":"` + relay.URL + `","admin_url":"` + admin.URL + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := NewClient(credential).Refresh(context.Background())
	if err != nil || refreshed.AccessToken != "next-access" || refreshed.RefreshToken != "next-refresh" {
		t.Fatalf("refresh = %#v, %v", refreshed, err)
	}
	models, err := NewClient(credential).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, ",") != "claude-sonnet-5,gpt-5.6" {
		t.Fatalf("models = %#v", models)
	}
	if seenSignature == "" {
		t.Fatal("model discovery was not signed")
	}
	limits, err := NewClient(credential).FetchLimits(context.Background())
	if err != nil || len(limits.Windows) != 2 || limits.Paid == nil || !*limits.Paid {
		t.Fatalf("limits = %#v, %v", limits, err)
	}
}

func TestServableModelsDropDatedTwin(t *testing.T) {
	got := servableModelIDs([]string{"claude-haiku-4-5", "claude-haiku-4-5-20251001", "gpt-5.6-20260101"})
	if strings.Join(got, ",") != "claude-haiku-4-5,gpt-5.6-20260101" {
		t.Fatalf("models = %#v", got)
	}
}
