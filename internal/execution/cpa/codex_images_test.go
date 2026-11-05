package cpa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

func TestCodexImage26UsesNativeImagesEndpoints(t *testing.T) {
	for _, model := range []string{"gpt-image-2.6", "gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, operation := range []execution.Operation{execution.OperationImagesGenerate, execution.OperationImagesEdit} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", model, operation, stream), func(t *testing.T) {
					adapter, _, _, keys, row := newAdapterFixture(t, credentialJSON("access", "refresh", time.Now().Add(time.Hour)))
					spec := validSpec(t, row, keys)
					spec.ClientProtocol = protocol.OpenAIImages
					spec.Operation = operation
					spec.RouteMode = execution.RouteNative
					spec.RouteRequirement = execution.RouteRequirementNative
					spec.ClientModel, spec.UpstreamModel = model, model
					spec.Path = "/v1/images/generations"
					spec.Body = fmt.Appendf(nil, `{"model":%q,"prompt":"draw a circle"}`, model)
					if operation == execution.OperationImagesEdit {
						spec.Path = "/v1/images/edits"
						spec.Body = fmt.Appendf(nil, `{"model":%q,"prompt":"draw a circle","images":[{"image_url":"data:image/png;base64,AA=="}]}`, model)
					}
					spec.Header = http.Header{"Content-Type": {"application/json"}}
					const imageResponse = `{"created":1,"data":[{"b64_json":"aA=="}]}`
					calls := 0
					transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
						calls++
						wantPath := "/backend-api/codex" + strings.TrimPrefix(spec.Path, "/v1")
						if request.Method != http.MethodPost || request.URL.Path != wantPath {
							t.Errorf("upstream request = %s %s, want POST %s", request.Method, request.URL.Path, wantPath)
						}
						var body struct {
							Model  string `json:"model"`
							Stream bool   `json:"stream"`
							Prompt string `json:"prompt"`
							Images []struct {
								ImageURL string `json:"image_url"`
							} `json:"images"`
						}
						if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
							t.Fatalf("decode upstream request: %v", err)
						}
						if body.Model != model || body.Stream != stream || body.Prompt != "draw a circle" {
							t.Errorf("upstream body = %+v", body)
						}
						if operation == execution.OperationImagesEdit && (len(body.Images) != 1 || body.Images[0].ImageURL != "data:image/png;base64,AA==") {
							t.Errorf("upstream edit images = %+v", body.Images)
						}
						contentType, responseBody := "application/json", imageResponse
						if stream {
							contentType = "text/event-stream"
							responseBody = "event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"aA==\"}\n\n"
						}
						return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(responseBody)), Request: request}, nil
					})
					ctx := context.WithValue(t.Context(), "cliproxy.roundtripper", http.RoundTripper(transport))
					if stream {
						var payload strings.Builder
						result := adapter.ExecuteStream(ctx, spec, func(event execution.StreamEvent) error {
							if event.Kind == execution.StreamEventData {
								payload.Write(event.Data)
							}
							return nil
						})
						if err := result.Validate(); err != nil || result.Error != nil || result.UpstreamProtocol != protocol.OpenAIImages || !strings.Contains(payload.String(), `"b64_json":"aA=="`) {
							t.Fatalf("stream result = %+v, validation=%v, payload=%s", result, err, payload.String())
						}
					} else {
						result := adapter.Execute(ctx, spec)
						if err := result.Validate(); err != nil || result.Error != nil || result.UpstreamProtocol != protocol.OpenAIImages || string(result.Body) != imageResponse {
							t.Fatalf("result = %+v, validation=%v", result, err)
						}
					}
					if calls != 1 {
						t.Fatalf("upstream calls = %d, want 1", calls)
					}
				})
			}
		}
	}
}
