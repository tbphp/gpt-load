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

func decisionsDialect(t *testing.T) Dialect {
	t.Helper()
	selected, _, err := standardRequest(protocol.Decisions, "public")
	if err != nil {
		t.Fatalf("Decisions standard request unavailable: %v", err)
	}
	return selected
}

func TestDecisionsRequestContract(t *testing.T) {
	d := decisionsDialect(t)
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"string state choice", `{"model":"public","state":"hello","questions":{"tier":{"type":"choice","instructions":"Choose a tier","criteria":{"low":"Simple","high":"Hard"}}}}`, true},
		{"object state score", `{"model":"public","state":{"task":"hello"},"questions":{"depth":{"type":"score","instructions":"Choose depth","criteria":["Low","Medium","High"]}}}`, true},
		{"array state noul", `{"model":"public","state":["hello"],"questions":{"urgent":{"type":"noul","instructions":"Is this urgent?","criteria":{"true":"Urgent","false":"Not urgent"}}}}`, true},
		{"structured instructions and criteria", `{"model":"public","state":"hello","questions":{"tier":{"type":"choice","instructions":{"question":"Choose","context":{"budget":1}},"criteria":{"low":null,"high":{"description":"Hard"}}},"depth":{"type":"score","instructions":["Choose",{"scope":"task"}],"criteria":["Low",{"description":"High"}]},"urgent":{"type":"noul","instructions":{"question":"Urgent?"}}}}`, true},
		{"extensions", `{"model":"public","state":"hello","questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}},"provider":{"order":["x"]},"trace":true}`, true},
		{"missing model", `{"state":"hello","questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}}}`, false},
		{"missing state", `{"model":"public","questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}}}`, false},
		{"null state", `{"model":"public","state":null,"questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}}}`, false},
		{"scalar state", `{"model":"public","state":1,"questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}}}`, false},
		{"empty questions", `{"model":"public","state":"hello","questions":{}}`, false},
		{"unknown question", `{"model":"public","state":"hello","questions":{"tier":{"type":"other","instructions":"Choose","criteria":{"low":"Low"}}}}`, false},
		{"scalar instructions", `{"model":"public","state":"hello","questions":{"tier":{"type":"choice","instructions":1,"criteria":{"low":"Low"}}}}`, false},
		{"choice criteria array", `{"model":"public","state":"hello","questions":{"tier":{"type":"choice","instructions":"Choose","criteria":["Low","High"]}}}`, false},
		{"choice scalar description", `{"model":"public","state":"hello","questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":true}}}}`, false},
		{"score too short", `{"model":"public","state":"hello","questions":{"tier":{"type":"score","instructions":"Choose","criteria":["Only"]}}}`, false},
		{"score null level", `{"model":"public","state":"hello","questions":{"tier":{"type":"score","instructions":"Choose","criteria":["Low",null]}}}`, false},
		{"stream", `{"model":"public","state":"hello","questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}},"stream":true}`, false},
		{"trailing data", `{"model":"public","state":"hello","questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}}}{}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := d.InspectRequest(&ParsedRequest{Method: http.MethodPost, Path: "/v1/systemone", Body: []byte(test.body)})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t, err=%v", test.valid, err)
			}
			if err == nil && (metadata.Operation != execution.OperationDecisionsCreate || metadata.Stream || !metadata.ObserveUsage || metadata.RouteRequirement != execution.RouteRequirementNative) {
				t.Fatalf("metadata = %#v", metadata)
			}
		})
	}
	for _, request := range []*ParsedRequest{
		nil,
		{Method: http.MethodGet, Path: "/v1/systemone"},
		{Method: http.MethodPost, Path: "/v1/decisions"},
		{Method: http.MethodPost, Path: "/v1/systemone", Header: http.Header{"Content-Type": {"text/plain"}}},
	} {
		if _, err := d.InspectRequest(request); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestDecisionsModelRewritePreservesNativePayload(t *testing.T) {
	d := decisionsDialect(t).(ModelRewriter)
	body := []byte(`{"model":"public","state":{"task":"hello"},"questions":{"tier":{"type":"choice","instructions":"Choose","criteria":{"low":"Low"}}},"stream":false,"api_key":"injected","fallbacks":["other"],"future":1.2300}`)
	request := &ParsedRequest{Method: http.MethodPost, Path: "/v1/systemone", Body: body}
	got, err := d.RewriteRequestModel(request, "upstream")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"model":"upstream"`, `"future":1.2300`, `"questions":`} {
		if !bytes.Contains(got.Body, []byte(value)) {
			t.Fatalf("missing %s in %s", value, got.Body)
		}
	}
	for _, value := range []string{"api_key", "fallbacks", "stream"} {
		if bytes.Contains(got.Body, []byte(`"`+value+`":`)) {
			t.Fatalf("control field retained: %s", got.Body)
		}
	}
	if !bytes.Equal(request.Body, body) {
		t.Fatal("request mutated")
	}

	response := []byte(`{"id":"decision-1","model":"upstream","answers":{"tier":{"choice":"low"}},"usage":{"input_tokens":7,"output_tokens":1},"provider":"Typesafe","future":1.2300}`)
	rewritten, err := d.RewriteResponseModel(response, "public")
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	if json.Unmarshal(response, &before) != nil || json.Unmarshal(rewritten, &after) != nil {
		t.Fatal("invalid response JSON")
	}
	if string(after["model"]) != `"public"` || !bytes.Equal(before["answers"], after["answers"]) || !bytes.Equal(before["future"], after["future"]) {
		t.Fatalf("response=%s", rewritten)
	}
}

func TestDecisionsUsage(t *testing.T) {
	d := decisionsDialect(t).(UsageExtractor)
	for _, test := range []struct {
		body  string
		want  usage.Tokens
		state usage.State
	}{
		{`{"usage":{"input_tokens":7,"output_tokens":1,"cost":0.000001}}`, usage.Tokens{UncachedInput: 7, Output: 1}, usage.StateComplete},
		{`{"usage":{"prompt_tokens":7,"completion_tokens":1,"total_tokens":8}}`, usage.Tokens{UncachedInput: 7, Output: 1}, usage.StateComplete},
		{`{"usage":{"input_tokens":0,"output_tokens":0}}`, usage.Tokens{}, usage.StateComplete},
		{`{"answers":{}}`, usage.Tokens{}, usage.StateMissing},
		{`{"usage":{"input_tokens":7}}`, usage.Tokens{UncachedInput: 7}, usage.StatePartial},
		{`{"usage":{"output_tokens":1}}`, usage.Tokens{Output: 1}, usage.StatePartial},
	} {
		got, err := d.ExtractUsage([]byte(test.body))
		if err != nil || got.State != test.state || got.Tokens != test.want {
			t.Fatalf("%s: %#v %v", test.body, got, err)
		}
	}
}
