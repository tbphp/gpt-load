package responsealias

import (
	"bytes"
	"encoding/json"
)

const (
	reasoningField = "reasoning"
	contentField   = "reasoning_content"
)

// normalizeOpenAIReasoning copies whichever reasoning spelling holds a string
// across every choices[].message and choices[].delta object to the other one
// so clients parsing either spelling keep displaying thinking output. Only an
// absent, null, or empty destination is filled, so an object that already
// carries two different string spellings is left untouched. Payloads that are
// not OpenAI chat completions objects or fail to parse are returned
// byte-identical.
func normalizeOpenAIReasoning(payload []byte) []byte {
	if !bytes.Contains(payload, []byte(`"`+reasoningField+`"`)) &&
		!bytes.Contains(payload, []byte(`"`+contentField+`"`)) {
		return payload
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil || doc == nil {
		return payload
	}
	choices, _ := doc["choices"].([]any)
	if len(choices) == 0 {
		return payload
	}
	changed := false
	for _, item := range choices {
		choice, _ := item.(map[string]any)
		if choice == nil {
			continue
		}
		for _, key := range [...]string{"delta", "message"} {
			part, _ := choice[key].(map[string]any)
			if part == nil {
				continue
			}
			if duplicateReasoningField(part) {
				changed = true
			}
		}
	}
	if !changed {
		return payload
	}
	rewritten, err := json.Marshal(doc)
	if err != nil {
		return payload
	}
	return rewritten
}

// moveReasoningField renames source to target on one message-shaped object
// and reports whether it wrote. The rename only happens when source holds a
// string; the source spelling is dropped even when target keeps its own
// content, so only one spelling survives.
func moveReasoningField(object map[string]any, source, target string) bool {
	text, ok := object[source].(string)
	if !ok {
		return false
	}
	writeReasoningTarget(object, target, text)
	delete(object, source)
	return true
}

// writeReasoningTarget writes text to target and reports whether it wrote.
// An absent, null, or empty-string target receives the value; a target
// holding any other value keeps its own content and nothing is written.
func writeReasoningTarget(object map[string]any, target, text string) bool {
	switch current := object[target].(type) {
	case nil:
	case string:
		if current != "" {
			return false
		}
	default:
		return false
	}
	object[target] = text
	return true
}

// duplicateReasoningField copies whichever reasoning spelling holds a
// non-empty string to the other one and reports whether it wrote, so both
// spellings survive and readers of either spelling work. Some upstreams emit
// an empty-string or null reasoning as a placeholder while carrying the text
// in reasoning_content, so such spellings count as absent: the empty one is
// filled from the other spelling instead of being selected as the source and
// blocking the copy.
func duplicateReasoningField(object map[string]any) bool {
	if text, ok := object[reasoningField].(string); ok && text != "" {
		return writeReasoningTarget(object, contentField, text)
	}
	text, ok := object[contentField].(string)
	if !ok || text == "" {
		return false
	}
	return writeReasoningTarget(object, reasoningField, text)
}
