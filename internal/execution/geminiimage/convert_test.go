package geminiimage

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
)

func geminiImageFixture(t *testing.T, metadata string) []byte {
	t.Helper()
	object := map[string]any{
		"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]string{"mimeType": "image/png", "data": "aW1hZ2U="}}}}}},
	}
	if metadata != "" {
		object["usageMetadata"] = json.RawMessage(metadata)
	}
	body, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestConvertRequestClassifiesRejectedInputs(t *testing.T) {
	for _, test := range []struct {
		name       string
		conversion bool
		payloads   []string
	}{
		{name: "invalid request", payloads: []string{
			`{}`, `{"prompt":""}`, `{"prompt":"   "}`, `{"prompt":5}`, `null`, `[]`,
			`{"prompt":"draw","n":1.5}`, `{"prompt":"draw","n":null}`, `{"prompt":"draw","n":0}`,
			`{"prompt":"draw","stream":"false"}`, `{"prompt":"draw","stream":null}`,
			`{"prompt":"draw","size":123}`, `{"prompt":"draw","quality":null}`,
			`{"prompt":"draw","response_format":true}`,
			`{"prompt":"draw","n":2,"size":123,"unknown_option":true}`,
		}},
		{name: "unsupported conversion", conversion: true, payloads: []string{
			`{"prompt":"draw","n":2}`, `{"prompt":"draw","stream":true}`,
			`{"prompt":"draw","response_format":"url"}`, `{"prompt":"draw","size":"1024x1024"}`,
			`{"prompt":"draw","quality":"high"}`, `{"prompt":"draw","image":"private data"}`,
			`{"prompt":"draw","unknown_option":true}`,
		}},
	} {
		for _, payload := range test.payloads {
			t.Run(test.name+"/"+payload, func(t *testing.T) {
				err := ValidateRequest([]byte(payload))
				var classified interface{ ConversionCode() string }
				if err == nil || errors.As(err, &classified) != test.conversion {
					t.Fatalf("request error = %v, want conversion unsupported = %t", err, test.conversion)
				}
				if test.conversion && classified.ConversionCode() != execution.ErrorCodeTargetConversionNotSupported {
					t.Fatalf("conversion code = %s", classified.ConversionCode())
				}
			})
		}
	}
	if err := ValidateRequest([]byte(`{"prompt":"draw"}`)); err != nil {
		t.Fatalf("default request rejected: %v", err)
	}
}

func TestConvertResponseRejectsInvalidImages(t *testing.T) {
	for name, payload := range map[string]string{
		"invalid JSON": "not JSON private response",
		"empty":        `{}`,
		"blocked":      `{"promptFeedback":{"blockReason":"SAFETY"}}`,
		"text only":    `{"candidates":[{"content":{"parts":[{"text":"private response"}]}}]}`,
		"empty image":  `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":""}}]}}]}`,
		"bad base64":   `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"!private response!"}}]}}]}`,
		"wrong MIME":   `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"text/plain","data":"AA=="}}]}}]}`,
		"two images":   `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"AA=="}},{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}}]}`,
		"thought only": `{"candidates":[{"content":{"parts":[{"thought":true,"inlineData":{"mimeType":"image/png","data":"AA=="}}]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ConvertResponse([]byte(payload))
			if err == nil {
				t.Fatal("invalid image output was accepted")
			}
			if !errors.Is(err, ErrInvalidResponse) || strings.Contains(err.Error(), "private response") {
				t.Fatalf("invalid image error = %v", err)
			}
		})
	}
}

func TestConvertResponsePreservesGeminiUsageStates(t *testing.T) {
	for name, metadata := range map[string]string{
		"missing":         "",
		"empty":           `{}`,
		"partial":         `{"promptTokenCount":10}`,
		"invalid":         `{"promptTokenCount":"invalid","candidatesTokenCount":20}`,
		"negative cached": `{"promptTokenCount":10,"cachedContentTokenCount":20,"candidatesTokenCount":5}`,
		"missing invalid": `{"promptTokenCount":"invalid","candidatesTokenCount":"invalid"}`,
	} {
		t.Run(name, func(t *testing.T) {
			upstream := geminiImageFixture(t, metadata)
			_, evidence, err := ConvertResponse(upstream)
			if err != nil {
				t.Fatal(err)
			}
			want, err := dialect.NewGemini().ExtractUsage(upstream)
			if err != nil {
				t.Fatal(err)
			}
			if evidence == nil || !reflect.DeepEqual(evidence.Normalized, want) {
				t.Fatalf("Images canonical usage = %+v, want %+v", evidence, want)
			}
		})
	}
}
