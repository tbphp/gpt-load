package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mathrand "math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"gpt-load/internal/dialect"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
)

func TestReadBodyAtMostDoesNotReturnPartialBody(t *testing.T) {
	if body, overflow, err := readBodyAtMost(strings.NewReader("1234"), 4); err != nil || overflow || string(body) != "1234" {
		t.Fatalf("exact = %q, %t, %v", body, overflow, err)
	}
	if body, overflow, err := readBodyAtMost(strings.NewReader("12345"), 4); err != nil || !overflow || body != nil {
		t.Fatalf("overflow = %q, %t, %v", body, overflow, err)
	}

	wantErr := errors.New("read failed")
	body, overflow, err := readBodyAtMost(io.MultiReader(strings.NewReader("12"), responseReadError{err: wantErr}), 4)
	if !errors.Is(err, wantErr) || overflow || body != nil {
		t.Fatalf("read error = %q, %t, %v", body, overflow, err)
	}

	body, overflow, err = readBodyAtMost(strings.NewReader("x"), math.MaxInt64)
	if err != nil || overflow || string(body) != "x" {
		t.Fatalf("max int limit = %q, %t, %v", body, overflow, err)
	}

	body, overflow, err = readBodyAtMost(strings.NewReader("x"), -1)
	if err == nil || overflow || body != nil {
		t.Fatalf("negative limit = %q, %t, %v", body, overflow, err)
	}
}

func TestForwarderBoundsNonStreamingBodies(t *testing.T) {
	if maxNonStreamingResponseBodyBytes != 32<<20 || maxErrorResponseBodyBytes != 64<<10 {
		t.Fatalf("response limits = %d/%d", maxNonStreamingResponseBodyBytes, maxErrorResponseBodyBytes)
	}
	tests := []struct {
		name, key       string
		status          int
		size            int64
		wantProtocolErr bool
		wantPlaceholder bool
	}{
		{name: "success exact", key: "key", status: http.StatusOK, size: maxNonStreamingResponseBodyBytes},
		{name: "success plus one", key: "key", status: http.StatusOK, size: maxNonStreamingResponseBodyBytes + 1, wantProtocolErr: true},
		{name: "error exact", key: "key", status: http.StatusUnauthorized, size: maxErrorResponseBodyBytes},
		{name: "error plus one", key: "key", status: http.StatusUnauthorized, size: maxErrorResponseBodyBytes + 1, wantPlaceholder: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				writer.(http.Flusher).Flush()
				_, _ = io.CopyN(writer, repeatingByteReader('x'), test.size)
			}))
			defer upstream.Close()

			result := testForward(t, upstream.URL, test.key, 10*time.Second)
			if test.wantProtocolErr {
				if !errors.Is(result.Err, ErrUpstreamProtocol) || !result.RequestWritten || result.StatusCode != 0 ||
					result.Body != nil || result.ClassificationBody != nil {
					t.Fatalf("result = %#v", result)
				}
				return
			}
			if result.Err != nil || result.StatusCode != test.status {
				t.Fatalf("result = %#v", result)
			}
			if test.wantPlaceholder {
				if string(result.Body) != redact.Placeholder || string(result.ClassificationBody) != redact.Placeholder {
					t.Fatalf("safe bodies = %q/%q", result.Body, result.ClassificationBody)
				}
				return
			}
			if int64(len(result.Body)) != test.size {
				t.Fatalf("body length = %d, want %d", len(result.Body), test.size)
			}
		})
	}
}

func TestForwarderBoundsDecompressedErrorBodies(t *testing.T) {
	for _, encoding := range []string{"gzip", "br", "deflate", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			for _, size := range []int{1 << 20, 1<<20 + 1} {
				plain := bytes.Repeat([]byte("x"), size)
				wire := compressResponseWithBoundedZstdWindow(t, encoding, plain)
				upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("Content-Encoding", encoding)
					writer.WriteHeader(http.StatusUnauthorized)
					_, _ = writer.Write(wire)
				}))

				result := testForward(t, upstream.URL, "key", 10*time.Second)
				upstream.Close()
				if result.Err != nil || result.StatusCode != http.StatusUnauthorized {
					t.Fatalf("size %d result = %#v", size, result)
				}
				if size == 1<<20 {
					if !bytes.Equal(result.Body, wire) || !bytes.Equal(result.ClassificationBody, plain) ||
						result.Header.Get("Content-Encoding") != encoding {
						t.Fatalf("exact limit result body lengths = %d/%d, headers=%#v", len(result.Body), len(result.ClassificationBody), result.Header)
					}
				} else if string(result.Body) != redact.Placeholder ||
					string(result.ClassificationBody) != redact.Placeholder || result.Header.Get("Content-Encoding") != "" {
					t.Fatalf("overflow result = %#v", result)
				}
			}
		})
	}
}

func TestForwarderFailsClosedWhenRedactionExpandsErrorBeyondBounds(t *testing.T) {
	tests := []struct {
		name     string
		encoding string
		plain    []byte
	}{
		{
			name:  "identity wire exceeds limit after redaction",
			plain: bytes.Repeat([]byte("a"), int(maxErrorResponseBodyBytes)),
		},
		{
			name:     "gzip classification body exceeds limit after redaction",
			encoding: "gzip",
			plain:    bytes.Repeat([]byte("a"), int(maxDecompressedErrorBodyBytes)),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := test.plain
			if test.encoding != "" {
				var err error
				wire, err = utils.CompressResponse(test.encoding, test.plain)
				if err != nil {
					t.Fatal(err)
				}
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if test.encoding != "" {
					writer.Header().Set("Content-Encoding", test.encoding)
				}
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			result := testForward(t, upstream.URL, "a", time.Second)
			if result.Err != nil || result.StatusCode != http.StatusUnauthorized ||
				string(result.Body) != redact.Placeholder ||
				string(result.ClassificationBody) != redact.Placeholder ||
				result.Header.Get("Content-Encoding") != "" {
				t.Fatalf(
					"result status=%d body=%d classification=%d encoding=%q err=%v",
					result.StatusCode, len(result.Body), len(result.ClassificationBody),
					result.Header.Get("Content-Encoding"), result.Err,
				)
			}
		})
	}
}

func TestPrepareErrorBodyFailsClosedForOversizedUnchangedWire(t *testing.T) {
	plain := make([]byte, 100<<10)
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_."
	random := mathrand.New(mathrand.NewSource(1))
	for index := range plain {
		plain[index] = alphabet[random.Intn(len(alphabet))]
	}
	gzipWire, err := utils.CompressResponse("gzip", plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(gzipWire) <= int(maxErrorResponseBodyBytes) {
		t.Fatalf("gzip fixture length = %d, want oversized wire", len(gzipWire))
	}

	tests := []struct {
		name    string
		headers http.Header
		wire    []byte
	}{
		{
			name:    "identity",
			headers: make(http.Header),
			wire:    bytes.Repeat([]byte("x"), int(maxErrorResponseBodyBytes)+1),
		},
		{
			name:    "gzip",
			headers: http.Header{"Content-Encoding": {"gzip"}},
			wire:    gzipWire,
		},
	}
	forwarder := &Forwarder{redactor: redact.New()}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := test.headers.Clone()
			wire, classification := forwarder.prepareErrorBody(headers, test.wire, ForwardInput{})
			if string(wire) != redact.Placeholder || string(classification) != redact.Placeholder ||
				headers.Get("Content-Encoding") != "" {
				t.Fatalf(
					"safe body lengths=%d/%d encoding=%q",
					len(wire), len(classification), headers.Get("Content-Encoding"),
				)
			}
		})
	}
}

func compressResponseWithBoundedZstdWindow(t *testing.T, encoding string, plain []byte) []byte {
	t.Helper()
	if encoding != "zstd" {
		wire, err := utils.CompressResponse(encoding, plain)
		if err != nil {
			t.Fatal(err)
		}
		return wire
	}
	encoder, err := zstd.NewWriter(
		nil,
		zstd.WithWindowSize(int(maxDecompressedErrorBodyBytes)),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(plain, nil)
}

func TestForwarderDoesNotReturnPartialBodyOnReadError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, buffer, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack response: %v", err)
			return
		}
		defer connection.Close()
		_, _ = fmt.Fprint(buffer, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\n1234")
		_ = buffer.Flush()
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "key", time.Second)
	if result.Err == nil || !result.RequestWritten || result.StatusCode != 0 || result.Body != nil || result.ClassificationBody != nil {
		t.Fatalf("result = %#v", result)
	}
}

type repeatingByteReader byte

func (reader repeatingByteReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = byte(reader)
	}
	return len(destination), nil
}

type responseReadError struct {
	err error
}

func (reader responseReadError) Read([]byte) (int, error) {
	return 0, reader.err
}

func TestSanitizeUpstreamResponseHeadersRemovesCurrentCredential(t *testing.T) {
	const secret = "provider-secret"
	source := http.Header{
		"Authorization":       {"Bearer unrelated"},
		"Proxy-Authorization": {"unrelated"},
		"Api-Key":             {"unrelated"},
		"X-Api-Key":           {"unrelated"},
		"X-Goog-Api-Key":      {"unrelated"},
		"X-Echo":              {"prefix-" + secret + "-suffix"},
		"X-Multi":             {"safe", secret},
		"X-Safe":              {"kept"},
	}
	before := source.Clone()
	got := sanitizeUpstreamResponseHeaders(source, secret)
	for _, name := range []string{
		"Authorization", "Proxy-Authorization", "Api-Key",
		"X-Api-Key", "X-Goog-Api-Key", "X-Echo", "X-Multi",
	} {
		if got.Values(name) != nil {
			t.Fatalf("Header %s survived: %#v", name, got.Values(name))
		}
	}
	if got.Get("X-Safe") != "kept" || !reflect.DeepEqual(source, before) {
		t.Fatalf("safe/source headers = %#v / %#v", got, source)
	}
}

func TestSanitizeUpstreamResponseHeadersHandlesNonCanonicalFieldNames(t *testing.T) {
	const secret = "provider-secret-noncanonical"
	source := http.Header{
		"authorization":       {"Bearer unrelated"},
		"pRoXy-AuThOrIzAtIoN": {"unrelated"},
		"aPi-kEy":             {"unrelated"},
		"x-aPi-kEy":           {"unrelated"},
		"x-gOoG-aPi-kEy":      {"unrelated"},
		"X-Echo":              {"prefix-" + secret},
		"x-echo":              {"suffix-" + secret},
		"X-Safe":              {"kept"},
	}
	before := source.Clone()

	got := sanitizeUpstreamResponseHeaders(source, secret)

	credentialNames := []string{
		"Authorization", "Proxy-Authorization", "Api-Key",
		"X-Api-Key", "X-Goog-Api-Key",
	}
	for actualName, values := range got {
		for _, credentialName := range credentialNames {
			if strings.EqualFold(actualName, credentialName) {
				t.Fatalf("credential Header %q survived: %#v", actualName, values)
			}
		}
		if headerValuesContainLiteral(values, secret) {
			t.Fatalf("Header %q retained current key: %#v", actualName, values)
		}
	}
	if safe := got["X-Safe"]; len(safe) != 1 || safe[0] != "kept" || !reflect.DeepEqual(source, before) {
		t.Fatalf("safe/source headers = %#v / %#v", got, source)
	}
}

func TestSanitizeUpstreamResponseHeadersRemovesAllCasingsOfMatchedField(t *testing.T) {
	const secret = "provider-secret-duplicate-casing"
	source := http.Header{
		"X-Echo": {"safe"},
		"x-echo": {"prefix-" + secret},
		"X-Safe": {"kept"},
	}
	before := source.Clone()

	got := sanitizeUpstreamResponseHeaders(source, secret)

	for actualName := range got {
		if strings.EqualFold(actualName, "X-Echo") {
			t.Fatalf("logical Header X-Echo survived as %q: %#v", actualName, got[actualName])
		}
	}
	if safe := got["X-Safe"]; len(safe) != 1 || safe[0] != "kept" || !reflect.DeepEqual(source, before) {
		t.Fatalf("safe/source headers = %#v / %#v", got, source)
	}
}

func TestSanitizeForwardResponseHeadersPreservesRepresentationHeaderLiteralCollisions(t *testing.T) {
	for _, test := range []struct {
		name, upstreamModel, headerName, headerValue string
	}{
		{name: "content encoding", upstreamModel: "gzip", headerName: "Content-Encoding", headerValue: "gzip"},
		{name: "content type", upstreamModel: "json", headerName: "Content-Type", headerValue: "application/json"},
		{name: "content type preserves case", upstreamModel: "JSON", headerName: "Content-Type", headerValue: "application/JSON"},
		{name: "problem json content type", upstreamModel: "problem", headerName: "Content-Type", headerValue: "application/problem+json"},
		{name: "ndjson content type", upstreamModel: "ndjson", headerName: "Content-Type", headerValue: "application/x-ndjson"},
		{name: "event stream content type", upstreamModel: "event", headerName: "Content-Type", headerValue: "text/event-stream"},
		{name: "plain text content type", upstreamModel: "plain", headerName: "Content-Type", headerValue: "text/plain"},
		{name: "content length", upstreamModel: "42", headerName: "Content-Length", headerValue: "42"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := http.Header{
				test.headerName:    {test.headerValue},
				"X-Upstream-Model": {test.upstreamModel},
			}
			got := sanitizeForwardResponseHeaders(source, ForwardInput{
				ExternalModel: "public-model", UpstreamModelID: test.upstreamModel,
			})

			if got.Get(test.headerName) != test.headerValue {
				t.Fatalf("%s = %q, want %q", test.headerName, got.Get(test.headerName), test.headerValue)
			}
			if got.Get("X-Upstream-Model") != "" {
				t.Fatalf("X-Upstream-Model survived: %#v", got)
			}
		})
	}
}

func TestSanitizeForwardResponseHeadersRemovesAliasedModelFromFieldNames(t *testing.T) {
	const upstreamModel = "provider-model"
	source := http.Header{
		"X-Provider-Model-Quota": {"safe"},
		"x-provider-model-quota": {"also-safe"},
		"X-Safe":                 {"kept"},
	}
	got := sanitizeForwardResponseHeaders(source, ForwardInput{
		ExternalModel: "public-model", UpstreamModelID: upstreamModel,
	})

	for name := range got {
		if strings.Contains(strings.ToLower(name), upstreamModel) {
			t.Fatalf("Header field name leaked aliased model as %q: %#v", name, got[name])
		}
	}
	if got.Get("X-Safe") != "kept" {
		t.Fatalf("safe header changed: %#v", got)
	}
}

func TestSanitizeForwardResponseHeadersPreservesSafeContentTypeFieldNameCollisions(t *testing.T) {
	for _, test := range []struct {
		name          string
		upstreamModel string
		contentType   string
	}{
		{name: "content in field name", upstreamModel: "content", contentType: "application/json"},
		{name: "type in field name", upstreamModel: "type", contentType: "text/event-stream"},
	} {
		t.Run(test.name, func(t *testing.T) {
			customHeader := "X-" + test.upstreamModel + "-Quota"
			source := http.Header{
				"Content-Type": {test.contentType},
				customHeader:   {"safe"},
			}
			got := sanitizeForwardResponseHeaders(source, ForwardInput{
				ExternalModel: "public-model", UpstreamModelID: test.upstreamModel,
			})

			if got.Get("Content-Type") != test.contentType {
				t.Fatalf("Content-Type = %q, want %q", got.Get("Content-Type"), test.contentType)
			}
			if got.Get(customHeader) != "" {
				t.Fatalf("custom Header field-name collision survived: %#v", got)
			}
		})
	}
}

func TestSanitizeForwardResponseHeadersInvalidatesSignaturesOnlyAfterDeletion(t *testing.T) {
	const upstreamModel = "provider-model"
	for _, test := range []struct {
		name   string
		source http.Header
	}{
		{
			name: "custom field name",
			source: http.Header{
				"X-Provider-Model-Quota": {"safe"},
			},
		},
		{
			name: "custom field value",
			source: http.Header{
				"X-Upstream": {"selected=" + upstreamModel},
			},
		},
		{
			name: "content type",
			source: http.Header{
				"Content-Type": {"application/vnd.provider-model+json"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := test.source.Clone()
			source["Signature"] = []string{"sig-canonical"}
			source["sIgNaTuRe"] = []string{"sig-noncanonical"}
			source["Signature-Input"] = []string{"input-canonical"}
			source["sIgNaTuRe-InPuT"] = []string{"input-noncanonical"}

			got := sanitizeForwardResponseHeaders(source, ForwardInput{
				ExternalModel: "public-model", UpstreamModelID: upstreamModel,
			})

			for name := range got {
				if strings.EqualFold(name, "Signature") || strings.EqualFold(name, "Signature-Input") {
					t.Fatalf("invalidated signature Header survived as %q: %#v", name, got[name])
				}
			}
		})
	}

	t.Run("alias without deletion preserves signatures", func(t *testing.T) {
		source := http.Header{
			"X-Safe":          {"kept"},
			"Signature":       {"sig"},
			"Signature-Input": {"input"},
		}
		got := sanitizeForwardResponseHeaders(source, ForwardInput{
			ExternalModel: "public-model", UpstreamModelID: upstreamModel,
		})
		if got.Get("Signature") != "sig" || got.Get("Signature-Input") != "input" {
			t.Fatalf("unchanged Header set lost signatures: %#v", got)
		}
	})

	t.Run("no alias preserves signatures", func(t *testing.T) {
		source := http.Header{
			"X-Upstream":      {"selected=" + upstreamModel},
			"Signature":       {"sig"},
			"Signature-Input": {"input"},
		}
		got := sanitizeForwardResponseHeaders(source, ForwardInput{
			ExternalModel: upstreamModel, UpstreamModelID: upstreamModel,
		})
		if got.Get("Signature") != "sig" || got.Get("Signature-Input") != "input" {
			t.Fatalf("non-alias Header set lost signatures: %#v", got)
		}
	})
}

func TestSanitizeForwardResponseHeadersRemovesAliasedModelFromContentTypeParameters(t *testing.T) {
	const upstreamModel = "provider-model"
	for _, test := range []struct {
		name, headerName, headerValue, upstreamModel string
	}{
		{
			name: "parameter value", headerName: "Content-Type",
			headerValue: `application/json; model=provider-model`, upstreamModel: upstreamModel,
		},
		{
			name: "parameter name", headerName: "Content-Type",
			headerValue: `application/json; provider-model=safe`, upstreamModel: upstreamModel,
		},
		{
			name: "malformed non-canonical field", headerName: "cOnTeNt-TyPe",
			headerValue: `application/json; model=provider-model; broken`, upstreamModel: upstreamModel,
		},
		{
			name: "malformed without parameter delimiter", headerName: "Content-Type",
			headerValue: `application/json provider-model`, upstreamModel: upstreamModel,
		},
		{
			name: "vendor media type", headerName: "Content-Type",
			headerValue: `application/vnd.provider-model+json`, upstreamModel: upstreamModel,
		},
		{
			name: "model crosses parameter delimiter", headerName: "Content-Type",
			headerValue: `application/json; model=safe`, upstreamModel: "json; model",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := http.Header{
				test.headerName: {test.headerValue},
				"X-Safe":        {"kept"},
			}
			got := sanitizeForwardResponseHeaders(source, ForwardInput{
				ExternalModel: "public-model", UpstreamModelID: test.upstreamModel,
			})

			for name := range got {
				if strings.EqualFold(name, "Content-Type") {
					t.Fatalf("logical Content-Type survived as %q: %#v", name, got[name])
				}
			}
			if got.Get("X-Safe") != "kept" {
				t.Fatalf("safe header changed: %#v", got)
			}
		})
	}
}

func TestSanitizeForwardResponseHeadersRemovesCaseSensitiveModelFromRawContentTypeParameters(t *testing.T) {
	source := http.Header{
		"Content-Type": {`application/JSON; JSON=safe`},
		"X-Safe":       {"kept"},
	}
	got := sanitizeForwardResponseHeaders(source, ForwardInput{
		ExternalModel: "public-model", UpstreamModelID: "JSON",
	})

	if got.Get("Content-Type") != "" {
		t.Fatalf("Content-Type survived: %#v", got)
	}
	if got.Get("X-Safe") != "kept" {
		t.Fatalf("safe header changed: %#v", got)
	}
}

func TestNonIdentityEncodingContainsKeyHandlesNonCanonicalFieldNames(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers http.Header
	}{
		{
			name: "lowercase encoding triggers success protocol failure",
			headers: http.Header{
				"content-encoding": {"gzip"},
			},
		},
		{
			name: "duplicate casing aggregates error response encodings",
			headers: http.Header{
				"Content-Encoding": {"identity"},
				"cOnTeNt-EnCoDiNg": {"gzip"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !nonIdentityEncodingContainsKey(test.headers, "gzip") {
				t.Fatalf("nonIdentityEncodingContainsKey(%#v, gzip) = false", test.headers)
			}
		})
	}
}

func TestForwarderFailsClosedWhenShortKeyMatchesContentEncoding(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{name: "success", status: http.StatusOK, wantErr: true},
		{name: "error", status: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire, err := utils.CompressResponse("gzip", []byte(`{"error":"safe"}`))
			if err != nil {
				t.Fatal(err)
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				w.WriteHeader(test.status)
				_, _ = w.Write(wire)
			}))
			defer upstream.Close()
			result := testForward(t, upstream.URL, "gzip", time.Second)
			if test.wantErr {
				if !errors.Is(result.Err, ErrUpstreamProtocol) || !result.RequestWritten {
					t.Fatalf("result = %#v", result)
				}
				return
			}
			if result.Err != nil || result.StatusCode != test.status ||
				string(result.Body) != redact.Placeholder || result.Header.Get("Content-Encoding") != "" {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestShortKeyErrorCollisionRemovesAllContentEncodingCasings(t *testing.T) {
	headers := http.Header{
		"Content-Encoding": {"identity"},
		"cOnTeNt-EnCoDiNg": {"gzip"},
	}
	if !nonIdentityEncodingContainsKey(headers, "identity") {
		t.Fatalf("nonIdentityEncodingContainsKey(%#v, identity) = false", headers)
	}

	result := UpstreamResult{StatusCode: http.StatusUnauthorized, RequestWritten: true}
	result.Body, result.ClassificationBody = failClosedErrorBody(headers)
	result.Header = sanitizeUpstreamResponseHeaders(headers, "identity")

	if result.StatusCode != http.StatusUnauthorized || string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("result = %#v", result)
	}
	for actualName := range result.Header {
		if strings.EqualFold(actualName, "Content-Encoding") {
			t.Fatalf("Content-Encoding survived as %q: %#v", actualName, result.Header[actualName])
		}
	}
}

func TestForwarderSanitizesResponseHeadersOnAllPaths(t *testing.T) {
	for _, test := range []struct {
		name   string
		stream bool
		status int
	}{
		{name: "nonstream success", status: http.StatusOK},
		{name: "nonstream error", status: http.StatusUnauthorized},
		{name: "stream success", stream: true, status: http.StatusOK},
		{name: "stream error", stream: true, status: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			const secret = "provider-secret-all-paths"
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("X-Echo", "prefix-"+secret)
				writer.Header().Set("X-Safe", "kept")
				writer.WriteHeader(test.status)
				if test.stream && test.status == http.StatusOK {
					_, _ = writer.Write([]byte("data: ok\n\n"))
					return
				}
				_, _ = writer.Write([]byte(`{"error":"safe"}`))
			}))
			defer upstream.Close()

			forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
			input := streamForwardInput(upstream.URL)
			input.APIKey = secret
			downstream := newRecordingResponseWriter()
			var result UpstreamResult
			if test.stream {
				result = forwarder.ForwardStream(t.Context(), input, downstream)
			} else {
				result = forwarder.Forward(t.Context(), input)
			}
			if result.Err != nil || result.Header.Get("X-Echo") != "" || result.Header.Get("X-Safe") != "kept" {
				t.Fatalf("result = %#v", result)
			}
			if test.stream && test.status == http.StatusOK &&
				(downstream.header.Get("X-Echo") != "" || downstream.header.Get("X-Safe") != "kept") {
				t.Fatalf("downstream headers = %#v", downstream.header)
			}
		})
	}
}

func TestForwarderSanitizesResponseHeadersWithCurrentAttemptKey(t *testing.T) {
	for _, secret := range []string{"secret-one", "secret-two"} {
		t.Run(secret, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("X-Echo", "prefix-"+secret)
				writer.Header().Set("X-Safe", "kept")
				_, _ = writer.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()

			result := testForward(t, upstream.URL, secret, time.Second)
			if result.Err != nil || result.Header.Get("X-Echo") != "" ||
				result.Header.Get("X-Safe") != "kept" {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestForwardStreamForcesIdentityAfterHeaderRules(t *testing.T) {
	tests := []struct {
		name  string
		rules state.HeaderRules
	}{
		{
			name: "set cannot override identity",
			rules: state.HeaderRules{Set: map[string]string{
				"Accept-Encoding": "gzip",
				"X-Custom":        "prefix-${API_KEY}",
			}},
		},
		{
			name:  "remove cannot delete identity",
			rules: state.HeaderRules{Remove: []string{"Accept-Encoding"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := make(chan http.Header, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				received <- request.Header.Clone()
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte("data: ok\n\n"))
			}))
			defer upstream.Close()

			forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
			downstream := newRecordingResponseWriter()
			input := streamForwardInput(upstream.URL)
			input.Group.HeaderRules = tt.rules
			result := forwarder.ForwardStream(context.Background(), input, downstream)

			if result.Err != nil || !result.Committed {
				t.Fatalf("ForwardStream() result = %#v", result)
			}
			headers := <-received
			if got := headers.Get("Accept-Encoding"); got != "identity" {
				t.Fatalf("Accept-Encoding = %q, want identity", got)
			}
			if got := headers.Get("Authorization"); got != "Bearer sk-upstream-secret" {
				t.Fatalf("Authorization = %q", got)
			}
			if tt.rules.Set != nil && headers.Get("X-Custom") != "prefix-sk-upstream-secret" {
				t.Fatalf("X-Custom = %q", headers.Get("X-Custom"))
			}
		})
	}
}

func TestForwardAliasForcesIdentityAfterHeaderRules(t *testing.T) {
	var receivedHeader http.Header
	var receivedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedHeader = request.Header.Clone()
		receivedBody, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"public"}`)
	input.ExternalModel = "public"
	input.UpstreamModelID = "provider"
	input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{"Accept-Encoding": "gzip"}}
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	result := forwarder.Forward(context.Background(), input)

	if result.Err != nil || result.StatusCode != http.StatusOK {
		t.Fatalf("Forward() result = %#v", result)
	}
	if receivedHeader.Get("Accept-Encoding") != "identity" {
		t.Fatalf("Accept-Encoding = %q, want identity", receivedHeader.Get("Accept-Encoding"))
	}
	if string(receivedBody) != `{"model":"provider"}` {
		t.Fatalf("upstream body = %s, want provider model", receivedBody)
	}
}

func TestForwarderRewritesAliasedNonStreamingResponsesAtBounds(t *testing.T) {
	t.Run("unsupported response encoding fails closed", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Content-Encoding", "gzip")
			_, _ = writer.Write([]byte(`{"model":"provider"}`))
		}))
		defer upstream.Close()

		input := streamForwardInput(upstream.URL)
		input.Request.Body = []byte(`{"model":"public"}`)
		input.ExternalModel = "public"
		input.UpstreamModelID = "provider"
		result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
			context.Background(), input,
		)

		if !errors.Is(result.Err, ErrUpstreamProtocol) || !result.RequestWritten || result.StatusCode != 0 {
			t.Fatalf("Forward() result = %#v, want uninspectable alias response protocol error", result)
		}
	})

	t.Run("rewrite expansion cannot exceed response bound", func(t *testing.T) {
		const prefix = `{"model":"p","padding":"`
		const suffix = `"}`
		padding := strings.Repeat("x", int(maxNonStreamingResponseBodyBytes)-len(prefix)-len(suffix))
		responseBody := prefix + padding + suffix
		if int64(len(responseBody)) != maxNonStreamingResponseBodyBytes {
			t.Fatalf("test response size = %d", len(responseBody))
		}
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Content-Length", strconv.Itoa(len(responseBody)))
			_, _ = io.WriteString(writer, responseBody)
		}))
		defer upstream.Close()

		input := streamForwardInput(upstream.URL)
		input.Request.Body = []byte(`{"model":"public"}`)
		input.ExternalModel = "public"
		input.UpstreamModelID = "p"
		result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
			context.Background(), input,
		)

		if !errors.Is(result.Err, ErrUpstreamProtocol) || !result.RequestWritten || result.StatusCode != 0 {
			t.Fatalf("Forward() result = %#v, want rewritten response overflow", result)
		}
		if strings.Contains(result.Err.Error(), "%!w(<nil>)") {
			t.Fatalf("overflow error formats nil rewrite error: %v", result.Err)
		}
	})
}

type readyGuardWriter struct {
	*recordingResponseWriter
	ready            *atomic.Bool
	committedTooSoon atomic.Bool
}

func (writer *readyGuardWriter) WriteHeader(status int) {
	if !writer.ready.Load() {
		writer.committedTooSoon.Store(true)
	}
	writer.recordingResponseWriter.WriteHeader(status)
}

func TestForwardStreamCallsReadyBeforeCommit(t *testing.T) {
	releaseUpstream := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUpstream) }) }

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: ok\n\n")
		writer.(http.Flusher).Flush()
		<-releaseUpstream
	}))
	defer func() {
		release()
		upstream.Close()
	}()

	var ready atomic.Bool
	var calls atomic.Int32
	readyCalled := make(chan struct{})
	var readyOnce sync.Once
	input := streamForwardInput(upstream.URL)
	input.OnStreamReady = func() {
		calls.Add(1)
		ready.Store(true)
		readyOnce.Do(func() { close(readyCalled) })
	}
	downstream := &readyGuardWriter{
		recordingResponseWriter: newRecordingResponseWriter(),
		ready:                   &ready,
	}
	done := make(chan UpstreamResult, 1)
	go func() {
		done <- NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
			context.Background(), input, downstream,
		)
	}()

	waitForSignal(t, readyCalled, "stream-ready callback")
	if calls.Load() != 1 {
		t.Fatalf("stream-ready calls while upstream is active = %d, want 1", calls.Load())
	}
	select {
	case result := <-done:
		t.Fatalf("ForwardStream() returned before upstream release: %#v", result)
	default:
	}
	release()

	select {
	case result := <-done:
		if result.Err != nil || !result.Committed || calls.Load() != 1 ||
			downstream.committedTooSoon.Load() {
			t.Fatalf("result=%#v calls=%d committedTooSoon=%t",
				result, calls.Load(), downstream.committedTooSoon.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("ForwardStream() did not finish after upstream release")
	}
}

func TestForwardStreamRejectsUnsupportedSuccessEncodingBeforeCommit(t *testing.T) {
	tests := []struct {
		name       string
		encodings  []string
		wantCommit bool
	}{
		{name: "missing encoding", wantCommit: true},
		{name: "empty encoding", encodings: []string{""}, wantCommit: true},
		{name: "identity", encodings: []string{" identity "}, wantCommit: true},
		{name: "gzip", encodings: []string{"gzip"}},
		{name: "encoding list", encodings: []string{"identity, gzip"}},
		{name: "multiple values", encodings: []string{"identity", "gzip"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				for _, encoding := range test.encodings {
					writer.Header().Add("Content-Encoding", encoding)
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte("data: ok\n\n"))
			}))
			defer upstream.Close()

			var calls atomic.Int32
			input := streamForwardInput(upstream.URL)
			input.OnStreamReady = func() { calls.Add(1) }
			forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
			downstream := newRecordingResponseWriter()
			result := forwarder.ForwardStream(context.Background(), input, downstream)

			if test.wantCommit {
				if result.Err != nil || !result.Committed ||
					downstream.body.String() != "data: ok\n\n" || calls.Load() != 1 {
					t.Fatalf("ForwardStream() valid result = %#v, body=%q calls=%d",
						result, downstream.body.String(), calls.Load())
				}
				return
			}
			if !errors.Is(result.Err, ErrUpstreamProtocol) || result.Committed ||
				!result.RetryableBeforeCommit || calls.Load() != 0 {
				t.Fatalf("ForwardStream() protocol result = %#v, calls=%d", result, calls.Load())
			}
			if downstream.status != 0 || downstream.body.Len() != 0 || downstream.flushes != 0 {
				t.Fatalf("downstream was touched before protocol rejection: %#v", downstream)
			}
		})
	}
}

func TestForwardStreamRejectsOversizedFirstEventAsProtocolError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(":" + strings.Repeat("x", maxFirstSSEEventBytes)))
	}))
	defer upstream.Close()

	var calls atomic.Int32
	input := streamForwardInput(upstream.URL)
	input.OnStreamReady = func() { calls.Add(1) }
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	downstream := newRecordingResponseWriter()
	result := forwarder.ForwardStream(context.Background(), input, downstream)

	if !errors.Is(result.Err, ErrUpstreamProtocol) ||
		!errors.Is(result.Err, errFirstSSEEventTooLarge) ||
		result.Committed || !result.RetryableBeforeCommit || calls.Load() != 0 {
		t.Fatalf("ForwardStream() result = %#v calls=%d, want retryable pre-commit protocol error",
			result, calls.Load())
	}
	if downstream.status != 0 || downstream.body.Len() != 0 || downstream.flushes != 0 {
		t.Fatalf("downstream was touched before oversized event rejection: %#v", downstream)
	}
}

func TestForwardStreamRejectsOversizedAliasedEventAsProtocolError(t *testing.T) {
	event := "data: " + strings.Repeat("x", maxSSEEventBytes-len("data: \n\n")+1) + "\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, event)
		writer.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"public","stream":true}`)
	input.ExternalModel = "public"
	input.UpstreamModelID = "provider"
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if !errors.Is(result.Err, ErrUpstreamProtocol) || !errors.Is(result.Err, errSSEEventTooLarge) ||
		result.Committed || !result.RetryableBeforeCommit {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
}

func TestForwardStreamRejectsMalformedAliasedEventAsProtocolError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: not-json\n\n")
		writer.(http.Flusher).Flush()
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"public","stream":true}`)
	input.ExternalModel = "public"
	input.UpstreamModelID = "provider"
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if !errors.Is(result.Err, ErrUpstreamProtocol) || result.Committed || !result.RetryableBeforeCommit {
		t.Fatalf("ForwardStream() result = %#v, want retryable pre-commit protocol error", result)
	}
	if downstream.status != 0 || downstream.body.Len() != 0 || downstream.flushes != 0 {
		t.Fatalf("downstream was touched before malformed event rejection: %#v", downstream)
	}
}

func TestForwardStreamSanitizesAliasedErrorEventPayloads(t *testing.T) {
	const (
		upstreamModel = "org/model"
		externalModel = "public-model"
		secret        = "stream/secret"
	)
	stream := "event: error\n" +
		`data: {"model":"org/model","org\/model":"org\u002fmodel failed","credential":"stream\u002fsecret"}` + "\n\n" +
		"event: error\n" +
		`data: {"message":"later org\/model stream\u002fsecret"}` + "\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, stream)
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.ExternalModel = externalModel
	input.UpstreamModelID = upstreamModel
	input.APIKey = secret
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if result.Err != nil || !result.Committed {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	payloads := decodeSSEJSONPayloads(t, downstream.body.Bytes())
	if len(payloads) != 2 {
		t.Fatalf("decoded payload count = %d, wire=%q", len(payloads), downstream.body.String())
	}
	if payloads[0]["model"] != externalModel ||
		payloads[0][externalModel] != externalModel+" failed" ||
		payloads[0]["credential"] != redact.Placeholder {
		t.Fatalf("first error payload = %#v", payloads[0])
	}
	if payloads[1]["message"] != "later "+externalModel+" "+redact.Placeholder {
		t.Fatalf("later error payload = %#v", payloads[1])
	}
}

func TestForwardStreamSanitizesUnaliasedErrorEventPayloads(t *testing.T) {
	const secret = "stream/secret"
	stream := "event: error\n" +
		`data: {"credential":"stream\u002fsecret","raw":"stream/secret"}` + "\n\n" +
		"event: error\n" +
		`data: {"message":"later stream\/secret"}` + "\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		setRepresentationMetadata(writer.Header())
		_, _ = io.WriteString(writer, stream)
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.APIKey = secret
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if result.Err != nil || !result.Committed {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	payloads := decodeSSEJSONPayloads(t, downstream.body.Bytes())
	if len(payloads) != 2 || payloads[0]["credential"] != redact.Placeholder ||
		payloads[0]["raw"] != redact.Placeholder ||
		payloads[1]["message"] != "later "+redact.Placeholder {
		t.Fatalf("sanitized unaliased payloads = %#v, wire=%q", payloads, downstream.body.String())
	}
	assertRepresentationMetadata(t, result.Header, false)
}

func TestForwardStreamPreservesHeuristicCredentialLikeContent(t *testing.T) {
	for _, test := range []struct {
		name          string
		externalModel string
		upstreamModel string
		wantModel     string
	}{
		{name: "no alias"},
		{
			name: "alias", externalModel: "public-model",
			upstreamModel: "provider-model", wantModel: "public-model",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := `{"token":"demo-value","api_key":"example"}`
			if test.upstreamModel != "" {
				payload = `{"model":"provider-model","token":"demo-value","api_key":"example"}`
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, "data: "+payload+"\n\n")
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.APIKey = "actual/secret"
			input.ExternalModel = test.externalModel
			input.UpstreamModelID = test.upstreamModel
			downstream := newRecordingResponseWriter()
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
				context.Background(), input, downstream,
			)

			if result.Err != nil || !result.Committed {
				t.Fatalf("ForwardStream() result = %#v", result)
			}
			payloads := decodeSSEJSONPayloads(t, downstream.body.Bytes())
			if len(payloads) != 1 || payloads[0]["token"] != "demo-value" || payloads[0]["api_key"] != "example" {
				t.Fatalf("preserved payloads = %#v, wire=%q", payloads, downstream.body.String())
			}
			if test.wantModel != "" && payloads[0]["model"] != test.wantModel {
				t.Fatalf("rewritten model = %#v, want %q", payloads[0]["model"], test.wantModel)
			}
		})
	}
}

func TestForwardStreamUnaliasedCredentialRewriteFailureRespectsCommitBoundary(t *testing.T) {
	const (
		secret    = "secret"
		collision = `{"secret":"first","[REDACTED]":"second"}`
	)
	for _, test := range []struct {
		name       string
		stream     string
		wantCommit bool
		wantBody   string
	}{
		{
			name:   "first error event fails before commit",
			stream: "event: error\ndata: " + collision + "\n\n",
		},
		{
			name: "later error event terminates committed stream",
			stream: `data: {"ok":true}` + "\n\n" +
				"event: error\ndata: " + collision + "\n\n",
			wantCommit: true,
			wantBody:   `data: {"ok":true}` + "\n\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.stream)
			}))
			defer upstream.Close()

			var calls atomic.Int32
			input := streamForwardInput(upstream.URL)
			input.APIKey = secret
			input.OnStreamReady = func() { calls.Add(1) }
			downstream := newRecordingResponseWriter()
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
				context.Background(), input, downstream,
			)

			wantCalls := int32(0)
			if test.wantCommit {
				wantCalls = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("stream-ready calls = %d, want %d", calls.Load(), wantCalls)
			}
			if !errors.Is(result.Err, ErrUpstreamProtocol) || result.Committed != test.wantCommit {
				t.Fatalf("ForwardStream() result = %#v, want protocol error committed=%t", result, test.wantCommit)
			}
			if !test.wantCommit && !result.RetryableBeforeCommit {
				t.Fatalf("pre-commit failure is not retryable: %#v", result)
			}
			if downstream.body.String() != test.wantBody {
				t.Fatalf("downstream body = %q, want %q", downstream.body.String(), test.wantBody)
			}
		})
	}
}

func TestForwardStreamRedactsEscapedAPIKeyBeforeOverlappingModelLiteral(t *testing.T) {
	const (
		upstreamModel = "provider"
		externalModel = "public"
		secret        = "provider/secret"
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, `event: error
data: {"message":"provider\u002fsecret"}

`)
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.ExternalModel = externalModel
	input.UpstreamModelID = upstreamModel
	input.APIKey = secret
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if result.Err != nil || !result.Committed {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	payloads := decodeSSEJSONPayloads(t, downstream.body.Bytes())
	if len(payloads) != 1 || payloads[0]["message"] != redact.Placeholder {
		t.Fatalf("sanitized overlapping payloads = %#v, wire=%q", payloads, downstream.body.String())
	}
}

func TestForwardStreamAliasPayloadRewriteFailureRespectsCommitBoundary(t *testing.T) {
	const (
		upstreamModel = "provider-model"
		externalModel = "public-model"
		collision     = `{"provider-model":"first","public-model":"second"}`
	)
	for _, test := range []struct {
		name       string
		stream     string
		wantCommit bool
		wantBody   string
	}{
		{
			name:   "first error event fails before commit",
			stream: "event: error\ndata: " + collision + "\n\n",
		},
		{
			name: "later error event terminates committed stream",
			stream: `data: {"model":"provider-model","delta":"ok"}` + "\n\n" +
				"event: error\ndata: " + collision + "\n\n",
			wantCommit: true,
			wantBody:   `data: {"delta":"ok","model":"public-model"}` + "\n\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.stream)
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.ExternalModel = externalModel
			input.UpstreamModelID = upstreamModel
			downstream := newRecordingResponseWriter()
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
				context.Background(), input, downstream,
			)

			if !errors.Is(result.Err, ErrUpstreamProtocol) || result.Committed != test.wantCommit {
				t.Fatalf("ForwardStream() result = %#v, want protocol error committed=%t", result, test.wantCommit)
			}
			if !test.wantCommit && !result.RetryableBeforeCommit {
				t.Fatalf("pre-commit failure is not retryable: %#v", result)
			}
			if downstream.body.String() != test.wantBody {
				t.Fatalf("downstream body = %q, want %q", downstream.body.String(), test.wantBody)
			}
		})
	}
}

func TestForwardStreamReturnsSafeBoundedNonSuccessResponse(t *testing.T) {
	const secret = "custom-upstream-secret"
	tests := []struct {
		name     string
		body     string
		wantBody string
	}{
		{name: "inspectable", body: `{"error":{"api_key":"` + secret + `"}}`, wantBody: `{"error":{"api_key":"[REDACTED]"}}`},
		{name: "over limit", body: strings.Repeat("x", maxStreamingErrorBodyBytes) + secret, wantBody: redact.Placeholder},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				setRepresentationMetadata(writer.Header())
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write([]byte(tt.body))
			}))
			defer upstream.Close()

			forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
			downstream := newRecordingResponseWriter()
			input := streamForwardInput(upstream.URL)
			input.APIKey = secret
			result := forwarder.ForwardStream(context.Background(), input, downstream)

			if result.Err != nil || result.Committed || result.StatusCode != http.StatusUnauthorized {
				t.Fatalf("ForwardStream() result = %#v", result)
			}
			if string(result.Body) != tt.wantBody || string(result.ClassificationBody) != tt.wantBody {
				t.Fatalf("safe bodies = %q / %q, want %q", result.Body, result.ClassificationBody, tt.wantBody)
			}
			if bytes.Contains(result.Body, []byte(secret)) || bytes.Contains(result.ClassificationBody, []byte(secret)) {
				t.Fatal("streaming error result leaked plaintext key")
			}
			if downstream.status != 0 || downstream.body.Len() != 0 {
				t.Fatal("ForwardStream() wrote non-success response before Handler verdict")
			}
			if tt.name == "over limit" {
				if result.Header.Get("Content-Length") != strconv.Itoa(len(redact.Placeholder)) {
					t.Fatalf("Content-Length = %q", result.Header.Get("Content-Length"))
				}
				assertRepresentationMetadata(t, result.Header, false)
			}
		})
	}
}

func TestForwardStreamTimesOutBeforeCompleteFirstEvent(t *testing.T) {
	upstreamCanceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("data: partial\n"))
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(upstreamCanceled)
	}))
	defer upstream.Close()

	var calls atomic.Int32
	input := streamForwardInput(upstream.URL)
	input.Group.Timeouts.FirstByte = 25 * time.Millisecond
	input.OnStreamReady = func() { calls.Add(1) }
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	downstream := newRecordingResponseWriter()
	result := forwarder.ForwardStream(context.Background(), input, downstream)

	if !errors.Is(result.Err, context.DeadlineExceeded) || result.Committed ||
		!result.RetryableBeforeCommit || calls.Load() != 0 {
		t.Fatalf("ForwardStream() timeout result = %#v calls=%d", result, calls.Load())
	}
	if downstream.status != 0 || downstream.body.Len() != 0 {
		t.Fatalf("partial event reached downstream: status/body=%d/%q", downstream.status, downstream.body.String())
	}
	select {
	case <-upstreamCanceled:
	case <-time.After(time.Second):
		t.Fatal("first-event timeout did not cancel upstream request")
	}
}

func TestForwardStreamDoesNotRetryParentDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	result := forwarder.ForwardStream(ctx, streamForwardInput("http://127.0.0.1:1"), newRecordingResponseWriter())

	if !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("ForwardStream() error = %v, want parent deadline", result.Err)
	}
	if result.RetryableBeforeCommit {
		t.Fatalf("ForwardStream() marked parent deadline retryable: %#v", result)
	}
}

func TestReleaseCommittedRequestReplayReleasesParsedBodyWithoutMutatingHTTPRequest(t *testing.T) {
	input := streamForwardInput("https://example.com")
	wantBody := bytes.Clone(input.Request.Body)
	request, _, replay, err := newUpstreamRequest(context.Background(), input, true)
	if err != nil {
		t.Fatalf("newUpstreamRequest() error = %v", err)
	}
	if request.GetBody == nil {
		t.Fatal("newUpstreamRequest() GetBody is nil; 307/308 cannot replay the request")
	}
	redirectBody, err := request.GetBody()
	if err != nil {
		t.Fatalf("GetBody() before release error = %v", err)
	}
	redirectPayload, err := io.ReadAll(redirectBody)
	_ = redirectBody.Close()
	if err != nil {
		t.Fatalf("read GetBody() before release: %v", err)
	}
	if !bytes.Equal(redirectPayload, wantBody) {
		t.Fatalf("GetBody() before release = %q, want %q", redirectPayload, wantBody)
	}
	originalBody := request.Body

	releaseCommittedRequestReplay(input.Request, replay)

	if input.Request.Body != nil {
		t.Fatal("ParsedRequest.Body still retains the replay buffer")
	}
	if request.Body != originalBody || request.GetBody == nil {
		t.Fatal("committed release mutated fields owned by the HTTP transport")
	}
	activeBody, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read active request body: %v", err)
	}
	if !bytes.Equal(activeBody, wantBody) {
		t.Fatalf("active request body = %q, want %q", activeBody, wantBody)
	}
	replayed, err := request.GetBody()
	if err != nil {
		t.Fatalf("GetBody() after release error = %v", err)
	}
	futureBody, err := io.ReadAll(replayed)
	_ = replayed.Close()
	if err != nil {
		t.Fatalf("read GetBody() after release: %v", err)
	}
	if len(futureBody) != 0 {
		t.Fatalf("GetBody() after release = %q, want empty", futureBody)
	}
}

func TestForwardStreamPreservesParsedRequestBeforeCommit(t *testing.T) {
	requestStarted := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		close(requestStarted)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: partial\n"))
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer upstream.Close()

	var calls atomic.Int32
	input := streamForwardInput(upstream.URL)
	input.OnStreamReady = func() { calls.Add(1) }
	input.Request.RawQuery = "trace=true"
	input.Request.Header.Set("X-Test", "one")
	want := cloneParsedRequestForGatewayTest(input.Request)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan UpstreamResult, 1)
	go func() {
		done <- NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
			ctx, input, newRecordingResponseWriter(),
		)
	}()

	waitForSignal(t, requestStarted, "upstream request")
	if !reflect.DeepEqual(input.Request, want) {
		t.Fatalf("ForwardStream() mutated ParsedRequest before commit:\n got %#v\nwant %#v", input.Request, want)
	}
	cancel()
	select {
	case result := <-done:
		if result.Committed {
			t.Fatalf("ForwardStream() committed partial event: %#v", result)
		}
		if calls.Load() != 0 {
			t.Fatalf("stream-ready calls after pre-commit cancellation = %d, want 0", calls.Load())
		}
		if !reflect.DeepEqual(input.Request, want) {
			t.Fatalf("ForwardStream() mutated ParsedRequest after pre-commit return:\n got %#v\nwant %#v", input.Request, want)
		}
	case <-time.After(time.Second):
		t.Fatal("ForwardStream() did not finish after pre-commit cancellation")
	}
}

func TestForwardStreamReleasesParsedBodyAfterCommit(t *testing.T) {
	releaseUpstream := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseUpstream) }) }

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: ready\n\n"))
		writer.(http.Flusher).Flush()
		<-releaseUpstream
	}))
	defer func() {
		release()
		upstream.Close()
	}()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = make([]byte, maxRequestBodyBytes)
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	downstream := newRecordingResponseWriter()
	done := make(chan UpstreamResult, 1)
	go func() {
		done <- forwarder.ForwardStream(context.Background(), input, downstream)
	}()

	waitForSignal(t, downstream.writes, "committed stream write")
	if input.Request.Body != nil {
		t.Fatalf("committed stream still retains %d request body bytes", len(input.Request.Body))
	}
	release()

	select {
	case result := <-done:
		if result.Err != nil || !result.Committed {
			t.Fatalf("ForwardStream() result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("ForwardStream() did not finish after upstream release")
	}
}

func cloneParsedRequestForGatewayTest(request *dialect.ParsedRequest) *dialect.ParsedRequest {
	clone := *request
	clone.Header = request.Header.Clone()
	clone.Body = bytes.Clone(request.Body)
	return &clone
}

func TestStreamingClientConfigHasNoTotalTimeout(t *testing.T) {
	timeouts := state.TimeoutConfig{
		Connect: 2 * time.Second, FirstByte: 3 * time.Second,
		Request: 4 * time.Second, StreamIdle: 5 * time.Second,
	}
	config := streamingClientConfig(timeouts)

	if config.ConnectTimeout != timeouts.Connect || config.ResponseHeaderTimeout != timeouts.FirstByte {
		t.Fatalf("stream connect/header timeouts = %s/%s", config.ConnectTimeout, config.ResponseHeaderTimeout)
	}
	if config.RequestTimeout != 0 {
		t.Fatalf("stream RequestTimeout = %s, want 0", config.RequestTimeout)
	}
}

func TestForwarderPreservesEndToEndRequestAndSuccessfulResponse(t *testing.T) {
	var received *http.Request
	var receivedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		received = request.Clone(request.Context())
		received.Header = request.Header.Clone()
		receivedBody, _ = io.ReadAll(request.Body)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Upstream", "kept")
		writer.Header().Set("Connection", "X-Upstream-Hop")
		writer.Header().Add("Connection", "X-Upstream-Hop-Second")
		writer.Header().Set("X-Upstream-Hop", "drop")
		writer.Header().Set("X-Upstream-Hop-Second", "drop")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"id":"chatcmpl-1"}`))
	}))
	defer upstream.Close()

	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	result := forwarder.Forward(context.Background(), ForwardInput{
		Dialect: dialect.NewOpenAI(upstream.Client()),
		Group: state.GroupView{
			ID: 1, Name: "openai", UpstreamURL: upstream.URL,
			Timeouts:    state.TimeoutConfig{Connect: time.Second, FirstByte: time.Second, Request: time.Second},
			HeaderRules: state.HeaderRules{Set: map[string]string{"X-Custom": "prefix-${API_KEY}"}},
		},
		APIKey: "sk-upstream-secret",
		Request: &dialect.ParsedRequest{
			Method:   http.MethodPost,
			Path:     "/v1/chat/completions",
			RawQuery: "trace=true",
			Header: http.Header{
				"Content-Type":  {"application/json"},
				"X-Passthrough": {"kept"},
				"Authorization": {"Bearer downstream-key"},
				"Connection":    {"X-Drop", "X-Drop-Second"},
				"X-Drop":        {"drop"},
				"X-Drop-Second": {"drop"},
			},
			Body: []byte(`{"model":"gpt-4o"}`),
		},
	})

	if result.Err != nil || result.StatusCode != http.StatusOK || !result.RequestWritten {
		t.Fatalf("Forward() result = %#v", result)
	}
	if string(result.Body) != `{"id":"chatcmpl-1"}` || len(result.ClassificationBody) != 0 {
		t.Fatalf("response bodies = wire %q classify %q", result.Body, result.ClassificationBody)
	}
	if result.Header.Get("X-Upstream") != "kept" ||
		result.Header.Get("X-Upstream-Hop") != "" ||
		result.Header.Get("X-Upstream-Hop-Second") != "" {
		t.Fatalf("response headers = %#v", result.Header)
	}
	if received.URL.RawQuery != "trace=true" || string(receivedBody) != `{"model":"gpt-4o"}` {
		t.Fatalf("upstream request URL/body = %s?%s %q", received.URL.Path, received.URL.RawQuery, receivedBody)
	}
	if received.Header.Get("Authorization") != "Bearer sk-upstream-secret" ||
		received.Header.Get("X-Custom") != "prefix-sk-upstream-secret" ||
		received.Header.Get("X-Passthrough") != "kept" {
		t.Fatalf("upstream headers = %#v", received.Header)
	}
	if received.Header.Get("X-Drop") != "" ||
		received.Header.Get("X-Drop-Second") != "" ||
		strings.Contains(received.Header.Get("Authorization"), "downstream-key") {
		t.Fatalf("upstream retained forbidden header: %#v", received.Header)
	}
	if got := received.Header.Get("User-Agent"); got != "" {
		t.Fatalf("upstream User-Agent = %q, want downstream absence preserved", got)
	}
}

func TestForwarderIsolatesAliasedModelFromNonStreamingErrors(t *testing.T) {
	const (
		externalModel = "public-model"
		upstreamModel = "provider-model"
		secret        = "custom-upstream-secret"
	)
	plain := []byte(`{"error":{"message":"model provider-model rejected custom-upstream-secret"}}`)

	for _, encoding := range []string{"", "gzip"} {
		name := "identity"
		if encoding != "" {
			name = encoding
		}
		t.Run(name, func(t *testing.T) {
			wire := plain
			if encoding != "" {
				var err error
				wire, err = utils.CompressResponse(encoding, plain)
				if err != nil {
					t.Fatalf("compress fixture: %v", err)
				}
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Upstream-Model", upstreamModel)
				if encoding != "" {
					writer.Header().Set("Content-Encoding", encoding)
				}
				setRepresentationMetadata(writer.Header())
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.Request.Body = []byte(`{"model":"public-model"}`)
			input.ExternalModel = externalModel
			input.UpstreamModelID = upstreamModel
			input.APIKey = secret
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
				context.Background(), input,
			)

			if result.Err != nil || result.StatusCode != http.StatusBadRequest {
				t.Fatalf("Forward() result = %#v", result)
			}
			if !bytes.Contains(result.ClassificationBody, []byte(upstreamModel)) ||
				bytes.Contains(result.ClassificationBody, []byte(secret)) ||
				!bytes.Contains(result.ClassificationBody, []byte(redact.Placeholder)) {
				t.Fatalf("ClassificationBody = %q", result.ClassificationBody)
			}
			downstreamBody := result.Body
			if encoding != "" {
				var err error
				downstreamBody, err = utils.DecompressResponse(encoding, result.Body)
				if err != nil {
					t.Fatalf("decompress downstream body: %v", err)
				}
			}
			if bytes.Contains(downstreamBody, []byte(upstreamModel)) ||
				!bytes.Contains(downstreamBody, []byte(externalModel)) ||
				bytes.Contains(downstreamBody, []byte(secret)) ||
				!bytes.Contains(downstreamBody, []byte(redact.Placeholder)) {
				t.Fatalf("downstream body = %q", downstreamBody)
			}
			if result.Header.Get("Content-Type") != "application/json" ||
				result.Header.Get("Content-Encoding") != encoding ||
				result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) {
				t.Fatalf("representation headers = %#v", result.Header)
			}
			assertHeadersDoNotContain(t, result.Header, upstreamModel)
			assertRepresentationMetadata(t, result.Header, false)
		})
	}
}

func TestForwarderRewritesEscapedAliasedModelInJSONErrors(t *testing.T) {
	const upstreamModel = "org/model"
	externalModel := "public\"\\model"
	plain := []byte(`{"org\/model":"org\u002fmodel unavailable","nested":["org/model"],"number":9007199254740993}`)

	for _, encoding := range []string{"", "gzip"} {
		name := "identity"
		wire := plain
		if encoding != "" {
			name = encoding
			var err error
			wire, err = utils.CompressResponse(encoding, plain)
			if err != nil {
				t.Fatalf("compress fixture: %v", err)
			}
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if encoding != "" {
					writer.Header().Set("Content-Encoding", encoding)
				}
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.ExternalModel = externalModel
			input.UpstreamModelID = upstreamModel
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
				context.Background(), input,
			)
			if result.Err != nil || result.StatusCode != http.StatusBadRequest {
				t.Fatalf("Forward() result = %#v", result)
			}
			if !bytes.Equal(result.ClassificationBody, plain) {
				t.Fatalf("ClassificationBody = %q, want original safe JSON %q", result.ClassificationBody, plain)
			}
			downstreamBody := result.Body
			if encoding != "" {
				var err error
				downstreamBody, err = utils.DecompressResponse(encoding, result.Body)
				if err != nil {
					t.Fatalf("decompress downstream body: %v", err)
				}
			}
			if !json.Valid(downstreamBody) {
				t.Fatalf("downstream body is invalid JSON: %q", downstreamBody)
			}
			if !bytes.Contains(downstreamBody, []byte("9007199254740993")) {
				t.Fatalf("downstream body lost JSON number precision: %q", downstreamBody)
			}
			var decoded map[string]any
			if err := json.Unmarshal(downstreamBody, &decoded); err != nil {
				t.Fatalf("decode downstream JSON: %v", err)
			}
			if decoded[externalModel] != externalModel+" unavailable" {
				t.Fatalf("rewritten object entry = %#v", decoded)
			}
			nested, ok := decoded["nested"].([]any)
			if !ok || len(nested) != 1 || nested[0] != externalModel {
				t.Fatalf("rewritten nested value = %#v", decoded["nested"])
			}
		})
	}
}

func TestForwarderRedactsEscapedAPIKeyInJSONErrors(t *testing.T) {
	const secret = "secret/key"
	plain := []byte(`{"secret\/key":"secret\u002fkey rejected"}`)

	for _, encoding := range []string{"", "gzip"} {
		name := "identity"
		wire := plain
		if encoding != "" {
			name = encoding
			var err error
			wire, err = utils.CompressResponse(encoding, plain)
			if err != nil {
				t.Fatalf("compress fixture: %v", err)
			}
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if encoding != "" {
					writer.Header().Set("Content-Encoding", encoding)
				}
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.APIKey = secret
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
				context.Background(), input,
			)
			if result.Err != nil || result.StatusCode != http.StatusUnauthorized {
				t.Fatalf("Forward() result = %#v", result)
			}
			downstreamBody := result.Body
			if encoding != "" {
				var err error
				downstreamBody, err = utils.DecompressResponse(encoding, result.Body)
				if err != nil {
					t.Fatalf("decompress downstream body: %v", err)
				}
			}
			for _, body := range [][]byte{downstreamBody, result.ClassificationBody} {
				var decoded map[string]any
				if err := json.Unmarshal(body, &decoded); err != nil {
					t.Fatalf("decode safe body %q: %v", body, err)
				}
				if decoded[redact.Placeholder] != redact.Placeholder+" rejected" {
					t.Fatalf("safe decoded body = %#v", decoded)
				}
			}
		})
	}
}

func TestPrepareErrorBodyFailsClosedWhenEscapedAPIKeyRewriteCollides(t *testing.T) {
	const secret = "secret/key"
	plain := []byte(`{"secret\/key":"leak","[REDACTED]":"safe"}`)
	forwarder := &Forwarder{redactor: redact.New()}
	headers := http.Header{"Content-Type": {"application/json"}}
	wire, classification := forwarder.prepareErrorBody(headers, plain, ForwardInput{APIKey: secret})

	if string(wire) != redact.Placeholder || string(classification) != redact.Placeholder {
		t.Fatalf("collision result wire=%q classification=%q", wire, classification)
	}
}

func TestPrepareErrorBodyFailsClosedBeforeRawAPIKeyCollisionRedaction(t *testing.T) {
	const secret = "secret"
	plain := []byte(`{"secret":"leak","[REDACTED]":"safe"}`)
	forwarder := &Forwarder{redactor: redact.New()}
	headers := http.Header{"Content-Type": {"application/json"}}
	wire, classification := forwarder.prepareErrorBody(headers, plain, ForwardInput{APIKey: secret})

	if string(wire) != redact.Placeholder || string(classification) != redact.Placeholder {
		t.Fatalf("collision result wire=%q classification=%q", wire, classification)
	}
}

func TestForwarderFailsClosedWhenJSONKeyRewriteCollides(t *testing.T) {
	plain := []byte(`{"provider-model":"first","public-model":"second"}`)
	forwarder := &Forwarder{redactor: redact.New()}
	headers := http.Header{"Content-Type": {"application/json"}}
	wire, classification := forwarder.prepareErrorBody(headers, plain, ForwardInput{
		ExternalModel: "public-model", UpstreamModelID: "provider-model",
	})

	if string(wire) != redact.Placeholder || !bytes.Equal(classification, plain) {
		t.Fatalf("collision result wire=%q classification=%q", wire, classification)
	}
}

func TestPrepareErrorBodyFailsClosedWhenModelExpansionExceedsLimit(t *testing.T) {
	plain := bytes.Repeat([]byte("x"), 64<<10)
	forwarder := &Forwarder{redactor: redact.New()}
	headers := make(http.Header)
	wire, classification := forwarder.prepareErrorBody(headers, plain, ForwardInput{
		ExternalModel: strings.Repeat("a", 32), UpstreamModelID: "x",
	})

	if string(wire) != redact.Placeholder || !bytes.Equal(classification, plain) {
		t.Fatalf("expansion result wire=%q classification length=%d", wire, len(classification))
	}
}

func TestForwardStreamIsolatesAliasedModelFromNonSuccessResponse(t *testing.T) {
	const (
		externalModel = "public-model"
		upstreamModel = "provider-model"
		secret        = "custom-upstream-secret"
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Upstream-Model", upstreamModel)
		setRepresentationMetadata(writer.Header())
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":"provider-model rate limited custom-upstream-secret"}`))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"public-model","stream":true}`)
	input.ExternalModel = externalModel
	input.UpstreamModelID = upstreamModel
	input.APIKey = secret
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if result.Err != nil || result.Committed || result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	if bytes.Contains(result.Body, []byte(upstreamModel)) ||
		!bytes.Contains(result.Body, []byte(externalModel)) ||
		bytes.Contains(result.Body, []byte(secret)) {
		t.Fatalf("downstream body = %q", result.Body)
	}
	if !bytes.Contains(result.ClassificationBody, []byte(upstreamModel)) ||
		bytes.Contains(result.ClassificationBody, []byte(secret)) {
		t.Fatalf("ClassificationBody = %q", result.ClassificationBody)
	}
	assertHeadersDoNotContain(t, result.Header, upstreamModel)
	assertRepresentationMetadata(t, result.Header, false)
	if downstream.status != 0 || downstream.body.Len() != 0 {
		t.Fatalf("ForwardStream() wrote error before Handler verdict: %#v", downstream)
	}
}

func TestForwarderPreservesErrorModelWithoutAlias(t *testing.T) {
	const upstreamModel = "provider-model"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Upstream-Model", upstreamModel)
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(upstreamModel))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Request.Body = []byte(`{"model":"provider-model"}`)
	input.ExternalModel = upstreamModel
	input.UpstreamModelID = upstreamModel
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
		context.Background(), input,
	)

	if string(result.Body) != upstreamModel || string(result.ClassificationBody) != upstreamModel ||
		result.Header.Get("X-Upstream-Model") != upstreamModel {
		t.Fatalf("non-alias response changed: %#v", result)
	}
}

func TestForwarderRedactsCompressedErrorAndPreservesEncoding(t *testing.T) {
	const secret = "custom-upstream-secret"
	plain := []byte(`{"error":{"api_key":"` + secret + `","code":"invalid_api_key"}}`)
	encoded, err := utils.CompressResponse("gzip", plain)
	if err != nil {
		t.Fatalf("compress fixture: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		setRepresentationMetadata(writer.Header())
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write(encoded)
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, secret, time.Second)
	if result.Err != nil || result.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Forward() result = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", result.Header.Get("Content-Encoding"))
	}
	decoded, err := utils.DecompressResponse("gzip", result.Body)
	if err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	for _, body := range [][]byte{decoded, result.ClassificationBody} {
		if bytes.Contains(body, []byte(secret)) || !bytes.Contains(body, []byte(redact.Placeholder)) {
			t.Fatalf("safe body = %q, want placeholder and no secret", body)
		}
	}
	if result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) {
		t.Fatalf("Content-Length = %q, body length = %d", result.Header.Get("Content-Length"), len(result.Body))
	}
	assertRepresentationMetadata(t, result.Header, false)
}

func TestForwarderPreservesUnchangedCompressedErrorWireBytes(t *testing.T) {
	plain := []byte(`{"error":{"code":"rate_limited"}}`)
	encoded, err := utils.CompressResponse("gzip", plain)
	if err != nil {
		t.Fatalf("compress fixture: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		setRepresentationMetadata(writer.Header())
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write(encoded)
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "custom-upstream-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("Forward() result = %#v", result)
	}
	if !bytes.Equal(result.Body, encoded) {
		t.Fatalf("wire body changed without redaction: got %x want %x", result.Body, encoded)
	}
	if !bytes.Equal(result.ClassificationBody, plain) {
		t.Fatalf("ClassificationBody = %q, want %q", result.ClassificationBody, plain)
	}
	if result.Header.Get("Content-Encoding") != "gzip" ||
		result.Header.Get("Content-Length") != strconv.Itoa(len(encoded)) {
		t.Fatalf("compressed response headers = %#v", result.Header)
	}
	assertRepresentationMetadata(t, result.Header, true)
}

func TestForwarderFailsClosedForUndecodableError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "unsupported")
		setRepresentationMetadata(writer.Header())
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte("opaque-secret-body"))
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "opaque-secret-body", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("Forward() result = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "" ||
		result.Header.Get("Content-Type") != "text/plain; charset=utf-8" ||
		result.Header.Get("Content-Length") != strconv.Itoa(len(redact.Placeholder)) ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
	assertRepresentationMetadata(t, result.Header, false)
}

func TestForwarderFailsClosedForMalformedEncoding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip, br")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte("opaque-body"))
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", time.Second)
	if result.Header.Get("Content-Encoding") != "" ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderFailsClosedForMultipleEncodingFieldValues(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Add("Content-Encoding", "identity")
		writer.Header().Add("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte("opaque-multi-value-body"))
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("Forward() result = %#v", result)
	}
	if len(result.Header.Values("Content-Encoding")) != 0 ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderFailsClosedForUnsupportedEmptyBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "unsupported")
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("Forward() result = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "" ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderFailsClosedForGzipEmptyBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", time.Second)
	if result.Err != nil || result.StatusCode != http.StatusBadGateway {
		t.Fatalf("Forward() result = %#v", result)
	}
	if result.Header.Get("Content-Encoding") != "" ||
		string(result.Body) != redact.Placeholder ||
		string(result.ClassificationBody) != redact.Placeholder {
		t.Fatalf("fail-closed result headers/body = %#v %q", result.Header, result.Body)
	}
}

func TestForwarderMarksConnectionFailureAsNotWritten(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := upstream.URL
	upstream.Close()

	result := testForward(t, url, "sk-secret", 200*time.Millisecond)
	if result.Err == nil || result.RequestWritten {
		t.Fatalf("connection failure result = %#v, want error before write", result)
	}
}

func TestForwarderMarksTimeoutAfterRequestWrite(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		<-request.Context().Done()
	}))
	defer upstream.Close()

	result := testForward(t, upstream.URL, "sk-secret", 25*time.Millisecond)
	if result.Err == nil || !result.RequestWritten || !isTimeoutError(result.Err) {
		t.Fatalf("post-write timeout result = %#v", result)
	}
}

func testForward(t *testing.T, upstreamURL, apiKey string, timeout time.Duration) UpstreamResult {
	t.Helper()
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	return forwarder.Forward(context.Background(), ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			ID: 1, UpstreamURL: upstreamURL,
			Timeouts: state.TimeoutConfig{Connect: timeout, FirstByte: timeout, Request: timeout},
		},
		APIKey: apiKey,
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost, Path: "/v1/chat/completions",
			Header: make(http.Header), Body: []byte(`{"model":"gpt-4o"}`),
		},
	})
}

func streamForwardInput(upstreamURL string) ForwardInput {
	return ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			ID: 1, Name: "openai", UpstreamURL: upstreamURL,
			Timeouts: state.TimeoutConfig{
				Connect: time.Second, FirstByte: time.Second,
				Request: time.Second, StreamIdle: time.Second,
			},
		},
		APIKey: "sk-upstream-secret",
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost, Path: "/v1/chat/completions",
			Header: make(http.Header), Body: []byte(`{"model":"gpt-4o","stream":true}`),
		},
	}
}

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

func assertRepresentationMetadata(t *testing.T, headers http.Header, wantPreserved bool) {
	t.Helper()
	want := map[string]string{
		"ETag":            `"wire-v1"`,
		"Digest":          "sha-256=wire-digest",
		"Content-MD5":     "d2lyZQ==",
		"Content-Range":   "bytes 0-9/10",
		"Content-Digest":  "sha-256=:d2lyZQ==:",
		"Repr-Digest":     "sha-256=:cmVwcg==:",
		"Signature":       "sig1=:c2lnbmF0dXJl:",
		"Signature-Input": `sig1=("content-digest");created=1`,
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
}

func TestInvalidateRewrittenStreamHeadersRemovesRepresentationMetadata(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Content-Length", "123")
	setRepresentationMetadata(headers)

	invalidateRewrittenStreamHeaders(headers)

	if headers.Get("Content-Length") != "" {
		t.Fatalf("Content-Length = %q, want removed", headers.Get("Content-Length"))
	}
	assertRepresentationMetadata(t, headers, false)
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

func decodeSSEJSONPayloads(t *testing.T, stream []byte) []map[string]any {
	t.Helper()
	remaining := bytes.Clone(stream)
	payloads := make([]map[string]any, 0)
	for len(remaining) > 0 {
		boundary := bytes.Index(remaining, []byte("\n\n"))
		if boundary < 0 {
			t.Fatalf("incomplete SSE output: %q", remaining)
		}
		event := remaining[:boundary+2]
		remaining = remaining[boundary+2:]
		var values [][]byte
		for _, line := range splitSSEEventLines(event) {
			if line.isData {
				values = append(values, line.data)
			}
		}
		if len(values) == 0 {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal(bytes.Join(values, []byte{'\n'}), &decoded); err != nil {
			t.Fatalf("decode SSE JSON payload: %v", err)
		}
		payloads = append(payloads, decoded)
	}
	return payloads
}
