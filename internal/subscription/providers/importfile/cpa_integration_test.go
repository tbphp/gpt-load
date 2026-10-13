package importfile_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/subscription/providers/antigravity"
	"gpt-load/internal/subscription/providers/grok"
	"gpt-load/internal/subscription/providers/importfile"
)

type importTransport func(*http.Request) (*http.Response, error)

func (f importTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCPAProjectionPreservesVerifiedProviderImport(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	requests := 0
	http.DefaultTransport = importTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		var body string
		switch r.URL.String() {
		case "https://www.googleapis.com/oauth2/v2/userinfo?alt=json":
			body = `{"id":"account","email":"sample@example.invalid","verified_email":true}`
		case "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist":
			body = `{"projectId":"verified-project"}`
		case "https://auth.x.ai/.well-known/openid-configuration":
			body = `{"device_authorization_endpoint":"https://auth.x.ai/oauth2/device/code","token_endpoint":"https://auth.x.ai/oauth2/token","userinfo_endpoint":"https://auth.x.ai/userinfo"}`
		case "https://auth.x.ai/userinfo":
			body = `{"sub":"account","email":"sample@example.invalid","email_verified":true}`
		default:
			return nil, fmt.Errorf("unexpected import endpoint: %s", r.URL)
		}
		if r.URL.Path != "/.well-known/openid-configuration" && r.Header.Get("Authorization") != "Bearer test-access" {
			t.Error("source metadata changed the upstream authorization")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	for _, test := range []struct {
		provider string
		importer func(context.Context, []byte) ([]byte, error)
	}{
		{"antigravity", func(ctx context.Context, raw []byte) ([]byte, error) {
			credential, err := antigravity.ImportCredential(ctx, raw)
			if err != nil {
				return nil, err
			}
			if credential.ProjectID != "verified-project" {
				t.Error("import did not discover the verified project")
			}
			return antigravity.MarshalCredential(credential)
		}},
		{"grok", func(ctx context.Context, raw []byte) ([]byte, error) {
			credential, err := grok.ImportCredential(ctx, raw)
			if err != nil {
				return nil, err
			}
			return grok.MarshalCredential(credential)
		}},
	} {
		t.Run(test.provider, func(t *testing.T) {
			for _, identity := range []string{"account", "wrong-account"} {
				raw := fmt.Sprintf(`{"type":%q,"access_token":"test-access","refresh_token":"test-refresh","account_id":%q,"email":"sample@example.invalid","expired":"2035-01-01T00:00:00Z","headers":{"Authorization":"metadata-marker"},"proxy_url":"https://metadata-marker.invalid","antigravity_capability_hints":{"project_id":"metadata-marker","model_ids":["metadata-marker"]}}`, test.provider, identity)
				document, err := importfile.Parse([]byte(raw))
				if err != nil || len(document.Entries) != 1 || document.Entries[0].ErrorCode != "" {
					t.Fatal("CPA source could not be normalized")
				}
				before := requests
				canonical, err := test.importer(t.Context(), document.Entries[0].Credential)
				if identity == "wrong-account" {
					if err == nil {
						t.Fatal("normalization bypassed account identity verification")
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if requests-before != 2 || strings.Contains(string(canonical), "metadata-marker") {
					t.Fatal("import skipped verification or retained source metadata")
				}
			}
		})
	}
}
