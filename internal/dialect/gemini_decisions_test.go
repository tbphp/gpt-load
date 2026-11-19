package dialect

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

func geminiDecisionsDialect(t *testing.T) Dialect {
	t.Helper()
	selected, request, err := standardRequest(protocol.GeminiDecisions, "public")
	if err != nil {
		t.Fatalf("Gemini Decisions standard request unavailable: %v", err)
	}
	if _, err := selected.InspectRequest(request); err != nil {
		t.Fatalf("Gemini Decisions standard request rejected: %v", err)
	}
	return selected
}

func TestGeminiDecisionsRequestContract(t *testing.T) {
	d := geminiDecisionsDialect(t)
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"documented noul", `{"model":"public","input":"The mug arrived cracked.","questions":{"is_damaged":{"type":"noul","instructions":"Did the item arrive damaged?"}}}`, true},
		{"multimodal input and extensions", `{"model":"public","input":[{"type":"text","text":"hello"},{"type":"image","data":"aGk=","mime_type":"image/png"}],"questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"","high":"Hard"}}},"future":1.2300}`, true},
		// 输入与题目由上游校验，网关只读取 model。
		{"questions left to upstream", `{"model":"public","input":"hello","questions":{}}`, true},
		{"missing model", `{"input":"hello","questions":{"q":{"type":"noul","instructions":"Ready?"}}}`, false},
		{"stream", `{"model":"public","input":"hello","questions":{"q":{"type":"noul","instructions":"Ready?"}},"stream":true}`, false},
		{"not an object", `[]`, false},
		{"trailing data", `{"model":"public"}{}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := d.InspectRequest(&ParsedRequest{Method: http.MethodPost, Path: "/v1beta/decisions", Body: []byte(test.body)})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t, err=%v", test.valid, err)
			}
			if err == nil && (metadata.Model == nil || *metadata.Model != "public" ||
				metadata.Operation != execution.OperationDecisionsCreate || metadata.Stream || !metadata.ObserveUsage ||
				metadata.RouteRequirement != execution.RouteRequirementNative) {
				t.Fatalf("metadata = %#v", metadata)
			}
		})
	}
	for _, request := range []*ParsedRequest{
		nil,
		{Method: http.MethodGet, Path: "/v1beta/decisions", Body: []byte(`{"model":"public"}`)},
		{Method: http.MethodPost, Path: "/v1/systemone", Body: []byte(`{"model":"public"}`)},
		{Method: http.MethodPost, Path: "/v1beta/decisions", Header: http.Header{"Content-Type": {"text/plain"}}, Body: []byte(`{"model":"public"}`)},
	} {
		if _, err := d.InspectRequest(request); err == nil {
			t.Fatalf("invalid request accepted: %#v", request)
		}
	}
}

func TestGeminiDecisionsModelRewritePreservesNativePayload(t *testing.T) {
	d := geminiDecisionsDialect(t).(ModelRewriter)
	body := []byte(`{"model":"public","input":[{"type":"text","text":"hello"}],"questions":{"q":{"type":"noul","instructions":"Ready?"}},"api_key":"injected","x-goog-api-key":"injected","future":1.2300}`)
	got, err := d.RewriteRequestModel(&ParsedRequest{Method: http.MethodPost, Path: "/v1beta/decisions", Body: body}, "gemini-decision-1")
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(got.Body, &object); err != nil {
		t.Fatal(err)
	}
	if string(object["model"]) != `"gemini-decision-1"` || string(object["input"]) != `[{"type":"text","text":"hello"}]` ||
		string(object["questions"]) != `{"q":{"type":"noul","instructions":"Ready?"}}` || string(object["future"]) != "1.2300" {
		t.Fatalf("body = %s", got.Body)
	}
	for _, field := range []string{"api_key", "x-goog-api-key"} {
		if _, exists := object[field]; exists {
			t.Fatalf("control field %s retained: %s", field, got.Body)
		}
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", got.Header.Get("Content-Type"))
	}

	response := []byte(`{"answers":{"q":{"type":"noul","noul":0.99,"confidence":0.99}},"usage":{"total_input_tokens":3}}`)
	rewritten, err := d.RewriteResponseModel(response, "public")
	if err != nil || !bytes.Equal(rewritten, response) {
		t.Fatalf("response = %s, %v; want unchanged without model field", rewritten, err)
	}
}

func TestGeminiDecisionsUsage(t *testing.T) {
	d := geminiDecisionsDialect(t).(UsageExtractor)
	for _, test := range []struct {
		body  string
		want  usage.Tokens
		state usage.State
	}{
		{`{"answers":{},"usage":{"total_input_tokens":100,"input_tokens_by_modality":[{"modality":"text","tokens":100}],"total_output_tokens":0,"total_tokens":100}}`, usage.Tokens{UncachedInput: 100}, usage.StateComplete},
		{`{"usage":{"total_input_tokens":7,"total_output_tokens":0}}`, usage.Tokens{UncachedInput: 7}, usage.StateComplete},
		{`{"usage":{"total_input_tokens":7}}`, usage.Tokens{UncachedInput: 7}, usage.StatePartial},
		{`{"answers":{}}`, usage.Tokens{}, usage.StateMissing},
	} {
		got, err := d.ExtractUsage([]byte(test.body))
		if err != nil || got.State != test.state || got.Tokens != test.want {
			t.Fatalf("%s: %#v %v", test.body, got, err)
		}
	}
	inconsistent, err := d.ExtractUsage([]byte(`{"usage":{"total_input_tokens":7,"total_output_tokens":0,"total_tokens":9}}`))
	if err != nil || inconsistent.State == usage.StateComplete {
		t.Fatalf("inconsistent total accepted: %#v %v", inconsistent, err)
	}
}
