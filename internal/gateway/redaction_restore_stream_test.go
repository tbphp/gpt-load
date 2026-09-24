package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"gpt-load/internal/protocol"
)

const streamTestToken = "gld1_3_abc"

func streamTestRestore(value string) (string, error) {
	if strings.Contains(value, "gld1_3_bad") {
		return "", errors.New("invalid token")
	}
	return strings.ReplaceAll(value, streamTestToken, "alice@example.invalid"), nil
}

func TestRedactionRestoreSSEChatKeepsOrderAcrossSplitToken(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	first := "id: 11\nevent: message\ndata: " + `{"id":"chat_1","choices":[{"index":0,"delta":{"content":"hello gld1_3_"}}]}` + "\n\n"
	other := "data: " + `{"choices":[{"index":1,"delta":{"content":"other"}}]}` + "\n\n"
	last := "data: " + `{"choices":[{"index":0,"delta":{"content":"abc!"}}]}` + "\n\n"
	if got, err := stream.Push([]byte(first[:len(first)-1])); err != nil || len(got) != 0 {
		t.Fatalf("partial event output/error = %q / %v", got, err)
	}
	if got, err := stream.Push([]byte(first[len(first)-1:])); err != nil || len(got) != 0 {
		t.Fatalf("split token was emitted early: %q / %v", got, err)
	}
	if got, err := stream.Push([]byte(other)); err != nil || len(got) != 0 {
		t.Fatalf("later choice overtook pending token: %q / %v", got, err)
	}
	got, err := stream.Push([]byte(last))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(first, "hello gld1_3_", "hello ", 1) +
		other + strings.Replace(last, "abc!", "alice@example.invalid!", 1)
	if string(got) != want {
		t.Fatalf("restored stream differs\ngot:  %q\nwant: %q", got, want)
	}
	if tail, err := stream.Finish(); err != nil || len(tail) != 0 {
		t.Fatalf("Finish() = %q / %v", tail, err)
	}
}

func TestRedactionRestoreSSESeparatesChoicesAndSplitCRLF(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	first := "id: first\r\ndata: " +
		`{"choices":[{"index":0,"delta":{"content":"gld1_3_"}}]}` + "\r\n\r"
	second := "data: " +
		`{"choices":[{"index":1,"delta":{"content":"gld1_3_"}}]}` + "\r\n\r\n"
	third := "data: " +
		`{"choices":[{"index":1,"delta":{"content":"abc"}}]}` + "\r\n\r\n"
	last := "data: " +
		`{"choices":[{"index":0,"delta":{"content":"abc"}}]}` + "\r\n\r\n"
	if got, err := stream.Push([]byte(first)); err != nil || len(got) != 0 {
		t.Fatalf("CRLF prefix emitted: %q / %v", got, err)
	}
	if got, err := stream.Push([]byte("\n" + second + third)); err != nil || len(got) != 0 {
		t.Fatalf("later choice overtook first: %q / %v", got, err)
	}
	got, err := stream.Push([]byte(last))
	want := strings.ReplaceAll(first+"\n"+second, "gld1_3_", "") +
		strings.ReplaceAll(third+last, `"abc"`, `"alice@example.invalid"`)
	if err != nil || string(got) != want {
		t.Fatalf("choice order or CRLF framing changed: %q / %v", got, err)
	}
}

func TestRedactionRestoreSSETerminalReleasedOnlyAfterOutput(t *testing.T) {
	cases := []struct {
		name     string
		protocol protocol.Protocol
		first    string
		terminal string
	}{
		{
			name: "Chat", protocol: protocol.OpenAICompletions,
			first:    "data: " + `{"choices":[{"index":0,"delta":{"content":"gld1_3_"}}]}` + "\n\n",
			terminal: "data: [DONE]\n\n",
		},
		{
			name: "Responses", protocol: protocol.OpenAIResponses,
			first: "event: response.output_text.delta\ndata: " +
				`{"type":"response.output_text.delta","output_index":0,"delta":"gld1_3_"}` + "\n\n",
			terminal: "event: response.completed\ndata: " +
				`{"type":"response.completed","response":{"output":[]}}` + "\n\n",
		},
		{
			name: "Anthropic", protocol: protocol.Anthropic,
			first: "event: content_block_delta\ndata: " +
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"gld1_3_"}}` + "\n\n",
			terminal: "event: message_stop\ndata: " + `{"type":"message_stop"}` + "\n\n",
		},
		{
			name: "Gemini", protocol: protocol.Gemini,
			first: "data: " +
				`{"candidates":[{"index":0,"content":{"parts":[{"text":"gld1_3_"}]}}]}` + "\n\n",
			terminal: "data: " +
				`{"candidates":[{"index":0,"finishReason":"STOP"}]}` + "\n\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := newRedactionRestoreSSE(tc.protocol, streamTestRestore, false)
			if got, err := stream.Push([]byte(tc.first)); err != nil || len(got) != 0 || stream.TerminalReleased() {
				t.Fatalf("unfinished event released terminal: %q / %v", got, err)
			}
			if got, err := stream.Push([]byte(tc.terminal)); err == nil || len(got) != 0 || stream.TerminalReleased() {
				t.Fatalf("terminal overtook truncated token: %q / %v", got, err)
			}
			stream = newRedactionRestoreSSE(tc.protocol, streamTestRestore, false)
			if got, err := stream.Push([]byte(tc.terminal)); err != nil ||
				string(got) != tc.terminal || !stream.TerminalReleased() {
				t.Fatalf("clean terminal was not released: %q / %v", got, err)
			}
		})
	}
	chat := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	finish := "data: " + `{"choices":[{"index":0,"finish_reason":"stop"}]}` + "\n\n"
	if got, err := chat.Push([]byte(finish)); err != nil || string(got) != finish || chat.TerminalReleased() {
		t.Fatalf("Chat choice finish was treated as stream terminal: %q / %v", got, err)
	}
	if got, err := chat.Push([]byte("data: [DONE]\n\n")); err != nil || len(got) == 0 || !chat.TerminalReleased() {
		t.Fatalf("Chat DONE was not released: %q / %v", got, err)
	}
}

func TestRedactionRestoreSSETerminalWaitsForSplitCRLF(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	if got, err := stream.Push([]byte("data: [DONE]\r\n\r")); err != nil || len(got) != 0 || stream.TerminalReleased() {
		t.Fatalf("terminal was released before CRLF completion: %q / %v", got, err)
	}
	got, err := stream.Push([]byte("\n"))
	if err != nil || string(got) != "data: [DONE]\r\n\r\n" || !stream.TerminalReleased() {
		t.Fatalf("completed terminal was not released: %q / %v", got, err)
	}
}

func TestRedactionRestoreSSEChatToolDocumentAndDone(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	makeEvent := func(fragment string) string {
		payload, _ := json.Marshal(map[string]any{
			"id": "chat_1",
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
				"tool_calls": []any{map[string]any{"index": 0, "id": "call_1",
					"function": map[string]any{"name": "send", "arguments": fragment}}},
			}}},
		})
		return "data: " + string(payload) + "\n\n"
	}
	first := makeEvent(`{"email":"gld1_3_`)
	second := makeEvent(`abc","count":2}`)
	end := "data: " + `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"
	out, err := stream.Push([]byte(first + second))
	if err != nil || len(out) == 0 {
		t.Fatalf("completed tool values did not stream: %v", err)
	}
	tail, err := stream.Push([]byte(end + "data: [DONE]\n\n"))
	out = append(out, tail...)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(out, []byte(end+"data: [DONE]\n\n")) {
		t.Fatalf("terminal events changed or reordered: %s", out)
	}
	fragments := collectChatToolArguments(t, out)
	if len(fragments) != 2 || strings.Join(fragments, "") != `{"email":"alice@example.invalid","count":2}` {
		t.Fatalf("restored arguments = %#v", fragments)
	}
	if _, err := stream.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestRedactionRestoreSSEResponsesDeltasAndSnapshots(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAIResponses, streamTestRestore, false)
	first := "event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"gld1_3_"}` + "\n\n"
	second := "event: response.output_text.delta\ndata: " + `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"abc"}` + "\n\n"
	done := "event: response.output_text.done\ndata: " + `{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"gld1_3_abc"}` + "\n\n"
	completed := "event: response.completed\ndata: " + `{"type":"response.completed","response":{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":"gld1_3_abc"}]}],"usage":{"output_tokens":7}}}` + "\n\n"
	if got, err := stream.Push([]byte(first)); err != nil || len(got) != 0 {
		t.Fatalf("partial response token leaked: %q / %v", got, err)
	}
	got, err := stream.Push([]byte(second + done + completed))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(got, []byte("alice@example.invalid")) != 3 ||
		bytes.Contains(got, []byte(streamTestToken)) ||
		!bytes.Contains(got, []byte(`"id":"resp_1"`)) ||
		!bytes.Contains(got, []byte(`"output_tokens":7`)) {
		t.Fatalf("Responses deltas/snapshots inconsistent: %s", got)
	}
	if tail, err := stream.Finish(); err != nil || len(tail) != 0 {
		t.Fatalf("Finish() = %q / %v", tail, err)
	}
}

func TestRedactionRestoreSSEStructuredOutputAndAnthropicToolJSON(t *testing.T) {
	cases := []struct {
		name       string
		protocol   protocol.Protocol
		structured bool
		first      string
		second     string
		end        string
		field      string
	}{
		{
			name:     "Responses structured text",
			protocol: protocol.OpenAIResponses, structured: true,
			first: "event: response.output_text.delta\ndata: " +
				`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"{\"email\":\"gld1_3_"}` + "\n\n",
			second: "event: response.output_text.delta\ndata: " +
				`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"abc\",\"n\":2}"}` + "\n\n",
			end: "event: response.output_text.done\ndata: " +
				`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"{\"email\":\"gld1_3_abc\",\"n\":2}"}` + "\n\n",
			field: "delta",
		},
		{
			name:     "Anthropic tool JSON",
			protocol: protocol.Anthropic,
			first: "event: content_block_delta\ndata: " +
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"email\":\"gld1_3_"}}` + "\n\n",
			second: "event: content_block_delta\ndata: " +
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"abc\",\"n\":2}"}}` + "\n\n",
			end: "event: content_block_stop\ndata: " +
				`{"type":"content_block_stop","index":0}` + "\n\n",
			field: "partial_json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := newRedactionRestoreSSE(tc.protocol, streamTestRestore, tc.structured)
			got, err := stream.Push([]byte(tc.first + tc.second))
			if err != nil || len(got) == 0 {
				t.Fatalf("completed values did not stream: %v", err)
			}
			tail, err := stream.Push([]byte(tc.end))
			got = append(got, tail...)
			if err != nil {
				t.Fatal(err)
			}
			var document strings.Builder
			for _, event := range bytes.Split(got, []byte("\n\n")) {
				if len(event) == 0 {
					continue
				}
				index := bytes.Index(event, []byte("data: "))
				if index < 0 {
					continue
				}
				value := gjson.GetBytes(event[index+len("data: "):], tc.field)
				if tc.protocol == protocol.Anthropic {
					value = gjson.GetBytes(event[index+len("data: "):], "delta.partial_json")
				}
				if value.Exists() {
					document.WriteString(value.Str)
				}
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(document.String()), &parsed); err != nil ||
				parsed["email"] != "alice@example.invalid" || parsed["n"] != float64(2) {
				t.Fatalf("restored document = %q / %v", document.String(), err)
			}
		})
	}
}

func TestRedactionRestoreSSEGeminiFunctionCallAndSignature(t *testing.T) {
	event := "data: " + `{"candidates":[{"index":0,"content":{"parts":[{"functionCall":{"name":"send","args":{"address":"gld1_3_abc","n":2}}}]}}],"usageMetadata":{"totalTokenCount":4}}` + "\n\n"
	stream := newRedactionRestoreSSE(protocol.Gemini, streamTestRestore, false)
	got, err := stream.Push([]byte(event))
	if err != nil || !bytes.Contains(got, []byte(`"address":"alice@example.invalid"`)) ||
		!bytes.Contains(got, []byte(`"totalTokenCount":4`)) {
		t.Fatalf("Gemini function call = %q / %v", got, err)
	}
	stream = newRedactionRestoreSSE(protocol.Gemini, streamTestRestore, false)
	signed := strings.Replace(event, `"functionCall"`, `"thoughtSignature":"signed","functionCall"`, 1)
	if _, err := stream.Push([]byte(signed)); err == nil {
		t.Fatal("signed Gemini part was rewritten")
	}
}

func TestRedactionRestoreSSEUsesEventNameWhenPayloadOmitsType(t *testing.T) {
	cases := []struct {
		protocol protocol.Protocol
		event    string
	}{
		{
			protocol.OpenAIResponses,
			"event: response.output_text.delta\ndata: " +
				`{"output_index":0,"content_index":0,"delta":"gld1_3_abc"}` + "\n\n",
		},
		{
			protocol.Anthropic,
			"event: content_block_delta\ndata: " +
				`{"index":0,"delta":{"type":"text_delta","text":"gld1_3_abc"}}` + "\n\n",
		},
	}
	for _, tc := range cases {
		stream := newRedactionRestoreSSE(tc.protocol, streamTestRestore, false)
		got, err := stream.Push([]byte(tc.event))
		if err != nil || !bytes.Contains(got, []byte("alice@example.invalid")) {
			t.Fatalf("%s event name fallback = %q / %v", tc.protocol, got, err)
		}
	}
}

func TestRedactionRestoreSSEAnthropicAndGemini(t *testing.T) {
	cases := []struct {
		name     string
		protocol protocol.Protocol
		first    string
		second   string
		end      string
	}{
		{
			name:     "anthropic",
			protocol: protocol.Anthropic,
			first: "event: content_block_delta\ndata: " +
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"gld1_3_"}}` + "\n\n",
			second: "event: content_block_delta\ndata: " +
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"abc"}}` + "\n\n",
			end: "event: content_block_stop\ndata: " +
				`{"type":"content_block_stop","index":0}` + "\n\n",
		},
		{
			name:     "gemini",
			protocol: protocol.Gemini,
			first: "data: " +
				`{"candidates":[{"index":0,"content":{"parts":[{"text":"gld1_3_"}]}}]}` + "\n\n",
			second: "data: " +
				`{"candidates":[{"index":0,"content":{"parts":[{"text":"abc"}]}}]}` + "\n\n",
			end: "data: " +
				`{"candidates":[{"index":0,"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":5}}` + "\n\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := newRedactionRestoreSSE(tc.protocol, streamTestRestore, false)
			if got, err := stream.Push([]byte(tc.first)); err != nil || len(got) != 0 {
				t.Fatalf("partial token emitted: %q / %v", got, err)
			}
			got, err := stream.Push([]byte(tc.second + tc.end))
			if err != nil || bytes.Count(got, []byte("alice@example.invalid")) != 1 ||
				!bytes.HasSuffix(got, []byte(tc.end)) {
				t.Fatalf("restored stream = %q / %v", got, err)
			}
		})
	}
}

func TestRedactionRestoreSSERejectsTruncatedToken(t *testing.T) {
	first := []byte("data: " + `{"choices":[{"index":0,"delta":{"content":"gld1_3_ab"}}]}` + "\n\n")
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	if got, err := stream.Push(first); err != nil || len(got) != 0 {
		t.Fatalf("truncated candidate output = %q / %v", got, err)
	}
	if got, err := stream.Push([]byte("data: [DONE]\n\n")); err == nil || len(got) != 0 {
		t.Fatalf("terminal accepted truncated candidate: %q / %v", got, err)
	}
	stream = newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	if _, err := stream.Push(first); err != nil {
		t.Fatal(err)
	}
	stream = newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	if _, err := stream.Push(first); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Finish(); err == nil {
		t.Fatal("EOF accepted truncated token")
	}
	stream = newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	if _, err := stream.Push([]byte("data: {\"choices\"")); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Finish(); !errors.Is(err, errSSEEventIncomplete) {
		t.Fatalf("incomplete SSE event error = %v", err)
	}
}

func TestRedactionRestoreSSEBoundsInterleavedChannels(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	for index := 0; index < maxRedactionStreamChannels; index++ {
		message := "data: " + `{"choices":[{"index":` + strconv.Itoa(index) +
			`,"delta":{"content":"gld1_3_"}}]}` + "\n\n"
		if output, err := stream.Push([]byte(message)); err != nil || len(output) != 0 {
			t.Fatalf("channel %d = %q / %v", index, output, err)
		}
	}
	extra := "data: " + `{"choices":[{"index":` + strconv.Itoa(maxRedactionStreamChannels) +
		`,"delta":{"content":"gld1_3_"}}]}` + "\n\n"
	if output, err := stream.Push([]byte(extra)); err == nil || len(output) != 0 {
		t.Fatalf("excess channel was accepted: %q / %v", output, err)
	}
}

func TestRedactionRestoreSSEFindsEscapedTokenAndRejectsCorruptToken(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	first := "data: " + `{"choices":[{"index":0,"delta":{"content":"\u0067ld1_3_"}}]}` + "\n\n"
	second := "data: " + `{"choices":[{"index":0,"delta":{"content":"abc"}}]}` + "\n\n"
	if got, err := stream.Push([]byte(first)); err != nil || len(got) != 0 {
		t.Fatalf("escaped token prefix escaped early: %q / %v", got, err)
	}
	got, err := stream.Push([]byte(second))
	if err != nil || !bytes.Contains(got, []byte("alice@example.invalid")) ||
		bytes.Contains(got, []byte(streamTestToken)) {
		t.Fatalf("escaped token was missed: %q / %v", got, err)
	}
	stream = newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	corrupt := "data: " + `{"choices":[{"index":0,"delta":{"content":"gld1_3_bad"}}]}` + "\n\n"
	if got, err := stream.Push([]byte(corrupt)); err == nil || len(got) != 0 {
		t.Fatalf("invalid token was released: %q / %v", got, err)
	}
}

func TestRedactionRestoreSSEMasksOnlyTokensInErrorEvents(t *testing.T) {
	const token = "gld1_50_Td2RZbM74JpizRwe4m_ji9Fsg5B9vD97EUK_HoAC0ce7JdwNiA"
	for _, eventPrefix := range []string{"event: error\n", ""} {
		wantName := ""
		if eventPrefix != "" {
			wantName = "error"
		}
		for _, encoded := range []string{token, `\u0067` + token[1:]} {
			stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
			event := []byte(eventPrefix + `data: {"error":{"message":"https://example.test/?q=go ` + encoded + `"}}` + "\n\n")
			got, err := stream.Push(event)
			if err != nil || !bytes.Contains(got, []byte("[REDACTED]")) ||
				!bytes.Contains(got, []byte("https://example.test/?q=go")) ||
				bytes.Contains(got, []byte(token)) {
				t.Fatalf("error event masking = %q / %v", got, err)
			}
			payload, name, ok := redactionSSEData(got)
			if !ok || name != wantName || !json.Valid(payload) ||
				strings.Contains(gjson.GetBytes(payload, "error.message").Str, token) {
				t.Fatalf("invalid or unmasked SSE error payload: %q", got)
			}
		}
	}
}

func TestRedactionRestoreSSEToolJSONFindsUnicodeEscapedToken(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	fragment := `{"email":"\u0067ld1_3_abc","count":2}`
	message, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
			"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{
				"arguments": fragment,
			}}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := append(append([]byte("data: "), message...), []byte("\n\n")...)
	got, err := stream.Push(first)
	if err != nil || len(got) == 0 {
		t.Fatalf("completed escaped token did not stream: %v", err)
	}
	tail, err := stream.Push([]byte("data: " + `{"choices":[{"index":0,"finish_reason":"tool_calls"}]}` + "\n\n"))
	got = append(got, tail...)
	if err != nil {
		t.Fatal(err)
	}
	fragments := collectChatToolArguments(t, got)
	if len(fragments) != 1 || fragments[0] != `{"email":"alice@example.invalid","count":2}` {
		t.Fatalf("escaped tool token not restored: %#v", fragments)
	}
}

func TestRedactionRestoreSSEPassesOrdinaryEventsByteExact(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	input := "id: 9\r\nevent: message\r\n: keep\r\ndata: " +
		`{"choices":[{"index":0,"delta":{"content":"ordinary"}}],"usage":{"label":"safe"}}` +
		"\r\n\r\n"
	got, err := stream.Push([]byte(input))
	if err != nil || string(got) != input {
		t.Fatalf("ordinary SSE changed: %q / %v", got, err)
	}
	if tail, err := stream.Finish(); err != nil || len(tail) != 0 {
		t.Fatalf("Finish() = %q / %v", tail, err)
	}
}

func TestRedactionRestoreSSEPreservesUnfinishedHeaderWithoutBodySeparator(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	first := "data: " + `{"choices":[{"index":0,"delta":{"content":"literal gld1_123"}}]}` + "\n\n"
	end := "data: " + `{"choices":[{"index":0,"finish_reason":"stop"}]}` + "\n\n"
	if out, err := stream.Push([]byte(first)); err != nil || len(out) != 0 {
		t.Fatalf("header prefix emitted early: %q / %v", out, err)
	}
	got, err := stream.Push([]byte(end))
	if err != nil || string(got) != first+end {
		t.Fatalf("ordinary similar prefix changed: %q / %v", got, err)
	}
}

func TestRedactionRestoreSSEAcceptsManyEventsInOneLargeChunk(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAICompletions, streamTestRestore, false)
	input := []byte(strings.Repeat("data: {}\n\n", (maxSSEEventBytes/len("data: {}\n\n"))+1))
	got, err := stream.Push(input)
	if err != nil || !bytes.Equal(got, input) {
		t.Fatalf("large transport chunk changed or failed: %d / %d / %v", len(got), len(input), err)
	}
}

func TestRedactionRestoreSSEKeepsDefaultEventLimit(t *testing.T) {
	stream := newRedactionRestoreSSE(protocol.OpenAIResponses, streamTestRestore, false)
	event := []byte("data: " + strings.Repeat("x", maxSSEEventBytes-len("data: \n\n")+1) + "\n\n")
	if _, err := stream.Push(event); !errors.Is(err, errSSEEventTooLarge) {
		t.Fatalf("oversized HTTP SSE event error = %v, want errSSEEventTooLarge", err)
	}
}

func TestRedactionTokenStreamDoesNotSplitCompleteCiphertext(t *testing.T) {
	for _, value := range []string{
		"gld1_1_g", "gld1_2_gl", "gld1_3_gld", "gld1_4_gld1",
	} {
		token := redactionTokenStream{}
		var out strings.Builder
		err := token.pushString(value, &out, func(v string) (string, error) { return v, nil }, false)
		if err != nil || out.String() != value || token.pending() {
			t.Fatalf("complete token not released: %v", err)
		}
	}
	token := redactionTokenStream{}
	var out strings.Builder
	if err := token.pushString("gld1_12000000_", &out, streamTestRestore, false); err != nil || out.Len() != 0 || !token.pending() {
		t.Fatalf("bounded long token prefix failed: %v", err)
	}
}

func collectChatToolArguments(t *testing.T, body []byte) []string {
	t.Helper()
	var fragments []string
	for _, event := range bytes.Split(body, []byte("\n\n")) {
		if !bytes.HasPrefix(event, []byte("data: ")) {
			continue
		}
		var payload struct {
			Choices []struct {
				Delta struct {
					ToolCalls []struct {
						Function struct {
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal(bytes.TrimPrefix(event, []byte("data: ")), &payload) != nil {
			continue
		}
		for _, choice := range payload.Choices {
			for _, call := range choice.Delta.ToolCalls {
				fragments = append(fragments, call.Function.Arguments)
			}
		}
	}
	return fragments
}
