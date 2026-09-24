package gateway

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/protocol"
)

func TestRedactionStoredStreamUsesResponseFormat(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	original := "a\"b\n\\c"
	token, err := c.EncryptToken(original)
	if err != nil {
		t.Fatal(err)
	}
	value := `{"key":"` + token + `"}`
	for _, format := range []string{"json_object", "json_schema"} {
		t.Run(format, func(t *testing.T) {
			s := newRedactionRestoreSSE(protocol.OpenAIResponses, c.RestoreText, false)
			response := map[string]any{"text": map[string]any{"format": map[string]any{"type": format}}, "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": value}}}}}
			_, err := s.Push(redactionReviewEvent(t, map[string]any{"type": "response.created", "response": map[string]any{"text": response["text"]}}))
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				event map[string]any
				path  string
			}{
				{map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": value}, "delta"},
				{map[string]any{"type": "response.output_text.done", "output_index": 0, "content_index": 0, "text": value}, "text"},
				{map[string]any{"type": "response.completed", "response": response}, "response.output.0.content.0.text"},
			} {
				got, err := s.Push(redactionReviewEvent(t, tc.event))
				if err != nil {
					t.Fatal(err)
				}
				payload, _, _ := redactionSSEData(got)
				restored := gjson.GetBytes(payload, tc.path).Str
				if !json.Valid([]byte(restored)) || gjson.Get(restored, "key").Str != original {
					t.Fatalf("%s did not preserve JSON value", tc.path)
				}
			}
		})
	}
}

func TestRedactionMultilineSSEPreservesPayload(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("restored")
	if err != nil {
		t.Fatal(err)
	}
	for _, eol := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(eol, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			wire := []byte("id: 7" + eol + ": keep" + eol + "event: chunk" + eol + "data: {\"choices\":" + eol + "data: [{\"index\":0,\"delta\":{\"content\":\"" + token + "\"},\"finish_reason\":\"stop\"}]}" + eol + eol)
			s := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
			got, err := s.Push(wire)
			if err != nil {
				t.Fatal(err)
			}
			tail, err := s.Finish()
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, tail...)
			payload, name, _ := redactionSSEData(got)
			if !json.Valid(payload) || gjson.GetBytes(payload, "choices.0.delta.content").Str != "restored" || name != "chunk" {
				t.Fatal("rewritten SSE lost part of its payload")
			}
			if !bytes.Contains(got, []byte("id: 7"+eol+": keep"+eol)) {
				t.Fatal("SSE metadata changed")
			}
		})
	}
}

func TestRedactionCompletedToolChannelsAreReleased(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("restored")
	if err != nil {
		t.Fatal(err)
	}
	s := newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
	for i := 0; i < 128; i++ {
		fragment := `{"key":"` + token + `"}`
		for _, part := range []string{fragment[:12], fragment[12:], " \n"} {
			ev := redactionBoundaryChat(t, map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": part}}}}, nil)
			if _, err := s.Push(ev); err != nil {
				t.Fatalf("tool %d: %v", i+1, err)
			}
		}
	}
	// 真正未完成的并行文档仍有上限。
	s = newRedactionRestoreSSE(protocol.OpenAICompletions, c.RestoreText, false)
	for i := 0; i <= maxRedactionStreamChannels; i++ {
		ev := redactionBoundaryChat(t, map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": `{"key":"`}}}}, nil)
		_, err := s.Push(ev)
		if (err != nil) != (i == maxRedactionStreamChannels) {
			t.Fatalf("pending channel limit changed at %d: %v", i, err)
		}
	}
}

func TestRedactionGeminiTextSurvivesPartPositionChanges(t *testing.T) {
	c := websocketRedactionTestCipher(t)
	token, err := c.EncryptToken("restored")
	if err != nil {
		t.Fatal(err)
	}
	for _, structured := range []bool{false, true} {
		value := token
		if structured {
			value = `{"key":"` + token + `"}`
		}
		for split := 1; split < len(value); split++ {
			s := newRedactionRestoreSSE(protocol.Gemini, c.RestoreText, structured)
			var out []byte
			for _, tc := range []struct {
				parts  []any
				finish string
			}{
				{[]any{map[string]any{"thought": true, "text": "reasoning"}, map[string]any{"text": value[:split]}}, ""},
				{[]any{map[string]any{"text": value[split:]}}, "STOP"},
			} {
				ev := redactionReviewEvent(t, map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"parts": tc.parts}, "finishReason": tc.finish}}})
				got, err := s.Push(ev)
				if err != nil {
					t.Fatalf("structured=%v split=%d: %v", structured, split, err)
				}
				out = append(out, got...)
			}
			var text strings.Builder
			for _, payload := range redactionContractPayloads(out) {
				for _, part := range gjson.GetBytes(payload, "candidates.0.content.parts").Array() {
					if !part.Get("thought").Bool() {
						text.WriteString(part.Get("text").Str)
					}
				}
			}
			want := "restored"
			if structured {
				want = `{"key":"restored"}`
			}
			if text.String() != want {
				t.Fatalf("part position changed restoration: structured=%v split=%d", structured, split)
			}
		}
	}
}
