package requestaudit

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestExtractFiltersOpaquePartsAndKeepsReadableContent(t *testing.T) {
	for _, test := range []struct {
		name, body string
		want       []string
	}{
		{
			"Responses reasoning and attachments",
			`{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"reasoning-summary"}],"encrypted_content":"opaque-reasoning"},{"role":"user","content":[{"type":"input_text","text":"user-question"},{"type":"input_image","image_url":"opaque-image"},{"type":"input_file","file_id":"opaque-file"},{"type":"input_text","text":"trailing-text"}]}]}`,
			[]string{"reasoning-summary", "user-question", "trailing-text"},
		},
		{
			"Chat image and audio",
			`{"messages":[{"role":"assistant","audio":{"id":"audio_a","data":"opaque-audio","transcript":"readable-transcript"}},{"role":"user","content":[{"type":"text","text":"user-question"},{"type":"image_url","image_url":{"url":"opaque-image"}},{"type":"input_audio","input_audio":{"data":"opaque-audio","format":"wav"}}]}]}`,
			[]string{"user-question", "readable-transcript"},
		},
		{
			"Anthropic thinking tools and text documents",
			`{"system":"system-instructions","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"readable-thinking","signature":"opaque-signature"},{"type":"redacted_thinking","data":"opaque-thinking"},{"type":"tool_use","id":"call_a","name":"lookup","input":{"type":"file","file_id":"business-file","encrypted_content":"business-value"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_a","content":[{"type":"image","source":{"type":"base64","data":"opaque-image"}},{"type":"text","text":"tool-text"}]},{"type":"document","source":{"type":"text","media_type":"text/plain","data":"document-text"}}]}]}`,
			[]string{"system-instructions", "readable-thinking", "lookup", "business-file", "business-value", "tool-text", "document-text"},
		},
		{
			"Gemini parts and structured tool data",
			`{"contents":[{"parts":[{"text":"user-question","thoughtSignature":"opaque-signature"},{"inlineData":{"mimeType":"image/png","data":"opaque-image"}},{"fileData":{"mimeType":"application/pdf","fileUri":"opaque-file"}},{"functionCall":{"name":"lookup","args":{"type":"file","file_id":"argument-file"}}},{"functionResponse":{"name":"lookup","response":{"type":"file","file_id":"business-file","encrypted_content":"business-value","content":"tool-text"},"parts":[{"inlineData":{"mimeType":"image/png","data":"opaque-tool-image"}}]}}]}]}`,
			[]string{"user-question", "lookup", "argument-file", "business-file", "business-value", "tool-text"},
		},
		{
			"Schemas and metadata are text data",
			`{"input":"user-question","metadata":{"type":"file","file_id":"business-file","encrypted_content":"business-value"},"tools":[{"type":"function","name":"lookup","parameters":{"type":"object","properties":{"file_id":{"type":"string"},"encrypted_content":{"type":"string"}}}}]}`,
			[]string{"user-question", "business-file", "business-value", "file_id", "encrypted_content", "lookup"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(test.body)
			doc, reason := Extract(body)
			if reason != "" {
				t.Fatalf("readable content was skipped: %s", reason)
			}
			var cache Cache
			review := cache.Prepare(nil, "jev", doc, DefaultConfig().Rules, time.Now())
			if review.Reason != "" || len(review.Payload) == 0 {
				t.Fatalf("no text review was prepared: %+v", review)
			}
			for _, want := range test.want {
				if !bytes.Contains(review.Payload, []byte(want)) {
					t.Errorf("readable content missing: %s", want)
				}
			}
			if bytes.Contains(review.Payload, []byte("opaque-")) || string(body) != test.body {
				t.Fatal("opaque content reached JEV or the business request was changed")
			}
		})
	}
}

func TestExtractOmitsUnitsWithoutReadableContent(t *testing.T) {
	for _, body := range []string{
		`{"input":[{"type":"reasoning","id":"rs_a","summary":[],"encrypted_content":"opaque-reasoning"}]}`,
		`{"input":[{"type":"compaction","id":"cmp_a","encrypted_content":"opaque-compaction"}]}`,
		`{"input":[{"type":"item_reference","id":"item_a"}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"opaque-image"}}]}]}`,
		`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"opaque-image"}}]}]}`,
	} {
		if doc := document(t, body); len(doc.Units) != 0 {
			t.Fatalf("empty content envelope became a review target: %s", body)
		}
	}
}

func TestOpaqueHistoryDoesNotInvalidateReviewedText(t *testing.T) {
	var cache Cache
	now := time.Now()
	body := `{"input":[{"type":"reasoning","summary":[],"encrypted_content":"opaque-old"},{"role":"user","content":"user-question"}]}`
	first := document(t, body)
	answer(t, cache.Prepare(nil, "jev", first, DefaultConfig().Rules, now), now)
	second := document(t, strings.ReplaceAll(body, "opaque-old", "opaque-new"))
	if first.Digest != second.Digest || len(cache.Prepare(nil, "jev", second, DefaultConfig().Rules, now).Payload) != 0 {
		t.Fatal("ignored history invalidated the text review")
	}
	changed := document(t, strings.ReplaceAll(body, "user-question", "changed-question"))
	if len(cache.Prepare(nil, "jev", changed, DefaultConfig().Rules, now).Payload) == 0 {
		t.Fatal("changed readable content reused an earlier verdict")
	}
}
