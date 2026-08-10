package gateway

import (
	"net/http"
	"strings"
	"testing"
)

func setRepresentationMetadata(headers http.Header) {
	headers.Set("ETag", `"wire-v1"`)
	headers.Set("Digest", "sha-256=wire-digest")
	headers.Set("Content-MD5", "d2lyZQ==")
	headers.Set("Content-Range", "bytes 0-9/10")
	headers.Set("Content-Digest", "sha-256=:d2lyZQ==:")
	headers.Set("Repr-Digest", "sha-256=:cmVwcg==:")
	headers.Set("Signature", "sig1=:c2lnbmF0dXJl:")
	headers.Set("Signature-Input", `sig1=("content-digest");created=1`)
}

func assertHeadersDoNotContain(t *testing.T, headers http.Header, literal string) {
	t.Helper()
	for name, values := range headers {
		for _, value := range values {
			if strings.Contains(value, literal) {
				t.Fatalf("header %q leaked %q in value %q", name, literal, value)
			}
		}
	}
}

func assertRepresentationMetadata(t *testing.T, headers http.Header, wantPreserved bool) {
	t.Helper()
	want := map[string]string{
		"ETag":           `"wire-v1"`,
		"Digest":         "sha-256=wire-digest",
		"Content-MD5":    "d2lyZQ==",
		"Content-Range":  "bytes 0-9/10",
		"Content-Digest": "sha-256=:d2lyZQ==:",
		"Repr-Digest":    "sha-256=:cmVwcg==:",
	}
	for name, value := range want {
		got := headers.Get(name)
		if wantPreserved && got != value {
			t.Errorf("%s = %q, want preserved value %q", name, got, value)
		}
		if !wantPreserved && got != "" {
			t.Errorf("%s = %q, want removed after body rewrite", name, got)
		}
	}
	for _, name := range []string{"Signature", "Signature-Input"} {
		if got := headers.Get(name); got != "" {
			t.Errorf("%s = %q, want removed from downstream response", name, got)
		}
	}
}
