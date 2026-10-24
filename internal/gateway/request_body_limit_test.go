package gateway

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/dialect"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/memlimit"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
)

func TestRequestBodyLimitFor(t *testing.T) {
	tests := []struct {
		name   string
		memory memlimit.Memory
		want   int64
	}{
		{name: "undetectable memory keeps the ceiling", memory: memlimit.Memory{}, want: 128 << 20},
		{name: "small cgroup limit uses the floor", memory: memlimit.Memory{CgroupLimit: 128 << 20, Total: 16 << 30}, want: 16 << 20},
		{name: "512MiB cgroup limit", memory: memlimit.Memory{CgroupLimit: 512 << 20, Total: 16 << 30}, want: 38347922},
		{name: "1GiB cgroup limit", memory: memlimit.Memory{CgroupLimit: 1 << 30, Total: 16 << 30}, want: 76695844},
		{name: "1.75GiB cgroup limit reaches the ceiling", memory: memlimit.Memory{CgroupLimit: 1792 << 20, Total: 16 << 30}, want: 128 << 20},
		{name: "host memory without a cgroup limit", memory: memlimit.Memory{Total: 1 << 30}, want: 76695844},
		{name: "large host keeps the ceiling", memory: memlimit.Memory{Total: 64 << 30}, want: 128 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requestBodyLimitFor(tt.memory); got != tt.want {
				t.Fatalf("requestBodyLimitFor(%+v) = %d; want %d", tt.memory, got, tt.want)
			}
		})
	}
}

func TestHandlerRequestBodyLimitConfiguration(t *testing.T) {
	handler := &Handler{}
	if got := handler.RequestBodyLimit(); got != maxRequestBodyBytes {
		t.Fatalf("unconfigured RequestBodyLimit() = %d; want %d", got, maxRequestBodyBytes)
	}
	handler.ConfigureRequestBodyLimit(memlimit.Memory{CgroupLimit: 512 << 20, Total: 16 << 30})
	if got := handler.RequestBodyLimit(); got != 38347922 {
		t.Fatalf("configured RequestBodyLimit() = %d; want %d", got, 38347922)
	}
}

// chatRequestOfSize 构造恰好 size 字节的合法 Chat Completions 请求体。
func chatRequestOfSize(t *testing.T, size int) []byte {
	t.Helper()
	const prefix, suffix = `{"model":"gpt-4o","messages":[{"role":"user","content":"`, `"}]}`
	padding := size - len(prefix) - len(suffix)
	if padding < 0 {
		t.Fatalf("size %d is too small for a chat request", size)
	}
	return []byte(prefix + strings.Repeat("A", padding) + suffix)
}

func newRequestBodyLimitTestRuntime(t *testing.T, limit int64) (*Handler, *gin.Engine, *scriptedForwarder) {
	t.Helper()
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: []byte(`{"ok":true}`), RequestWritten: true,
	}}}
	handler, _, _ := newHandlerForTest(t, forwarder, "sk-upstream")
	handler.requestBodyLimit = limit
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return handler, engine, forwarder
}

// TestHandlerEnforcesConfiguredRequestBodyLimit 回归：按内存推算的上限同时约束请求体的传输大小和解码后大小，
// 恰好等于上限的请求照常转发，超过上限返回 413 request_too_large。
func TestHandlerEnforcesConfiguredRequestBodyLimit(t *testing.T) {
	const limit = 1 << 20
	tests := []struct {
		name          string
		body          []byte
		encoding      string
		contentLength int64
		wantCode      string
	}{
		{name: "identity at the limit", body: chatRequestOfSize(t, limit)},
		{name: "identity over the limit", body: chatRequestOfSize(t, limit+1), wantCode: "request_too_large"},
		{
			name: "declared length over the limit", body: chatRequestOfSize(t, 1024),
			contentLength: limit + 1, wantCode: "request_too_large",
		},
		{
			name: "gzip decoded at the limit", encoding: "gzip",
			body: encodeContentCodingForGatewayTest(t, contentcoding.Gzip, chatRequestOfSize(t, limit)),
		},
		{
			name: "gzip decoded over the limit", encoding: "gzip",
			body:     encodeContentCodingForGatewayTest(t, contentcoding.Gzip, chatRequestOfSize(t, limit+1)),
			wantCode: "request_too_large",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, engine, forwarder := newRequestBodyLimitTestRuntime(t, limit)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer gl-client")
			if test.encoding != "" {
				request.Header.Set("Content-Encoding", test.encoding)
			}
			if test.contentLength > 0 {
				request.ContentLength = test.contentLength
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if test.wantCode != "" {
				assertGatewayReasonTest(t, recorder, http.StatusRequestEntityTooLarge, test.wantCode)
				if len(forwarder.inputs) != 0 {
					t.Fatal("oversized request reached upstream")
				}
				return
			}
			if recorder.Code != http.StatusOK || len(forwarder.inputs) != 1 {
				t.Fatalf("response = %d %s, forwarded = %d; want 200 and one attempt", recorder.Code, recorder.Body.String(), len(forwarder.inputs))
			}
			if got := len(forwarder.inputs[0].Request.Body); got != limit {
				t.Fatalf("forwarded body = %d bytes; want %d", got, limit)
			}
		})
	}
}

// TestHandlerParameterOverrideRespectsConfiguredRequestBodyLimit 回归：参数覆盖后的请求体沿用同一个上限，
// 覆盖把请求体推过上限时返回 413，不能按固定的 128MiB 放行。
func TestHandlerParameterOverrideRespectsConfiguredRequestBodyLimit(t *testing.T) {
	const limit = 1 << 20
	rules, err := parameteroverride.Compile([]any{map[string]any{"set": map[string]any{"temperature": 0.5}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		size     int
		wantCode string
	}{
		{name: "override stays within the limit", size: limit - 64},
		{name: "override pushes past the limit", size: limit, wantCode: "request_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, engine, forwarder := newRequestBodyLimitTestRuntime(t, limit)
			snapshot := handler.manager.Current()
			group := snapshot.Groups[1]
			group.ParameterOverrides = rules
			snapshot.Groups[1] = group
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(chatRequestOfSize(t, test.size)))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if test.wantCode != "" {
				assertGatewayReasonTest(t, recorder, http.StatusRequestEntityTooLarge, test.wantCode)
				if len(forwarder.inputs) != 0 {
					t.Fatal("overridden request over the limit reached upstream")
				}
				return
			}
			if recorder.Code != http.StatusOK || len(forwarder.inputs) != 1 ||
				!bytes.Contains(forwarder.inputs[0].Request.Body, []byte(`"temperature":0.5`)) {
				t.Fatalf("response = %d %s, forwarded = %d; want the overridden request", recorder.Code, recorder.Body.String(), len(forwarder.inputs))
			}
		})
	}
}

// TestRedactOutboundRequestRespectsBodyLimit 回归：脱敏后变长的请求体超过上限时拒绝发送。
func TestRedactOutboundRequestRespectsBodyLimit(t *testing.T) {
	rules, err := requestredact.Compile([]requestredact.Rule{{Pattern: "secret", Replacement: "a-much-longer-replacement"}})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"secret secret secret"}]}`)
	request := &dialect.ParsedRequest{Body: body, Header: http.Header{"Content-Type": {"application/json"}}}

	if _, err := redactOutboundRequest(rules, protocol.OpenAICompletions, request, int64(len(body))); !errors.Is(err, requestredact.ErrContent) {
		t.Fatalf("redaction past the limit: err = %v; want %v", err, requestredact.ErrContent)
	}
	clean, err := redactOutboundRequest(rules, protocol.OpenAICompletions, request, int64(len(body))*2)
	if err != nil || !bytes.Contains(clean.Body, []byte("a-much-longer-replacement")) {
		t.Fatalf("redaction within the limit = %s, %v", clean.Body, err)
	}
}
