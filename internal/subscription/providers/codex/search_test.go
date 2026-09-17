package codex

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type searchFailingBody struct {
	io.Reader
}

func (body searchFailingBody) Read(buffer []byte) (int, error) {
	n, _ := body.Reader.Read(buffer)
	return n, io.ErrUnexpectedEOF
}

func (body searchFailingBody) Close() error { return nil }

func TestCodexStandaloneSearchPreservesNativeHTTP(t *testing.T) {
	const body = `{"id":"session","model":"gpt-5","commands":{"search_query":[{"q":"OpenAI"}]},"settings":{"external_web_access":true},"future":42}`
	const output = `{"encrypted_output":"opaque","output":"result","results":[]}`
	var calls int
	transport := targetRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.String() != "https://relay.example/team/backend-api/codex/alpha/search" {
			t.Errorf("target = %s %s", request.Method, request.URL)
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil || string(payload) != body {
			t.Errorf("payload = %s, error = %v", payload, err)
		}
		if request.Header.Get("Authorization") != "Bearer test-access" ||
			request.Header.Get("Chatgpt-Account-Id") != "account-one" ||
			request.Header.Get("Accept") != "application/json" || request.Header.Get("Accept-Encoding") != "identity" ||
			request.Header.Get("Session-Id") != "session" {
			t.Error("search lost account or HTTP representation headers")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(output)), Request: request}, nil
	})
	ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
	response, err := NewExecutor().Execute(ctx, "credential", Credential{
		Type: Provider, AccessToken: "test-access", RefreshToken: "test-refresh", AccountID: "account-one",
	}, ExecuteRequest{Model: "gpt-5", Payload: []byte(body), Format: "openai-response", RequestPath: "/v1/alpha/search",
		BaseURL: "https://relay.example/team", Headers: http.Header{"Session-Id": {"session"}}})
	if err != nil || calls != 1 || string(response.Payload) != output || response.UpstreamRequestPath != "/team/backend-api/codex/alpha/search" {
		t.Fatalf("search response = %#v, calls = %d, error = %v", response, calls, err)
	}
}

func TestCodexStandaloneSearchHTTPFailureAndCancellation(t *testing.T) {
	executor := NewExecutor()
	credential := Credential{Type: Provider, AccessToken: "test-access", RefreshToken: "test-refresh", AccountID: "account-one"}
	request := ExecuteRequest{Model: "gpt-5", Payload: []byte(`{"id":"session","model":"gpt-5"}`),
		Format: "openai-response", RequestPath: "/v1/alpha/search"}
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			transport := targetRoundTripper(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != "https://chatgpt.com/backend-api/codex/alpha/search" || req.GetBody != nil {
					t.Error("search changed native target or enabled implicit replay")
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"17"}},
					Body: io.NopCloser(strings.NewReader(`{"error":{"message":"search unavailable"}}`)), Request: req}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			response, err := executor.Execute(ctx, "credential", credential, request)
			var statusError interface{ StatusCode() int }
			if !errors.As(err, &statusError) || statusError.StatusCode() != status || calls != 1 || response.StatusCode != status {
				t.Fatalf("HTTP failure = %#v, error = %v, calls = %d", response, err, calls)
			}
		})
	}
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		transport := targetRoundTripper(func(req *http.Request) (*http.Response, error) {
			cancel()
			<-req.Context().Done()
			return nil, req.Context().Err()
		})
		ctx = context.WithValue(ctx, "cliproxy.roundtripper", http.RoundTripper(transport))
		_, err := executor.Execute(ctx, "credential", credential, request)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled search error = %v", err)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		calls := 0
		transport := targetRoundTripper(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://other.example"}},
				Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		})
		ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
		_, err := executor.Execute(ctx, "credential", credential, request)
		if err == nil || calls != 1 {
			t.Fatalf("redirect error = %v, calls = %d", err, calls)
		}
	})
}

func TestCodexStandaloneSearchBodyReadFailure(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			transport := targetRoundTripper(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"17"}},
					Body: searchFailingBody{Reader: strings.NewReader(`{"output":"must not succeed"}`)}, Request: req}, nil
			})
			ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
			response, err := NewExecutor().Execute(ctx, "credential", Credential{
				Type: Provider, AccessToken: "test-access", RefreshToken: "test-refresh", AccountID: "account-one",
			}, ExecuteRequest{Model: "gpt-5", Payload: []byte(`{"id":"session","model":"gpt-5"}`),
				Format: "openai-response", RequestPath: "/v1/alpha/search"})
			if !errors.Is(err, io.ErrUnexpectedEOF) || response.StatusCode != status ||
				response.Headers.Get("Retry-After") != "17" || len(response.Payload) != 0 {
				t.Fatalf("read failure = %#v, error = %v", response, err)
			}
			if status != http.StatusOK {
				var statusError interface{ StatusCode() int }
				var retryError interface{ RetryAfter() *time.Duration }
				if !errors.As(err, &statusError) || statusError.StatusCode() != status ||
					!errors.As(err, &retryError) || retryError.RetryAfter() == nil || *retryError.RetryAfter() != 17*time.Second {
					t.Fatalf("HTTP read failure lost status or retry delay: %v", err)
				}
			}
		})
	}
}
