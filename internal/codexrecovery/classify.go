package codexrecovery

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode"
)

// Signal identifies an upstream rejection of Responses continuation state.
// Empty means the error is unrelated.
type Signal string

const (
	SignalThinkingSignatureInvalid         Signal = "thinking_signature_invalid"
	SignalInvalidEncryptedContent          Signal = "invalid_encrypted_content"
	SignalEncryptedContentDecryptionFailed Signal = "encrypted_content_decryption_failed"
	// SignalEncryptedContextInvalid covers sessions carrying encrypted context
	// from a previous turn that a non-authoring upstream refuses to validate.
	SignalEncryptedContextInvalid Signal = "encrypted_context_invalid"
	SignalItemResourceMismatch    Signal = "response_item_resource_mismatch"
	SignalItemNotFound            Signal = "response_item_not_found"
)

// InvalidEncryptedContentSignal reports whether the rejection is specifically
// about opaque encrypted content and can therefore be remembered by digest.
func InvalidEncryptedContentSignal(signal Signal) bool {
	switch signal {
	case SignalThinkingSignatureInvalid,
		SignalInvalidEncryptedContent,
		SignalEncryptedContentDecryptionFailed,
		SignalEncryptedContextInvalid:
		return true
	default:
		return false
	}
}

// InvalidItemIDSignal reports whether the rejection is about a history item's
// upstream identity rather than its opaque encrypted content.
func InvalidItemIDSignal(signal Signal) bool {
	switch signal {
	case SignalItemResourceMismatch, SignalItemNotFound:
		return true
	default:
		return false
	}
}

// Classify maps an upstream error body (or error text) to a recovery signal.
// It accepts a bare error code, a JSON error payload, or an SSE-framed payload;
// anything else yields the empty signal.
func Classify(upstreamErrorText string) Signal {
	if exact := signalForExactErrorCode(upstreamErrorText); exact != "" {
		return exact
	}
	payloads := structuredErrorPayloads(upstreamErrorText)
	for _, payload := range payloads {
		if signal := signalForStructuredErrorPayload(payload); signal != "" {
			return signal
		}
	}
	// Only bare messages use this fallback. Text inside a successful response
	// or another non-error JSON object is not evidence of a rejection.
	if len(payloads) == 0 {
		return signalForErrorMessage(upstreamErrorText)
	}
	return ""
}

// ClassifyParts maps an already-decoded error code / message pair to a signal.
// The streaming failure surface splits the payload before classification.
func ClassifyParts(errorCode, errorMessage string) Signal {
	if exact := signalForExactErrorCode(errorCode); exact != "" {
		return exact
	}
	return signalForErrorMessage(errorMessage)
}

func signalForExactErrorCode(value string) Signal {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(SignalThinkingSignatureInvalid):
		return SignalThinkingSignatureInvalid
	case string(SignalInvalidEncryptedContent):
		return SignalInvalidEncryptedContent
	case string(SignalEncryptedContentDecryptionFailed):
		return SignalEncryptedContentDecryptionFailed
	case string(SignalEncryptedContextInvalid):
		return SignalEncryptedContextInvalid
	case string(SignalItemResourceMismatch):
		return SignalItemResourceMismatch
	case string(SignalItemNotFound), "item_not_found":
		return SignalItemNotFound
	default:
		return ""
	}
}

func structuredErrorPayloads(value string) []map[string]any {
	var payloads []map[string]any
	if direct := parseJSONRecord(value); direct != nil {
		payloads = append(payloads, direct)
	}

	var eventDataLines []string
	appendEventPayload := func() {
		if len(eventDataLines) == 0 {
			return
		}
		if payload := parseJSONRecord(strings.Join(eventDataLines, "\n")); payload != nil {
			payloads = append(payloads, payload)
		}
		eventDataLines = nil
	}
	for _, line := range splitLines(value) {
		if len(line) == 0 {
			appendEventPayload()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			eventDataLines = append(eventDataLines, strings.TrimLeftFunc(line[len("data:"):], unicode.IsSpace))
		}
	}
	appendEventPayload()
	return payloads
}

func splitLines(value string) []string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

func signalForStructuredErrorPayload(payload map[string]any) Signal {
	nestedError, hasNestedError := payload["error"].(map[string]any)
	// Responses failures carry the error under response.error, e.g. the data
	// line of a streaming response.failed event.
	responsesError, hasResponsesError := func() (map[string]any, bool) {
		response, ok := payload["response"].(map[string]any)
		if !ok {
			return nil, false
		}
		errorObject, ok := response["error"].(map[string]any)
		return errorObject, ok
	}()
	candidates := []map[string]any{payload}
	if hasNestedError {
		candidates = append(candidates, nestedError)
	}
	if hasResponsesError {
		candidates = append(candidates, responsesError)
	}
	for candidateIndex, candidate := range candidates {
		if code, isString := candidate["code"].(string); isString {
			if signal := signalForExactErrorCode(code); signal != "" {
				return signal
			}
		}
		errorPayload := candidateIndex >= 1 ||
			func() bool {
				typeField, isString := payload["type"].(string)
				return isString && typeField == "error"
			}() ||
			hasNestedError
		if errorPayload {
			if message, isString := candidate["message"].(string); isString {
				if signal := signalForErrorMessage(message); signal != "" {
					return signal
				}
			}
		}
	}
	return ""
}

func signalForErrorMessage(value string) Signal {
	normalized := strings.ToLower(value)
	if strings.Contains(normalized, "requested item was created under a different") &&
		strings.Contains(normalized, "openai resource") {
		return SignalItemResourceMismatch
	}
	if strings.Contains(normalized, "no item found with id") ||
		(strings.Contains(normalized, "item with id") && strings.Contains(normalized, "not found")) ||
		(strings.Contains(normalized, "items are not persisted") && strings.Contains(normalized, "store")) {
		return SignalItemNotFound
	}
	if looksLikeEncryptedContentDecryptionFailure(value) {
		return SignalEncryptedContentDecryptionFailed
	}
	return ""
}

func looksLikeEncryptedContentDecryptionFailure(value string) bool {
	normalized := strings.ToLower(value)
	contains := func(needle string) bool { return strings.Contains(normalized, needle) }
	return contains("encrypted") &&
		(contains("could not be decrypted") ||
			contains("could not be decoded") ||
			contains("could not be verified") ||
			contains("could not be parsed") ||
			contains("could not validate"))
}

func parseJSONRecord(value string) map[string]any {
	var parsed any
	decoder := json.NewDecoder(bytes.NewReader([]byte(value)))
	if err := decoder.Decode(&parsed); err != nil {
		return nil
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil
	}
	record, ok := parsed.(map[string]any)
	if !ok {
		return nil
	}
	return record
}
