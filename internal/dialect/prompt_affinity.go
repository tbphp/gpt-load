package dialect

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/protocol"
)

const maxPromptAffinityRoleBytes = 4 << 10

type promptAffinityRoot struct {
	Messages               []promptAffinityMessage `json:"messages"`
	Instructions           json.RawMessage         `json:"instructions"`
	Input                  json.RawMessage         `json:"input"`
	System                 json.RawMessage         `json:"system"`
	SystemInstruction      json.RawMessage         `json:"systemInstruction"`
	SystemInstructionSnake json.RawMessage         `json:"system_instruction"`
	Contents               []promptAffinityContent `json:"contents"`
}

type promptAffinityMessage struct {
	Role    string          `json:"role"`
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Content json.RawMessage `json:"content"`
}

type promptAffinityContent struct {
	Role  string          `json:"role"`
	Parts json.RawMessage `json:"parts"`
}

type canonicalPromptAffinityPrefix struct {
	Version    int      `json:"v"`
	SystemRole string   `json:"system_role,omitempty"`
	System     []string `json:"system,omitempty"`
	User       []string `json:"user"`
}

func inspectPromptAffinityPrefix(clientProtocol protocol.Protocol, body []byte) []byte {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var root promptAffinityRoot
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}

	var systemRole string
	var system []string
	var user []string
	switch clientProtocol {
	case protocol.OpenAICompletions:
		systemRole, system, user = openAIMessageAffinityPrefix(root.Messages)
	case protocol.OpenAIResponses:
		systemRole, system, user = responsesAffinityPrefix(root)
	case protocol.Anthropic:
		systemRole = "system"
		system = promptTextParts(root.System)
		_, _, user = openAIMessageAffinityPrefix(root.Messages)
	case protocol.Gemini:
		systemRole = "system"
		systemInstruction := root.SystemInstruction
		if len(systemInstruction) == 0 {
			systemInstruction = root.SystemInstructionSnake
		}
		system = geminiContentText(systemInstruction)
		for _, content := range root.Contents {
			role := strings.ToLower(strings.TrimSpace(content.Role))
			if role != "" && role != "user" {
				continue
			}
			parts := promptTextParts(content.Parts)
			if len(parts) > 0 {
				user = parts
				break
			}
		}
	default:
		return nil
	}

	system = boundedPromptText(system, maxPromptAffinityRoleBytes)
	user = boundedPromptText(user, maxPromptAffinityRoleBytes)
	if len(user) == 0 {
		return nil
	}
	if len(system) == 0 {
		systemRole = ""
	}
	encoded, err := json.Marshal(canonicalPromptAffinityPrefix{
		Version: 1, SystemRole: systemRole, System: system, User: user,
	})
	if err != nil {
		return nil
	}
	return encoded
}

func openAIMessageAffinityPrefix(
	messages []promptAffinityMessage,
) (string, []string, []string) {
	var systemRole string
	var system []string
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		parts := promptTextParts(message.Content)
		if len(parts) == 0 {
			continue
		}
		switch role {
		case "system", "developer":
			if len(system) == 0 {
				systemRole = role
				system = parts
			}
		case "user":
			return systemRole, system, parts
		}
	}
	return systemRole, system, nil
}

func responsesAffinityPrefix(
	root promptAffinityRoot,
) (string, []string, []string) {
	systemRole := "instructions"
	system := promptTextParts(root.Instructions)
	trimmedInput := bytes.TrimSpace(root.Input)
	if len(trimmedInput) == 0 {
		return systemRole, system, nil
	}
	var direct string
	if json.Unmarshal(trimmedInput, &direct) == nil {
		if direct == "" {
			return systemRole, system, nil
		}
		return systemRole, system, []string{direct}
	}

	var items []json.RawMessage
	if err := json.Unmarshal(trimmedInput, &items); err != nil {
		return systemRole, system, nil
	}
	for _, raw := range items {
		var message promptAffinityMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(message.Role))
		parts := promptTextParts(message.Content)
		if len(parts) == 0 && strings.TrimSpace(message.Text) != "" {
			parts = []string{message.Text}
		}
		switch role {
		case "system", "developer":
			if len(system) == 0 && len(parts) > 0 {
				systemRole = role
				system = parts
			}
		case "user":
			if len(parts) > 0 {
				return systemRole, system, parts
			}
		case "":
			itemType := normalizeFeatureName(message.Type)
			if len(parts) > 0 && (itemType == "inputtext" || itemType == "text") {
				return systemRole, system, parts
			}
		}
	}
	return systemRole, system, nil
}

func geminiContentText(raw json.RawMessage) []string {
	var content promptAffinityContent
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil
	}
	return promptTextParts(content.Parts)
}

func promptTextParts(raw json.RawMessage) []string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var direct string
	if json.Unmarshal(trimmed, &direct) == nil {
		if strings.TrimSpace(direct) == "" {
			return nil
		}
		return []string{direct}
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return nil
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		var directBlock string
		if json.Unmarshal(block, &directBlock) == nil {
			if strings.TrimSpace(directBlock) != "" {
				parts = append(parts, directBlock)
			}
			continue
		}
		var object struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(block, &object); err == nil && strings.TrimSpace(object.Text) != "" {
			parts = append(parts, object.Text)
		}
	}
	return parts
}

func boundedPromptText(source []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	remaining := limit
	result := make([]string, 0, len(source))
	for _, value := range source {
		if strings.TrimSpace(value) == "" || remaining == 0 {
			continue
		}
		if len(value) > remaining {
			value = truncatePromptText(value, remaining)
		}
		if value == "" {
			continue
		}
		result = append(result, value)
		remaining -= len(value)
	}
	return result
}

func truncatePromptText(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	if limit <= 0 {
		return ""
	}
	value = value[:limit]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
