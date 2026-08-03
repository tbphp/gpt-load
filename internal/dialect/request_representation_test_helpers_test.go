package dialect

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/state"
)

func legacyRepresentationHeaderRules(extra map[string]string) state.HeaderRules {
	set := map[string]string{
		"Accept-Encoding":  "gzip",
		"Content-Encoding": "zstd",
		"Content-Length":   "999",
		"ETag":             `"stale"`,
		"Digest":           "sha-256=stale",
		"Content-MD5":      "stale-md5",
		"Content-Range":    "bytes 0-1/2",
		"Content-Digest":   "sha-256=:c3RhbGU=:",
		"Repr-Digest":      "sha-256=:c3RhbGU=:",
		"Signature":        "stale-signature",
		"Signature-Input":  "stale-signature-input",
		"X-Business":       "preserved",
	}
	for name, value := range extra {
		set[name] = value
	}
	return state.HeaderRules{Set: set}
}

func assertOutboundRequestRepresentation(
	t *testing.T,
	request *http.Request,
	wantBodyLength int64,
	wantHeaders map[string]string,
) {
	t.Helper()
	if values := requestHeaderFieldValues(request.Header, "Accept-Encoding"); !reflect.DeepEqual(values, []string{"identity"}) {
		t.Fatalf("Accept-Encoding values = %#v, want [identity]", values)
	}
	for _, name := range []string{
		"Content-Encoding",
		"Content-Length",
		"ETag",
		"Digest",
		"Content-MD5",
		"Content-Range",
		"Content-Digest",
		"Repr-Digest",
		"Signature",
		"Signature-Input",
	} {
		if values := requestHeaderFieldValues(request.Header, name); values != nil {
			t.Errorf("%s values = %#v, want absent", name, values)
		}
	}
	if request.ContentLength != wantBodyLength {
		t.Errorf("ContentLength = %d, want %d", request.ContentLength, wantBodyLength)
	}
	for name, want := range wantHeaders {
		if got := request.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func assertProbeRequestRepresentation(
	t *testing.T,
	request *http.Request,
	wantHeaders map[string]string,
) {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read probe request body: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("probe request body is empty")
	}
	assertOutboundRequestRepresentation(t, request, int64(len(body)), wantHeaders)
}

func requestHeaderFieldValues(headers http.Header, target string) []string {
	var values []string
	for name, fieldValues := range headers {
		if strings.EqualFold(name, target) {
			values = append(values, fieldValues...)
		}
	}
	return values
}
