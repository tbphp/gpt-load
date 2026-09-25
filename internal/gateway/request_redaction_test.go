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
	"github.com/sirupsen/logrus"

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

func TestRequestRedactionEncryptsStableValuesForOneAccessKey(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"choices":[]}`), auditReply(`{"choices":[]}`)}}
	h, manager, _ := newHandlerForTest(t, f, "answer-key")
	compiled, err := requestredact.Compile([]requestredact.Rule{{Pattern: `gpt-4o|alice@example\.com|bob@example\.com`, Mode: requestredact.ModeEncrypt}})
	if err != nil {
		t.Fatal(err)
	}
	manager.Current().RequestRedaction = compiled
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	requestBody := `{"model":"gpt-4o","messages":[{"role":"user","content":"alice@example.com and bob@example.com and alice@example.com"}]}`
	for range 2 {
		if response := sendAuditRequest(engine, requestBody); response.Code != http.StatusOK {
			t.Fatalf("request status = %d", response.Code)
		}
	}
	if len(f.inputs) != 2 {
		t.Fatalf("forwarded attempts = %d", len(f.inputs))
	}
	for _, input := range f.inputs {
		if input.ExternalModel != "gpt-4o" || !bytes.Contains(input.Request.Body, []byte(`"model":"gpt-4o"`)) {
			t.Fatal("routing model was changed by content encryption")
		}
	}
	var first, second struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(f.inputs[0].Request.Body, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(f.inputs[1].Request.Body, &second); err != nil {
		t.Fatal(err)
	}
	value := first.Messages[0].Content
	parts := strings.Split(value, " and ")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "gld1_") || !strings.HasPrefix(parts[1], "gld1_") || parts[0] == parts[1] || parts[0] != parts[2] || second.Messages[0].Content != value {
		t.Fatalf("unstable or indistinguishable encrypted values")
	}
	if strings.Contains(value, "alice@example.com") || strings.Contains(value, "bob@example.com") {
		t.Fatal("plaintext reached upstream")
	}
}

func TestRequestRedactionPreservesAuthenticatedHistoryAfterEncryptRuleRemoval(t *testing.T) {
	f := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"choices":[]}`)}}
	h, manager, _ := newHandlerForTest(t, f, "answer-key")
	cipher, err := h.encryption.NewRedactionCipher(1)
	if err != nil {
		t.Fatal(err)
	}
	token, err := cipher.EncryptToken("alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	manager.Current().RequestRedaction, err = requestredact.Compile([]requestredact.Rule{{
		Pattern: `gld1_[0-9]+_[A-Za-z0-9_-]+`, Replacement: "[MUTATED]",
	}})
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, h)
	response := sendAuditRequest(engine, `{"model":"gpt-4o","messages":[{"role":"user","content":"`+token+`"}]}`)
	if response.Code != http.StatusOK || len(f.inputs) != 1 ||
		!bytes.Contains(f.inputs[0].Request.Body, []byte(token)) ||
		bytes.Contains(f.inputs[0].Request.Body, []byte("[MUTATED]")) {
		t.Fatalf("authenticated history changed: status=%d attempts=%d", response.Code, len(f.inputs))
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

func TestLogUnrestoredRedactionTokens(t *testing.T) {
	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	handler := &Handler{logger: logger}
	handler.logUnrestoredRedactionTokens(nil, "request-1")
	cipher := websocketRedactionTestCipher(t)
	handler.logUnrestoredRedactionTokens(cipher, "request-1")
	if output.Len() != 0 {
		t.Fatalf("logged without unrestored tokens: %s", output.String())
	}
	if _, err := cipher.RestoreText("damaged gld1_3_abc"); err != nil {
		t.Fatal(err)
	}
	handler.logUnrestoredRedactionTokens(cipher, "request-1")
	if line := output.String(); !strings.Contains(line, "request_id=request-1") || !strings.Contains(line, "occurrences=1") {
		t.Fatalf("unrestored token log = %q", line)
	}
}

func TestRedactionMayRestore(t *testing.T) {
	cases := []struct {
		body       string
		reversible bool
		want       bool
	}{
		{``, true, true},
		{``, false, false},
		{`{"input":"hello"}`, true, false},
		{`{"input":"gld1_44_abc"}`, false, true},
		{`{"input":"\u0067ld1_44_abc"}`, false, true},
		{`{"input":[{"type":"reasoning","encrypted_content":"opaque"}]}`, true, true},
		{`{"input":[{"type":"reasoning","encrypted_content":"opaque"}]}`, false, false},
		{`{"contents":[{"parts":[{"text":"x","thoughtSignature":"s"}]}]}`, true, true},
		{`{"messages":[{"content":[{"type":"thinking","signature":"s"}]}]}`, true, true},
		{`{"messages":[{"content":[{"type":"thinking","signature":"s"}]}]}`, false, false},
		{`{"previous_response_id":"resp_1","input":"hi"}`, true, true},
		{`{"conversation":"conv_1","input":"hi"}`, true, true},
		{`{"cachedContent":"cachedContents/c1","contents":[]}`, true, true},
		{`{"cached_content":"cachedContents/c1","contents":[]}`, true, true},
		{`{"container":"container_1","messages":[]}`, true, true},
		{`{"tools":[{"type":"code_interpreter","container_id":"cntr_1"}],"input":"hi"}`, true, true},
		{`{"container":"container_1","messages":[]}`, false, false},
		{`{"messages":[{"role":"user","content":"restart the container"}]}`, true, false},
	}
	for _, tc := range cases {
		request := &dialect.ParsedRequest{Body: []byte(tc.body)}
		if got := redactionMayRestore(request, tc.reversible); got != tc.want {
			t.Errorf("redactionMayRestore(%s, reversible=%v) = %v, want %v", tc.body, tc.reversible, got, tc.want)
		}
	}
}

func TestRequestRedactionRestoresOnlyWhenCiphertextMayReturn(t *testing.T) {
	encrypt := []requestredact.Rule{{Pattern: `alice@example\.com`, Mode: requestredact.ModeEncrypt}}
	cases := []struct {
		name  string
		rules []requestredact.Rule
		body  string
		want  bool
	}{
		{"no rules", nil, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, false},
		{"rule without match", encrypt, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, false},
		{"encrypted content", encrypt, `{"model":"gpt-4o","messages":[{"role":"user","content":"alice@example.com"}]}`, true},
		{"signed history without encrypt rules", nil, `{"model":"gpt-4o","messages":[{"role":"assistant","content":"ok","reasoning_details":[{"type":"reasoning.text","text":"x","signature":"s"}]},{"role":"user","content":"hi"}]}`, false},
		{"signed history with encrypt rules", encrypt, `{"model":"gpt-4o","messages":[{"role":"assistant","content":"ok","reasoning_details":[{"type":"reasoning.text","text":"x","signature":"s"}]},{"role":"user","content":"hi"}]}`, true},
	}
	for _, tc := range cases {
		f := &scriptedForwarder{results: []UpstreamResult{auditReply(`{"choices":[]}`)}}
		h, manager, _ := newHandlerForTest(t, f, "answer-key")
		if tc.rules != nil {
			compiled, err := requestredact.Compile(tc.rules)
			if err != nil {
				t.Fatal(err)
			}
			manager.Current().RequestRedaction = compiled
		}
		engine := gin.New()
		bindGatewayRoutesForTest(t, engine, h)
		if response := sendAuditRequest(engine, tc.body); response.Code != http.StatusOK || len(f.inputs) != 1 {
			t.Fatalf("%s: status=%d attempts=%d", tc.name, response.Code, len(f.inputs))
		}
		if got := f.inputs[0].RedactionCipher != nil; got != tc.want {
			t.Errorf("%s: restoration enabled = %v, want %v", tc.name, got, tc.want)
		}
	}
}
