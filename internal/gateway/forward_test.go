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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/contentcoding"
	platformhttp "gpt-load/internal/platform/httpclient"
	platformheader "gpt-load/internal/platform/httpheader"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type dialectSpecificCredentialHeaders struct {
	dialect.Dialect
}

func (dialectSpecificCredentialHeaders) CredentialHeaderNames() []string {
	return []string{"X-Dialect-Credential"}
}

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

func TestForwardCredentialSecretsExcludeOrdinaryHeaderRuleValues(t *testing.T) {
	const apiKey = "fake-provider-key-for-redaction"
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			HeaderRules: state.HeaderRules{Set: map[string]string{
				"Authorization":   "Bearer ${API_KEY}",
				"Accept-Encoding": "gzip",
				"X-Custom":        "ordinary",
			}},
		},
		APIKey: apiKey,
		Request: &dialect.ParsedRequest{
			Header: make(http.Header),
		},
	}

	got := resolvedCredentialSecretValues(input, nil)
	want := []string{"Bearer " + apiKey, apiKey}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolvedCredentialSecretValues() = %#v, want %#v", got, want)
	}
	got[0] = "mutated"
	if next := resolvedCredentialSecretValues(input, nil); !reflect.DeepEqual(next, want) {
		t.Fatalf("resolvedCredentialSecretValues() returned aliased values: %#v", next)
	}

	for _, selected := range []dialect.Dialect{
		dialect.NewOpenAI(http.DefaultClient),
		dialect.NewAnthropic(http.DefaultClient),
		dialect.NewGemini(http.DefaultClient),
	} {
		namer, ok := selected.(dialect.CredentialHeaderNamer)
		if !ok {
			t.Fatalf("%T does not implement CredentialHeaderNamer", selected)
		}
		names := namer.CredentialHeaderNames()
		if len(names) == 0 {
			t.Fatalf("%T returned no credential Header names", selected)
		}
		original := names[0]
		names[0] = "mutated"
		if next := namer.CredentialHeaderNames(); len(next) == 0 || next[0] != original {
			t.Fatalf("%T returned an aliased credential Header slice: %#v", selected, next)
		}
	}
}

func TestGatewayCredentialPolicyRecognizesSharedBaseAndDialectSpecificNames(t *testing.T) {
	baseNames := []string{
		"Authorization",
		"Proxy-Authorization",
		"Api-Key",
		"X-Api-Key",
		"X-Goog-Api-Key",
	}
	selected := dialectSpecificCredentialHeaders{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
	}
	source := http.Header{"X-Safe": {"kept"}}
	for index, name := range baseNames {
		if !platformheader.IsCredentialName(name) {
			t.Fatalf("shared policy does not recognize base credential Header %q", name)
		}
		source.Set(name, fmt.Sprintf("base-secret-%d", index))
	}
	source.Set("X-Dialect-Credential", "dialect-secret")
	input := ForwardInput{
		Dialect: selected,
		APIKey:  "provider-api-key",
		Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}

	sanitized := sanitizeForwardResponseHeaders(source, input)
	for _, name := range append(baseNames, "X-Dialect-Credential") {
		if values := sanitized.Values(name); values != nil {
			t.Errorf("credential response Header %q survived: %#v", name, values)
		}
	}
	if got := sanitized.Get("X-Safe"); got != "kept" {
		t.Fatalf("safe response Header = %q, want kept", got)
	}

	finalHeaders := http.Header{
		"Authorization":        {"base-final-secret"},
		"X-Dialect-Credential": {"dialect-final-secret"},
	}
	secrets := resolvedCredentialSecretValues(input, finalHeaders)
	for _, want := range []string{
		"provider-api-key",
		"base-final-secret",
		"dialect-final-secret",
	} {
		if !slices.Contains(secrets, want) {
			t.Errorf("resolved credential secrets = %#v, want %q", secrets, want)
		}
	}
}

func TestForwardRequestRulesCannotRestoreReservedHeaders(t *testing.T) {
	const apiKey = "fake-request-sanitizer-provider-key"
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			UpstreamURL: "https://api.example.test",
			HeaderRules: state.HeaderRules{Set: map[string]string{
				"Authorization":       "Token ${API_KEY}",
				"Connection":          "X-Rule-Hop",
				"X-Rule-Hop":          "restored",
				"Cookie":              "session=fake",
				"Cookie2":             "legacy=fake",
				"Proxy-Authorization": "Bearer proxy-fake",
				"Proxy-Custom":        "proxy-fake",
				requestIDHeader:       "restored-request-id",
				debugHeaderGroup:      "restored-group",
				debugHeaderKey:        "restored-key",
				debugHeaderAttempts:   "restored-attempts",
				"Accept-Encoding":     "gzip",
				"Content-Encoding":    "br",
			}, Remove: []string{"Accept-Encoding", "Content-Encoding"}},
		},
		APIKey: apiKey,
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost,
			Path:   "/v1/chat/completions",
			Header: http.Header{
				"Connection":       {"X-Client-Hop"},
				"X-Client-Hop":     {"client-hop"},
				"Cookie":           {"client=fake"},
				"Proxy-Client-Hop": {"proxy-client"},
				"accept-encoding":  {"zstd"},
				"content-encoding": {"gzip"},
			},
			Body: []byte(`{"model":"gpt-test"}`),
		},
	}

	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	request, _, replay, err := forwarder.newUpstreamRequest(t.Context(), input, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = request.Body.Close()
		replay.release()
	})

	if got := request.Header.Get("Authorization"); got != "Token "+apiKey {
		t.Fatalf("Authorization = %q, want configured credential", got)
	}
	if values := headerFieldValues(request.Header, "Accept-Encoding"); !reflect.DeepEqual(values, []string{"identity"}) {
		t.Fatalf("Accept-Encoding values = %#v, want [identity]", values)
	}
	for _, name := range []string{
		"Connection",
		"X-Rule-Hop",
		"X-Client-Hop",
		"Cookie",
		"Cookie2",
		"Proxy-Authorization",
		"Proxy-Custom",
		"Proxy-Client-Hop",
		requestIDHeader,
		debugHeaderGroup,
		debugHeaderKey,
		debugHeaderAttempts,
		"Content-Encoding",
	} {
		if values := headerFieldValues(request.Header, name); values != nil {
			t.Errorf("upstream request Header %s survived: %#v", name, values)
		}
	}
}

func TestForwardAllRequestsUseIdentityRepresentation(t *testing.T) {
	tests := []struct {
		name      string
		stream    bool
		configure func(*ForwardInput)
		assert    func(*testing.T, []byte)
	}{
		{
			name: "normal request",
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				if string(body) != `{"model":"public-model","stream":true}` {
					t.Fatalf("upstream body = %s, want unchanged plaintext", body)
				}
			},
		},
		{
			name: "alias rewrite",
			configure: func(input *ForwardInput) {
				input.ExternalModel = "public-model"
				input.UpstreamModelID = "provider-model"
			},
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil || payload["model"] != "provider-model" {
					t.Fatalf("upstream alias body = %s, error = %v", body, err)
				}
			},
		},
		{
			name:   "stream usage injection",
			stream: true,
			configure: func(input *ForwardInput) {
				input.Group.InjectUsageOptions = true
			},
			assert: func(t *testing.T, body []byte) {
				t.Helper()
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatalf("decode upstream stream body: %v", err)
				}
				options, ok := payload["stream_options"].(map[string]any)
				if !ok || options["include_usage"] != true {
					t.Fatalf("upstream stream body = %s, want include_usage", body)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := streamForwardInput("https://api.example.test")
			input.Request.Header = http.Header{
				"Accept-Encoding":  {"gzip"},
				"accept-encoding":  {"br"},
				"Content-Encoding": {"gzip"},
				"content-encoding": {"zstd"},
				"Content-Type":     {"application/json"},
				"Accept":           {"application/json"},
				"Idempotency-Key":  {"request-1"},
			}
			input.Request.Body = []byte(`{"model":"public-model","stream":true}`)
			if test.configure != nil {
				test.configure(&input)
			}

			request, _, replay, err := NewForwarder(
				platformhttp.NewHTTPClientManager(),
				redact.New(),
			).newUpstreamRequest(t.Context(), input, test.stream)
			if err != nil {
				t.Fatalf("newUpstreamRequest() error = %v", err)
			}
			t.Cleanup(func() {
				_ = request.Body.Close()
				replay.release()
			})
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read upstream request body: %v", err)
			}
			test.assert(t, body)
			if values := headerFieldValues(request.Header, "Accept-Encoding"); !reflect.DeepEqual(values, []string{"identity"}) {
				t.Fatalf("Accept-Encoding values = %#v, want [identity]", values)
			}
			if values := headerFieldValues(request.Header, "Content-Encoding"); values != nil {
				t.Fatalf("Content-Encoding values = %#v, want absent", values)
			}
			if request.ContentLength != int64(len(body)) {
				t.Fatalf("ContentLength = %d, want %d", request.ContentLength, len(body))
			}
			for name, want := range map[string]string{
				"Content-Type":    "application/json",
				"Accept":          "application/json",
				"Idempotency-Key": "request-1",
				"Authorization":   "Bearer sk-upstream-secret",
			} {
				if got := request.Header.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestForwardNormalizesFinalRequestMetadata(t *testing.T) {
	input := streamForwardInput("https://api.example.test")
	input.Request.Header = http.Header{
		"Content-Type":     {"application/json"},
		"Accept":           {"application/json"},
		"Idempotency-Key":  {"request-1"},
		"Content-Length":   {"999"},
		"content-length":   {"998"},
		"Content-Encoding": {"gzip"},
		"content-encoding": {"br"},
		"ETag":             {`"client"`},
		"Digest":           {"sha-256=client"},
		"Content-MD5":      {"client-md5"},
		"Content-Range":    {"bytes 0-1/2"},
		"Content-Digest":   {"sha-256=:Y2xpZW50:"},
		"Repr-Digest":      {"sha-256=:cmVwcg==:"},
		"Signature":        {"client-signature"},
		"Signature-Input":  {"client-signature-input"},
		"accept-encoding":  {"gzip"},
		"Accept-Encoding":  {"br"},
	}
	input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{
		"Authorization":    "Token ${API_KEY}",
		"Digest":           "sha-256=rule",
		"Signature":        "rule-signature",
		"Content-Encoding": "zstd",
		"Accept-Encoding":  "deflate",
	}}

	request, _, replay, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).newUpstreamRequest(t.Context(), input, false)
	if err != nil {
		t.Fatalf("newUpstreamRequest() error = %v", err)
	}
	t.Cleanup(func() {
		_ = request.Body.Close()
		replay.release()
	})
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read upstream body: %v", err)
	}
	if request.ContentLength != int64(len(body)) {
		t.Fatalf("ContentLength = %d, want %d", request.ContentLength, len(body))
	}
	for _, name := range []string{
		"Content-Length",
		"Content-Encoding",
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
			t.Errorf("%s values = %#v, want removed", name, values)
		}
	}
	if values := headerFieldValues(request.Header, "Accept-Encoding"); !reflect.DeepEqual(values, []string{"identity"}) {
		t.Fatalf("Accept-Encoding values = %#v, want [identity]", values)
	}
	for name, want := range map[string]string{
		"Content-Type":    "application/json",
		"Accept":          "application/json",
		"Idempotency-Key": "request-1",
		"Authorization":   "Token sk-upstream-secret",
	} {
		if got := request.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestNewUpstreamRequestDoesNotExposeGetBody(t *testing.T) {
	const payload = `{"model":"gpt-test","messages":[]}`
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			UpstreamURL: "https://example.com",
		},
		APIKey: "fake-provider-key",
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost,
			Path:   "/v1/chat/completions",
			Header: http.Header{
				"Idempotency-Key":   {"client-idempotency"},
				"X-Idempotency-Key": {"client-x-idempotency"},
			},
			Body: []byte(payload),
		},
	}

	request, _, replay, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).newUpstreamRequest(t.Context(), input, false)
	if err != nil {
		t.Fatalf("newUpstreamRequest() error = %v", err)
	}
	t.Cleanup(func() {
		_ = request.Body.Close()
		replay.release()
	})

	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read upstream Body: %v", err)
	}
	if string(body) != payload || request.ContentLength != int64(len(payload)) {
		t.Fatalf(
			"upstream Body/ContentLength = %q/%d, want %q/%d",
			body,
			request.ContentLength,
			payload,
			len(payload),
		)
	}
	if request.GetBody != nil {
		t.Fatal("newUpstreamRequest() exposed GetBody to net/http.Transport")
	}
	if request.Header.Get("Idempotency-Key") != "client-idempotency" ||
		request.Header.Get("X-Idempotency-Key") != "client-x-idempotency" {
		t.Fatalf("idempotency headers = %#v", request.Header)
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
					if !bytes.Equal(result.Body, plain) || !bytes.Equal(result.ClassificationBody, plain) ||
						len(headerFieldValues(result.Header, "Content-Encoding")) != 0 ||
						result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) {
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
		name            string
		encoding        string
		plain           []byte
		wantPlaceholder bool
	}{
		{
			name:  "identity redaction remains within decoded limit",
			plain: bytes.Repeat([]byte("a"), int(maxErrorResponseBodyBytes)),
		},
		{
			name:            "gzip classification body exceeds limit after redaction",
			encoding:        "gzip",
			plain:           bytes.Repeat([]byte("a"), int(maxDecompressedErrorBodyBytes)),
			wantPlaceholder: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := test.plain
			if test.encoding != "" {
				wire = encodeContentCodingForGatewayTest(t, contentcoding.Encoding(test.encoding), test.plain)
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
				result.Header.Get("Content-Encoding") != "" {
				t.Fatalf(
					"result status=%d body=%d classification=%d encoding=%q err=%v",
					result.StatusCode, len(result.Body), len(result.ClassificationBody),
					result.Header.Get("Content-Encoding"), result.Err,
				)
			}
			if test.wantPlaceholder {
				if string(result.Body) != redact.Placeholder || string(result.ClassificationBody) != redact.Placeholder {
					t.Fatalf("overflow result = %#v", result)
				}
				return
			}
			if len(result.Body) != len(result.ClassificationBody) || int64(len(result.Body)) > maxDecompressedErrorBodyBytes || bytes.Contains(result.Body, []byte("a")) {
				t.Fatalf("safe identity result = %#v", result)
			}
		})
	}
}

func TestPrepareErrorRepresentationFailsClosedForOversizedWire(t *testing.T) {
	plain := make([]byte, 100<<10)
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_."
	random := mathrand.New(mathrand.NewSource(1))
	for index := range plain {
		plain[index] = alphabet[random.Intn(len(alphabet))]
	}
	gzipWire := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain)
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
			prepared := forwarder.prepareErrorRepresentation(ForwardInput{}, headers, test.wire, nil)
			if string(prepared.downstream) != redact.Placeholder ||
				string(prepared.classification) != redact.Placeholder ||
				len(headerFieldValues(prepared.headers, "Content-Encoding")) != 0 ||
				!reflect.DeepEqual(headers, test.headers) {
				t.Fatalf(
					"safe representation = %#v, source headers = %#v",
					prepared, headers,
				)
			}
		})
	}
}

func compressResponseWithBoundedZstdWindow(t *testing.T, encoding string, plain []byte) []byte {
	t.Helper()
	if encoding != "zstd" {
		return encodeContentCodingForGatewayTest(t, contentcoding.Encoding(encoding), plain)
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

func TestSanitizeForwardResponseHeadersAlwaysDropsSignatures(t *testing.T) {
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

	t.Run("alias without deletion still removes signatures", func(t *testing.T) {
		source := http.Header{
			"X-Safe":          {"kept"},
			"Signature":       {"sig"},
			"Signature-Input": {"input"},
		}
		got := sanitizeForwardResponseHeaders(source, ForwardInput{
			ExternalModel: "public-model", UpstreamModelID: upstreamModel,
		})
		if got.Get("Signature") != "" || got.Get("Signature-Input") != "" {
			t.Fatalf("unchanged Header set retained signatures: %#v", got)
		}
	})

	t.Run("no alias still removes signatures", func(t *testing.T) {
		source := http.Header{
			"X-Upstream":      {"selected=" + upstreamModel},
			"Signature":       {"sig"},
			"Signature-Input": {"input"},
		}
		got := sanitizeForwardResponseHeaders(source, ForwardInput{
			ExternalModel: upstreamModel, UpstreamModelID: upstreamModel,
		})
		if got.Get("Signature") != "" || got.Get("Signature-Input") != "" {
			t.Fatalf("non-alias Header set retained signatures: %#v", got)
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

func TestForwarderDoesNotTreatShortKeyAsContentCodingFailure(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantBody string
	}{
		{name: "success coding header is terminated", status: http.StatusOK, wantBody: `{"error":"safe"}`},
		{name: "error coding header is terminated", status: http.StatusUnauthorized, wantBody: `{"error":"safe"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, []byte(`{"error":"safe"}`))
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				w.WriteHeader(test.status)
				_, _ = w.Write(wire)
			}))
			defer upstream.Close()
			result := testForward(t, upstream.URL, "gzip", time.Second)
			if result.Err != nil || result.StatusCode != test.status ||
				string(result.Body) != test.wantBody ||
				len(headerFieldValues(result.Header, "Content-Encoding")) != 0 {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestErrorRepresentationRejectsAllContentEncodingCasings(t *testing.T) {
	headers := http.Header{
		"Content-Encoding": {"identity"},
		"cOnTeNt-EnCoDiNg": {"gzip"},
	}
	prepared := (&Forwarder{redactor: redact.New()}).prepareErrorRepresentation(
		ForwardInput{}, headers, []byte("opaque"), nil,
	)
	if string(prepared.downstream) != redact.Placeholder ||
		string(prepared.classification) != redact.Placeholder {
		t.Fatalf("prepared = %#v", prepared)
	}
	for actualName := range prepared.headers {
		if strings.EqualFold(actualName, "Content-Encoding") {
			t.Fatalf("Content-Encoding survived as %q: %#v", actualName, prepared.headers[actualName])
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
			const (
				secret            = "provider-secret-all-paths"
				ordinaryRuleValue = "independent-header-rule-value"
			)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("X-Echo", "prefix-"+secret)
				writer.Header().Set("X-Rule-Echo", "prefix-"+request.Header.Get("X-Custom-Credential"))
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
			input.Group.HeaderRules = state.HeaderRules{
				Set: map[string]string{"X-Custom-Credential": ordinaryRuleValue},
			}
			downstream := newRecordingResponseWriter()
			var result UpstreamResult
			if test.stream {
				result = forwarder.ForwardStream(t.Context(), input, downstream)
			} else {
				result = forwarder.Forward(t.Context(), input)
			}
			if result.Err != nil ||
				result.Header.Get("X-Echo") != "" ||
				result.Header.Get("X-Rule-Echo") != "prefix-"+ordinaryRuleValue ||
				result.Header.Get("X-Safe") != "kept" {
				t.Fatalf("result = %#v", result)
			}
			if test.stream && test.status == http.StatusOK &&
				(downstream.header.Get("X-Echo") != "" ||
					downstream.header.Get("X-Rule-Echo") != "prefix-"+ordinaryRuleValue ||
					downstream.header.Get("X-Safe") != "kept") {
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

func providerErrorBeforeCommit(result UpstreamResult) bool {
	return result.ProviderErrorBeforeCommit
}

func TestForwardStreamReturnsFirstProviderErrorBeforeCommit(t *testing.T) {
	const secret = "sk-obviously-fake-provider-error"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Retry-After", "9")
		_, _ = io.WriteString(
			writer,
			"event: error\ndata: {\"error\":{\"type\":\"rate_limit_error\",\"message\":\""+secret+"\"}}\n\n",
		)
	}))
	defer upstream.Close()

	var ready atomic.Int32
	input := streamForwardInput(upstream.URL)
	input.APIKey = secret
	input.OnStreamReady = func() { ready.Add(1) }
	downstream := newRecordingResponseWriter()
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(context.Background(), input, downstream)

	if result.Err != nil || result.StatusCode != http.StatusOK || result.Committed ||
		!providerErrorBeforeCommit(result) || !result.RequestWritten {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	if result.Body != nil || bytes.Contains(result.ClassificationBody, []byte(secret)) ||
		!bytes.Contains(result.ClassificationBody, []byte("rate_limit_error")) {
		t.Fatalf("body/classification = %q/%q", result.Body, result.ClassificationBody)
	}
	if result.ErrorSummary != fixedErrorSummary("upstream_sse_error") {
		t.Fatalf("ErrorSummary = %q", result.ErrorSummary)
	}
	if result.Header.Get("Retry-After") != "9" || ready.Load() != 0 ||
		downstream.status != 0 || downstream.body.Len() != 0 {
		t.Fatalf("header/ready/downstream = %#v/%d/%d/%q",
			result.Header, ready.Load(), downstream.status, downstream.body.String())
	}
	if result.Usage.State != usage.StateMissing {
		t.Fatalf("Usage = %#v, want missing", result.Usage)
	}
}

func TestForwardStreamBoundsAndRedactsFirstProviderError(t *testing.T) {
	const (
		secret       = "sk-obviously-fake-bounded-error"
		headerSecret = "obviously-fake-header-rule-value"
	)
	padding := strings.Repeat("x", maxFirstSSEEventBytes-256)
	payload := `{"error":{"type":"server_overloaded","message":"` +
		secret + ` ` + headerSecret + ` ` + padding + `"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: "+payload+"\n\n")
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.APIKey = secret
	input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{
		"X-Provider-Trace": headerSecret,
	}}
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(context.Background(), input, newRecordingResponseWriter())

	if result.Err != nil || !providerErrorBeforeCommit(result) {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	if len(result.ClassificationBody) > maxFirstSSEEventBytes ||
		bytes.Contains(result.ClassificationBody, []byte(secret)) ||
		!bytes.Contains(result.ClassificationBody, []byte(headerSecret)) {
		t.Fatalf("unsafe ClassificationBody length/content = %d/%q",
			len(result.ClassificationBody), result.ClassificationBody)
	}
	if strings.Contains(result.ErrorSummary, secret) ||
		strings.Contains(result.ErrorSummary, headerSecret) ||
		len(result.ErrorSummary) > maxRequestLogSummaryBytes {
		t.Fatalf("unsafe ErrorSummary = %q", result.ErrorSummary)
	}
}

func TestForwardStreamClassifiesAliasedNonObjectProviderErrorBeforeRewrite(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
	}{
		{name: "plain text", payload: "rate_limit_error provider-model"},
		{name: "JSON array", payload: `["rate_limit_error","provider-model"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, "event: error\ndata: "+test.payload+"\n\n")
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.ExternalModel = "public-model"
			input.UpstreamModelID = "provider-model"
			input.Request.Body = []byte(`{"model":"public-model","stream":true}`)
			downstream := newRecordingResponseWriter()
			result := NewForwarder(
				platformhttp.NewHTTPClientManager(),
				redact.New(),
			).ForwardStream(context.Background(), input, downstream)

			if result.Err != nil || !result.ProviderErrorBeforeCommit ||
				result.Committed || result.StatusCode != http.StatusOK {
				t.Fatalf("ForwardStream() result = %#v", result)
			}
			if bytes.Contains(result.ClassificationBody, []byte("provider-model")) ||
				!bytes.Contains(result.ClassificationBody, []byte("public-model")) ||
				!bytes.Contains(result.ClassificationBody, []byte("rate_limit_error")) {
				t.Fatalf("ClassificationBody = %q", result.ClassificationBody)
			}
			decision := health.Judge(input.Dialect, health.Attempt{
				StatusCode:                result.StatusCode,
				Body:                      result.ClassificationBody,
				Header:                    result.Header,
				Now:                       time.Unix(1, 0),
				ProviderErrorBeforeCommit: true,
			})
			if decision.Category != health.FailureCategoryRateLimited ||
				decision.Action != health.ActionCooldownKey {
				t.Fatalf("health decision = %#v", decision)
			}
			if downstream.status != 0 || downstream.body.Len() != 0 {
				t.Fatalf("downstream status/body = %d/%q", downstream.status, downstream.body.String())
			}
		})
	}
}

func TestForwardStreamProviderErrorReturnsOnlyRateLimitHeaders(t *testing.T) {
	const (
		apiKey     = "sk-obviously-fake-provider-header"
		ruleEcho   = "ordinary-header-rule-literal"
		retryAfter = "9"
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Retry-After", retryAfter)
		writer.Header().Set("Anthropic-Ratelimit-Tokens-Reset", "30s")
		writer.Header().Set("X-Ratelimit-Reset-Requests", "45s")
		writer.Header().Set("X-Rule-Echo", ruleEcho)
		writer.Header().Set("Authorization", "Bearer "+apiKey)
		writer.Header().Set("X-Api-Key", apiKey)
		_, _ = io.WriteString(writer, "event: error\ndata: rate_limit_error\n\n")
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.APIKey = apiKey
	input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{
		"X-Rule-Echo": ruleEcho,
	}}
	result := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).ForwardStream(context.Background(), input, newRecordingResponseWriter())

	if result.Err != nil || !result.ProviderErrorBeforeCommit {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	want := http.Header{
		"Retry-After":                      {retryAfter},
		"Anthropic-Ratelimit-Tokens-Reset": {"30s"},
		"X-Ratelimit-Reset-Requests":       {"45s"},
	}
	if !reflect.DeepEqual(result.Header, want) {
		t.Fatalf("provider error Header = %#v, want %#v", result.Header, want)
	}
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	decision := health.Judge(input.Dialect, health.Attempt{
		StatusCode:                result.StatusCode,
		Body:                      result.ClassificationBody,
		Header:                    result.Header,
		Now:                       now,
		ProviderErrorBeforeCommit: true,
	})
	if decision.Category != health.FailureCategoryRateLimited ||
		decision.Action != health.ActionCooldownKey ||
		!decision.CooldownUntil.Equal(now.Add(9*time.Second)) {
		t.Fatalf("health decision = %#v", decision)
	}
}

func TestForwardStreamObservesCleanEOFAndFirstSSEError(t *testing.T) {
	t.Run("clean EOF", func(t *testing.T) {
		const wire = "data: {\"model\":\"gpt-4o\"}\n\n"
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, wire)
		}))
		defer upstream.Close()

		downstream := newRecordingResponseWriter()
		result := NewForwarder(
			platformhttp.NewHTTPClientManager(),
			redact.New(),
		).ForwardStream(
			context.Background(),
			streamForwardInput(upstream.URL),
			downstream,
		)

		if result.Err != nil || !result.Committed ||
			result.Stream.EndReason != StreamEndCleanEOF ||
			result.Stream.ErrorSummary != "" {
			t.Fatalf("ForwardStream() clean result = %#v", result)
		}
		if downstream.status != http.StatusOK || downstream.body.String() != wire {
			t.Fatalf(
				"clean downstream status/body = %d/%q, want %d/%q",
				downstream.status,
				downstream.body.String(),
				http.StatusOK,
				wire,
			)
		}
	})

	t.Run("first safe SSE error summary", func(t *testing.T) {
		const (
			apiKey            = "opaque-provider-secret"
			resolvedAuth      = "Token " + apiKey
			resolvedRule      = "opaque  literal\tvalue"
			ordinaryEncoding  = "gzip"
			globalSecret      = "sk-global-secret-123456789"
			upstreamModel     = "upstream-private-model"
			externalModel     = "public-model"
			disallowedSummary = "must-not-persist"
		)
		firstMessage := strings.Join([]string{
			apiKey,
			resolvedAuth,
			resolvedRule,
			ordinaryEncoding,
			globalSecret,
			upstreamModel,
		}, " ")
		firstPayload := `{"error":{"message":` + strconv.Quote(firstMessage) +
			`},"debug":"` + disallowedSummary + `"}`
		secondPayload := `{"error":{"message":"second error must not replace first"}}`
		wire := "data: {\"model\":\"" + upstreamModel + "\",\"ready\":true}\r\n\r\n" +
			"event: error\r\ndata: " + firstPayload + "\r\n\r\n" +
			"event: error\ndata: " + secondPayload + "\n\n"
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, wire)
		}))
		defer upstream.Close()

		input := streamForwardInput(upstream.URL)
		input.APIKey = apiKey
		input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{
			"Authorization":   "Token ${API_KEY}",
			"X-Custom":        resolvedRule,
			"Accept-Encoding": ordinaryEncoding,
		}}
		input.ExternalModel = externalModel
		input.UpstreamModelID = upstreamModel
		input.Request.Body = []byte(`{"model":"` + externalModel + `","stream":true}`)
		downstream := newRecordingResponseWriter()
		result := NewForwarder(
			platformhttp.NewHTTPClientManager(),
			redact.New(),
		).ForwardStream(context.Background(), input, downstream)

		wantSummary := strings.Repeat(redact.Placeholder+" ", 5) + upstreamModel
		if result.Err != nil || !result.Committed ||
			result.Stream.EndReason != StreamEndSSEError ||
			result.Stream.ErrorSummary != wantSummary {
			t.Fatalf(
				"ForwardStream() SSE observation = %#v, want summary %q",
				result,
				wantSummary,
			)
		}
		output := downstream.body.String()
		for _, secret := range []string{apiKey, resolvedAuth, upstreamModel} {
			if strings.Contains(output, secret) {
				t.Fatalf("downstream SSE leaked %q: %q", secret, output)
			}
		}
		firstBoundary := strings.Index(output, "\r\n\r\n")
		if firstBoundary < 0 {
			t.Fatalf("downstream SSE lacks first CRLF event boundary: %q", output)
		}
		secondBoundaryOffset := strings.Index(output[firstBoundary+len("\r\n\r\n"):], "\r\n\r\n")
		if secondBoundaryOffset < 0 {
			t.Fatalf("downstream SSE lacks committed error event boundary: %q", output)
		}
		secondStart := firstBoundary + len("\r\n\r\n")
		secondEvent := output[secondStart : secondStart+secondBoundaryOffset]
		dataIndex := strings.Index(secondEvent, "data: ")
		if dataIndex < 0 {
			t.Fatalf("first downstream SSE error event lacks data: %q", secondEvent)
		}
		var decodedFirstPayload map[string]any
		if err := json.Unmarshal(
			[]byte(secondEvent[dataIndex+len("data: "):]),
			&decodedFirstPayload,
		); err != nil {
			t.Fatalf("decode first downstream SSE payload: %v", err)
		}
		firstError, ok := decodedFirstPayload["error"].(map[string]any)
		if !ok {
			t.Fatalf("first downstream SSE error = %#v", decodedFirstPayload["error"])
		}
		downstreamMessage, ok := firstError["message"].(string)
		if !ok {
			t.Fatalf("first downstream SSE error message = %#v", firstError["message"])
		}
		for _, ordinary := range []string{resolvedRule, ordinaryEncoding} {
			if !strings.Contains(downstreamMessage, ordinary) {
				t.Fatalf(
					"ordinary HeaderRule value %q was changed in downstream SSE message: %q",
					ordinary,
					downstreamMessage,
				)
			}
			if strings.Contains(result.Stream.ErrorSummary, ordinary) {
				t.Fatalf("stream ErrorSummary leaked HeaderRules value %q: %q", ordinary, result.Stream.ErrorSummary)
			}
		}
		if strings.Contains(result.Stream.ErrorSummary, "opaque literal value") {
			t.Fatalf("stream ErrorSummary leaked normalized HeaderRules value: %q", result.Stream.ErrorSummary)
		}
		if !strings.Contains(output, globalSecret) {
			t.Fatalf("observation redactor changed downstream SSE: %q", output)
		}
		if strings.Contains(result.Stream.ErrorSummary, disallowedSummary) ||
			strings.Contains(result.Stream.ErrorSummary, "second error") ||
			strings.Contains(result.Stream.ErrorSummary, globalSecret) {
			t.Fatalf("unsafe/non-first SSE summary = %q", result.Stream.ErrorSummary)
		}
		if !strings.Contains(output, externalModel) ||
			!strings.Contains(output, "second error must not replace first") ||
			!strings.Contains(output, "\r\n\r\n") ||
			strings.Count(output, "event: error") != 2 {
			t.Fatalf("rewritten SSE framing/body = %q", output)
		}
	})
}

type cancelingStreamResponseWriter struct {
	*recordingResponseWriter
	cancel context.CancelFunc
	err    error
}

func (writer *cancelingStreamResponseWriter) Write([]byte) (int, error) {
	writer.cancel()
	return 0, writer.err
}

func TestForwardStreamPrioritizesCancellationAndDownstreamFailure(t *testing.T) {
	const wire = "data: first\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, wire)
	}))
	defer upstream.Close()

	t.Run("client cancellation wins over write failure", func(t *testing.T) {
		wantErr := errors.New("downstream write failed after cancellation")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		downstream := &cancelingStreamResponseWriter{
			recordingResponseWriter: newRecordingResponseWriter(),
			cancel:                  cancel,
			err:                     wantErr,
		}
		result := NewForwarder(
			platformhttp.NewHTTPClientManager(),
			redact.New(),
		).ForwardStream(ctx, streamForwardInput(upstream.URL), downstream)

		if !result.Committed ||
			result.Stream.EndReason != StreamEndClientCanceled ||
			!errors.Is(result.Err, wantErr) {
			t.Fatalf("ForwardStream() cancellation-priority result = %#v", result)
		}
		if downstream.status != http.StatusOK || downstream.body.Len() != 0 {
			t.Fatalf(
				"downstream status/body = %d/%q, want committed 200/empty",
				downstream.status,
				downstream.body.String(),
			)
		}
	})

	t.Run("ordinary write failure", func(t *testing.T) {
		wantErr := errors.New("downstream write failed")
		downstream := newRecordingResponseWriter()
		downstream.writeErr = wantErr
		result := NewForwarder(
			platformhttp.NewHTTPClientManager(),
			redact.New(),
		).ForwardStream(
			context.Background(),
			streamForwardInput(upstream.URL),
			downstream,
		)

		if !result.Committed ||
			result.Stream.EndReason != StreamEndDownstreamWriteFailure ||
			!errors.Is(result.Err, wantErr) {
			t.Fatalf("ForwardStream() write-failure result = %#v", result)
		}
		if downstream.status != http.StatusOK || downstream.body.Len() != 0 {
			t.Fatalf(
				"downstream status/body = %d/%q, want committed 200/empty",
				downstream.status,
				downstream.body.String(),
			)
		}
	})
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
				result.RetryableBeforeCommit || calls.Load() != 0 {
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
		result.Committed || result.RetryableBeforeCommit || calls.Load() != 0 {
		t.Fatalf("ForwardStream() result = %#v calls=%d, want terminal pre-commit protocol error",
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
		result.Committed || result.RetryableBeforeCommit {
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

	if !errors.Is(result.Err, ErrUpstreamProtocol) || result.Committed || result.RetryableBeforeCommit {
		t.Fatalf("ForwardStream() result = %#v, want terminal pre-commit protocol error", result)
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
	stream := `data: {"ready":true}` + "\n\n" +
		"event: error\n" +
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
	if len(payloads) != 3 {
		t.Fatalf("decoded payload count = %d, wire=%q", len(payloads), downstream.body.String())
	}
	if payloads[1]["model"] != externalModel ||
		payloads[1][externalModel] != externalModel+" failed" ||
		payloads[1]["credential"] != redact.Placeholder {
		t.Fatalf("first error payload = %#v", payloads[1])
	}
	if payloads[2]["message"] != "later "+externalModel+" "+redact.Placeholder {
		t.Fatalf("later error payload = %#v", payloads[2])
	}
}

func TestForwardStreamSanitizesDataOnlyAliasedErrorPayloads(t *testing.T) {
	const (
		upstreamModel = "provider-model"
		externalModel = "public-model"
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, `data: {"ready":true}

data: {"error":{"message":"provider-model failed"}}

`)
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.ExternalModel = externalModel
	input.UpstreamModelID = upstreamModel
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if result.Err != nil || !result.Committed {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	payloads := decodeSSEJSONPayloads(t, downstream.body.Bytes())
	if len(payloads) != 2 {
		t.Fatalf("decoded payloads = %#v, wire=%q", payloads, downstream.body.String())
	}
	errorObject := payloads[1]["error"].(map[string]any)
	if errorObject["message"] != externalModel+" failed" {
		t.Fatalf("error message = %#v, want %q", errorObject["message"], externalModel+" failed")
	}
}

func TestForwardStreamSanitizesTypeErrorAliasedPayloads(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, `data: {"ready":true}

data: {"type":"error","message":"provider-model failed"}

`)
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.ExternalModel = "public-model"
	input.UpstreamModelID = "provider-model"
	downstream := newRecordingResponseWriter()
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, downstream,
	)

	if result.Err != nil || !result.Committed {
		t.Fatalf("ForwardStream() result = %#v", result)
	}
	payloads := decodeSSEJSONPayloads(t, downstream.body.Bytes())
	if len(payloads) != 2 || payloads[1]["message"] != "public-model failed" {
		t.Fatalf("sanitized payloads = %#v, wire=%q", payloads, downstream.body.String())
	}
}

func TestForwardStreamAliasOnlyRewritesModelFieldsInSuccessfulEvents(t *testing.T) {
	const (
		upstreamModel = "provider-model"
		externalModel = "public-model"
		secret        = "provider-secret"
		content       = "provider-model " + redact.Placeholder
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, `data: {"model":"provider-model","choices":[{"delta":{"content":"provider-model provider-secret","tool_calls":[{"function":{"arguments":"{\"model\":\"provider-model\",\"key\":\"provider-secret\"}"}}]}}]}

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
	if len(payloads) != 1 || payloads[0]["model"] != externalModel {
		t.Fatalf("rewritten payloads = %#v, wire=%q", payloads, downstream.body.String())
	}
	choices := payloads[0]["choices"].([]any)
	delta := choices[0].(map[string]any)["delta"].(map[string]any)
	if delta["content"] != content {
		t.Fatalf("generated content = %#v, want %q", delta["content"], content)
	}
	toolCalls := delta["tool_calls"].([]any)
	arguments := toolCalls[0].(map[string]any)["function"].(map[string]any)["arguments"]
	if arguments != `{"model":"provider-model","key":"[REDACTED]"}` {
		t.Fatalf("tool arguments = %#v, want provider model preserved and credential redacted", arguments)
	}
}

func TestForwardStreamUsesFinalSSEEventTypeForAliasSanitization(t *testing.T) {
	for _, test := range []struct {
		name       string
		eventLines string
	}{
		{name: "later message event", eventLines: "event: error\nevent: message\n"},
		{name: "later empty event", eventLines: "event: error\nevent\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.eventLines+`data: {"model":"provider-model","content":"provider-model is generated content"}`+"\n\n")
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.ExternalModel = "public-model"
			input.UpstreamModelID = "provider-model"
			downstream := newRecordingResponseWriter()
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
				context.Background(), input, downstream,
			)

			if result.Err != nil || !result.Committed {
				t.Fatalf("ForwardStream() result = %#v", result)
			}
			payloads := decodeSSEJSONPayloads(t, downstream.body.Bytes())
			if len(payloads) != 1 || payloads[0]["model"] != "public-model" ||
				payloads[0]["content"] != "provider-model is generated content" {
				t.Fatalf("rewritten payloads = %#v, wire=%q", payloads, downstream.body.String())
			}
		})
	}
}

func TestForwardStreamSanitizesUnaliasedErrorEventPayloads(t *testing.T) {
	const secret = "stream/secret"
	stream := `data: {"ready":true}` + "\n\n" +
		"event: error\n" +
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
	if len(payloads) != 3 || payloads[1]["credential"] != redact.Placeholder ||
		payloads[1]["raw"] != redact.Placeholder ||
		payloads[2]["message"] != "later "+redact.Placeholder {
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
			if !test.wantCommit && result.RetryableBeforeCommit {
				t.Fatalf("request-written pre-commit failure is retryable: %#v", result)
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
		_, _ = io.WriteString(writer, `data: {"ready":true}

event: error
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
	if len(payloads) != 2 || payloads[1]["message"] != redact.Placeholder {
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
			if !test.wantCommit && result.RetryableBeforeCommit {
				t.Fatalf("request-written pre-commit failure is retryable: %#v", result)
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

func TestForwardStreamBuffersCodedNonSuccessRepresentation(t *testing.T) {
	plain := []byte(`{"error":{"code":"rate_limited"}}`)
	for _, encoding := range []contentcoding.Encoding{
		contentcoding.Identity,
		contentcoding.Gzip,
		contentcoding.Brotli,
		contentcoding.Deflate,
		contentcoding.Zstd,
	} {
		t.Run(string(encoding), func(t *testing.T) {
			wire := encodeContentCodingForGatewayTest(t, encoding, plain)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Encoding", string(encoding))
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			downstream := newRecordingResponseWriter()
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
				context.Background(), streamForwardInput(upstream.URL), downstream,
			)
			if result.Err != nil || result.Committed || result.StatusCode != http.StatusUnauthorized ||
				!bytes.Equal(result.Body, plain) || !bytes.Equal(result.ClassificationBody, plain) ||
				len(headerFieldValues(result.Header, "Content-Encoding")) != 0 ||
				result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) {
				t.Fatalf("ForwardStream() result = %#v, want buffered plaintext error", result)
			}
			if downstream.status != 0 || downstream.body.Len() != 0 {
				t.Fatalf("ForwardStream() wrote non-success response before Handler verdict: %d/%q", downstream.status, downstream.body.String())
			}
		})
	}

	t.Run("multiple content-encoding values fail closed", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Add("Content-Encoding", "identity")
			writer.Header().Add("Content-Encoding", "gzip")
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte("opaque"))
		}))
		defer upstream.Close()

		result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
			context.Background(), streamForwardInput(upstream.URL), newRecordingResponseWriter(),
		)
		if result.Err != nil || result.Committed || result.StatusCode != http.StatusBadGateway ||
			string(result.Body) != redact.Placeholder || string(result.ClassificationBody) != redact.Placeholder ||
			len(headerFieldValues(result.Header, "Content-Encoding")) != 0 {
			t.Fatalf("ForwardStream() result = %#v, want fail-closed buffered error", result)
		}
	})
}

func TestForwardStreamContentCodingBoundary(t *testing.T) {
	for _, test := range []struct {
		name      string
		values    []string
		wantError bool
	}{
		{name: "absent"},
		{name: "empty", values: []string{""}},
		{name: "identity", values: []string{"IDENTITY"}},
		{name: "gzip", values: []string{"gzip"}, wantError: true},
		{name: "brotli", values: []string{"br"}, wantError: true},
		{name: "deflate", values: []string{"deflate"}, wantError: true},
		{name: "zstd", values: []string{"zstd"}, wantError: true},
		{name: "stacked", values: []string{"gzip, br"}, wantError: true},
		{name: "unknown", values: []string{"compress"}, wantError: true},
		{name: "multiple", values: []string{"identity", "gzip"}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				for _, value := range test.values {
					writer.Header().Add("Content-Encoding", value)
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte("data: first\n\n"))
			}))
			defer upstream.Close()

			downstream := newRecordingResponseWriter()
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
				context.Background(), streamForwardInput(upstream.URL), downstream,
			)
			if test.wantError {
				if !errors.Is(result.Err, ErrUpstreamProtocol) || result.Committed ||
					downstream.status != 0 || downstream.body.Len() != 0 {
					t.Fatalf("ForwardStream() result/downstream = %#v/%d/%q", result, downstream.status, downstream.body.String())
				}
				return
			}
			if result.Err != nil || !result.Committed || downstream.status != http.StatusOK ||
				downstream.body.String() != "data: first\n\n" {
				t.Fatalf("ForwardStream() result/downstream = %#v/%d/%q", result, downstream.status, downstream.body.String())
			}
		})
	}

	t.Run("case-colliding header map is rejected", func(t *testing.T) {
		err := validateStreamContentEncoding(http.Header{
			"Content-Encoding": {"identity"},
			"content-encoding": {"identity"},
		})
		if !errors.Is(err, ErrUpstreamProtocol) {
			t.Fatalf("validateStreamContentEncoding() error = %v, want upstream protocol error", err)
		}
	})
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
		result.RetryableBeforeCommit || calls.Load() != 0 {
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
	request, _, replay, err := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).newUpstreamRequest(context.Background(), input, true)
	if err != nil {
		t.Fatalf("newUpstreamRequest() error = %v", err)
	}
	if request.GetBody != nil {
		t.Fatal("newUpstreamRequest() exposed GetBody before commit")
	}
	originalBody := request.Body

	releaseCommittedRequestReplay(input.Request, replay)

	if input.Request.Body != nil {
		t.Fatal("ParsedRequest.Body still retains the replay buffer")
	}
	if request.Body != originalBody || request.GetBody != nil {
		t.Fatal("committed release mutated fields owned by the HTTP transport")
	}
	activeBody, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read active request body: %v", err)
	}
	if !bytes.Equal(activeBody, wantBody) {
		t.Fatalf("active request body = %q, want %q", activeBody, wantBody)
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
	requestBodyRead := make(chan struct{})
	firstSSEEvent := make(chan struct{})
	downstream := newBarrierStreamResponseWriter()

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		close(requestBodyRead)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: ready\n\n"))
		writer.(http.Flusher).Flush()
		close(firstSSEEvent)
		<-downstream.release
	}))
	defer func() {
		downstream.unblock()
		upstream.Close()
	}()

	input := streamForwardInput(upstream.URL)
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	done := make(chan UpstreamResult, 1)
	go func() {
		done <- forwarder.ForwardStream(context.Background(), input, downstream)
	}()

	waitForSignal(t, requestBodyRead, "upstream request body read")
	waitForSignal(t, firstSSEEvent, "first upstream SSE event")
	waitForSignal(t, downstream.firstFlush, "first downstream SSE event flush")
	if input.Request.Body != nil {
		t.Fatalf("committed stream still retains %d request body bytes", len(input.Request.Body))
	}
	downstream.unblock()

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
	if !config.DisableCompression {
		t.Fatal("stream DisableCompression = false; transport must not implicitly decode while request normalization explicitly negotiates identity")
	}
}

func TestNonStreamingClientConfigDisablesImplicitCompression(t *testing.T) {
	config := nonStreamingClientConfig(state.TimeoutConfig{})
	if !config.DisableCompression {
		t.Fatal("non-stream DisableCompression = false; transport must not implicitly decode while request normalization explicitly negotiates identity")
	}
}

func TestGatewayClientConfigsDisableRedirects(t *testing.T) {
	timeouts := state.TimeoutConfig{
		Connect: 2 * time.Second, FirstByte: 3 * time.Second,
		Request: 4 * time.Second, StreamIdle: 5 * time.Second,
	}
	for name, config := range map[string]*platformhttp.Config{
		"non-streaming": nonStreamingClientConfig(timeouts),
		"streaming":     streamingClientConfig(timeouts),
	} {
		if !config.DisableRedirects {
			t.Errorf("%s client DisableRedirects = false, want true", name)
		}
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
				wire = encodeContentCodingForGatewayTest(t, contentcoding.Encoding(encoding), plain)
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
			if bytes.Contains(downstreamBody, []byte(upstreamModel)) ||
				!bytes.Contains(downstreamBody, []byte(externalModel)) ||
				bytes.Contains(downstreamBody, []byte(secret)) ||
				!bytes.Contains(downstreamBody, []byte(redact.Placeholder)) {
				t.Fatalf("downstream body = %q", downstreamBody)
			}
			if result.Header.Get("Content-Type") != "application/json" ||
				result.Header.Get("Content-Encoding") != "" ||
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
			wire = encodeContentCodingForGatewayTest(t, contentcoding.Encoding(encoding), plain)
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
			wire = encodeContentCodingForGatewayTest(t, contentcoding.Encoding(encoding), plain)
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

func TestPrepareErrorRepresentationFailsClosedWhenEscapedAPIKeyRewriteCollides(t *testing.T) {
	const secret = "secret/key"
	plain := []byte(`{"secret\/key":"leak","[REDACTED]":"safe"}`)
	forwarder := &Forwarder{redactor: redact.New()}
	headers := http.Header{"Content-Type": {"application/json"}}
	prepared := forwarder.prepareErrorRepresentation(ForwardInput{APIKey: secret}, headers, plain, []string{secret})

	if string(prepared.downstream) != redact.Placeholder || string(prepared.classification) != redact.Placeholder {
		t.Fatalf("collision result = %#v", prepared)
	}
}

func TestPrepareErrorRepresentationFailsClosedBeforeRawAPIKeyCollisionRedaction(t *testing.T) {
	const secret = "secret"
	plain := []byte(`{"secret":"leak","[REDACTED]":"safe"}`)
	forwarder := &Forwarder{redactor: redact.New()}
	headers := http.Header{"Content-Type": {"application/json"}}
	prepared := forwarder.prepareErrorRepresentation(ForwardInput{APIKey: secret}, headers, plain, []string{secret})

	if string(prepared.downstream) != redact.Placeholder || string(prepared.classification) != redact.Placeholder {
		t.Fatalf("collision result = %#v", prepared)
	}
}

func TestForwarderFailsClosedWhenJSONKeyRewriteCollides(t *testing.T) {
	plain := []byte(`{"provider-model":"first","public-model":"second"}`)
	forwarder := &Forwarder{redactor: redact.New()}
	headers := http.Header{"Content-Type": {"application/json"}}
	prepared := forwarder.prepareErrorRepresentation(ForwardInput{
		ExternalModel: "public-model", UpstreamModelID: "provider-model",
	}, headers, plain, nil)

	if string(prepared.downstream) != redact.Placeholder || string(prepared.classification) != redact.Placeholder {
		t.Fatalf("collision result = %#v", prepared)
	}
}

func TestPrepareErrorRepresentationFailsClosedWhenModelExpansionExceedsLimit(t *testing.T) {
	plain := bytes.Repeat([]byte("x"), 64<<10)
	forwarder := &Forwarder{redactor: redact.New()}
	headers := make(http.Header)
	prepared := forwarder.prepareErrorRepresentation(ForwardInput{
		ExternalModel: strings.Repeat("a", 32), UpstreamModelID: "x",
	}, headers, plain, nil)

	if string(prepared.downstream) != redact.Placeholder || string(prepared.classification) != redact.Placeholder {
		t.Fatalf("expansion result = %#v", prepared)
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

func TestForwarderRedactsCompressedErrorAsPlaintext(t *testing.T) {
	const secret = "custom-upstream-secret"
	plain := []byte(`{"error":{"api_key":"` + secret + `","code":"invalid_api_key"}}`)
	encoded := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain)
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
	if result.Header.Get("Content-Encoding") != "" {
		t.Fatalf("Content-Encoding = %q, want absent", result.Header.Get("Content-Encoding"))
	}
	for _, body := range [][]byte{result.Body, result.ClassificationBody} {
		if bytes.Contains(body, []byte(secret)) || !bytes.Contains(body, []byte(redact.Placeholder)) {
			t.Fatalf("safe body = %q, want placeholder and no secret", body)
		}
	}
	if result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) {
		t.Fatalf("Content-Length = %q, body length = %d", result.Header.Get("Content-Length"), len(result.Body))
	}
	assertRepresentationMetadata(t, result.Header, false)
}

func TestForwarderErrorRepresentationsArePlain(t *testing.T) {
	plain := []byte(`{"error":{"code":"rate_limited"}}`)
	for _, encoding := range []contentcoding.Encoding{
		contentcoding.Identity,
		contentcoding.Gzip,
		contentcoding.Brotli,
		contentcoding.Deflate,
		contentcoding.Zstd,
	} {
		t.Run(string(encoding), func(t *testing.T) {
			wire := encodeContentCodingForGatewayTest(t, encoding, plain)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if encoding != contentcoding.Identity {
					writer.Header().Set("Content-Encoding", string(encoding))
				}
				writer.WriteHeader(http.StatusTooManyRequests)
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			result := testForward(t, upstream.URL, "safe-upstream-key", time.Second)
			if result.Err != nil || result.StatusCode != http.StatusTooManyRequests ||
				!bytes.Equal(result.Body, plain) || !bytes.Equal(result.ClassificationBody, plain) ||
				len(headerFieldValues(result.Header, "Content-Encoding")) != 0 ||
				result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) {
				t.Fatalf("Forward() result = %#v, want plaintext error representation", result)
			}
		})
	}
}

func TestForwarderErrorRepresentationMetadataPolicy(t *testing.T) {
	plain := []byte(`{"error":"safe"}`)
	for _, test := range []struct {
		name             string
		encoding         contentcoding.Encoding
		wantMetadataKept bool
	}{
		{name: "unchanged identity", encoding: contentcoding.Identity, wantMetadataKept: true},
		{name: "decoded gzip", encoding: contentcoding.Gzip, wantMetadataKept: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire := encodeContentCodingForGatewayTest(t, test.encoding, plain)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Encoding", string(test.encoding))
				setRepresentationMetadata(writer.Header())
				writer.WriteHeader(http.StatusBadGateway)
				_, _ = writer.Write(wire)
			}))
			defer upstream.Close()

			result := testForward(t, upstream.URL, "safe-upstream-key", time.Second)
			if result.Err != nil || !bytes.Equal(result.Body, plain) {
				t.Fatalf("Forward() result = %#v", result)
			}
			if test.wantMetadataKept {
				for name, want := range map[string]string{
					"ETag":           `"wire-v1"`,
					"Digest":         "sha-256=wire-digest",
					"Content-MD5":    "d2lyZQ==",
					"Content-Range":  "bytes 0-9/10",
					"Content-Digest": "sha-256=:d2lyZQ==:",
					"Repr-Digest":    "sha-256=:cmVwcg==:",
				} {
					if got := result.Header.Get(name); got != want {
						t.Fatalf("%s = %q, want %q", name, got, want)
					}
				}
				if result.Header.Get("Signature") != "" || result.Header.Get("Signature-Input") != "" {
					t.Fatalf("normalized identity representation retained stale signatures: %#v", result.Header)
				}
			} else {
				assertRepresentationMetadata(t, result.Header, false)
			}
		})
	}
}

func TestForwarderErrorContentCodingFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		coding string
		body   []byte
		apiKey string
	}{
		{name: "unknown", coding: "unknown", body: []byte("opaque")},
		{name: "stacked", coding: "gzip, br", body: []byte("opaque")},
		{name: "corrupt gzip", coding: "gzip", body: []byte("not-gzip")},
		{name: "short key no special bypass", coding: "gzip", body: encodeContentCodingForGatewayTest(t, contentcoding.Gzip, []byte(`{"error":"safe"}`)), apiKey: "gzip"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Encoding", test.coding)
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write(test.body)
			}))
			defer upstream.Close()

			result := testForward(t, upstream.URL, test.apiKey, time.Second)
			want := []byte(redact.Placeholder)
			if test.name == "short key no special bypass" {
				want = []byte(`{"error":"safe"}`)
			}
			if result.Err != nil || result.StatusCode != http.StatusUnauthorized ||
				!bytes.Equal(result.Body, want) || !bytes.Equal(result.ClassificationBody, want) ||
				len(headerFieldValues(result.Header, "Content-Encoding")) != 0 {
				t.Fatalf("Forward() result = %#v, want safe error representation", result)
			}
		})
	}
}

func TestForwarderReturnsUnchangedCompressedErrorAsPlaintext(t *testing.T) {
	plain := []byte(`{"error":{"code":"rate_limited"}}`)
	encoded := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain)
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
	if !bytes.Equal(result.Body, plain) {
		t.Fatalf("downstream body = %q, want %q", result.Body, plain)
	}
	if !bytes.Equal(result.ClassificationBody, plain) {
		t.Fatalf("ClassificationBody = %q, want %q", result.ClassificationBody, plain)
	}
	if len(headerFieldValues(result.Header, "Content-Encoding")) != 0 ||
		result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) {
		t.Fatalf("plaintext response headers = %#v", result.Header)
	}
	assertRepresentationMetadata(t, result.Header, false)
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

func TestForwarderBodylessResponseSemantics(t *testing.T) {
	tests := []struct {
		name              string
		method            string
		status            int
		contentEncoding   string
		contentLength     string
		wantErr           bool
		wantContentLength string
	}{
		{
			name:              "head preserves oversized representation length without reading it",
			method:            http.MethodHead,
			status:            http.StatusOK,
			contentEncoding:   "identity",
			contentLength:     strconv.FormatInt(maxNonStreamingResponseBodyBytes+1, 10),
			wantContentLength: strconv.FormatInt(maxNonStreamingResponseBodyBytes+1, 10),
		},
		{
			name:            "not modified is bodyless",
			method:          http.MethodGet,
			status:          http.StatusNotModified,
			contentEncoding: "identity",
			contentLength:   "321",
		},
		{
			name:            "no content removes length",
			method:          http.MethodGet,
			status:          http.StatusNoContent,
			contentEncoding: "identity",
			contentLength:   "321",
		},
		{
			name:              "reset content keeps zero length only",
			method:            http.MethodGet,
			status:            http.StatusResetContent,
			contentEncoding:   "identity",
			contentLength:     "0",
			wantContentLength: "0",
		},
		{
			name:            "bodyless response rejects compressed encoding",
			method:          http.MethodHead,
			status:          http.StatusOK,
			contentEncoding: "gzip",
			wantErr:         true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != test.method {
					t.Errorf("request method = %s, want %s", request.Method, test.method)
				}
				if test.contentEncoding != "" {
					writer.Header().Set("Content-Encoding", test.contentEncoding)
				}
				if test.contentLength != "" {
					writer.Header().Set("Content-Length", test.contentLength)
				}
				writer.Header().Set("ETag", `"safe"`)
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte("must not be read"))
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.ObserveUsage = false
			input.Request.Method = test.method
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
				context.Background(), input,
			)

			if test.wantErr {
				if !errors.Is(result.Err, ErrUpstreamProtocol) || !result.RequestWritten {
					t.Fatalf("Forward() result = %#v, want written upstream protocol error", result)
				}
				return
			}
			if result.Err != nil || result.StatusCode != test.status || !result.RequestWritten || len(result.Body) != 0 || len(result.ClassificationBody) != 0 {
				t.Fatalf("Forward() result = %#v, want bodyless response", result)
			}
			if values := headerFieldValues(result.Header, "Content-Encoding"); len(values) != 0 {
				t.Fatalf("Content-Encoding values = %#v, want absent", values)
			}
			if got := result.Header.Get("Content-Length"); got != test.wantContentLength {
				t.Fatalf("Content-Length = %q, want %q", got, test.wantContentLength)
			}
			if result.Header.Get("ETag") != `"safe"` {
				t.Fatalf("ETag = %q, want safe validator preserved", result.Header.Get("ETag"))
			}
		})
	}
}

func TestForwarderBodylessResponseInvalidatesSignaturesAfterHeaderNormalization(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		status           int
		contentEncoding  string
		contentLength    string
		credentialHeader bool
	}{
		{
			name:            "head removes identity coding",
			method:          http.MethodHead,
			status:          http.StatusOK,
			contentEncoding: "identity",
			contentLength:   "12",
		},
		{
			name:            "not modified removes identity coding",
			method:          http.MethodGet,
			status:          http.StatusNotModified,
			contentEncoding: "identity",
			contentLength:   "12",
		},
		{
			name:          "reset content removes nonzero length",
			method:        http.MethodGet,
			status:        http.StatusResetContent,
			contentLength: "12",
		},
		{
			name:             "head sanitizes credential header",
			method:           http.MethodHead,
			status:           http.StatusOK,
			contentLength:    "12",
			credentialHeader: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if test.contentEncoding != "" {
					writer.Header().Set("Content-Encoding", test.contentEncoding)
				}
				if test.contentLength != "" {
					writer.Header().Set("Content-Length", test.contentLength)
				}
				writer.Header().Set("Signature", "sig")
				writer.Header().Set("Signature-Input", `sig1=("content-encoding" "content-length")`)
				if test.credentialHeader {
					writer.Header().Set("Authorization", "Bearer upstream-secret")
				}
				writer.WriteHeader(test.status)
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.ObserveUsage = false
			input.Request.Method = test.method
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
				context.Background(), input,
			)
			if result.Err != nil || result.StatusCode != test.status || !result.RequestWritten ||
				len(headerFieldValues(result.Header, "Signature")) != 0 ||
				len(headerFieldValues(result.Header, "Signature-Input")) != 0 {
				t.Fatalf("Forward() result = %#v, want bodyless result without stale signatures", result)
			}
		})
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

func TestForwarderDecodesContentEncodingListedAsConnectionToken(t *testing.T) {
	plain := []byte(`{"id":"safe"}`)
	clients := platformhttp.NewHTTPClientManager()
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			ID: 1, UpstreamURL: "https://api.example.test",
			Timeouts: state.TimeoutConfig{Connect: time.Second, FirstByte: time.Second, Request: time.Second},
		},
		APIKey: "sk-upstream-secret",
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost,
			Path:   "/v1/chat/completions",
			Header: make(http.Header),
			Body:   []byte(`{"model":"gpt-4o"}`),
		},
	}
	clients.GetClient(nonStreamingClientConfig(input.Group.Timeouts)).Transport = forwarderRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Connection":       {"Content-Encoding"},
				"Content-Encoding": {"gzip"},
			},
			Body:    io.NopCloser(bytes.NewReader(encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain))),
			Request: request,
		}, nil
	})

	result := NewForwarder(clients, redact.New()).Forward(context.Background(), input)
	if result.Err != nil || !bytes.Equal(result.Body, plain) || len(headerFieldValues(result.Header, "Content-Encoding")) != 0 {
		t.Fatalf("Forward() result = %#v, want plaintext response without Content-Encoding", result)
	}
}

func TestForwarderBodylessResponseInvalidatesSignaturesAfterConnectionTokenCleanup(t *testing.T) {
	clients := platformhttp.NewHTTPClientManager()
	input := streamForwardInput("https://api.example.test")
	input.ObserveUsage = false
	input.Request.Method = http.MethodHead
	clients.GetClient(nonStreamingClientConfig(input.Group.Timeouts)).Transport = forwarderRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Connection":      {"X-Signed"},
				"X-Signed":        {"upstream-only"},
				"Content-Length":  {"12"},
				"Signature":       {"sig"},
				"Signature-Input": {`sig1=("x-signed" "content-length")`},
			},
			Body:    io.NopCloser(strings.NewReader("")),
			Request: request,
		}, nil
	})

	result := NewForwarder(clients, redact.New()).Forward(context.Background(), input)
	if result.Err != nil || result.StatusCode != http.StatusOK || !result.RequestWritten {
		t.Fatalf("Forward() result = %#v, want bodyless forwarded response", result)
	}
	if len(headerFieldValues(result.Header, "X-Signed")) != 0 ||
		len(headerFieldValues(result.Header, "Signature")) != 0 ||
		len(headerFieldValues(result.Header, "Signature-Input")) != 0 {
		t.Fatalf("bodyless response retained hop-by-hop cleanup or stale signatures: %#v", result.Header)
	}
	if result.Header.Get("Content-Length") != "12" {
		t.Fatalf("Content-Length = %q, want preserved bodyless representation length", result.Header.Get("Content-Length"))
	}
}

func TestForwardStreamRejectsContentEncodingListedAsConnectionToken(t *testing.T) {
	plain := []byte("data: first\n\n")
	wire := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain)
	clients := platformhttp.NewHTTPClientManager()
	input := streamForwardInput("https://api.example.test")
	clients.GetClient(streamingClientConfig(input.Group.Timeouts)).Transport = forwarderRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Connection":       {"Content-Encoding"},
				"Content-Encoding": {"gzip"},
			},
			Body:          io.NopCloser(bytes.NewReader(wire)),
			ContentLength: int64(len(wire)),
			Request:       request,
		}, nil
	})

	downstream := newRecordingResponseWriter()
	result := NewForwarder(clients, redact.New()).ForwardStream(context.Background(), input, downstream)
	if !errors.Is(result.Err, ErrUpstreamProtocol) || result.Committed || result.RetryableBeforeCommit {
		t.Fatalf("ForwardStream() result = %#v, want terminal pre-commit protocol error", result)
	}
	if downstream.status != 0 || downstream.body.Len() != 0 || downstream.flushes != 0 {
		t.Fatalf("downstream was touched before protocol rejection: %#v", downstream)
	}
}

func TestForwarderDropsSignaturesFromUnchangedIdentityResponse(t *testing.T) {
	plain := []byte(`{"id":"safe"}`)
	clients := platformhttp.NewHTTPClientManager()
	input := streamForwardInput("https://api.example.test")
	input.ObserveUsage = false
	input.Request.Body = []byte(`{"model":"gpt-4o"}`)
	clients.GetClient(nonStreamingClientConfig(input.Group.Timeouts)).Transport = forwarderRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Length":  {strconv.Itoa(len(plain))},
				"ETag":            {`"wire-v1"`},
				"Signature":       {"sig"},
				"Signature-Input": {`sig1=("etag")`},
			},
			Body:          io.NopCloser(bytes.NewReader(plain)),
			ContentLength: int64(len(plain)),
			Request:       request,
		}, nil
	})

	result := NewForwarder(clients, redact.New()).Forward(context.Background(), input)
	if result.Err != nil || !bytes.Equal(result.Body, plain) || !reflect.DeepEqual(headerFieldValues(result.Header, "ETag"), []string{`"wire-v1"`}) {
		t.Fatalf("Forward() result = %#v, bodyEqual=%t etag=%#v want unchanged identity representation with safe validator", result, bytes.Equal(result.Body, plain), headerFieldValues(result.Header, "ETag"))
	}
	if len(headerFieldValues(result.Header, "Signature")) != 0 ||
		len(headerFieldValues(result.Header, "Signature-Input")) != 0 {
		t.Fatalf("unchanged identity response retained signatures: %#v", result.Header)
	}
}

func TestForwarderBodylessResponseDropsUnchangedIdentitySignatures(t *testing.T) {
	clients := platformhttp.NewHTTPClientManager()
	input := streamForwardInput("https://api.example.test")
	input.ObserveUsage = false
	input.Request.Method = http.MethodHead
	clients.GetClient(nonStreamingClientConfig(input.Group.Timeouts)).Transport = forwarderRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Length":  {"12"},
				"ETag":            {`"wire-v1"`},
				"Signature":       {"sig"},
				"Signature-Input": {`sig1=("etag")`},
			},
			Body:    io.NopCloser(strings.NewReader("")),
			Request: request,
		}, nil
	})

	result := NewForwarder(clients, redact.New()).Forward(context.Background(), input)
	if result.Err != nil || result.StatusCode != http.StatusOK || !reflect.DeepEqual(headerFieldValues(result.Header, "ETag"), []string{`"wire-v1"`}) {
		t.Fatalf("Forward() result = %#v, etag=%#v want unchanged bodyless representation with safe validator", result, headerFieldValues(result.Header, "ETag"))
	}
	if len(headerFieldValues(result.Header, "Signature")) != 0 ||
		len(headerFieldValues(result.Header, "Signature-Input")) != 0 {
		t.Fatalf("unchanged bodyless response retained signatures: %#v", result.Header)
	}
}

type forwarderRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip forwarderRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func testForward(t *testing.T, upstreamURL, apiKey string, timeout time.Duration) UpstreamResult {
	t.Helper()
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	return forwarder.Forward(context.Background(), ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			ID: 1, UpstreamURL: testUpstreamBaseURL(upstreamURL, protocol.OpenAICompletions),
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
		Dialect:      dialect.NewOpenAI(http.DefaultClient),
		ObserveUsage: true,
		Group: state.GroupView{
			ID: 1, Name: "openai", UpstreamURL: testUpstreamBaseURL(upstreamURL, protocol.OpenAICompletions),
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

type streamUsageInjectorDialect struct {
	*dialect.OpenAI
	inject func(*dialect.ParsedRequest) (*dialect.ParsedRequest, error)
}

func (d streamUsageInjectorDialect) InjectStreamUsage(request *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
	return d.inject(request)
}

type dialectWithoutStreamUsageInjector struct{ dialect.Dialect }

func TestForwardNeverInjectsNonStreamingRequest(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"response"}`))
	}))
	defer upstream.Close()

	injectCalls := 0
	input := streamForwardInput(upstream.URL)
	input.Group.InjectUsageOptions = true
	input.Request.Body = []byte(`{"model":"gpt-4o"}`)
	input.Dialect = streamUsageInjectorDialect{
		OpenAI: dialect.NewOpenAI(http.DefaultClient),
		inject: func(request *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
			injectCalls++
			return dialect.NewOpenAI(http.DefaultClient).InjectStreamUsage(request)
		},
	}

	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(context.Background(), input)
	if result.Err != nil || injectCalls != 0 {
		t.Fatalf("Forward() result/inject calls = %#v/%d", result, injectCalls)
	}
	if _, exists := received["stream_options"]; exists {
		t.Fatalf("non-stream upstream request was injected: %#v", received)
	}
}

func TestForwardStreamSkipsDialectWithoutStreamUsageInjector(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[]}\n\n"))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Group.InjectUsageOptions = true
	input.Dialect = dialectWithoutStreamUsageInjector{Dialect: dialect.NewOpenAI(http.DefaultClient)}
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	result := forwarder.ForwardStream(context.Background(), input, newRecordingResponseWriter())
	if result.Err != nil || !result.Committed || forwarder.usageCapture.failureTotal.Load() != 0 {
		t.Fatalf("ForwardStream() result/failures = %#v/%d", result, forwarder.usageCapture.failureTotal.Load())
	}
	if _, exists := received["stream_options"]; exists {
		t.Fatalf("capability-less dialect injected usage: %#v", received)
	}
}

func TestForwardStreamInjectsOpenAIUsageWhenEffective(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[]}\n\n"))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Group.InjectUsageOptions = true
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, newRecordingResponseWriter(),
	)
	if result.Err != nil || !result.Committed {
		t.Fatalf("ForwardStream() = %#v", result)
	}
	options, ok := received["stream_options"].(map[string]any)
	if !ok || options["include_usage"] != true {
		t.Fatalf("upstream request = %#v, want stream_options.include_usage=true", received)
	}
}

func TestForwardStreamInjectsUsageAfterModelAliasRewrite(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[]}\n\n"))
	}))
	defer upstream.Close()

	input := streamForwardInput(upstream.URL)
	input.Group.InjectUsageOptions = true
	input.ExternalModel = "public-model"
	input.UpstreamModelID = "provider-model"
	input.Request.Body = []byte(`{"model":"public-model","stream":true}`)
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
		context.Background(), input, newRecordingResponseWriter(),
	)
	if result.Err != nil || !result.Committed || received["model"] != "provider-model" {
		t.Fatalf("ForwardStream() result/request = %#v/%#v", result, received)
	}
	options, ok := received["stream_options"].(map[string]any)
	if !ok || options["include_usage"] != true {
		t.Fatalf("rewritten request did not retain injected usage: %#v", received)
	}
}

func TestForwardStreamNormalizesFinalRequestMetadata(t *testing.T) {
	type capturedRequest struct {
		body          []byte
		headers       http.Header
		contentLength int64
	}
	tests := []struct {
		name            string
		configure       func(*ForwardInput)
		wantBodyChanged bool
	}{
		{
			name: "usage injection invalidates stale metadata",
			configure: func(input *ForwardInput) {
				input.Group.InjectUsageOptions = true
			},
			wantBodyChanged: true,
		},
		{
			name: "disabled injection still removes stale metadata",
		},
		{
			name: "failed injection still removes stale metadata when body is unchanged",
			configure: func(input *ForwardInput) {
				input.Group.InjectUsageOptions = true
				input.Dialect = streamUsageInjectorDialect{
					OpenAI: dialect.NewOpenAI(http.DefaultClient),
					inject: func(*dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
						return nil, errors.New("inject failed")
					},
				}
			},
		},
		{
			name: "model alias rewrite invalidates stale metadata",
			configure: func(input *ForwardInput) {
				input.ExternalModel = "public-model"
				input.UpstreamModelID = "provider-model"
				input.Request.Body = []byte(`{"model":"public-model","stream":true}`)
			},
			wantBodyChanged: true,
		},
		{
			name: "group header rules cannot restore stale metadata",
			configure: func(input *ForwardInput) {
				input.Group.InjectUsageOptions = true
				input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{
					"Digest":    "sha-256=group-digest",
					"Signature": "group-signature",
				}}
			},
			wantBodyChanged: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := make(chan capturedRequest, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Error(err)
				}
				received <- capturedRequest{
					body:          body,
					headers:       request.Header.Clone(),
					contentLength: request.ContentLength,
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte("data: {\"choices\":[]}\n\n"))
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			setRepresentationMetadata(input.Request.Header)
			if test.configure != nil {
				test.configure(&input)
			}
			originalBody := bytes.Clone(input.Request.Body)
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
				context.Background(), input, newRecordingResponseWriter(),
			)
			if result.Err != nil || !result.Committed {
				t.Fatalf("ForwardStream() = %#v", result)
			}
			got := <-received
			if changed := !bytes.Equal(got.body, originalBody); changed != test.wantBodyChanged {
				t.Fatalf("body changed = %t, want %t: original=%s upstream=%s", changed, test.wantBodyChanged, originalBody, got.body)
			}
			if got.contentLength != int64(len(got.body)) {
				t.Fatalf("ContentLength = %d, want %d", got.contentLength, len(got.body))
			}

			assertRepresentationMetadata(t, got.headers, false)
		})
	}
}

func TestForwardStreamDoesNotInjectWhenEffectiveFalse(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-4o","stream":true}`,
		`{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":false}}`,
		`{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":true}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var received []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				received, _ = io.ReadAll(request.Body)
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte("data: {\"choices\":[]}\n\n"))
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.Group.InjectUsageOptions = false
			input.Request.Body = []byte(body)
			want := bytes.Clone(input.Request.Body)
			result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).ForwardStream(
				context.Background(), input, newRecordingResponseWriter(),
			)
			if result.Err != nil || !bytes.Equal(received, want) {
				t.Fatalf("result/body = %#v/%s, want original %s", result, received, want)
			}
		})
	}
}

func TestForwardStreamUsageInjectionFailureKeepsRewrittenRequest(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		inject func(*dialect.ParsedRequest) (*dialect.ParsedRequest, error)
	}{
		{name: "error", inject: func(*dialect.ParsedRequest) (*dialect.ParsedRequest, error) { return nil, errors.New("inject failed") }},
		{name: "panic", inject: func(*dialect.ParsedRequest) (*dialect.ParsedRequest, error) { panic("inject panic") }},
		{name: "nil", inject: func(*dialect.ParsedRequest) (*dialect.ParsedRequest, error) { return nil, nil }},
		{name: "oversize", inject: func(request *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
			return &dialect.ParsedRequest{Method: request.Method, Path: request.Path, RawQuery: request.RawQuery, Header: request.Header.Clone(), Body: bytes.Repeat([]byte("x"), int(maxRequestBodyBytes)+1)}, nil
		}},
		{name: "invalid options", body: `{"model":"public-model","stream":true,"stream_options":false}`, inject: dialect.NewOpenAI(http.DefaultClient).InjectStreamUsage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var received map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
					t.Error(err)
				}
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = writer.Write([]byte("data: {\"choices\":[]}\n\n"))
			}))
			defer upstream.Close()

			input := streamForwardInput(upstream.URL)
			input.Group.InjectUsageOptions = true
			input.ExternalModel = "public-model"
			input.UpstreamModelID = "upstream-model"
			if test.body != "" {
				input.Request.Body = []byte(test.body)
			} else {
				input.Request.Body = []byte(`{"model":"public-model","stream":true}`)
			}
			input.Dialect = streamUsageInjectorDialect{OpenAI: dialect.NewOpenAI(http.DefaultClient), inject: test.inject}
			forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
			result := forwarder.ForwardStream(context.Background(), input, newRecordingResponseWriter())
			if result.Err != nil || !result.Committed || received["model"] != "upstream-model" {
				t.Fatalf("result/request = %#v/%#v", result, received)
			}
			if options, exists := received["stream_options"].(map[string]any); exists && options["include_usage"] == true {
				t.Fatalf("fail-open request unexpectedly injected usage: %#v", received)
			}
			if forwarder.usageCapture.failureTotal.Load() != 1 {
				t.Fatalf("failure total = %d, want 1", forwarder.usageCapture.failureTotal.Load())
			}
		})
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

func TestInvalidateRewrittenBodyHeadersRemovesRepresentationMetadata(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Content-Length", "123")
	setRepresentationMetadata(headers)
	for _, name := range []string{
		"content-length", "eTAG", "dIGEST", "content-md5", "content-range",
		"content-digest", "repr-digest", "signature", "signature-input",
	} {
		headers[name] = []string{"case-colliding-stale-value"}
	}

	invalidateRewrittenBodyHeaders(headers)

	for actualName := range headers {
		if isRepresentationMetadataHeader(actualName) &&
			!strings.EqualFold(actualName, "Content-Encoding") {
			t.Fatalf("stale metadata survived as %q: %#v", actualName, headers[actualName])
		}
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
