package bifrost

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
)

func TestCompatibleAssistantToolTurnsKeepContentAndBoundaries(t *testing.T) {
	const user = `{"role":"user","content":"hi"}`
	const full = `{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"Full."}],"summary":[{"type":"summary_text","text":"Short."}]}`
	const summary = `{"type":"reasoning","summary":[{"type":"summary_text","text":"Short."}]}`
	const text = `{"type":"message","role":"assistant","id":"msg_1","content":[{"type":"output_text","text":"Checking."}]}`
	const call = `{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{}"}`
	const output = `{"type":"function_call_output","call_id":"call_1","output":"done"}`
	const chatUser = `{"role":"user","content":"hi"}`
	const chatCall = `{"index":0,"type":"function","id":"call_1","function":{"name":"weather","arguments":"{}"}}`
	const chatOutput = `{"role":"tool","tool_call_id":"call_1","content":"done"}`
	for _, tc := range []struct {
		name, input, want string
	}{
		{
			"full text wins over summary",
			user + `,` + full + `,` + text + `,` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full.","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"summary only",
			user + `,` + summary + `,` + text + `,` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Short.","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"full reasoning across fragments",
			user + `,` + full + `,` + text + `,{"type":"reasoning","content":[{"type":"reasoning_text","text":"More."}]},` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full.\nMore.","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"later encrypted reasoning blocks summary fallback",
			user + `,` + summary + `,` + text + `,{"type":"reasoning","encrypted_content":"opaque"},` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"no reasoning is invented",
			user + `,` + text + `,` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"tool call before narration",
			user + `,` + full + `,` + call + `,` + text + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full.","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"interleaved parallel calls preserve order",
			user + `,` + full + `,` + call + `,` + text + `,{"type":"function_call","call_id":"call_2","name":"lookup","arguments":"{\"value\":1}"},` + output + `,{"type":"function_call_output","call_id":"call_2","output":"second"}`,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full.","tool_calls":[` + chatCall + `,{"index":0,"type":"function","id":"call_2","function":{"name":"lookup","arguments":"{\"value\":1}"}}]},` + chatOutput + `,{"role":"tool","tool_call_id":"call_2","content":"second"}`,
		},
		{
			"user boundary",
			user + `,` + full + `,` + text + `,{"role":"user","content":"next"},` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full."},{"role":"user","content":"next"},{"role":"assistant","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"system boundary",
			user + `,` + full + `,` + text + `,{"role":"system","content":"next"},` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full."},{"role":"system","content":"next"},{"role":"assistant","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"developer boundary",
			user + `,` + full + `,` + text + `,{"role":"developer","content":"next"},` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full."},{"role":"system","content":"next"},{"role":"assistant","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"tool result separates rounds with identical reasoning",
			user + `,` + full + `,` + text + `,` + call + `,` + output + `,` + full + `,` + text + `,` + call + `,` + output,
			chatUser + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full.","tool_calls":[` + chatCall + `]},` + chatOutput + `,{"role":"assistant","content":"Checking.","reasoning_content":"Full.","tool_calls":[` + chatCall + `]},` + chatOutput,
		},
		{
			"text only history keeps its message boundaries",
			user + `,` + text + `,` + text,
			chatUser + `,{"role":"assistant","content":"Checking."},{"role":"assistant","content":"Checking."}`,
		},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{"model":"client-model","stream":%t,"input":[%s]}`, stream, tc.input))
				wire := runCompatibleResponses(t, body, stream)
				var got, want []map[string]any
				if err := json.Unmarshal([]byte(gjson.GetBytes(wire, "messages").Raw), &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(`[`+tc.want+`]`), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("upstream messages = %s, want [%s]", gjson.GetBytes(wire, "messages").Raw, tc.want)
				}
			})
		}
	}
}

func TestCompatibleAssistantToolTurnPreservesBlocksAndReasoningDetails(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		wantReason  *string
	}{
		{"signed full text", `{"type":"reasoning","content":[{"type":"reasoning_text","text":"Full.","signature":"sig"}],"summary":[{"type":"summary_text","text":"Short."}]}`, schemas.Ptr("Full.")},
		{"encrypted summary", `{"type":"reasoning","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"Short."}]}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const text = "quote\"\npath\\file <tag>& 中文"
			var input []schemas.ResponsesMessage
			textJSON, err := json.Marshal(text)
			if err != nil {
				t.Fatal(err)
			}
			body := `[` + tc.input + `,{"role":"assistant","content":` + string(textJSON) + `},{"role":"assistant","content":[{"type":"output_text","text":"Again."},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]},{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{}"}]`
			if err := json.Unmarshal([]byte(body), &input); err != nil {
				t.Fatal(err)
			}
			request := &schemas.BifrostResponsesRequest{Model: "compatible-model", Input: input}
			before, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			sdkMessages := request.ToChatRequest().Input
			ctx := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
			defer ctx.Cancel()
			chat, _, failure := prepareCompatibleChatRequest(ctx, request)
			if failure != nil {
				t.Fatal(failure)
			}
			if len(chat.Input) != 1 {
				t.Fatalf("assistant turn has %d messages, want 1", len(chat.Input))
			}
			message := chat.Input[0]
			if message.Content == nil || message.Content.ContentStr != nil || len(message.Content.ContentBlocks) != 3 ||
				message.Content.ContentBlocks[0].Text == nil || *message.Content.ContentBlocks[0].Text != text ||
				message.Content.ContentBlocks[1].Text == nil || *message.Content.ContentBlocks[1].Text != "Again." ||
				message.Content.ContentBlocks[2].ImageURLStruct == nil || message.Content.ContentBlocks[2].ImageURLStruct.URL != "data:image/png;base64,AA==" {
				t.Fatalf("assistant content changed: %+v", message.Content)
			}
			if message.ChatAssistantMessage == nil || !reflect.DeepEqual(message.Reasoning, tc.wantReason) ||
				!reflect.DeepEqual(message.ReasoningDetails, sdkMessages[0].ReasoningDetails) || len(message.ToolCalls) != 1 {
				t.Fatalf("reasoning details or tool calls changed: %+v", message.ChatAssistantMessage)
			}
			after, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("preparation mutated the original request")
			}
		})
	}
}
