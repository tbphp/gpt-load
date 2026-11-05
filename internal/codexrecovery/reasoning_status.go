package codexrecovery

import "encoding/json"

// StripReasoningStatus removes only the output-only status on inline reasoning
// items. IDs, encrypted content, summaries, and other input items stay intact.
// Unlike Sanitize, this is a pre-dispatch schema adjustment, not error recovery.
func StripReasoningStatus(body []byte) ([]byte, bool) {
	object, ok := decodeJSONObject(body)
	if !ok {
		return nil, false
	}
	items, _ := responseInputItems(object["input"])
	changed := false
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok || record["type"] != "reasoning" {
			continue
		}
		if _, exists := record["status"]; exists {
			delete(record, "status")
			changed = true
		}
	}
	if !changed {
		return nil, false
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, false
	}
	return encoded, true
}
