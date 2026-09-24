package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gpt-load/internal/protocol"
)

func restoreTestMarker(value string) (string, error) {
	return strings.ReplaceAll(value, "gld1_A", "alice@example.invalid"), nil
}

func TestRestoreUnaryBusinessFieldsOnlyChangesChatBusinessValues(t *testing.T) {
	input := []byte(`{"id":"gld1_A","choices":[{"message":{"content":"say gld1_A","tool_calls":[{"id":"gld1_A","function":{"name":"gld1_A","arguments":"{\"email\":\"gld1_A\", \"n\":7,\"literal\":\"\\u003c\"}"}}],"function_call":{"name":"gld1_A","arguments":"{\"password\":\"gld1_A\"}"}}}],"usage":{"label":"gld1_A"}}`)
	want := []byte(`{"id":"gld1_A","choices":[{"message":{"content":"say alice@example.invalid","tool_calls":[{"id":"gld1_A","function":{"name":"gld1_A","arguments":"{\"email\":\"alice@example.invalid\", \"n\":7,\"literal\":\"\\u003c\"}"}}],"function_call":{"name":"gld1_A","arguments":"{\"password\":\"alice@example.invalid\"}"}}}],"usage":{"label":"gld1_A"}}`)
	got, err := restoreUnaryBusinessFields(input, protocol.OpenAICompletions, restoreTestMarker, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("restored response differs\ngot:  %s\nwant: %s", got, want)
	}
	if !json.Valid(got) {
		t.Fatal("outer JSON became invalid")
	}
}

func TestRestoreUnaryBusinessFieldsResponsesEnvelopeAndTypes(t *testing.T) {
	input := []byte(`{"type":"response.completed","response":{"id":"gld1_A","output":[{"type":"message","content":[{"type":"output_text","text":"gld1_A"},{"type":"refusal","text":"gld1_A"}]},{"type":"function_call","call_id":"gld1_A","name":"gld1_A","arguments":"{\"x\":\"gld1_A\",\"count\":2}"}],"output_text":"gld1_A"}}`)
	want := []byte(`{"type":"response.completed","response":{"id":"gld1_A","output":[{"type":"message","content":[{"type":"output_text","text":"alice@example.invalid"},{"type":"refusal","text":"gld1_A"}]},{"type":"function_call","call_id":"gld1_A","name":"gld1_A","arguments":"{\"x\":\"alice@example.invalid\",\"count\":2}"}],"output_text":"alice@example.invalid"}}`)
	got, err := restoreUnaryBusinessFields(input, protocol.OpenAIResponses, restoreTestMarker, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("restored response differs\ngot:  %s\nwant: %s", got, want)
	}
}

func TestRestoreUnaryBusinessFieldsResponsesInputItems(t *testing.T) {
	input := []byte(`{"object":"list","data":[{"id":"gld1_A","type":"message","content":[{"type":"input_text","text":"gld1_A"},{"type":"input_image","image_url":"gld1_A"}]},{"type":"function_call_output","call_id":"gld1_A","output":"{\"password\":\"gld1_A\",\"n\":9007199254740993}"},{"type":"function_call","name":"gld1_A","arguments":"{\"account\":\"gld1_A\"}"}],"has_more":false}`)
	want := []byte(`{"object":"list","data":[{"id":"gld1_A","type":"message","content":[{"type":"input_text","text":"alice@example.invalid"},{"type":"input_image","image_url":"gld1_A"}]},{"type":"function_call_output","call_id":"gld1_A","output":"{\"password\":\"alice@example.invalid\",\"n\":9007199254740993}"},{"type":"function_call","name":"gld1_A","arguments":"{\"account\":\"alice@example.invalid\"}"}],"has_more":false}`)
	got, err := restoreUnaryBusinessFields(input, protocol.OpenAIResponses, restoreTestMarker, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("input_items restoration differs\ngot:  %s\nwant: %s", got, want)
	}
}

func TestRestoreUnaryBusinessFieldsAnthropicAndGemini(t *testing.T) {
	cases := []struct {
		name     string
		protocol protocol.Protocol
		input    string
		want     string
	}{
		{
			name:     "Anthropic",
			protocol: protocol.Anthropic,
			input:    `{"id":"gld1_A","content":[{"type":"text","text":"gld1_A"},{"type":"thinking","thinking":"gld1_A","signature":"signed"},{"type":"tool_use","id":"gld1_A","name":"gld1_A","input":{"gld1_A":"gld1_A","count":1,"nested":["gld1_A",false]}}]}`,
			want:     `{"id":"gld1_A","content":[{"type":"text","text":"alice@example.invalid"},{"type":"thinking","thinking":"gld1_A","signature":"signed"},{"type":"tool_use","id":"gld1_A","name":"gld1_A","input":{"gld1_A":"alice@example.invalid","count":1,"nested":["alice@example.invalid",false]}}]}`,
		},
		{
			name:     "Gemini",
			protocol: protocol.Gemini,
			input:    `{"candidates":[{"content":{"parts":[{"text":"gld1_A"},{"functionCall":{"name":"gld1_A","args":{"code":"gld1_A","count":2}}},{"inlineData":{"data":"gld1_A"}},{"text":"gld1_A","thought":true}]}}],"modelVersion":"gld1_A"}`,
			want:     `{"candidates":[{"content":{"parts":[{"text":"alice@example.invalid"},{"functionCall":{"name":"gld1_A","args":{"code":"alice@example.invalid","count":2}}},{"inlineData":{"data":"gld1_A"}},{"text":"gld1_A","thought":true}]}}],"modelVersion":"gld1_A"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := restoreUnaryBusinessFields([]byte(tc.input), tc.protocol, restoreTestMarker, false)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("restored response differs\ngot:  %s\nwant: %s", got, tc.want)
			}
		})
	}
}

func TestRestoreUnaryBusinessFieldsStructuredTextPreservesInnerJSON(t *testing.T) {
	input := []byte(`{"choices":[{"message":{"content":"{\"escaped\":\"gld1_A\",\"keep\":\"\\u003c\", \"n\":1}"}}]}`)
	want := []byte(`{"choices":[{"message":{"content":"{\"escaped\":\"a\\\"b\\n\",\"keep\":\"\\u003c\", \"n\":1}"}}]}`)
	restore := func(value string) (string, error) {
		return strings.ReplaceAll(value, "gld1_A", "a\"b\n"), nil
	}
	got, err := restoreUnaryBusinessFields(input, protocol.OpenAICompletions, restore, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("structured JSON differs\ngot:  %s\nwant: %s", got, want)
	}
	var outer struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(got, &outer); err != nil {
		t.Fatal(err)
	}
	var inner map[string]any
	if err := json.Unmarshal([]byte(outer.Choices[0].Message.Content), &inner); err != nil {
		t.Fatalf("inner JSON became invalid: %v", err)
	}
	if inner["escaped"] != "a\"b\n" || inner["n"] != float64(1) {
		t.Fatalf("inner JSON values changed unexpectedly: %+v", inner)
	}
}

func TestRestoreUnaryBusinessFieldsNoChangeReturnsOriginalBytesAndIgnoresWrongProtocol(t *testing.T) {
	input := []byte(` { "choices" : [ { "message" : { "content" : "ordinary" } } ] } `)
	got, err := restoreUnaryBusinessFields(input, protocol.OpenAICompletions, restoreTestMarker, false)
	if err != nil || &got[0] != &input[0] {
		t.Fatalf("unchanged response was copied: %v", err)
	}
	marker := []byte(`{"choices":[{"message":{"content":"gld1_A"}}]}`)
	got, err = restoreUnaryBusinessFields(marker, protocol.Gemini, restoreTestMarker, false)
	if err != nil || &got[0] != &marker[0] {
		t.Fatalf("wrong protocol response was modified: %v", err)
	}
}

func TestRestoreUnaryBusinessFieldsSkipsOrdinaryBodiesWithoutParsing(t *testing.T) {
	called := false
	restore := func(value string) (string, error) {
		called = true
		return value, nil
	}
	large := append([]byte(`{"choices":[{"message":{"content":"`), bytes.Repeat([]byte("ordinary text "), 100000)...)
	large = append(large, []byte(`"}}]}`)...)
	for _, body := range [][]byte{large, []byte("opaque non-JSON response")} {
		got, err := restoreUnaryBusinessFields(body, protocol.OpenAICompletions, restore, false)
		if err != nil || &got[0] != &body[0] || called {
			t.Fatalf("ordinary response was parsed or copied: err=%v called=%v", err, called)
		}
	}
}

func TestRestoreUnaryBusinessFieldsFailsOnInvalidBusinessToken(t *testing.T) {
	badRestore := func(value string) (string, error) {
		if strings.Contains(value, "gld1_BAD") {
			return "", errors.New("bad token")
		}
		return value, nil
	}
	_, err := restoreUnaryBusinessFields([]byte(`{"choices":[{"message":{"content":"gld1_BAD"}}]}`), protocol.OpenAICompletions, badRestore, false)
	if err == nil {
		t.Fatal("invalid business token was accepted")
	}
	got, err := restoreUnaryBusinessFields([]byte(`{"id":"gld1_BAD","choices":[]}`), protocol.OpenAICompletions, badRestore, false)
	if err != nil || len(got) == 0 {
		t.Fatalf("metadata token should not be restored: %v", err)
	}
}

func TestRestoreUnaryBusinessFieldsFindsEscapedMarker(t *testing.T) {
	input := []byte(`{"choices":[{"message":{"content":"\u0067ld1_A"}}]}`)
	want := []byte(`{"choices":[{"message":{"content":"alice@example.invalid"}}]}`)
	got, err := restoreUnaryBusinessFields(input, protocol.OpenAICompletions, restoreTestMarker, false)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("escaped marker was missed: %v, %s", err, got)
	}
}

func TestRestoreUnaryBusinessFieldsFindsEscapedMarkerInArguments(t *testing.T) {
	input := []byte(`{"choices":[{"message":{"tool_calls":[{"function":{"arguments":"{\"email\":\"\\u0067ld1_A\",\"keep\":\"\\u003c\"}"}}]}}]}`)
	want := []byte(`{"choices":[{"message":{"tool_calls":[{"function":{"arguments":"{\"email\":\"alice@example.invalid\",\"keep\":\"\\u003c\"}"}}]}}]}`)
	got, err := restoreUnaryBusinessFields(input, protocol.OpenAICompletions, restoreTestMarker, false)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("embedded escaped marker was missed: %v, %s", err, got)
	}
}

func TestRestoreUnaryBusinessFieldsRejectsSignedGeminiPartRewrite(t *testing.T) {
	input := []byte(`{"candidates":[{"content":{"parts":[{"text":"gld1_A","thoughtSignature":"signed"}]}}]}`)
	if _, err := restoreUnaryBusinessFields(input, protocol.Gemini, restoreTestMarker, false); err == nil {
		t.Fatal("signed part was rewritten")
	}
}

func TestRestoreUnaryBusinessFieldsBoundsEmbeddedValues(t *testing.T) {
	deep := `{"content":[{"type":"tool_use","input":` +
		strings.Repeat("[", maxUnaryRestoreDepth+1) + `"gld1_A"` +
		strings.Repeat("]", maxUnaryRestoreDepth+1) + `}]}`
	if _, err := restoreUnaryBusinessFields([]byte(deep), protocol.Anthropic, restoreTestMarker, false); err == nil {
		t.Fatal("excessive nested JSON depth was accepted")
	}
	many := `{"content":[{"type":"tool_use","input":[` +
		strings.Repeat(`"gld1_A",`, maxUnaryRestorePatches) + `"gld1_A"]}]}`
	if _, err := restoreUnaryBusinessFields([]byte(many), protocol.Anthropic, restoreTestMarker, false); err == nil {
		t.Fatal("excessive patch count was accepted")
	}
}
