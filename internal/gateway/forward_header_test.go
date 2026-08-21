package gateway

import (
	"net/http"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestSanitizeForwardResponseHeadersDropsUpstreamOriginPolicy(t *testing.T) {
	source := http.Header{
		"X-Request-Id":                  {"request-1"},
		"X-Ratelimit-Remaining":         {"9"},
		"Retry-After":                   {"3"},
		"Location":                      {"/v1/responses/resp_1"},
		"Etag":                          {`"response-1"`},
		"Cache-Control":                 {"no-cache"},
		"Server":                        {"upstream-edge"},
		"Via":                           {"upstream-proxy"},
		"Strict-Transport-Security":     {"max-age=31536000"},
		"Cross-Origin-Opener-Policy":    {"same-origin"},
		"Cross-Origin-Resource-Policy":  {"same-origin"},
		"Referrer-Policy":               {"strict-origin"},
		"Content-Security-Policy":       {"default-src 'none'"},
		"Permissions-Policy":            {"camera=()"},
		"Origin-Agent-Cluster":          {"?1"},
		"Report-To":                     {`{"group":"upstream"}`},
		"Nel":                           {`{"success_fraction":0.01}`},
		"Cf-Ray":                        {"edge-trace"},
		"Cf-Cache-Status":               {"DYNAMIC"},
		"X-Codex-Turn-State":            {"private-state"},
		"X-Codex-Plan-Type":             {"pro"},
		"X-Models-Etag":                 {"private-model-cache"},
		"X-Openai-Proxy-Wasm":           {"v0.1"},
		"Access-Control-Allow-Origin":   {"*"},
		"Access-Control-Expose-Headers": {"X-Request-Id"},
		"X-Content-Type-Options":        {"nosniff"},
		"X-Frame-Options":               {"DENY"},
	}

	headers := sanitizeForwardResponseHeaders(source, ForwardInput{
		ClientProtocol: protocol.OpenAIResponses,
		Operation:      execution.OperationResponsesRetrieve,
		RouteMode:      execution.RouteNative,
	})
	for _, name := range []string{
		"X-Request-Id", "X-Ratelimit-Remaining", "Retry-After", "Location", "Etag", "Cache-Control",
	} {
		if headers.Get(name) == "" {
			t.Fatalf("safe response header %s was removed: %#v", name, headers)
		}
	}
	for _, name := range []string{
		"Server", "Via", "Strict-Transport-Security", "Cross-Origin-Opener-Policy",
		"Cross-Origin-Resource-Policy", "Referrer-Policy", "Content-Security-Policy",
		"Permissions-Policy", "Origin-Agent-Cluster", "Report-To", "Nel", "Cf-Ray",
		"Cf-Cache-Status", "X-Codex-Turn-State", "X-Codex-Plan-Type", "X-Models-Etag",
		"X-Openai-Proxy-Wasm", "Access-Control-Allow-Origin", "Access-Control-Expose-Headers",
		"X-Content-Type-Options", "X-Frame-Options",
	} {
		if headers.Values(name) != nil {
			t.Fatalf("upstream origin header %s survived: %#v", name, headers.Values(name))
		}
	}
}

func TestSanitizeForwardResponseHeadersNarrowsConvertedResponses(t *testing.T) {
	source := http.Header{
		"Content-Type":                 {"application/json"},
		"Content-Length":               {"123"},
		"Content-Range":                {"bytes 0-122/123"},
		"Cache-Control":                {"no-cache"},
		"X-Request-Id":                 {"request-1"},
		"Retry-After":                  {"3"},
		"X-Gpt-Load-Token-Count":       {"local-estimate"},
		"X-Ratelimit-Remaining":        {"9"},
		"Anthropic-Ratelimit-Requests": {"8"},
		"X-Goog-Quota-Project":         {"upstream-project"},
		"Location":                     {"/provider/resource"},
		"Etag":                         {`"provider-representation"`},
		"X-Provider-Debug":             {"private"},
	}

	headers := sanitizeForwardResponseHeaders(source, ForwardInput{
		ClientProtocol: protocol.OpenAIResponses,
		Operation:      execution.OperationResponsesCreate,
		RouteMode:      execution.RouteConverted,
	})
	for _, name := range []string{
		"Content-Type", "Content-Length", "Content-Range", "Cache-Control", "X-Request-Id", "Retry-After",
		"X-Gpt-Load-Token-Count",
	} {
		if headers.Get(name) == "" {
			t.Fatalf("converted response header %s was removed: %#v", name, headers)
		}
	}
	for _, name := range []string{
		"X-Ratelimit-Remaining", "Anthropic-Ratelimit-Requests", "X-Goog-Quota-Project",
		"Location", "Etag", "X-Provider-Debug",
	} {
		if headers.Values(name) != nil {
			t.Fatalf("upstream-specific converted header %s survived: %#v", name, headers.Values(name))
		}
	}
}
