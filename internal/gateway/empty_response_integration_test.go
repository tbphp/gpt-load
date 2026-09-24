package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/state"
)

const (
	// 上游正常走完协议终态，但没有任何内容增量。
	emptyStreamBody = "data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\"," +
		"\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"id\":\"chat_1\",\"object\":\"chat.completion.chunk\"," +
		"\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"

	contentStreamBody = "data: {\"id\":\"chat_2\",\"object\":\"chat.completion.chunk\"," +
		"\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: [DONE]\n\n"
)

func newSSEServer(t *testing.T, body string, calls *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(body))
		writer.(http.Flusher).Flush()
	}))
	t.Cleanup(server.Close)
	return server
}

// TestEmptyStreamRetriesNextCandidate 固定核心行为：开启空回检测后，上游正常
// 完成但没有产出的流不会被提交给客户端，网关改用下一个候选重试。
func TestEmptyStreamRetriesNextCandidate(t *testing.T) {
	var emptyCalls, contentCalls atomic.Int64
	emptyUpstream := newSSEServer(t, emptyStreamBody, &emptyCalls)
	contentUpstream := newSSEServer(t, contentStreamBody, &contentCalls)

	engine, _ := newStreamingGatewayEngine(t,
		streamGatewayGroup{
			id: 1, name: "empty", upstreamURL: emptyUpstream.URL,
			apiKey: "sk-empty", emptyRetry: true,
		},
		streamGatewayGroup{
			id: 2, name: "content", upstreamURL: contentUpstream.URL,
			apiKey: "sk-content", emptyRetry: true,
		},
	)

	recorder := performStreamingRequest(engine)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != contentStreamBody {
		t.Fatalf("body = %q, want the retried upstream content", body)
	}
	if emptyCalls.Load() != 1 {
		t.Fatalf("empty upstream calls = %d, want 1", emptyCalls.Load())
	}
	if contentCalls.Load() != 1 {
		t.Fatalf("content upstream calls = %d, want 1", contentCalls.Load())
	}
}

// TestEmptyStreamDeliveredWhenRetriesExhausted 保证重试用尽后客户端仍然拿到上游
// 真实返回的那条空流，而不是网关生成的错误。
func TestEmptyStreamDeliveredWhenRetriesExhausted(t *testing.T) {
	var firstCalls, secondCalls atomic.Int64
	first := newSSEServer(t, emptyStreamBody, &firstCalls)
	second := newSSEServer(t, emptyStreamBody, &secondCalls)

	engine, _ := newStreamingGatewayEngine(t,
		streamGatewayGroup{
			id: 1, name: "empty-a", upstreamURL: first.URL,
			apiKey: "sk-empty-a", emptyRetry: true,
		},
		streamGatewayGroup{
			id: 2, name: "empty-b", upstreamURL: second.URL,
			apiKey: "sk-empty-b", emptyRetry: true,
		},
	)

	recorder := performStreamingRequest(engine)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != emptyStreamBody {
		t.Fatalf("body = %q, want the upstream empty stream", body)
	}
	if total := firstCalls.Load() + secondCalls.Load(); total < 2 {
		t.Fatalf("upstream calls = %d, want the empty response retried", total)
	}
}

// TestEmptyStreamDisabledByDefault 保证默认配置下行为完全不变：空回照常提交，
// 上游只被调用一次。
func TestEmptyStreamDisabledByDefault(t *testing.T) {
	var calls atomic.Int64
	upstream := newSSEServer(t, emptyStreamBody, &calls)

	engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
		id: 1, name: "default", upstreamURL: upstream.URL, apiKey: "sk-default",
	})

	recorder := performStreamingRequest(engine)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != emptyStreamBody {
		t.Fatalf("body = %q, want the upstream stream", body)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

// TestContentStreamCommitsWithoutRetry 保证有产出的流不受影响：既不重试，也
// 完整交付。
func TestContentStreamCommitsWithoutRetry(t *testing.T) {
	var calls atomic.Int64
	upstream := newSSEServer(t, contentStreamBody, &calls)

	engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
		id: 1, name: "content-only", upstreamURL: upstream.URL,
		apiKey: "sk-content-only", emptyRetry: true,
	})

	recorder := performStreamingRequest(engine)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if body := recorder.Body.String(); body != contentStreamBody {
		t.Fatalf("body = %q, want the upstream stream", body)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

// TestPrereadWindowReleasesLongPreamble 固定预读上限：上游持续发出不含产出的
// 事件时，网关不会一直压着响应，超过事件数上限即照常提交。
func TestPrereadWindowReleasesLongPreamble(t *testing.T) {
	var builder strings.Builder
	for range emptyResponsePrereadEvents + 4 {
		builder.WriteString("data: {\"id\":\"chat_3\",\"object\":\"chat.completion.chunk\"," +
			"\"choices\":[{\"index\":0,\"delta\":{}}]}\n\n")
	}
	builder.WriteString("data: {\"id\":\"chat_3\",\"object\":\"chat.completion.chunk\"," +
		"\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late\"}}]}\n\n")
	builder.WriteString("data: [DONE]\n\n")
	body := builder.String()

	var calls atomic.Int64
	upstream := newSSEServer(t, body, &calls)
	engine, _ := newStreamingGatewayEngine(t, streamGatewayGroup{
		id: 1, name: "long-preamble", upstreamURL: upstream.URL,
		apiKey: "sk-long-preamble", emptyRetry: true,
	})

	recorder := performStreamingRequest(engine)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Body.String(); got != body {
		t.Fatalf("body = %q, want the full upstream stream", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want 1", calls.Load())
	}
}

// TestEmptyResponseRetryExemptions 固定必须豁免的请求形态。漏掉任何一条都会把
// 「按约定不产出内容」的正常请求变成反复重试。
func TestEmptyResponseRetryExemptions(t *testing.T) {
	t.Parallel()

	group := state.GroupView{EmptyResponseRetry: true}
	tests := []struct {
		name     string
		metadata dialect.RequestMetadata
		body     string
		want     bool
	}{
		{
			name:     "chat completion is covered",
			metadata: dialect.RequestMetadata{Operation: execution.OperationChatCompletion},
			body:     `{"model":"gpt-4o"}`,
			want:     true,
		},
		{
			name:     "responses create is covered",
			metadata: dialect.RequestMetadata{Operation: execution.OperationResponsesCreate},
			body:     `{"model":"gpt-4o"}`,
			want:     true,
		},
		{
			name:     "prewarm create is exempt",
			metadata: dialect.RequestMetadata{Operation: execution.OperationResponsesCreate},
			body:     `{"model":"gpt-4o","generate":false}`,
			want:     false,
		},
		{
			name: "response continuation is exempt",
			metadata: dialect.RequestMetadata{
				Operation:          execution.OperationResponsesCreate,
				PreviousResponseID: "resp_1",
			},
			body: `{"model":"gpt-4o"}`,
			want: false,
		},
		{
			name:     "stored conversation is exempt",
			metadata: dialect.RequestMetadata{Operation: execution.OperationResponsesCreate},
			body:     `{"model":"gpt-4o","conversation":"conv_1"}`,
			want:     false,
		},
		{
			name:     "null conversation is covered",
			metadata: dialect.RequestMetadata{Operation: execution.OperationResponsesCreate},
			body:     `{"model":"gpt-4o","conversation":null}`,
			want:     true,
		},
		{
			name:     "embeddings are exempt",
			metadata: dialect.RequestMetadata{Operation: execution.OperationEmbeddingsCreate},
			body:     `{"model":"text-embedding-3-small"}`,
			want:     false,
		},
		{
			name:     "image generation is exempt",
			metadata: dialect.RequestMetadata{Operation: execution.OperationImagesGenerate},
			body:     `{"model":"gpt-image-1"}`,
			want:     false,
		},
		{
			name:     "count tokens is exempt",
			metadata: dialect.RequestMetadata{Operation: execution.OperationCountTokens},
			body:     `{"model":"claude-sonnet-4"}`,
			want:     false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := emptyResponseRetryEnabled(group, test.metadata, []byte(test.body))
			if got != test.want {
				t.Fatalf("emptyResponseRetryEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}

// TestEmptyResponseRetryDisabledGroupIgnoresEverything 保证分组开关是总闸。
func TestEmptyResponseRetryDisabledGroupIgnoresEverything(t *testing.T) {
	t.Parallel()

	got := emptyResponseRetryEnabled(
		state.GroupView{EmptyResponseRetry: false},
		dialect.RequestMetadata{Operation: execution.OperationChatCompletion},
		[]byte(`{"model":"gpt-4o"}`),
	)
	if got {
		t.Fatal("emptyResponseRetryEnabled() = true, want false when the group disables it")
	}
}

type streamOutcome struct {
	status      int
	contentType string
	body        string
	calls       int64
}

func streamOutcomeAcrossTwoCandidates(t *testing.T, body string, enabled bool) streamOutcome {
	t.Helper()
	var firstCalls, secondCalls atomic.Int64
	first := newSSEServer(t, body, &firstCalls)
	second := newSSEServer(t, body, &secondCalls)
	engine, _ := newStreamingGatewayEngine(t,
		streamGatewayGroup{id: 1, name: "first", upstreamURL: first.URL, apiKey: "sk-first", emptyRetry: enabled},
		streamGatewayGroup{id: 2, name: "second", upstreamURL: second.URL, apiKey: "sk-second", emptyRetry: enabled},
	)
	recorder := performStreamingRequest(engine)
	return streamOutcome{
		status:      recorder.Code,
		contentType: recorder.Header().Get("Content-Type"),
		body:        recorder.Body.String(),
		calls:       firstCalls.Load() + secondCalls.Load(),
	}
}

// TestEmptyRetryLeavesOtherEndingsUnchanged 固定开关的影响边界：只有「自然结束
// 却没有产出」才会被重试；其余结局在开启后必须与关闭时逐字节一致，包括压住
// 前导后才出现的错误、没有终态就断开的流、由终止原因解释的空结果，以及携带
// 非标准产出字段的响应。
func TestEmptyRetryLeavesOtherEndingsUnchanged(t *testing.T) {
	const preamble = "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\"," +
		"\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n"
	cases := map[string]string{
		"error after held preamble": preamble + "data: {\"error\":{\"message\":\"upstream exploded\"}}\n\n",
		"eof without terminal":      preamble,
		"output budget exhausted": preamble + "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\"," +
			"\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n",
		"content filtered": preamble + "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\"," +
			"\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"content_filter\"}]}\n\ndata: [DONE]\n\n",
		"non-standard content field": "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\"," +
			"\"choices\":[{\"index\":0,\"delta\":{\"images\":[{\"type\":\"image_url\"}]}}]}\n\ndata: [DONE]\n\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			disabled := streamOutcomeAcrossTwoCandidates(t, body, false)
			enabled := streamOutcomeAcrossTwoCandidates(t, body, true)
			if enabled != disabled {
				t.Fatalf("enabled outcome = %+v, want identical to disabled %+v", enabled, disabled)
			}
		})
	}
}

// TestEmptyResponseAttemptIsVisibleInLogs 保证空回尝试在请求日志里可辨认，
// 而不是显示成通用的上游错误。
func TestEmptyResponseAttemptIsVisibleInLogs(t *testing.T) {
	t.Parallel()

	result := UpstreamResult{
		StatusCode:                http.StatusOK,
		EmptyResponseBeforeCommit: true,
		ExecutionError:            &execution.ErrorEvidence{Code: health.EmptyResponseCode},
	}
	if got := upstreamErrorCode(result, health.FailureCategoryAmbiguous); got != health.EmptyResponseCode {
		t.Fatalf("upstreamErrorCode() = %q, want %q", got, health.EmptyResponseCode)
	}
	if got := fixedErrorSummary(health.EmptyResponseCode); got == fixedErrorSummary("") {
		t.Fatalf("fixedErrorSummary() = %q, want a dedicated summary", got)
	}
}

// TestEmptyResponseExemptionsUseOutboundRequest 保证豁免依据实际发往上游的请求。分组
// 参数覆盖可以写入 conversation 或 generate:false：请求一旦关联上游会话或变成预热，
// 就不能再参与空回重试。
func TestEmptyResponseExemptionsUseOutboundRequest(t *testing.T) {
	cases := map[string]struct {
		set  map[string]any
		want bool
	}{
		"without override":            {want: true},
		"override adds conversation":  {set: map[string]any{"conversation": "conv_1"}, want: false},
		"override turns into prewarm": {set: map[string]any{"generate": false}, want: false},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			forwarder := &scriptedForwarder{streamResults: []UpstreamResult{{
				StatusCode: http.StatusOK, Committed: true,
				Stream: StreamObservation{EndReason: StreamEndCleanEOF},
			}}}
			handler, engine, _ := newContinuationFixture(t, forwarder)
			group := handler.manager.Current().Groups[1]
			group.EmptyResponseRetry = true
			if test.set != nil {
				rules, err := parameteroverride.Compile([]any{map[string]any{"set": test.set}})
				if err != nil {
					t.Fatal(err)
				}
				group.ParameterOverrides = rules
			}
			handler.manager.Current().Groups[1] = group

			serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"hi","stream":true}`, http.StatusOK)
			if len(forwarder.streamInputs) == 0 {
				t.Fatal("request was not forwarded")
			}
			if got := forwarder.streamInputs[0].EmptyResponseRetry; got != test.want {
				t.Fatalf("EmptyResponseRetry = %v, want %v", got, test.want)
			}
		})
	}
}
