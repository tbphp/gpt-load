package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
	"gpt-load/internal/state"
)

func TestRequestRedactionMultipartOnlyReplacesPrompt(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.SetBoundary("redaction-boundary"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("prompt", "private-value"); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("model", "private-value"); err != nil {
		t.Fatal(err)
	}
	image, err := w.CreateFormFile("image", "input.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := image.Write([]byte("private-value")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	c, _ := requestredact.Compile([]requestredact.Rule{{Pattern: "private-value", Replacement: "[VALUE]"}})
	request := &dialect.ParsedRequest{Body: body.Bytes(), Header: http.Header{"Content-Type": {w.FormDataContentType()}, "Content-Length": {"123"}}}
	clean, err := redactOutboundRequest(c, protocol.OpenAIImages, request)
	if err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("Content-Length") != "123" || clean.Header.Get("Content-Length") != "" || !bytes.Equal(body.Bytes(), request.Body) {
		t.Fatal("original request or rewritten framing changed incorrectly")
	}
	_, params, err := mime.ParseMediaType(clean.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(bytes.NewReader(clean.Body), params["boundary"])
	for i, want := range []struct{ name, value string }{{"prompt", "[VALUE]"}, {"model", "private-value"}, {"image", "private-value"}} {
		part, err := r.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(part)
		if err != nil || part.FormName() != want.name || string(value) != want.value {
			t.Fatalf("part %d = %s %s / %v", i, part.FormName(), value, err)
		}
	}
}

func TestRequestRedactionProtectsAuditAndBusinessWithStableCache(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{auditReply(auditPass), auditReply(`{"choices":[]}`), auditReply(`{"choices":[]}`)}}
	h, engine := auditEngine(t, f)
	compiled, err := requestredact.Compile([]requestredact.Rule{{Pattern: "secret-1234", Replacement: "[SECRET]"}})
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Current().RequestRedaction = compiled
	body := `{"model":"gpt-4o","messages":[{"role":"system","content":"secret-1234"},{"role":"user","content":"secret-1234"}]}`
	for range 2 {
		if r := sendAuditRequest(engine, body); r.Code != 200 {
			t.Fatalf("request = %d %s", r.Code, r.Body)
		}
	}
	if len(f.inputs) != 3 {
		t.Fatalf("unchanged review cache missed: %d", len(f.inputs))
	}
	for _, input := range f.inputs {
		if bytes.Contains(input.Request.Body, []byte("secret-1234")) || !bytes.Contains(input.Request.Body, []byte("[SECRET]")) {
			t.Fatalf("unredacted outbound body: %s", input.Request.Body)
		}
	}
}

func TestRequestRedactionAutomaticSelectionAndRetriesUseIndependentCopies(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"error":{"message":"rate limited"}}`)},
		auditReply(`{"choices":[]}`),
	}}
	h, manager, _ := newHandlerForTest(t, f, "key-a", "key-b")
	configureAutoModelTest(t, h, manager, state.FilterSet{})
	manager.Current().RequestRedaction, _ = requestredact.Compile([]requestredact.Rule{{Pattern: "private-value", Replacement: "private-value-hidden"}})
	calls := 0
	h.decisionClient = autoDecisionClient(func(request *http.Request) (*http.Response, error) {
		calls++
		body, err := io.ReadAll(request.Body)
		if err != nil || !bytes.Contains(body, []byte("private-value-hidden")) || bytes.Contains(body, []byte("hidden-hidden")) {
			t.Errorf("unexpected decision content %s / %v", body, err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"answers":{"preset":{"choice":"balanced","confidence":0.9}},"usage":{"input_tokens":20,"output_tokens":0}}`))}, nil
	})
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	response := sendAuditRequest(engine, `{"model":"auto-probe","messages":[{"role":"user","content":"private-value"}]}`)
	if response.Code != 200 || calls != 1 || len(f.inputs) != 2 {
		t.Fatalf("status=%d decisions=%d attempts=%d", response.Code, calls, len(f.inputs))
	}
	for _, input := range f.inputs {
		if !bytes.Contains(input.Request.Body, []byte(`"content":"private-value-hidden"`)) {
			t.Fatalf("retry changed content: %s", input.Request.Body)
		}
	}
}

func TestRequestRedactionWebsocketPreservesEachTurn(t *testing.T) {
	h, engine, input := websocketTestHandler(t, "http://unused.invalid/v1", channel.OpenAI)
	input.RequestRedaction = []requestredact.Rule{{Pattern: "private-value", Replacement: "[VALUE]"}}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	seen := make(chan []byte, 2)
	h.forwarder = websocketScriptForwarder{AttemptForwarder: h.forwarder, open: func(_ context.Context, _ ForwardInput) (execution.WebsocketSession, execution.WebsocketResult) {
		session := &websocketScriptSession{done: make(chan struct{})}
		session.turn = func(ctx context.Context, body []byte, emit func(context.Context, []byte) error) execution.WebsocketResult {
			seen <- bytes.Clone(body)
			if err := emit(ctx, websocketCompleted("resp_redacted", "")); err != nil {
				return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent, Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled}}
			}
			return execution.WebsocketResult{DispatchState: execution.DispatchMaybeSent}
		}
		return session, execution.WebsocketResult{}
	}}
	server := httptest.NewServer(engine)
	defer server.Close()
	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()
	for range 2 {
		if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "public", "input": []any{map[string]any{"role": "user", "content": "private-value"}}, "store": false}); err != nil {
			t.Fatal(err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Fatal(err)
		}
		select {
		case body := <-seen:
			if bytes.Contains(body, []byte("private-value")) || !bytes.Contains(body, []byte("[VALUE]")) {
				t.Fatalf("unredacted turn %s", body)
			}
		case <-time.After(time.Second):
			t.Fatal("missing upstream turn")
		}
	}
}

func TestRequestRedactionRunsAfterGroupOverridesWithoutChangingRouting(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"choices":[]}`)}}
	h, engine := auditEngine(t, f)
	snapshot := h.manager.Current()
	snapshot.RequestAudit.Enabled = false
	snapshot.RequestRedaction, _ = requestredact.Compile([]requestredact.Rule{{Pattern: "secret", Replacement: "secret-redacted"}, {Pattern: "gpt-4o", Replacement: "changed-model"}})
	var raw any
	if err := json.Unmarshal([]byte(`[{"set":{"messages":[{"role":"user","content":"secret"}]}}]`), &raw); err != nil {
		t.Fatal(err)
	}
	rules, err := parameteroverride.Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	group := snapshot.Groups[1]
	group.ParameterOverrides = rules
	snapshot.Groups[1] = group
	r := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"original"}]}`)
	if r.Code != 200 || len(f.inputs) != 1 {
		t.Fatalf("request = %d %s", r.Code, r.Body)
	}
	body := f.inputs[0].Request.Body
	if !bytes.Contains(body, []byte(`"content":"secret-redacted"`)) || !bytes.Contains(body, []byte(`"model":"gpt-4o"`)) {
		t.Fatalf("unexpected outbound request %s", body)
	}
}
