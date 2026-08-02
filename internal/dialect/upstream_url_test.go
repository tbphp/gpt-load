package dialect

import (
	"strings"
	"testing"
)

func TestResolveUpstreamURL(t *testing.T) {
	tests := []struct {
		name         string
		baseURL      string
		resourcePath string
		rawQuery     string
		want         string
		wantErr      bool
	}{
		{
			name:         "complete API prefix",
			baseURL:      "https://api.openai.com/v1",
			resourcePath: "/chat/completions",
			rawQuery:     "trace=true",
			want:         "https://api.openai.com/v1/chat/completions?trace=true",
		},
		{
			name:         "custom path prefix and trailing slash",
			baseURL:      "https://proxy.example.com/tenant/openai/v1/",
			resourcePath: "/responses/resp_123",
			want:         "https://proxy.example.com/tenant/openai/v1/responses/resp_123",
		},
		{
			name:         "root base URL",
			baseURL:      "https://api.example.com",
			resourcePath: "/chat/completions",
			want:         "https://api.example.com/chat/completions",
		},
		{
			name:         "base and client queries keep current order",
			baseURL:      "https://proxy.example.com/v1?api-version=2026-01-01",
			resourcePath: "/chat/completions",
			rawQuery:     "trace=true&filter=a%2Fb",
			want:         "https://proxy.example.com/v1/chat/completions?api-version=2026-01-01&trace=true&filter=a%2Fb",
		},
		{
			name:         "empty request query preserves base query",
			baseURL:      "https://proxy.example.com/v1?api-version=2026-01-01",
			resourcePath: "/models",
			want:         "https://proxy.example.com/v1/models?api-version=2026-01-01",
		},
		{name: "empty resource", baseURL: "https://api.example.com/v1", wantErr: true},
		{name: "relative resource", baseURL: "https://api.example.com/v1", resourcePath: "models", wantErr: true},
		{name: "double slash resource", baseURL: "https://api.example.com/v1", resourcePath: "//models", wantErr: true},
		{name: "relative base", baseURL: "api.example.com/v1", resourcePath: "/models", wantErr: true},
		{name: "unsupported scheme", baseURL: "ftp://api.example.com/v1", resourcePath: "/models", wantErr: true},
		{name: "userinfo", baseURL: "https://user:secret@api.example.com/v1", resourcePath: "/models", wantErr: true},
		{name: "fragment", baseURL: "https://api.example.com/v1#secret", resourcePath: "/models", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveUpstreamURL(tt.baseURL, tt.resourcePath, tt.rawQuery)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveUpstreamURL() = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveUpstreamURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveUpstreamURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveUpstreamURLDoesNotExposeMalformedBaseURL(t *testing.T) {
	const secret = "query-secret"

	_, err := resolveUpstreamURL(
		"https://api.example.com/v1/%zz?token="+secret,
		"/models",
		"",
	)
	if err == nil {
		t.Fatal("resolveUpstreamURL() error = nil, want malformed base URL error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("resolveUpstreamURL() error exposes query secret: %v", err)
	}
}
