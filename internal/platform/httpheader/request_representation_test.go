package httpheader

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeUpstreamRequestRepresentation(t *testing.T) {
	source := http.Header{
		"Accept-Encoding":  {"gzip"},
		"accept-encoding":  {"br"},
		"Content-Encoding": {"zstd"},
		"content-encoding": {"deflate"},
		"Content-Length":   {"999"},
		"content-length":   {"998"},
		"ETag":             {`"stale"`},
		"Digest":           {"sha-256=stale"},
		"Content-MD5":      {"stale-md5"},
		"Content-Range":    {"bytes 0-1/2"},
		"Content-Digest":   {"sha-256=:c3RhbGU=:"},
		"Repr-Digest":      {"sha-256=:c3RhbGU=:"},
		"Signature":        {"stale-signature"},
		"Signature-Input":  {"stale-signature-input"},
		"Authorization":    {"Bearer secret"},
		"Content-Type":     {"application/json"},
		"X-Business":       {"preserved"},
	}
	original := source.Clone()
	request := &http.Request{Header: source, ContentLength: 999}

	NormalizeUpstreamRequestRepresentation(request, 37)

	if request.ContentLength != 37 {
		t.Fatalf("ContentLength = %d, want 37", request.ContentLength)
	}
	if values := headerFieldValues(request.Header, "Accept-Encoding"); !reflect.DeepEqual(values, []string{"identity"}) {
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
		if values := headerFieldValues(request.Header, name); values != nil {
			t.Errorf("%s values = %#v, want absent", name, values)
		}
	}
	for name, want := range map[string]string{
		"Authorization": "Bearer secret",
		"Content-Type":  "application/json",
		"X-Business":    "preserved",
	} {
		if got := request.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if !reflect.DeepEqual(source, original) {
		t.Fatalf("source header map was mutated: got %#v, want %#v", source, original)
	}
}

func TestNormalizeUpstreamRequestRepresentationInitializesNilHeader(t *testing.T) {
	request := &http.Request{}

	NormalizeUpstreamRequestRepresentation(request, 0)

	if values := request.Header.Values("Accept-Encoding"); !reflect.DeepEqual(values, []string{"identity"}) {
		t.Fatalf("Accept-Encoding values = %#v, want [identity]", values)
	}
	if request.ContentLength != 0 {
		t.Fatalf("ContentLength = %d, want 0", request.ContentLength)
	}
}

func TestStripRequestRepresentationMetadataRemovesCaseCollidingFields(t *testing.T) {
	headers := http.Header{
		"Content-Encoding": {"gzip"},
		"content-length":   {"123"},
		"eTAG":             {`"stale"`},
		"dIGEST":           {"sha-256=stale"},
		"content-md5":      {"stale-md5"},
		"content-range":    {"bytes 0-1/2"},
		"content-digest":   {"sha-256=:c3RhbGU=12:"},
		"repr-digest":      {"sha-256=:c3RhbGU=12:"},
		"signature":        {"stale-signature"},
		"signature-input":  {"sig1=(\"etag\")"},
		"Content-Type":     {"application/json"},
	}

	StripRequestRepresentationMetadata(headers)

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
		if values := headerFieldValues(headers, name); values != nil {
			t.Errorf("%s values = %#v, want absent", name, values)
		}
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want preserved value", got)
	}
}

func headerFieldValues(headers http.Header, target string) []string {
	var values []string
	for name, fieldValues := range headers {
		if strings.EqualFold(name, target) {
			values = append(values, fieldValues...)
		}
	}
	return values
}
