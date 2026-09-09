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

func rerankDialect(t *testing.T) Dialect {
	t.Helper()
	selected, _, err := standardRequest(protocol.Protocol("rerank"), "public")
	if err != nil {
		t.Fatalf("Rerank standard request unavailable: %v", err)
	}
	return selected
}

func TestRerankRequestContract(t *testing.T) {
	d := rerankDialect(t)
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"text", `{"model":"public","query":"hello","documents":["a",""]}`, true},
		{"options", `{"model":"public","query":"hello","documents":["a"],"top_n":1,"return_documents":false,"stream":false,"future":1.2300}`, true},
		{"missing model", `{"query":"q","documents":["a"]}`, false},
		{"missing query", `{"model":"m","documents":["a"]}`, false},
		{"empty query", `{"model":"m","query":" ","documents":["a"]}`, false},
		{"missing documents", `{"model":"m","query":"q"}`, false},
		{"empty documents", `{"model":"m","query":"q","documents":[]}`, false},
		{"null document", `{"model":"m","query":"q","documents":[null]}`, false},
		{"object documents", `{"model":"m","query":"q","documents":[{"text":"a"}]}`, false},
		{"query duplicate", `{"model":"m","query":"q","query":"r","documents":["a"]}`, false},
		{"case alias", `{"model":"m","Query":"q","documents":["a"]}`, false},
		{"documents duplicate", `{"model":"m","query":"q","documents":["a"],"documents":["b"]}`, false},
		{"top_n zero", `{"model":"m","query":"q","documents":["a"],"top_n":0}`, false},
		{"top_n fractional", `{"model":"m","query":"q","documents":["a"],"top_n":1.5}`, false},
		{"return_documents null", `{"model":"m","query":"q","documents":["a"],"return_documents":null}`, false},
		{"stream", `{"model":"m","query":"q","documents":["a"],"stream":true}`, false},
		{"trailing data", `{"model":"m","query":"q","documents":["a"]}{}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata, err := d.InspectRequest(&ParsedRequest{Method: http.MethodPost, Path: "/v1/rerank", Body: []byte(test.body)})
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t, err=%v", test.valid, err)
			}
			if err == nil && (metadata.Operation != execution.Operation("rerank") || metadata.Stream || !metadata.ObserveUsage || metadata.RouteRequirement != execution.RouteRequirementNative || len(metadata.AffinityPrefix) != 0) {
				t.Fatalf("metadata = %#v", metadata)
			}
		})
	}
	for _, request := range []*ParsedRequest{
		nil,
		{Method: http.MethodGet, Path: "/v1/rerank"},
		{Method: http.MethodPost, Path: "/v1/embeddings"},
		{Method: http.MethodPost, Path: "/v1/rerank", Header: http.Header{"Content-Type": {"text/plain"}}},
		{Method: http.MethodPost, Path: "/v1/rerank", Body: []byte{'{', 0xff, '}'}},
	} {
		if _, err := d.InspectRequest(request); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}

func TestRerankModelRewritePreservesWireValues(t *testing.T) {
	d := rerankDialect(t).(ModelRewriter)
	body := []byte(`{"model":"public","query":"q","documents":["a"],"stream":false,"api_key":"injected","fallbacks":["other"],"future":1.2300}`)
	request := &ParsedRequest{Method: http.MethodPost, Path: "/v1/rerank", Body: body}
	got, err := d.RewriteRequestModel(request, "upstream")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"model":"upstream"`, `"future":1.2300`, `"documents":["a"]`} {
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
	response := []byte(`{"model":"upstream","results":[{"index":1,"relevance_score":0.1234567890123456789,"document":{"text":"upstream"}}],"future":1.2300}`)
	rewritten, err := d.RewriteResponseModel(response, "public")
	var before, after map[string]json.RawMessage
	if json.Unmarshal(response, &before) != nil || json.Unmarshal(rewritten, &after) != nil {
		t.Fatal("invalid response JSON")
	}
	if err != nil || string(after["model"]) != `"public"` || !bytes.Equal(before["results"], after["results"]) || !bytes.Equal(before["future"], after["future"]) {
		t.Fatalf("response=%s err=%v", rewritten, err)
	}
	withoutModel := []byte(`{"results":[]}`)
	gotBody, err := d.RewriteResponseModel(withoutModel, "public")
	if err != nil || !bytes.Equal(gotBody, withoutModel) {
		t.Fatalf("absent model: %s %v", gotBody, err)
	}
}

func TestRerankUsageRequiresConsistentTokenEvidence(t *testing.T) {
	d := rerankDialect(t).(UsageExtractor)
	for _, test := range []struct {
		body  string
		input int64
		state usage.State
	}{
		{`{"usage":{"total_tokens":20}}`, 20, usage.StateComplete},
		{`{"usage":{"prompt_tokens":20,"total_tokens":20}}`, 20, usage.StateComplete},
		{`{"usage":{"input_tokens":20}}`, 20, usage.StateComplete},
		{`{"meta":{"tokens":{"input_tokens":20}}}`, 20, usage.StateComplete},
		{`{"meta":{"billed_units":{"input_tokens":20}}}`, 20, usage.StateComplete},
		{`{"usage":{"total_tokens":0}}`, 0, usage.StateComplete},
		{`{"meta":{"billed_units":{"search_units":1}}}`, 0, usage.StateMissing},
		{`{"results":[]}`, 0, usage.StateMissing},
		{`{"usage":{"total_tokens":-1}}`, 0, usage.StateMissing},
		{`{"usage":{"total_tokens":1.5}}`, 0, usage.StateMissing},
		{`{"usage":{"total_tokens":9223372036854775808}}`, 0, usage.StateMissing},
		{`{"usage":{"prompt_tokens":10,"total_tokens":20}}`, 0, usage.StateMissing},
		{`{"usage":{"input_tokens":10,"total_tokens":10,"output_tokens":2}}`, 0, usage.StateMissing},
	} {
		got, err := d.ExtractUsage([]byte(test.body))
		if err != nil || got.State != test.state || got.Tokens != (usage.Tokens{UncachedInput: test.input}) {
			t.Fatalf("%s: %#v %v", test.body, got, err)
		}
	}
}
