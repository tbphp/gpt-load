package dialect

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestOpenAIImagesInspectJSONRequests(t *testing.T) {
	t.Parallel()

	selected := NewOpenAIImages()
	tests := []struct {
		name      string
		path      string
		body      string
		operation execution.Operation
		stream    bool
		wantError bool
	}{
		{
			name: "generation", path: "/v1/images/generations",
			body:      `{"model":"gpt-image-2","prompt":"draw","quality":"auto"}`,
			operation: execution.OperationImagesGenerate,
		},
		{
			name: "generation stream", path: "/v1/images/generations",
			body:      `{"model":"gpt-image-2","prompt":"draw","stream":true}`,
			operation: execution.OperationImagesGenerate, stream: true,
		},
		{
			name: "JSON edit", path: "/v1/images/edits",
			body:      `{"model":"gpt-image-2","prompt":"edit","images":[{"image_url":"data:image/png;base64,AA=="}]}`,
			operation: execution.OperationImagesEdit,
		},
		{
			name: "missing model", path: "/v1/images/generations",
			body: `{"prompt":"draw"}`, wantError: true,
		},
		{
			name: "invalid stream", path: "/v1/images/generations",
			body: `{"model":"gpt-image-2","stream":"true"}`, wantError: true,
		},
		{
			name: "trailing JSON", path: "/v1/images/generations",
			body: `{"model":"gpt-image-2"}{}`, wantError: true,
		},
		{
			name: "unknown path", path: "/v1/images/variations",
			body: `{"model":"gpt-image-2"}`, wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			metadata, err := selected.InspectRequest(&ParsedRequest{
				Method: http.MethodPost,
				Path:   test.path,
				Header: http.Header{"Content-Type": {"application/json"}},
				Body:   []byte(test.body),
			})
			if test.wantError {
				if err == nil {
					t.Fatalf("InspectRequest() = %#v, nil error", metadata)
				}
				return
			}
			if err != nil {
				t.Fatalf("InspectRequest() error = %v", err)
			}
			if metadata.Model == nil || *metadata.Model != "gpt-image-2" ||
				metadata.Operation != test.operation || metadata.Stream != test.stream ||
				metadata.RouteRequirement != execution.RouteRequirementNative ||
				!metadata.ObserveUsage {
				t.Fatalf("metadata = %#v", metadata)
			}
		})
	}
}

func TestOpenAIImagesModelRewritePreservesUnknownFields(t *testing.T) {
	t.Parallel()

	selected := NewOpenAIImages()
	original := &ParsedRequest{
		Method: http.MethodPost,
		Path:   "/v1/images/generations",
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   []byte(`{"model":"public-image","prompt":"draw","future":{"kept":true}}`),
	}
	rewritten, err := selected.RewriteRequestModel(original, "upstream-image")
	if err != nil {
		t.Fatalf("RewriteRequestModel() error = %v", err)
	}
	if bytes.Equal(rewritten.Body, original.Body) {
		t.Fatal("RewriteRequestModel() did not rewrite the body")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(rewritten.Body, &object); err != nil {
		t.Fatalf("decode rewritten body: %v", err)
	}
	var model string
	if err := json.Unmarshal(object["model"], &model); err != nil || model != "upstream-image" {
		t.Fatalf("rewritten model = %q, %v", model, err)
	}
	if string(object["future"]) != `{"kept":true}` {
		t.Fatalf("future field = %s", object["future"])
	}
	if string(original.Body) != `{"model":"public-image","prompt":"draw","future":{"kept":true}}` {
		t.Fatal("RewriteRequestModel() mutated the original")
	}

	response := []byte(`{"created":1,"model":"upstream-image","data":[{"b64_json":"AA=="}]}`)
	downstream, err := selected.RewriteResponseModel(response, "public-image")
	if err != nil || !bytes.Contains(downstream, []byte(`"model":"public-image"`)) ||
		!bytes.Contains(downstream, []byte(`"b64_json":"AA=="`)) {
		t.Fatalf("RewriteResponseModel() = %s, %v", downstream, err)
	}
}

func TestOpenAIImagesStreamLifecycle(t *testing.T) {
	t.Parallel()

	selected := NewOpenAIImages()
	if !selected.RequiresTerminalEvent() {
		t.Fatal("RequiresTerminalEvent() = false")
	}
	tests := []struct {
		name        string
		event       StreamEvent
		disposition StreamEventDisposition
		wantError   bool
	}{
		{name: "generation partial", event: StreamEvent{Name: "image_generation.partial_image", Payload: []byte(`{"type":"image_generation.partial_image","b64_json":"AA=="}`)}},
		{name: "edit partial", event: StreamEvent{Payload: []byte(`{"type":"image_edit.partial_image","b64_json":"AA=="}`)}},
		{name: "generation completed", event: StreamEvent{Name: "image_generation.completed", Payload: []byte(`{"type":"image_generation.completed","b64_json":"AA=="}`)}, disposition: StreamEventCompleted},
		{name: "edit completed", event: StreamEvent{Payload: []byte(`{"type":"image_edit.completed","b64_json":"AA=="}`)}, disposition: StreamEventCompleted},
		{name: "completed with error", event: StreamEvent{Payload: []byte(`{"type":"image_generation.completed","error":{"message":"failed"}}`)}, disposition: StreamEventFailed},
		{name: "completed with null error", event: StreamEvent{Payload: []byte(`{"type":"image_generation.completed","error":null}`)}, disposition: StreamEventCompleted},
		{name: "completed with empty error", event: StreamEvent{Payload: []byte(`{"type":"image_generation.completed","error":{}}`)}, disposition: StreamEventCompleted},
		{name: "error event", event: StreamEvent{Payload: []byte(`{"type":"error","error":{"message":"failed"}}`)}, disposition: StreamEventFailed},
		{name: "null event", event: StreamEvent{Payload: []byte(`null`)}, wantError: true},
		{name: "name conflict", event: StreamEvent{Name: "image_generation.completed", Payload: []byte(`{"type":"image_edit.completed"}`)}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification, err := selected.ClassifyStreamEvent(test.event)
			if test.wantError {
				if err == nil {
					t.Fatalf("ClassifyStreamEvent() = %#v, nil error", classification)
				}
				return
			}
			if err != nil || classification.Disposition != test.disposition {
				t.Fatalf("ClassifyStreamEvent() = %#v, %v", classification, err)
			}
		})
	}
}

func TestOpenAIImagesStandardRequestUsesGeneration(t *testing.T) {
	t.Parallel()

	metadata, err := InspectStandardRequest(protocol.OpenAIImages, "gpt-image-2")
	if err != nil {
		t.Fatalf("InspectStandardRequest() error = %v", err)
	}
	if metadata.Model == nil || *metadata.Model != "gpt-image-2" ||
		metadata.Operation != execution.OperationImagesGenerate ||
		metadata.RouteRequirement != execution.RouteRequirementNative {
		t.Fatalf("metadata = %#v", metadata)
	}
}
