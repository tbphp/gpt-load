package embedded

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

func devinTestGzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestDevinTrailerSanitizationKeepsQuotaClassificationUnderCompression(t *testing.T) {
	for _, encoding := range []string{"plain", "http-gzip", "connect-gzip", "both"} {
		t.Run(encoding, func(t *testing.T) {
			trailer := []byte(`{"error":{"code":"failed_precondition","message":"Your weekly usage quota has been exhausted. devin-session-token\u0024review-sensitive-token"}}`)
			flag := helps.ConnectFlagEndStream
			if encoding == "connect-gzip" || encoding == "both" {
				trailer = devinTestGzip(t, trailer)
				flag |= helps.ConnectFlagCompressed
			}
			wire := append(helps.WrapConnectEnvelope(devinTestBytes(3, []byte("partial"))), helps.WrapConnectEnvelopeWithFlag(flag, trailer)...)
			if encoding == "http-gzip" || encoding == "both" {
				wire = devinTestGzip(t, wire)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if encoding == "http-gzip" || encoding == "both" {
					w.Header().Set("Content-Encoding", "gzip")
				}
				_, _ = w.Write(wire)
			}))
			defer server.Close()
			executor := NewDevinHTTPExecutor().(*devinHTTPExecutor)
			executor.baseURL = server.URL
			credential := DevinCredential{Type: ProviderDevin, SessionToken: "devin-session-token$review-sensitive-token", UserID: "review-user"}
			request := ExecuteRequest{Model: "swe-2", Format: "openai-response", Payload: []byte(`{"input":"hello"}`), Headers: http.Header{"Accept-Encoding": []string{"gzip"}}, ConfiguredHeaders: []string{"Accept-Encoding"}}
			for _, streaming := range []bool{false, true} {
				var executionErr error
				var output []byte
				if streaming {
					stream, err := executor.ExecuteStreamCanonical(t.Context(), "one", credential, request)
					if err != nil {
						t.Fatal(err)
					}
					for chunk := range stream.Chunks {
						output = append(output, chunk.Payload...)
						if chunk.Err != nil {
							executionErr = chunk.Err
						}
					}
				} else {
					response, err := executor.ExecuteCanonical(t.Context(), "one", credential, request)
					output, executionErr = response.Payload, err
				}
				var upstream *DevinExecutionError
				if !errors.As(executionErr, &upstream) || upstream.StatusCode() != 429 || !upstream.IsCredentialScoped() {
					t.Fatalf("quota classification lost: stream=%v error=%v", streaming, executionErr)
				}
				if bytes.Contains(output, []byte("review-sensitive-token")) || strings.Contains(executionErr.Error(), "review-sensitive-token") {
					t.Fatal("credential escaped trailer normalization")
				}
			}
		})
	}
}

type devinResponseTestBody struct {
	*bytes.Reader
	closed bool
}

func (body *devinResponseTestBody) Close() error { body.closed = true; return nil }
func (body *devinResponseTestBody) Read(raw []byte) (int, error) {
	return body.Reader.Read(raw[:min(len(raw), 3)])
}

func TestDevinResponseBodyPreservesDataBytesAndClose(t *testing.T) {
	data := helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagCompressed, devinTestGzip(t, devinTestBytes(3, []byte("answer"))))
	wire := append(data, helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagEndStream, []byte(`{"metadata":{"trace":["one"]}}`))...)
	source := &devinResponseTestBody{Reader: bytes.NewReader(wire)}
	body := &devinResponseBody{ReadCloser: source}
	output, err := io.ReadAll(body)
	if err != nil || !bytes.Equal(output, wire) {
		t.Fatal("valid data or trailer changed")
	}
	if err := body.Close(); err != nil || !source.closed {
		t.Fatal("source was not closed")
	}
}

func TestDevinResponseBodyRejectsInvalidTrailerBeforeExposingItsHeader(t *testing.T) {
	for _, trailer := range []string{`{"error":`, `{"error":17}`, `[]`, `null`, `{"error":{"message":17}}`, `{"Error":{"message":"echo"}}`} {
		t.Run(trailer, func(t *testing.T) {
			data := helps.WrapConnectEnvelope(devinTestBytes(3, []byte("answer")))
			wire := append(append([]byte(nil), data...), helps.WrapConnectEnvelopeWithFlag(helps.ConnectFlagEndStream, []byte(trailer))...)
			body := &devinResponseBody{ReadCloser: io.NopCloser(bytes.NewReader(wire))}
			output, err := io.ReadAll(body)
			if err == nil || !bytes.Equal(output, data) {
				t.Fatal("invalid trailer reached the SDK")
			}
		})
	}
}

func TestDevinResponseBodyPreservesTruncatedReadError(t *testing.T) {
	wire := helps.WrapConnectEnvelope(devinTestBytes(3, []byte("answer")))
	body := &devinResponseBody{ReadCloser: io.NopCloser(bytes.NewReader(wire[:len(wire)-1]))}
	if _, err := io.ReadAll(body); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated frame error=%v", err)
	}
}
