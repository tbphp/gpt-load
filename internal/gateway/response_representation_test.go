package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/contentcoding"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

type successRepresentationEncoding struct {
	name     string
	encoding string
	wire     []byte
}

func assertRepresentationValidatorsPreservedWithoutSignatures(t *testing.T, headers http.Header) {
	t.Helper()
	for name, want := range map[string]string{
		"ETag":           `"wire-v1"`,
		"Digest":         "sha-256=wire-digest",
		"Content-MD5":    "d2lyZQ==",
		"Content-Range":  "bytes 0-9/10",
		"Content-Digest": "sha-256=:d2lyZQ==:",
		"Repr-Digest":    "sha-256=:cmVwcg==:",
	} {
		if got := headers.Get(name); got != want {
			t.Errorf("%s = %q, want preserved value %q", name, got, want)
		}
	}
	if headers.Get("Signature") != "" || headers.Get("Signature-Input") != "" {
		t.Errorf("stale signatures survived representation normalization: %#v", headers)
	}
}

func TestPrepareSuccessRepresentationReturnsPlainIdentity(t *testing.T) {
	plain := []byte(`{"id":"safe-response","usage":{"prompt_tokens":100,"completion_tokens":30}}`)
	encodings := []contentcoding.Encoding{
		contentcoding.Identity,
		contentcoding.Gzip,
		contentcoding.Brotli,
		contentcoding.Deflate,
		contentcoding.Zstd,
	}
	for _, encoding := range encodings {
		t.Run(string(encoding), func(t *testing.T) {
			result := forwardSuccessRepresentation(
				t,
				successRepresentationEncoding{
					name:     string(encoding),
					encoding: string(encoding),
					wire:     encodeContentCodingForGatewayTest(t, encoding, plain),
				},
				"fake-provider-secret-plaintext-boundary",
				nil,
			)

			if result.Err != nil || result.StatusCode != http.StatusOK {
				t.Fatalf("Forward() result = %#v, want successful response", result)
			}
			if !bytes.Equal(result.Body, plain) {
				t.Fatalf("downstream body = %x, want plaintext %x", result.Body, plain)
			}
			if values := headerFieldValues(result.Header, "Content-Encoding"); len(values) != 0 {
				t.Fatalf("Content-Encoding values = %#v, want absent", values)
			}
			if got := result.Header.Get("Content-Length"); got != strconv.Itoa(len(plain)) {
				t.Fatalf("Content-Length = %q, want %d", got, len(plain))
			}
		})
	}
}

func TestPrepareSuccessRepresentationMetadataPolicy(t *testing.T) {
	plain := []byte(`{"id":"safe-response"}`)

	identity := forwardSuccessRepresentation(
		t,
		successRepresentationEncoding{
			name:     "identity",
			encoding: "identity",
			wire:     bytes.Clone(plain),
		},
		"fake-provider-secret-metadata-identity",
		nil,
	)
	if identity.Err != nil || !bytes.Equal(identity.Body, plain) {
		t.Fatalf("identity Forward() result = %#v", identity)
	}
	assertRepresentationValidatorsPreservedWithoutSignatures(t, identity.Header)

	gzipResult := forwardSuccessRepresentation(
		t,
		successRepresentationEncoding{
			name:     "gzip",
			encoding: "gzip",
			wire:     encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain),
		},
		"fake-provider-secret-metadata-gzip",
		nil,
	)
	if gzipResult.Err != nil || !bytes.Equal(gzipResult.Body, plain) {
		t.Fatalf("gzip Forward() result = %#v", gzipResult)
	}
	assertRepresentationMetadata(t, gzipResult.Header, false)
	if values := headerFieldValues(gzipResult.Header, "Content-Encoding"); len(values) != 0 {
		t.Fatalf("Content-Encoding values = %#v, want absent", values)
	}
}

func TestPrepareSuccessRepresentationRebuildsContentLengthAcrossHeaderCasings(t *testing.T) {
	plain := []byte(`{"id":"safe-response"}`)
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}
	headers := http.Header{
		"Content-Encoding": {"identity"},
		"Content-Length":   {"999"},
		"content-length":   {"998"},
	}

	result, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).prepareSuccessRepresentation(input, http.StatusOK, headers, plain, nil)
	if err != nil {
		t.Fatalf("prepareSuccessRepresentation() error = %v", err)
	}
	if values := headerFieldValues(result.headers, "Content-Length"); len(values) != 1 || values[0] != strconv.Itoa(len(plain)) {
		t.Fatalf("Content-Length values = %#v, want exactly [%d]", values, len(plain))
	}
}

func TestPrepareSuccessRepresentationDropsSignaturesAfterRepresentationHeaderNormalization(t *testing.T) {
	plain := []byte(`{"id":"safe-response"}`)
	headers := http.Header{
		"Content-Encoding": {"identity"},
		"Content-Length":   {strconv.Itoa(len(plain))},
		"Signature":        {"sig1=:c2lnOg==:"},
		"Signature-Input":  {`sig1=("content-encoding" "content-length");created=1`},
	}
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}

	result, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).prepareSuccessRepresentation(input, http.StatusOK, headers, plain, nil)
	if err != nil || result.changed || result.headers.Get("Signature") != "" || result.headers.Get("Signature-Input") != "" {
		t.Fatalf("prepareSuccessRepresentation() = %#v, %v; want normalized representation without signatures", result, err)
	}
}

func TestPrepareSuccessRepresentationRejectsPartialContentWithoutSafeContentRange(t *testing.T) {
	plain := []byte(`{"id":"partial"}`)
	input := ForwardInput{
		Dialect:         dialect.NewOpenAI(http.DefaultClient),
		ExternalModel:   "public-model",
		UpstreamModelID: "provider-model",
		Request:         &dialect.ParsedRequest{Header: make(http.Header)},
	}
	headers := http.Header{
		"Content-Encoding": {"identity"},
		"Content-Length":   {strconv.Itoa(len(plain))},
		"Content-Range":    {"bytes provider-model/100"},
	}

	result, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).prepareSuccessRepresentation(input, http.StatusPartialContent, headers, plain, nil)
	if !errors.Is(err, ErrUpstreamProtocol) || result.headers != nil {
		t.Fatalf("prepareSuccessRepresentation() = %#v, %v; want unsafe partial response rejection", result, err)
	}
}

func TestPrepareSuccessRepresentationValidatesPartialContentRangeAgainstPlaintext(t *testing.T) {
	plain := []byte("abc")
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}
	tests := []struct {
		name    string
		headers http.Header
		wantErr bool
	}{
		{
			name: "matching decimal range",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes 10-12/100"},
			},
		},
		{
			name: "range length differs from plaintext",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes 10-13/100"},
			},
			wantErr: true,
		},
		{
			name: "plus-prefixed range bound",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes +10-12/100"},
			},
			wantErr: true,
		},
		{
			name: "plus-prefixed complete length",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes 10-12/+100"},
			},
			wantErr: true,
		},
		{
			name: "case-colliding duplicate range",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes 10-12/100"},
				"content-range":  {"bytes 10-12/100"},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NewForwarder(
				platformhttp.NewHTTPClientManager(),
				redact.New(),
			).prepareSuccessRepresentation(input, http.StatusPartialContent, test.headers, plain, nil)
			if test.wantErr {
				if !errors.Is(err, ErrUpstreamProtocol) || result.headers != nil {
					t.Fatalf("prepareSuccessRepresentation() = %#v, %v; want protocol error", result, err)
				}
				return
			}
			if err != nil || !bytes.Equal(result.downstream, plain) ||
				result.headers.Get("Content-Range") != "bytes 10-12/100" {
				t.Fatalf("prepareSuccessRepresentation() = %#v, %v; want valid partial response", result, err)
			}
		})
	}
}

func TestHasSafeHeadContentRange(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
		want    bool
	}{
		{
			name: "matching content length",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes 10-12/100"},
			},
			want: true,
		},
		{
			name: "wildcard total",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes 10-12/*"},
			},
			want: true,
		},
		{
			name: "content length absent",
			headers: http.Header{
				"Content-Range": {"bytes 10-12/100"},
			},
			want: true,
		},
		{
			name: "content length mismatch",
			headers: http.Header{
				"Content-Length": {"4"},
				"Content-Range":  {"bytes 10-12/100"},
			},
		},
		{
			name: "invalid bounds",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes 12-10/100"},
			},
		},
		{
			name: "case colliding content range",
			headers: http.Header{
				"Content-Length": {"3"},
				"Content-Range":  {"bytes 10-12/100"},
				"content-range":  {"bytes 10-12/100"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasSafeHeadContentRange(test.headers); got != test.want {
				t.Fatalf("hasSafeHeadContentRange() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPrepareSuccessRepresentationRejectsChangedPartialContent(t *testing.T) {
	const (
		upstreamModel = "provider-model"
		externalModel = "public-model"
	)
	plain := []byte(`{"model":"provider-model","id":"partial"}`)
	forwardPartial := func(
		t *testing.T,
		encoding contentcoding.Encoding,
		configure func(*ForwardInput),
	) UpstreamResult {
		t.Helper()
		wire := encodeContentCodingForGatewayTest(t, encoding, plain)
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Encoding", string(encoding))
			writer.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/100", len(plain)-1))
			writer.WriteHeader(http.StatusPartialContent)
			_, _ = writer.Write(wire)
		}))
		defer upstream.Close()

		input := usageForwardInput(upstream.URL, dialect.NewOpenAI(http.DefaultClient))
		input.Request.Body = []byte(`{"model":"public-model"}`)
		if configure != nil {
			configure(&input)
		}
		return NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
			context.Background(),
			input,
		)
	}

	t.Run("identity alias rewrite", func(t *testing.T) {
		result := forwardPartial(t, contentcoding.Identity, func(input *ForwardInput) {
			input.ExternalModel = externalModel
			input.UpstreamModelID = upstreamModel
		})
		if !errors.Is(result.Err, ErrUpstreamProtocol) || result.StatusCode != 0 || len(result.Body) != 0 {
			t.Fatalf("Forward() result = %#v, want changed 206 protocol error", result)
		}
	})

	t.Run("compressed representation", func(t *testing.T) {
		result := forwardPartial(t, contentcoding.Gzip, nil)
		if !errors.Is(result.Err, ErrUpstreamProtocol) || result.StatusCode != 0 || len(result.Body) != 0 {
			t.Fatalf("Forward() result = %#v, want decoded 206 protocol error", result)
		}
	})

	t.Run("identity unchanged", func(t *testing.T) {
		result := forwardPartial(t, contentcoding.Identity, nil)
		if result.Err != nil || result.StatusCode != http.StatusPartialContent ||
			!bytes.Equal(result.Body, plain) || result.Header.Get("Content-Range") != fmt.Sprintf("bytes 0-%d/100", len(plain)-1) ||
			len(headerFieldValues(result.Header, "Content-Encoding")) != 0 {
			t.Fatalf("Forward() result = %#v, want unchanged legal 206", result)
		}
	})
}

func TestPrepareSuccessRepresentationBoundsWireAndDecodedBodies(t *testing.T) {
	forwarder := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New())
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}

	caseCollidingHeaders := http.Header{
		"Content-Encoding": {"identity"},
		"content-encoding": {"gzip"},
	}
	result, err := forwarder.prepareSuccessRepresentation(
		input,
		http.StatusOK,
		caseCollidingHeaders,
		[]byte(`{"id":"safe-response"}`),
		nil,
	)
	if !errors.Is(err, ErrUpstreamProtocol) || result.headers != nil {
		t.Fatalf("case-colliding Content-Encoding result/error = %#v/%v, want protocol error", result, err)
	}

	overflow := gzipRepeatedByte(t, 'x', maxNonStreamingResponseBodyBytes+1)
	result, err = forwarder.prepareSuccessRepresentation(
		input,
		http.StatusOK,
		http.Header{"Content-Encoding": {"gzip"}},
		overflow,
		nil,
	)
	if !errors.Is(err, ErrUpstreamProtocol) || result.headers != nil {
		t.Fatalf("decoded overflow result/error = %#v/%v, want protocol error", result, err)
	}

	result, err = forwarder.prepareSuccessRepresentation(
		input,
		http.StatusOK,
		http.Header{"Content-Encoding": {"identity"}},
		bytes.Repeat([]byte("w"), int(maxNonStreamingResponseBodyBytes)+1),
		nil,
	)
	if !errors.Is(err, ErrUpstreamProtocol) || result.headers != nil {
		t.Fatalf("wire overflow result/error = %#v/%v, want protocol error", result, err)
	}
}

func gzipRepeatedByte(t *testing.T, value byte, count int64) []byte {
	t.Helper()
	var wire bytes.Buffer
	compressor := gzip.NewWriter(&wire)
	if _, err := io.CopyN(compressor, repeatingByteReader(value), count); err != nil {
		t.Fatalf("stream gzip fixture: %v", err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	return wire.Bytes()
}

func successRepresentationEncodings(t *testing.T, plain []byte) []successRepresentationEncoding {
	t.Helper()
	encodings := []string{"", "identity", "gzip", "br", "deflate", "zstd"}
	result := make([]successRepresentationEncoding, 0, len(encodings))
	for _, encoding := range encodings {
		fixtureEncoding := contentcoding.Identity
		if encoding != "" {
			fixtureEncoding = contentcoding.Encoding(encoding)
		}
		wire := encodeContentCodingForGatewayTest(t, fixtureEncoding, plain)
		name := encoding
		if name == "" {
			name = "empty"
		}
		result = append(result, successRepresentationEncoding{
			name:     name,
			encoding: encoding,
			wire:     wire,
		})
	}
	return result
}

func forwardSuccessRepresentation(
	t *testing.T,
	representation successRepresentationEncoding,
	apiKey string,
	configureInput func(*ForwardInput),
) UpstreamResult {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if representation.encoding != "" {
			writer.Header().Set("Content-Encoding", representation.encoding)
		}
		writer.Header().Set("Content-Length", strconv.Itoa(len(representation.wire)))
		setRepresentationMetadata(writer.Header())
		_, _ = writer.Write(representation.wire)
	}))
	defer upstream.Close()

	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		Group: state.GroupView{
			ID:          1,
			UpstreamURL: testUpstreamBaseURL(upstream.URL, protocol.OpenAICompletions),
			Timeouts: state.TimeoutConfig{
				Connect:   time.Second,
				FirstByte: time.Second,
				Request:   5 * time.Second,
			},
		},
		APIKey: apiKey,
		Request: &dialect.ParsedRequest{
			Method: http.MethodPost,
			Path:   "/v1/chat/completions",
			Header: make(http.Header),
			Body:   []byte(`{"model":"public-model"}`),
		},
	}
	if configureInput != nil {
		configureInput(&input)
	}
	return NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
		context.Background(),
		input,
	)
}

func TestForwardSuccessRepresentationReturnsPlaintextAndConditionalMetadata(t *testing.T) {
	plain := []byte(`{"id":"safe-response","usage":{"prompt_tokens":100,"completion_tokens":30}}`)
	for _, representation := range successRepresentationEncodings(t, plain) {
		t.Run(representation.name, func(t *testing.T) {
			result := forwardSuccessRepresentation(
				t,
				representation,
				"fake-provider-secret-unchanged",
				nil,
			)

			if result.Err != nil || result.StatusCode != http.StatusOK || !result.RequestWritten {
				t.Fatalf("Forward() result = %#v, want successful written response", result)
			}
			if !bytes.Equal(result.Body, plain) {
				t.Fatalf("downstream body = %x, want plaintext %x", result.Body, plain)
			}
			if values := headerFieldValues(result.Header, "Content-Encoding"); len(values) != 0 {
				t.Fatalf("Content-Encoding values = %#v, want absent", values)
			}
			if result.Header.Get("Content-Length") != strconv.Itoa(len(plain)) {
				t.Fatalf("Content-Length = %q, want %d", result.Header.Get("Content-Length"), len(plain))
			}
			switch representation.encoding {
			case "":
				assertRepresentationMetadata(t, result.Header, true)
			case "identity":
				assertRepresentationValidatorsPreservedWithoutSignatures(t, result.Header)
			default:
				assertRepresentationMetadata(t, result.Header, false)
			}
		})
	}
}

func TestPrepareSuccessRepresentationSanitizesUnchangedCredentialMetadataCollision(t *testing.T) {
	const credential = "fake-metadata-credential"
	wire := []byte(`{"id":"safe-response"}`)
	contentLength := strconv.Itoa(len(wire))
	tests := []struct {
		name   string
		value  string
		secret string
	}{
		{name: "Content-Encoding", value: "identity", secret: "identity"},
		{name: "Content-Length", value: contentLength, secret: contentLength},
		{name: "ETag", value: `"fake-metadata-credential"`, secret: credential},
		{name: "Digest", value: "sha-256=fake-metadata-credential", secret: credential},
		{name: "Content-MD5", value: "fake-metadata-credential", secret: credential},
		{name: "Content-Range", value: "bytes fake-metadata-credential/100", secret: credential},
		{name: "Content-Digest", value: "sha-256=:fake-metadata-credential:", secret: credential},
		{name: "Repr-Digest", value: "sha-256=:fake-metadata-credential:", secret: credential},
		{name: "Signature", value: "sig1=:fake-metadata-credential:", secret: credential},
		{name: "Signature-Input", value: `sig1=("fake-metadata-credential")`, secret: credential},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := http.Header{
				"Content-Encoding": {"identity"},
				"Content-Length":   {contentLength},
			}
			headers.Set(test.name, test.value)
			input := ForwardInput{
				Dialect: dialect.NewOpenAI(http.DefaultClient),
				APIKey:  test.secret,
				Request: &dialect.ParsedRequest{Header: make(http.Header)},
			}

			result, err := NewForwarder(
				platformhttp.NewHTTPClientManager(),
				redact.New(),
			).prepareSuccessRepresentation(input, http.StatusOK, headers, wire, []string{test.secret})

			if test.name == "Content-Length" {
				if !errors.Is(err, ErrUpstreamProtocol) ||
					result.downstream != nil ||
					result.headers != nil {
					t.Fatalf(
						"prepareSuccessRepresentation() = %#v, %v; want fail-closed regenerated length collision",
						result,
						err,
					)
				}
				return
			}
			if err != nil || !bytes.Equal(result.downstream, wire) ||
				len(headerFieldValues(result.headers, "Content-Encoding")) != 0 ||
				result.headers.Get(test.name) != "" {
				t.Fatalf(
					"prepareSuccessRepresentation() = %#v, %v; want sanitized plaintext response",
					result,
					err,
				)
			}
		})
	}

	headers := http.Header{
		"Content-Encoding": {"identity"},
		"Content-Length":   {contentLength},
	}
	setRepresentationMetadata(headers)
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		APIKey:  credential,
		Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}
	result, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).prepareSuccessRepresentation(input, http.StatusOK, headers, wire, []string{credential})
	if err != nil ||
		result.changed ||
		!bytes.Equal(result.downstream, wire) ||
		len(headerFieldValues(result.headers, "Content-Encoding")) != 0 ||
		result.headers.Get("Content-Length") != contentLength {
		t.Fatalf(
			"non-collision representation = %#v, %v; want unchanged wire and framing",
			result,
			err,
		)
	}
	assertRepresentationValidatorsPreservedWithoutSignatures(t, result.headers)
}

func TestPrepareSuccessRepresentationRebuildsChangedBodyDespiteStaleCredentialMetadata(t *testing.T) {
	const credential = "fake-stale-metadata-credential"
	plain := []byte(`{"echo":"fake-stale-metadata-credential"}`)
	wire := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain)
	headers := http.Header{
		"Content-Encoding": {"gzip"},
		"Content-Length":   {strconv.Itoa(len(wire))},
	}
	setRepresentationMetadata(headers)
	headers.Set("ETag", `"fake-stale-metadata-credential"`)
	input := ForwardInput{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		APIKey:  credential,
		Request: &dialect.ParsedRequest{Header: make(http.Header)},
	}

	result, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).prepareSuccessRepresentation(input, http.StatusOK, headers, wire, []string{credential})

	if err != nil || !result.changed {
		t.Fatalf("prepareSuccessRepresentation() = %#v, %v; want rebuilt response", result, err)
	}
	gotPlain := result.downstream
	if string(gotPlain) != `{"echo":"[REDACTED]"}` {
		t.Fatalf("rebuilt plain = %q", gotPlain)
	}
	if len(headerFieldValues(result.headers, "Content-Encoding")) != 0 ||
		result.headers.Get("Content-Length") != strconv.Itoa(len(result.downstream)) {
		t.Fatalf("rebuilt framing headers = %#v", result.headers)
	}
	assertRepresentationMetadata(t, result.headers, false)
}

func TestPrepareSuccessRepresentationRemovesAliasedModelFromUnchangedMetadata(t *testing.T) {
	const (
		upstreamModel = "provider-private-model"
		externalModel = "public-model"
	)
	wire := []byte(`{"id":"safe-response"}`)
	headers := http.Header{
		"Content-Encoding": {"identity"},
		"Content-Length":   {strconv.Itoa(len(wire))},
		"X-Safe":           {"kept"},
	}
	for _, name := range []string{
		"ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest",
		"Repr-Digest", "Signature", "Signature-Input",
	} {
		headers.Set(name, "contains="+upstreamModel)
	}
	input := ForwardInput{
		Dialect:         dialect.NewOpenAI(http.DefaultClient),
		ExternalModel:   externalModel,
		UpstreamModelID: upstreamModel,
		Request:         &dialect.ParsedRequest{Header: make(http.Header)},
	}

	result, err := NewForwarder(
		platformhttp.NewHTTPClientManager(),
		redact.New(),
	).prepareSuccessRepresentation(input, http.StatusOK, headers, wire, nil)

	if err != nil || result.changed || !bytes.Equal(result.downstream, wire) {
		t.Fatalf("prepareSuccessRepresentation() = %#v, %v; want unchanged wire", result, err)
	}
	if len(headerFieldValues(result.headers, "Content-Encoding")) != 0 ||
		result.headers.Get("Content-Length") != strconv.Itoa(len(wire)) ||
		result.headers.Get("X-Safe") != "kept" {
		t.Fatalf("unchanged safe headers = %#v", result.headers)
	}
	for _, name := range []string{
		"ETag", "Digest", "Content-MD5", "Content-Range", "Content-Digest",
		"Repr-Digest", "Signature", "Signature-Input",
	} {
		if value := result.headers.Get(name); value != "" {
			t.Fatalf("%s leaked aliased upstream model: %#v", name, result.headers)
		}
	}
}

func TestForwardSuccessRepresentationRedactsAndRebuildsMetadata(t *testing.T) {
	const (
		apiKey        = "fake-provider-secret-redaction"
		upstreamModel = "provider-model"
		externalModel = "public-model"
	)
	tests := []struct {
		name           string
		plain          []byte
		configureInput func(*ForwardInput)
		wantPlain      []byte
	}{
		{
			name:      "credential redaction",
			plain:     []byte(`{"echo":"Bearer fake-provider-secret-redaction fake-provider-secret-redaction"}`),
			wantPlain: []byte(`{"echo":"[REDACTED] [REDACTED]"}`),
		},
		{
			name:  "model alias rewrite",
			plain: []byte(`{"model":"provider-model","id":"safe-response"}`),
			configureInput: func(input *ForwardInput) {
				input.ExternalModel = externalModel
				input.UpstreamModelID = upstreamModel
			},
			wantPlain: []byte(`{"id":"safe-response","model":"public-model"}`),
		},
	}

	for _, test := range tests {
		for _, representation := range successRepresentationEncodings(t, test.plain) {
			t.Run(test.name+"/"+representation.name, func(t *testing.T) {
				result := forwardSuccessRepresentation(
					t,
					representation,
					apiKey,
					test.configureInput,
				)

				if result.Err != nil || result.StatusCode != http.StatusOK || !result.RequestWritten {
					t.Fatalf("Forward() result = %#v, want successful rewritten response", result)
				}
				plain := result.Body
				if !bytes.Equal(plain, test.wantPlain) {
					t.Fatalf("downstream plain = %q, want %q", plain, test.wantPlain)
				}
				if bytes.Contains(plain, []byte(apiKey)) {
					t.Fatalf("downstream plain leaked credential: %q", plain)
				}
				if values := headerFieldValues(result.Header, "Content-Encoding"); len(values) != 0 {
					t.Fatalf("Content-Encoding values = %#v, want absent", values)
				}
				if result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) {
					t.Fatalf("Content-Length = %q, want %d", result.Header.Get("Content-Length"), len(result.Body))
				}
				assertRepresentationMetadata(t, result.Header, false)
			})
		}
	}
}

func TestForwardSuccessRepresentationRejectsUnknownStackedMalformedAndPlainOverflow(t *testing.T) {
	compressedOverflow := gzipRepeatedByte(t, 'a', maxNonStreamingResponseBodyBytes+1)
	tests := []struct {
		name       string
		apiKey     string
		encodings  []string
		wire       []byte
		contentLen string
		writeBody  func(io.Writer)
	}{
		{name: "unknown", apiKey: "fake-provider-secret-unknown", encodings: []string{"compress"}, wire: []byte("opaque-wire")},
		{name: "stacked", apiKey: "fake-provider-secret-stacked", encodings: []string{"gzip, br"}, wire: []byte("opaque-wire")},
		{name: "multiple fields", apiKey: "fake-provider-secret-multiple", encodings: []string{"identity", "gzip"}, wire: []byte("opaque-wire")},
		{name: "malformed gzip", apiKey: "fake-provider-secret-gzip", encodings: []string{"gzip"}, wire: []byte("not-a-gzip-stream")},
		{name: "malformed br", apiKey: "fake-provider-secret-br", encodings: []string{"br"}, wire: []byte{0xff, 0xff, 0xff}},
		{name: "malformed deflate", apiKey: "fake-provider-secret-deflate", encodings: []string{"deflate"}, wire: []byte("not-a-deflate-stream")},
		{name: "malformed zstd", apiKey: "fake-provider-secret-zstd", encodings: []string{"zstd"}, wire: []byte("not-a-zstd-stream")},
		{name: "empty gzip", apiKey: "fake-provider-secret-empty-gzip", encodings: []string{"gzip"}},
		{name: "empty br", apiKey: "fake-provider-secret-empty-br", encodings: []string{"br"}},
		{name: "empty deflate", apiKey: "fake-provider-secret-empty-deflate", encodings: []string{"deflate"}},
		{name: "empty zstd", apiKey: "fake-provider-secret-empty-zstd", encodings: []string{"zstd"}},
		{
			name:      "wire overflow",
			apiKey:    "fake-provider-secret-wire-overflow",
			encodings: nil,
			writeBody: func(writer io.Writer) {
				_, _ = io.CopyN(writer, repeatingByteReader('w'), maxNonStreamingResponseBodyBytes+1)
			},
		},
		{
			name:      "plain overflow",
			apiKey:    "fake-provider-secret-plain-overflow",
			encodings: []string{"gzip"},
			wire:      compressedOverflow,
		},
		{
			name:       "Content-Length credential collision",
			apiKey:     "42",
			wire:       bytes.Repeat([]byte("x"), 42),
			contentLen: "42",
		},
		{
			name:   "rebuilt Content-Length credential collision",
			apiKey: "21",
			wire:   []byte(`{"echo":"21"}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				for _, encoding := range test.encodings {
					writer.Header().Add("Content-Encoding", encoding)
				}
				if test.contentLen != "" {
					writer.Header().Set("Content-Length", test.contentLen)
				}
				setRepresentationMetadata(writer.Header())
				if test.writeBody != nil {
					test.writeBody(writer)
				} else {
					_, _ = writer.Write(test.wire)
				}
			}))
			defer upstream.Close()

			result := testForward(t, upstream.URL, test.apiKey, 10*time.Second)
			if !errors.Is(result.Err, ErrUpstreamProtocol) ||
				!result.RequestWritten ||
				result.StatusCode != 0 ||
				len(result.Body) != 0 ||
				len(result.ClassificationBody) != 0 {
				t.Fatalf("Forward() result = %#v, want fail-closed post-write protocol error", result)
			}
		})
	}
}

func TestForwardSuccessRepresentationDropsContentEncodingCredentialCollision(t *testing.T) {
	plain := []byte(`{"id":"safe-response"}`)
	result := forwardSuccessRepresentation(
		t,
		successRepresentationEncoding{
			name:     "gzip",
			encoding: "gzip",
			wire:     encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain),
		},
		"gzip",
		nil,
	)
	if result.Err != nil || !bytes.Equal(result.Body, plain) ||
		len(headerFieldValues(result.Header, "Content-Encoding")) != 0 {
		t.Fatalf("Forward() result = %#v, want plaintext with coding header removed", result)
	}
}

func TestForwardSuccessRepresentationFailsClosedOnResidualCredentialTokens(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
		body   string
	}{
		{name: "numeric", apiKey: "42", body: `{"echo":42}`},
		{name: "boolean", apiKey: "true", body: `{"echo":true}`},
		{name: "null", apiKey: "null", body: `{"echo":null}`},
		{name: "placeholder collision", apiKey: redact.Placeholder, body: `{"echo":"[REDACTED]"}`},
		{name: "rewritten key collision", apiKey: "fake-key", body: `{"fake-key":1,"[REDACTED]":2}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := forwardSuccessRepresentation(
				t,
				successRepresentationEncoding{name: "identity", encoding: "identity", wire: []byte(test.body)},
				test.apiKey,
				nil,
			)

			if !errors.Is(result.Err, ErrUpstreamProtocol) ||
				!result.RequestWritten ||
				result.StatusCode != 0 ||
				len(result.Body) != 0 ||
				len(result.ClassificationBody) != 0 {
				t.Fatalf("Forward() result = %#v, want fail-closed residual credential protocol error", result)
			}
		})
	}
}

func TestForwardSuccessRepresentationRedactsEscapedAndFinalCredentialValues(t *testing.T) {
	tests := []struct {
		name           string
		apiKey         string
		body           string
		configureInput func(*ForwardInput)
	}{
		{
			name:   "escaped JSON credential",
			apiKey: "fake-escaped-credential",
			body:   `{"echo":"fake-\u0065scaped-credential"}`,
		},
		{
			name:   "final custom credential Header value",
			apiKey: "fake-provider-key-for-custom-credential",
			body:   `{"echo":"final-custom-fake-credential"}`,
			configureInput: func(input *ForwardInput) {
				input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{
					"Authorization": "final-custom-fake-credential",
				}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := forwardSuccessRepresentation(
				t,
				successRepresentationEncoding{name: "identity", encoding: "identity", wire: []byte(test.body)},
				test.apiKey,
				test.configureInput,
			)
			if result.Err != nil || result.StatusCode != http.StatusOK ||
				string(result.Body) != `{"echo":"[REDACTED]"}` {
				t.Fatalf("Forward() result = %#v, want redacted JSON credential string", result)
			}
		})
	}
}

func TestForwardSuccessRepresentationRedactsMultipleCredentialsInSingleJSONPass(t *testing.T) {
	const (
		apiKey          = "fake-overlap-credential"
		finalCredential = "prefix-fake-overlap-credential"
	)
	plain := []byte(
		`{"long":"prefix-fake-overlap-credential","short":"fake-overlap-credential",` +
			`"escaped":"fake-\u006fverlap-credential"}`,
	)
	result := forwardSuccessRepresentation(
		t,
		successRepresentationEncoding{name: "identity", encoding: "identity", wire: plain},
		apiKey,
		func(input *ForwardInput) {
			input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{
				"Authorization": finalCredential,
			}}
		},
	)

	var got map[string]string
	if err := json.Unmarshal(result.Body, &got); result.Err != nil ||
		result.StatusCode != http.StatusOK ||
		err != nil ||
		got["long"] != redact.Placeholder ||
		got["short"] != redact.Placeholder ||
		got["escaped"] != redact.Placeholder {
		t.Fatalf("Forward() error/status/body/decoded = %v/%d/%q/%#v, decode error=%v", result.Err, result.StatusCode, result.Body, got, err)
	}
}

type overflowingResponseDialect struct {
	*dialect.OpenAI
}

func (dialect overflowingResponseDialect) RewriteResponseModel([]byte, string) ([]byte, error) {
	return bytes.Repeat([]byte("x"), int(maxNonStreamingResponseBodyBytes)+1), nil
}

type nonJSONAliasResponseDialect struct {
	*dialect.OpenAI
}

func (dialect nonJSONAliasResponseDialect) RewriteResponseModel([]byte, string) ([]byte, error) {
	return []byte("alias=fake-non-json-alias-key"), nil
}

func TestForwardSuccessRepresentationRejectsChangedOutputWireOverflow(t *testing.T) {
	result := forwardSuccessRepresentation(
		t,
		successRepresentationEncoding{
			name:     "identity",
			encoding: "identity",
			wire:     []byte(`{"model":"provider-model"}`),
		},
		"fake-provider-secret-output-overflow",
		func(input *ForwardInput) {
			input.Dialect = overflowingResponseDialect{OpenAI: dialect.NewOpenAI(http.DefaultClient)}
			input.ExternalModel = "public-model"
			input.UpstreamModelID = "provider-model"
		},
	)

	if !errors.Is(result.Err, ErrUpstreamProtocol) ||
		!result.RequestWritten ||
		result.StatusCode != 0 ||
		len(result.Body) != 0 ||
		len(result.ClassificationBody) != 0 {
		t.Fatalf("Forward() result = %#v, want fail-closed changed output overflow", result)
	}
}

func TestForwardSuccessRepresentationRejectsCredentialIntroducedByAliasRewrite(t *testing.T) {
	const providerBody = `{"model":"provider-model","id":"safe-response"}`
	tests := []struct {
		name            string
		encoding        string
		apiKey          string
		finalCredential string
		externalModel   string
		nonJSON         bool
	}{
		{
			name:          "identity escaped API key",
			encoding:      "identity",
			apiKey:        "fake<alias-key",
			externalModel: "fake<alias-key",
		},
		{
			name:            "gzip escaped final credential substring",
			encoding:        "gzip",
			apiKey:          "fake-provider-key-for-alias",
			finalCredential: "final&alias-credential",
			externalModel:   "public-final&alias-credential-model",
		},
		{
			name:          "identity non-JSON alias result",
			encoding:      "identity",
			apiKey:        "fake-non-json-alias-key",
			externalModel: "public-model",
			nonJSON:       true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := encodeContentCodingForGatewayTest(t, contentcoding.Encoding(test.encoding), []byte(providerBody))
			result := forwardSuccessRepresentation(
				t,
				successRepresentationEncoding{
					name:     test.encoding,
					encoding: test.encoding,
					wire:     wire,
				},
				test.apiKey,
				func(input *ForwardInput) {
					input.ExternalModel = test.externalModel
					input.UpstreamModelID = "provider-model"
					if test.finalCredential != "" {
						input.Group.HeaderRules = state.HeaderRules{Set: map[string]string{
							"Authorization": test.finalCredential,
						}}
					}
					if test.nonJSON {
						input.Dialect = nonJSONAliasResponseDialect{
							OpenAI: dialect.NewOpenAI(http.DefaultClient),
						}
					}
				},
			)

			if !errors.Is(result.Err, ErrUpstreamProtocol) ||
				!result.RequestWritten ||
				result.StatusCode != 0 ||
				len(result.Body) != 0 ||
				len(result.ClassificationBody) != 0 {
				t.Fatalf("Forward() result = %#v, want fail-closed alias credential protocol error", result)
			}
		})
	}
}

func TestForwardSuccessRepresentationExtractsUsageFromPlainBody(t *testing.T) {
	const (
		apiKey        = "fake-provider-secret-usage"
		upstreamModel = "provider-model"
		externalModel = "public-model"
	)
	plain := []byte(
		`{"model":"provider-model","echo":"fake-provider-secret-usage",` +
			`"usage":{"prompt_tokens":100,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":20}}}`,
	)
	wire := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain)
	var captured []byte
	selected := usageExtractorDialect{
		Dialect: dialect.NewOpenAI(http.DefaultClient),
		extract: func(body []byte) (usage.Result, error) {
			captured = bytes.Clone(body)
			return usage.Result{
				State: usage.StateComplete,
				Tokens: usage.Tokens{
					UncachedInput: 80,
					CacheRead:     20,
					Output:        30,
				},
			}, nil
		},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("Content-Length", strconv.Itoa(len(wire)))
		setRepresentationMetadata(writer.Header())
		_, _ = writer.Write(wire)
	}))
	defer upstream.Close()

	input := usageForwardInput(upstream.URL, selected)
	input.APIKey = apiKey
	input.ExternalModel = externalModel
	input.UpstreamModelID = upstreamModel
	input.Request.Body = []byte(`{"model":"public-model"}`)
	result := NewForwarder(platformhttp.NewHTTPClientManager(), redact.New()).Forward(
		context.Background(),
		input,
	)

	wantUsage := usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{UncachedInput: 80, CacheRead: 20, Output: 30},
	}
	if result.Err != nil || result.Usage != wantUsage {
		t.Fatalf("Forward() error/usage = %v/%#v, want nil/%#v", result.Err, result.Usage, wantUsage)
	}
	if bytes.Contains(captured, []byte(apiKey)) ||
		!bytes.Contains(captured, []byte(redact.Placeholder)) ||
		!bytes.Contains(captured, []byte(upstreamModel)) ||
		bytes.Contains(captured, []byte(externalModel)) {
		t.Fatalf("usage extractor body = %q, want safe provider plain before alias rewrite", captured)
	}
	downstreamPlain := result.Body
	if bytes.Contains(downstreamPlain, []byte(apiKey)) ||
		!bytes.Contains(downstreamPlain, []byte(redact.Placeholder)) ||
		!bytes.Contains(downstreamPlain, []byte(externalModel)) ||
		bytes.Contains(downstreamPlain, []byte(upstreamModel)) {
		t.Fatalf("downstream plain = %q", downstreamPlain)
	}
	if result.Header.Get("Content-Length") != strconv.Itoa(len(result.Body)) ||
		len(headerFieldValues(result.Header, "Content-Encoding")) != 0 {
		t.Fatalf("downstream representation headers = %#v", result.Header)
	}
	assertRepresentationMetadata(t, result.Header, false)
}
