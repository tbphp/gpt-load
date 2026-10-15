package codexrecovery

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// Removals counts the encrypted-content items stripped from a request body.
// The counts feed audit metadata; they are never sent upstream.
type Removals struct {
	ReasoningEncryptedContent      int
	FunctionOutputEncryptedContent int
	AgentMessageEncryptedContent   int
	CompactionEncryptedContent     int
	ReasoningItem                  int
	AgentMessageItem               int
	CompactionItem                 int
}

// Empty reports whether nothing was removed.
func (r Removals) Empty() bool {
	return r.ReasoningEncryptedContent == 0 &&
		r.FunctionOutputEncryptedContent == 0 &&
		r.AgentMessageEncryptedContent == 0 &&
		r.CompactionEncryptedContent == 0
}

// Total returns the number of encrypted-content entries removed across all
// categories.
func (r Removals) Total() int {
	return r.ReasoningEncryptedContent +
		r.FunctionOutputEncryptedContent +
		r.AgentMessageEncryptedContent +
		r.CompactionEncryptedContent
}

// Sanitize removes the account-bound encrypted state an upstream refused to
// validate: reasoning, tool output, agent message, and compaction
// `encrypted_content` entries. It returns the rewritten body and the removal
// counts. The boolean result is false when the body is not a JSON object or
// carries no removable encrypted content, in which case the caller keeps the
// original body.
//
// The rewrite is intentionally structural: only the encrypted opaque fields
// are dropped, and items that become empty are removed. No other field of the
// client request is touched.
func Sanitize(body []byte) ([]byte, Removals, bool) {
	object, ok := decodeJSONObject(body)
	if !ok {
		return nil, Removals{}, false
	}
	inputItems, originalIsArray := responseInputItems(object["input"])
	if inputItems == nil {
		return nil, Removals{}, false
	}

	var removals Removals
	kept := make([]any, 0, len(inputItems))
	for _, item := range inputItems {
		record, isObject := item.(map[string]any)
		if !isObject {
			kept = append(kept, item)
			continue
		}

		itemType, _ := record["type"].(string)
		if itemType == "reasoning" {
			if _, isString := record["encrypted_content"].(string); isString {
				copy := cloneJSONMap(record)
				delete(copy, "encrypted_content")
				removals.ReasoningEncryptedContent++
				if isEmptyReasoningItem(copy) {
					removals.ReasoningItem++
					continue
				}
				kept = append(kept, copy)
				continue
			}
		}

		if isCompactionItemWithEncryptedContent(record) {
			removals.CompactionEncryptedContent++
			removals.CompactionItem++
			continue
		}

		if itemType == "function_call_output" || itemType == "custom_tool_call_output" {
			stripped := stripEncryptedContentItems(record["output"])
			if stripped.changed {
				replacement := cloneJSONMap(record)
				replacement["output"] = stripped.output
				removals.FunctionOutputEncryptedContent += stripped.removedCount
				kept = append(kept, replacement)
				continue
			}
		}

		if itemType == "agent_message" {
			stripped := stripEncryptedContentItems(record["content"])
			if stripped.changed {
				removals.AgentMessageEncryptedContent += stripped.removedCount
				if contentArray, isArray := stripped.output.([]any); isArray && len(contentArray) == 0 {
					removals.AgentMessageItem++
					continue
				}
				replacement := cloneJSONMap(record)
				replacement["content"] = stripped.output
				kept = append(kept, replacement)
				continue
			}
		}
		kept = append(kept, item)
	}

	if removals.Empty() {
		return nil, Removals{}, false
	}
	if originalIsArray {
		object["input"] = kept
	} else if len(kept) > 0 {
		object["input"] = kept[0]
	} else {
		object["input"] = []any{}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, Removals{}, false
	}
	return encoded, removals, true
}

// responseInputItems normalizes the Responses `input` field to a slice. It
// mirrors `Array.isArray(body.input) ? body.input : [body.input]`, and reports
// whether the caller supplied an array so the shape can be restored.
func responseInputItems(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case map[string]any:
		return []any{typed}, false
	default:
		return nil, false
	}
}

func isCompactionItemWithEncryptedContent(item map[string]any) bool {
	itemType, _ := item["type"].(string)
	if itemType != "compaction" && itemType != "compaction_summary" && itemType != "context_compaction" {
		return false
	}
	_, encryptedIsString := item["encrypted_content"].(string)
	return encryptedIsString
}

type strippedItems struct {
	output       any
	changed      bool
	removedCount int
}

func stripEncryptedContentItems(value any) strippedItems {
	switch typed := value.(type) {
	case []any:
		removedCount := 0
		output := make([]any, 0, len(typed))
		for _, item := range typed {
			if isEncryptedContentItem(item) {
				removedCount++
				continue
			}
			output = append(output, item)
		}
		return strippedItems{output: output, changed: removedCount > 0, removedCount: removedCount}
	case map[string]any:
		if isEncryptedContentItem(typed) {
			return strippedItems{output: []any{}, changed: true, removedCount: 1}
		}
	}
	return strippedItems{output: value, changed: false}
}

// isEncryptedContentItem reports an object with type `encrypted_content` and a
// string `encrypted_content` value.
func isEncryptedContentItem(value any) bool {
	record, isObject := value.(map[string]any)
	if !isObject {
		return false
	}
	itemType, _ := record["type"].(string)
	_, encryptedIsString := record["encrypted_content"].(string)
	return itemType == "encrypted_content" && encryptedIsString
}

// isEmptyReasoningItem reports a reasoning item that carries nothing but
// framing fields once its encrypted content is removed.
func isEmptyReasoningItem(item map[string]any) bool {
	for key, value := range item {
		switch key {
		case "type", "id", "status":
			continue
		}
		switch typed := value.(type) {
		case nil:
			continue
		case []any:
			if len(typed) == 0 {
				continue
			}
		case string:
			if strings.TrimSpace(typed) == "" {
				continue
			}
		}
		return false
	}
	return true
}

func cloneJSONMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func decodeJSONObject(body []byte) (map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	object, ok := parsed.(map[string]any)
	return object, ok
}
