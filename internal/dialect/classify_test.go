package dialect

import (
	"net/http"
	"testing"

	"gpt-load/internal/health"
)

func TestClassifyProviderErrorIgnoresHTTP2xxShortcut(t *testing.T) {
	type providerErrorClassifier interface {
		ClassifyProviderError([]byte) health.FailureCategory
	}

	dialects := []struct {
		name  string
		value Dialect
		cases []struct {
			name string
			body string
			want health.FailureCategory
		}
	}{
		{
			name:  "openai",
			value: NewOpenAI(http.DefaultClient),
			cases: []struct {
				name string
				body string
				want health.FailureCategory
			}{
				{name: "rate limited", body: `{"error":{"type":"rate_limit_error"}}`, want: health.FailureCategoryRateLimited},
				{name: "model unavailable", body: `{"error":{"code":"model_not_found"}}`, want: health.FailureCategoryModelUnavailable},
				{name: "invalid key", body: `{"error":{"code":"invalid_api_key"}}`, want: health.FailureCategoryInvalidKey},
				{name: "host error", body: `{"error":{"type":"server_overloaded"}}`, want: health.FailureCategoryUpstreamHostError},
				{name: "unknown provider error", body: `{"error":{"type":"unexpected_provider_error"}}`, want: health.FailureCategoryClientError},
			},
		},
		{
			name:  "anthropic",
			value: NewAnthropic(http.DefaultClient),
			cases: []struct {
				name string
				body string
				want health.FailureCategory
			}{
				{name: "rate limited", body: `{"type":"error","error":{"type":"rate_limit_error"}}`, want: health.FailureCategoryRateLimited},
				{name: "model unavailable", body: `{"type":"error","error":{"type":"model_not_found"}}`, want: health.FailureCategoryModelUnavailable},
				{name: "invalid key", body: `{"type":"error","error":{"type":"authentication_error"}}`, want: health.FailureCategoryInvalidKey},
				{name: "host error", body: `{"type":"error","error":{"type":"overloaded_error"}}`, want: health.FailureCategoryUpstreamHostError},
				{name: "unknown provider error", body: `{"type":"error","error":{"type":"unexpected_provider_error"}}`, want: health.FailureCategoryClientError},
			},
		},
		{
			name:  "gemini",
			value: NewGemini(http.DefaultClient),
			cases: []struct {
				name string
				body string
				want health.FailureCategory
			}{
				{name: "rate limited", body: `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, want: health.FailureCategoryRateLimited},
				{name: "model unavailable", body: `{"error":{"status":"MODEL_NOT_FOUND"}}`, want: health.FailureCategoryModelUnavailable},
				{name: "invalid key", body: `{"error":{"status":"UNAUTHENTICATED"}}`, want: health.FailureCategoryInvalidKey},
				{name: "host error", body: `{"error":{"status":"SERVICE_UNAVAILABLE"}}`, want: health.FailureCategoryUpstreamHostError},
				{name: "unknown provider error", body: `{"error":{"status":"UNEXPECTED_PROVIDER_ERROR"}}`, want: health.FailureCategoryClientError},
			},
		},
	}

	for _, dialectCase := range dialects {
		t.Run(dialectCase.name, func(t *testing.T) {
			classifier, ok := dialectCase.value.(providerErrorClassifier)
			if !ok {
				t.Fatalf("%T does not implement the optional provider-error classifier", dialectCase.value)
			}
			for _, test := range dialectCase.cases {
				t.Run(test.name, func(t *testing.T) {
					body := []byte(test.body)
					if got := dialectCase.value.ClassifyStatus(http.StatusOK, body); got != health.FailureCategoryOK {
						t.Fatalf("ClassifyStatus(200) = %v, want OK", got)
					}
					if got := classifier.ClassifyProviderError(body); got != test.want {
						t.Fatalf("ClassifyProviderError() = %v, want %v", got, test.want)
					}
				})
			}
		})
	}
}
