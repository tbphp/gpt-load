package gateway

import (
	"net/http"
	"net/url"
	"testing"

	"gpt-load/internal/state"
)

type testHasher struct{}

func (testHasher) Hash(value string) string { return "hash:" + value }

func TestExtractClientKeyUsesDocumentedCarrierOrder(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		query  url.Values
		want   string
	}{
		{name: "Bearer", header: http.Header{"Authorization": {"  bEaReR   gl-bearer  "}}, want: "gl-bearer"},
		{name: "x-api-key", header: http.Header{"X-Api-Key": {" claude-key "}}, want: "claude-key"},
		{name: "x-goog-api-key", header: http.Header{"X-Goog-Api-Key": {" gemini-key "}}, want: "gemini-key"},
		{name: "query key", query: url.Values{"key": {"query-key"}}, want: "query-key"},
		{
			name: "Bearer wins",
			header: http.Header{
				"Authorization":  {"Bearer first"},
				"X-Api-Key":      {"second"},
				"X-Goog-Api-Key": {"third"},
			},
			query: url.Values{"key": {"fourth"}},
			want:  "first",
		},
		{
			name:   "non-Bearer Authorization falls through",
			header: http.Header{"Authorization": {"Basic ignored"}, "X-Api-Key": {"accepted"}},
			want:   "accepted",
		},
		{
			name:   "malformed Bearer falls through",
			header: http.Header{"Authorization": {"Bearer too many fields"}, "X-Api-Key": {"accepted"}},
			want:   "accepted",
		},
		{
			name:   "empty x-api-key falls through",
			header: http.Header{"X-Api-Key": {"  "}, "X-Goog-Api-Key": {"accepted"}},
			want:   "accepted",
		},
		{name: "empty", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &http.Request{Header: tt.header, URL: &url.URL{RawQuery: tt.query.Encode()}}
			if got := extractClientKey(request); got != tt.want {
				t.Fatalf("extractClientKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractClientKeyRemovesQueryKeyAndPreservesOtherParameters(t *testing.T) {
	request := &http.Request{
		Header: http.Header{"Authorization": {"Bearer header-key"}},
		URL: &url.URL{RawQuery: url.Values{
			"key":   {"query-key", "duplicate-key"},
			"model": {"gemini/pro"},
			"alt":   {"sse"},
		}.Encode()},
	}

	if got := extractClientKey(request); got != "header-key" {
		t.Fatalf("extractClientKey() = %q, want %q", got, "header-key")
	}
	query := request.URL.Query()
	if _, ok := query["key"]; ok {
		t.Fatalf("downstream query still contains key: %q", request.URL.RawQuery)
	}
	if got := query.Get("model"); got != "gemini/pro" {
		t.Fatalf("downstream model query = %q, want %q", got, "gemini/pro")
	}
	if got := query.Get("alt"); got != "sse" {
		t.Fatalf("downstream alt query = %q, want %q", got, "sse")
	}
}

func TestExtractClientKeyRemovesQueryKeysWithoutReencodingRawQuery(t *testing.T) {
	tests := []struct {
		name         string
		rawQuery     string
		wantKey      string
		wantRawQuery string
	}{
		{
			name:         "preserves malformed non-key fragments byte-for-byte",
			rawQuery:     "key=client&filter=%ZZ&sig=a%2Fb&z=1",
			wantKey:      "client",
			wantRawQuery: "filter=%ZZ&sig=a%2Fb&z=1",
		},
		{
			name:         "removes every decoded key fragment and skips malformed values",
			rawQuery:     "key=%ZZ&k%65y=second&key=third&keep=%2f",
			wantKey:      "second",
			wantRawQuery: "keep=%2f",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &http.Request{URL: &url.URL{RawQuery: tt.rawQuery}}
			if got := extractClientKey(request); got != tt.wantKey {
				t.Fatalf("extractClientKey() = %q, want %q", got, tt.wantKey)
			}
			if got := request.URL.RawQuery; got != tt.wantRawQuery {
				t.Fatalf("downstream raw query = %q, want %q", got, tt.wantRawQuery)
			}
		})
	}
}

func TestAuthenticateUsesFirstCredentialWithoutFallback(t *testing.T) {
	snapshot := &state.ConfigSnapshot{AccessKeysByHash: map[string]state.AccessKeyView{
		"hash:valid": {ID: 7, Name: "valid"},
	}}
	request := &http.Request{
		Header: http.Header{"Authorization": {"Bearer invalid"}, "X-Api-Key": {"valid"}},
		URL:    &url.URL{},
	}

	if _, ok := authenticate(request, snapshot, testHasher{}); ok {
		t.Fatal("authenticate() accepted lower-priority credential after invalid Bearer")
	}

	request.Header.Del("Authorization")
	got, ok := authenticate(request, snapshot, testHasher{})
	if !ok || got.ID != 7 {
		t.Fatalf("authenticate() = (%#v, %t), want access key 7", got, ok)
	}
}

func TestAuthenticateAcceptsEverySupportedCarrier(t *testing.T) {
	snapshot := &state.ConfigSnapshot{AccessKeysByHash: map[string]state.AccessKeyView{
		"hash:valid": {ID: 7, Name: "valid"},
	}}
	tests := []struct {
		name   string
		header http.Header
		query  string
	}{
		{name: "Bearer", header: http.Header{"Authorization": {"Bearer valid"}}},
		{name: "x-api-key", header: http.Header{"X-Api-Key": {"valid"}}},
		{name: "x-goog-api-key", header: http.Header{"X-Goog-Api-Key": {"valid"}}},
		{name: "query", query: "key=valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &http.Request{Header: tt.header, URL: &url.URL{RawQuery: tt.query}}
			got, ok := authenticate(request, snapshot, testHasher{})
			if !ok || got.ID != 7 {
				t.Fatalf("authenticate() = (%#v, %t), want access key 7", got, ok)
			}
			if request.URL.Query().Has("key") {
				t.Fatalf("downstream query still contains key: %q", request.URL.RawQuery)
			}
		})
	}
}

func TestAuthenticateRejectsMissingDependenciesAndCredentials(t *testing.T) {
	request := &http.Request{Header: make(http.Header), URL: &url.URL{}}
	snapshot := &state.ConfigSnapshot{AccessKeysByHash: map[string]state.AccessKeyView{}}
	if _, ok := authenticate(nil, snapshot, testHasher{}); ok {
		t.Fatal("authenticate(nil request) succeeded")
	}
	if _, ok := authenticate(request, nil, testHasher{}); ok {
		t.Fatal("authenticate(nil snapshot) succeeded")
	}
	if _, ok := authenticate(request, snapshot, nil); ok {
		t.Fatal("authenticate(nil hasher) succeeded")
	}
	if _, ok := authenticate(request, snapshot, testHasher{}); ok {
		t.Fatal("authenticate() succeeded without a credential")
	}
}
