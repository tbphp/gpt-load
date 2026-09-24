package embedded

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	internalexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	"github.com/tidwall/gjson"
)

func TestChatGPTExecutorPostsBasispointsHeadersAndClampsEffort(t *testing.T) {
	var gotPath, gotAuth, gotAccount, gotOpenAIAccount, gotAuthMode, gotEffort, gotTask, gotTurn, gotUA, gotProduct, gotEditor, gotOfficeHost string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("chatgpt-account-id")
		gotOpenAIAccount = r.Header.Get("x-openai-account-id")
		gotAuthMode = r.Header.Get("x-basispoints-auth-mode")
		gotUA = r.Header.Get("User-Agent")
		gotProduct = r.Header.Get("X-Openai-Internal-Basispoints-Client-Product")
		gotEditor = r.Header.Get("X-Openai-Internal-Basispoints-Client-Editor")
		gotOfficeHost = r.Header.Get("X-Openai-Internal-Basispoints-Office-Host")
		body, _ := io.ReadAll(r.Body)
		gotEffort = gjson.GetBytes(body, "reasoning.effort").String()
		gotTask = gjson.GetBytes(body, "metadata.task_id").String()
		gotTurn = gjson.GetBytes(body, "metadata.turn_id").String()
		if r.Header.Get("x-grok-conv-id") != "" {
			t.Fatalf("leaked grok header: %v", r.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}` + "\n\n"))
	}))
	defer server.Close()

	executor := &chatgptHTTPExecutor{
		cfg: &internalconfig.Config{}, inner: internalexecutor.NewXAIExecutor(&internalconfig.Config{}),
		baseURL: server.URL,
	}
	credential := CodexCredential{
		Type: ProviderCodex, AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccountID: "acct-from-file",
	}
	payload := []byte(`{"model":"gpt-5","input":"hi","reasoning":{"effort":"max"}}`)
	response, err := executor.ExecuteCanonical(t.Context(), "credential-1", credential, ExecuteRequest{
		Model: "gpt-5", Format: "openai-response", Payload: payload, OriginalRequest: payload,
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response.Payload, []byte("ok")) {
		t.Fatalf("response = %s", response.Payload)
	}
	if gotPath != "/responses" {
		t.Fatalf("path = %q, want /responses", gotPath)
	}
	if gotAuth != "Bearer access-secret" || gotAccount != "acct-from-file" ||
		gotOpenAIAccount != "acct-from-file" || gotAuthMode != "chatgpt" {
		t.Fatalf("headers auth=%q account=%q openai=%q mode=%q", gotAuth, gotAccount, gotOpenAIAccount, gotAuthMode)
	}
	if gotEffort != "high" {
		t.Fatalf("effort = %q, want high", gotEffort)
	}
	if gotTask == "" || gotTurn != gotTask {
		t.Fatalf("metadata task=%q turn=%q", gotTask, gotTurn)
	}
	if !strings.Contains(gotUA, "Chrome/") {
		t.Fatalf("user-agent = %q", gotUA)
	}
	if gotProduct != "basispoints-excel-plugin" || gotEditor != "excel" || gotOfficeHost != "Excel" {
		t.Fatalf("excel headers product=%q editor=%q host=%q", gotProduct, gotEditor, gotOfficeHost)
	}
}

func TestChatGPTAccountIDFallsBackToJWTClaim(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id":      "acct-from-jwt",
			"chatgpt_account_user_id": "user-from-jwt",
		},
	})
	token := strings.Join([]string{
		base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)),
		base64.RawURLEncoding.EncodeToString(payload),
		"sig",
	}, ".")
	if got := chatgptAccountID(CodexCredential{AccessToken: token}); got != "acct-from-jwt" {
		t.Fatalf("chatgptAccountID() = %q, want acct-from-jwt", got)
	}
	if got := chatgptAccountUserID(CodexCredential{AccessToken: token}); got != "user-from-jwt" {
		t.Fatalf("chatgptAccountUserID() = %q, want user-from-jwt", got)
	}
	if got := chatgptAccountID(CodexCredential{AccessToken: token, AccountID: "acct-file"}); got != "acct-file" {
		t.Fatalf("file account id should win, got %q", got)
	}
}

func TestPrepareBasispointsBodySmugglesToolsAndLocksTurn(t *testing.T) {
	body := []byte(`{
		"model":"gpt-6-astra",
		"tools":[{"type":"function","name":"get_weather","description":"weather lookup"}],
		"tool_choice":"auto",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"tokyo"}]}]
	}`)
	got := prepareBasispointsBody(body)
	if gjson.GetBytes(got, "tools").Exists() || gjson.GetBytes(got, "tool_choice").Exists() {
		t.Fatalf("client tools leaked: %s", got)
	}
	task := gjson.GetBytes(got, "metadata.task_id").String()
	turn := gjson.GetBytes(got, "metadata.turn_id").String()
	if task == "" || turn != task {
		t.Fatalf("task/turn = %q/%q", task, turn)
	}
	first := gjson.GetBytes(got, "input.0")
	if first.Get("role").String() != "developer" || !strings.Contains(first.Raw, "get_weather") || !strings.Contains(first.Raw, "run_officejs") {
		t.Fatalf("developer catalog = %s", first.Raw)
	}
	again := prepareBasispointsBody(got)
	if gjson.GetBytes(again, "metadata.turn_id").String() != turn {
		t.Fatalf("turn_id changed on rewrite")
	}
}

func TestRewriteOfficeJSCallAndRestoreFullOutput(t *testing.T) {
	call := []byte(`{
		"type":"function_call",
		"name":"run_officejs",
		"call_id":"call_QsjZ",
		"id":"fc_1",
		"summary":"Get current weather for Tokyo",
		"references":["Tokyo weather"],
		"arguments":{"summary":"Get current weather for Tokyo","code":"{\"tool\":\"get_weather\",\"args\":{\"city\":\"Tokyo\"}}","destructive":false,"references":["Tokyo weather"]}
	}`)
	rewritten, ok := rewriteOfficeJSValue(gjson.ParseBytes(call))
	if !ok {
		t.Fatal("expected unwrap")
	}
	raw, err := json.Marshal(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(raw, "name").String() != "get_weather" {
		t.Fatalf("name = %s", raw)
	}
	if gjson.GetBytes(raw, "arguments").String() != `{"city":"Tokyo"}` && gjson.GetBytes(raw, "arguments").Raw != `{"city":"Tokyo"}` {
		t.Fatalf("args = %s", raw)
	}
	body := []byte(`{
		"input":[
			{"type":"function_call","name":"get_weather","call_id":"call_QsjZ","arguments":"{\"city\":\"Tokyo\"}"},
			{"type":"function_call_output","call_id":"call_QsjZ","output":"18C"}
		]
	}`)
	got := prepareBasispointsBody(body)
	out := gjson.GetBytes(got, "input.1")
	if out.Get("type").String() != "function_call_output" || out.Get("call_id").String() != "call_QsjZ" {
		t.Fatalf("output = %s", out.Raw)
	}
	if out.Get("id").String() != "fc_1" || out.Get("summary").String() != "Get current weather for Tokyo" {
		t.Fatalf("identity lost: %s", out.Raw)
	}
	if out.Get("output").String() != "18C" {
		t.Fatalf("output value = %s", out.Raw)
	}
	callItem := gjson.GetBytes(got, "input.0")
	if callItem.Get("name").String() != "run_officejs" {
		t.Fatalf("call item = %s", callItem.Raw)
	}
	if gjson.GetBytes(got, "metadata.agent_iteration").Int() != 1 {
		t.Fatalf("agent_iteration = %s", got)
	}
	if gjson.GetBytes(got, "metadata.turn_id").String() != gjson.GetBytes(got, "metadata.task_id").String() {
		t.Fatalf("turn unlocked: %s", got)
	}
}

func TestChatGPTExecutionBaseURLDefaultsToBasispoints(t *testing.T) {
	executor := &chatgptHTTPExecutor{}
	got, err := executor.executionBaseURL("")
	if err != nil || got != defaultChatGPTBaseURL {
		t.Fatalf("executionBaseURL() = %q, %v", got, err)
	}
	got, err = executor.executionBaseURL(" https://relay.example/bps/ ")
	if err != nil || got != "https://relay.example/bps" {
		t.Fatalf("custom root = %q, %v", got, err)
	}
}
