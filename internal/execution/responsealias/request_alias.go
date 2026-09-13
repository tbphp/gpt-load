package responsealias

import (
	"bytes"
	"encoding/json"
)

// RewriteRequestMessages renames the reasoning spelling selected by mode
// across every object in the messages array of one chat completions request
// body, so the upstream that the admin configured can replay the client's
// retained thinking. After a rename only the target spelling remains; a
// non-empty existing target keeps its value and the source spelling is
// dropped. Only the rename modes apply: off and the response-only duplicate
// leave the body untouched. Bodies that are not chat completions objects or
// fail to parse are returned byte-identical.
func RewriteRequestMessages(body []byte, mode ReasoningMode) []byte {
	var source, target string
	switch mode {
	case ReasoningModeReasoningToContent:
		source, target = reasoningField, contentField
	case ReasoningModeContentToReasoning:
		source, target = contentField, reasoningField
	default:
		return body
	}
	if !bytes.Contains(body, []byte(`"`+source+`"`)) {
		return body
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil || doc == nil {
		return body
	}
	messages, _ := doc["messages"].([]any)
	if len(messages) == 0 {
		return body
	}
	changed := false
	for _, item := range messages {
		message, _ := item.(map[string]any)
		if message == nil {
			continue
		}
		if moveReasoningField(message, source, target) {
			changed = true
		}
	}
	if !changed {
		return body
	}
	rewritten, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return rewritten
}
