package embedded

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
)

func randInt63() int64 {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0
	}
	return int64(binary.BigEndian.Uint64(value[:]) & ^(uint64(1) << 63))
}

// kiroOrigin constants.
const (
	kiroOriginCLI           = "KIRO_CLI"
	kiroChatTriggerManual   = "MANUAL"
	kiroAgentTaskTypeVibe   = "vibe"
	kiroToolResultSuccess   = "success"
	kiroToolResultError     = "error"
	kiroCachePointType      = "ephemeral"
	kiroThinkingToolName    = "thinking"
	kiroRequestPathMessages = "/v1/messages"
)

// kiroAnthropicMessage mirrors the subset of the Anthropic Messages request we
// understand when the incoming format is "claude".
type kiroAnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Model   string          `json:"model,omitempty"`
}

// kiroAnthropicBlock is one Anthropic content block.
type kiroAnthropicBlock struct {
	Type             string           `json:"type"`
	Text             string           `json:"text,omitempty"`
	ID               string           `json:"id,omitempty"`
	Name             string           `json:"name,omitempty"`
	Input            json.RawMessage  `json:"input,omitempty"`
	Source           *kiroImageSource `json:"source,omitempty"`
	Thinking         string           `json:"thinking,omitempty"`
	Signature        string           `json:"signature,omitempty"`
	RedactedThinking string           `json:"redacted_thinking,omitempty"`
}

// kiroImageSource is an Anthropic image block source.
type kiroImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}

// kiroRequest is the parsed Anthropic Messages request.
type kiroRequest struct {
	Model       string                 `json:"model"`
	System      interface{}            `json:"system,omitempty"`
	Messages    []kiroAnthropicMessage `json:"messages"`
	MaxTokens   int                    `json:"max_tokens"`
	Stream      bool                   `json:"stream"`
	Tools       []json.RawMessage      `json:"tools,omitempty"`
	Thinking    *kiroThinkingConfig    `json:"thinking,omitempty"`
	Temperature *float64               `json:"temperature,omitempty"`
}

// kiroThinkingConfig is the Anthropic extended thinking config.
type kiroThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// kiroToolSpec is the Anthropic tool definition shape we forward to Kiro.
type kiroToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// kiroToolEntry is a Kiro tool entry (union: toolSpecification or cachePoint).
type kiroToolEntry struct {
	ToolSpecification *kiroToolSpecification `json:"toolSpecification,omitempty"`
	CachePoint        *kiroCachePoint        `json:"cachePoint,omitempty"`
}

type kiroToolSpecification struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema kiroInputSchema `json:"inputSchema"`
}

type kiroInputSchema struct {
	JSON map[string]any `json:"json"`
}

type kiroCachePoint struct {
	Type string `json:"type"`
}

// kiroImage is a Kiro inline image.
type kiroImage struct {
	Format string          `json:"format"`
	Source kiroImageSource `json:"source"`
}

// kiroToolResult is a Kiro tool result entry.
type kiroToolResult struct {
	ToolUseID string                  `json:"toolUseId"`
	Status    string                  `json:"status"`
	Content   []kiroToolResultContent `json:"content"`
}

type kiroToolResultContent struct {
	Text string         `json:"text,omitempty"`
	JSON map[string]any `json:"json,omitempty"`
}

// kiroUserInputMessageContext is the per-message tool/tool-result envelope.
type kiroUserInputMessageContext struct {
	Tools       []kiroToolEntry  `json:"tools,omitempty"`
	ToolResults []kiroToolResult `json:"toolResults,omitempty"`
}

// kiroUserInputMessage is the current user message.
type kiroUserInputMessage struct {
	Content string                       `json:"content"`
	ModelID string                       `json:"modelId,omitempty"`
	Origin  string                       `json:"origin,omitempty"`
	Context *kiroUserInputMessageContext `json:"userInputMessageContext,omitempty"`
	Images  []kiroImage                  `json:"images,omitempty"`
}

// kiroHistoryUser is a user message inside history.
type kiroHistoryUser struct {
	Content string                       `json:"content"`
	ModelID string                       `json:"modelId,omitempty"`
	Origin  string                       `json:"origin,omitempty"`
	Context *kiroUserInputMessageContext `json:"userInputMessageContext,omitempty"`
}

// kiroReasoningContent is a replayed reasoning blob.
type kiroReasoningContent struct {
	RedactedContent string `json:"redactedContent"`
}

// kiroHistoryToolUse is a tool call inside assistant history.
type kiroHistoryToolUse struct {
	ToolUseID string `json:"toolUseId"`
	Name      string `json:"name"`
	Input     any    `json:"input"`
}

// kiroHistoryAssistant is an assistant message inside history.
type kiroHistoryAssistant struct {
	MessageID        string                `json:"messageId,omitempty"`
	Content          string                `json:"content"`
	ToolUses         []kiroHistoryToolUse  `json:"toolUses,omitempty"`
	ReasoningContent *kiroReasoningContent `json:"reasoningContent,omitempty"`
}

// kiroHistoryEntry is a union history entry.
type kiroHistoryEntry struct {
	UserInputMessage         *kiroHistoryUser      `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *kiroHistoryAssistant `json:"assistantResponseMessage,omitempty"`
}

// kiroConversationState is the conversation envelope.
type kiroConversationState struct {
	ConversationID  string             `json:"conversationId,omitempty"`
	ChatTriggerType string             `json:"chatTriggerType"`
	AgentTaskType   string             `json:"agentTaskType"`
	CurrentMessage  kiroCurrentMessage `json:"currentMessage"`
	History         []kiroHistoryEntry `json:"history,omitempty"`
}

type kiroCurrentMessage struct {
	UserInputMessage kiroUserInputMessage `json:"userInputMessage"`
}

// kiroOutputConfig carries the Claude reasoning effort.
type kiroOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

// kiroAdditionalModelRequestFields is the sibling envelope for model options.
type kiroAdditionalModelRequestFields struct {
	OutputConfig *kiroOutputConfig `json:"output_config,omitempty"`
}

// kiroPayload is the top-level Kiro request body.
type kiroPayload struct {
	ConversationState            kiroConversationState             `json:"conversationState"`
	ProfileARN                   string                            `json:"profileArn,omitempty"`
	AdditionalModelRequestFields *kiroAdditionalModelRequestFields `json:"additionalModelRequestFields,omitempty"`
}

// parseKiroRequest parses an Anthropic Messages JSON request body.
func parseKiroRequest(raw []byte) (kiroRequest, error) {
	var request kiroRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return kiroRequest{}, fmt.Errorf("parse Kiro request: %w", err)
	}
	return request, nil
}

// normalizeKiroSystem flattens the Anthropic system (string or list) to a single string.
func normalizeKiroSystem(system interface{}) string {
	if system == nil {
		return ""
	}
	switch value := system.(type) {
	case string:
		return value
	case []interface{}:
		var parts []string
		for _, item := range value {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			text, ok := block["text"].(string)
			if ok && text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	default:
		return ""
	}
}

// extractKiroUserContent splits an Anthropic user message content (string or
// block array) into plain text + image blocks.
func extractKiroUserContent(content json.RawMessage) (text string, images []kiroImage) {
	var plain string
	if err := json.Unmarshal(content, &plain); err == nil {
		return plain, nil
	}
	var blocks []kiroAnthropicBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return "", nil
	}
	var parts []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "image":
			if block.Source != nil && block.Source.Data != "" {
				format := ""
				if block.Source.MediaType != "" {
					format = strings.TrimPrefix(block.Source.MediaType, "image/")
				}
				images = append(images, kiroImage{
					Format: format,
					Source: kiroImageSource{Type: "bytes", Data: block.Source.Data},
				})
			}
		}
	}
	return strings.Join(parts, "\n"), images
}

// kiroConversationFromRequest converts an Anthropic request into a Kiro payload.
// It returns the conversation state, the output effort (if any), and the flattened
// history list. All user tool results and assistant tool uses are folded into the
// history so the model sees the full multi-turn tool loop.
func kiroConversationFromRequest(request kiroRequest) (kiroConversationState, string) {
	state := kiroConversationState{
		ChatTriggerType: kiroChatTriggerManual,
		AgentTaskType:   kiroAgentTaskTypeVibe,
	}
	modelID := request.Model
	var conversationID string
	var history []kiroHistoryEntry
	var lastImages []kiroImage

	tools := buildKiroToolEntries(request.Tools)

	for _, message := range request.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		content, images := extractKiroUserContent(message.Content)
		switch role {
		case "user":
			hasToolResult := jsonToolResultInMessage(message.Content)
			entry := kiroHistoryEntry{
				UserInputMessage: &kiroHistoryUser{
					Content: content,
					ModelID: modelID,
					Origin:  kiroOriginCLI,
				},
			}
			if hasToolResult {
				results := extractKiroToolResults(message.Content)
				if len(results) > 0 {
					entry.UserInputMessage.Context = &kiroUserInputMessageContext{ToolResults: results}
				}
			} else if len(images) > 0 {
				// Images are only lifted onto the final current message; history
				// user entries carry text only. Capture them here so the last user
				// turn's images reach the currentMessage below.
				lastImages = images
			}
			history = append(history, entry)
		case "assistant":
			assistant := kiroHistoryAssistant{MessageID: newKiroMessageID()}
			parts, _ := parseKiroContentBlocks(message.Content)
			var toolUses []kiroHistoryToolUse
			for _, block := range parts {
				switch block.Type {
				case "text":
					if assistant.Content == "" {
						assistant.Content = block.Text
					}
				case "tool_use":
					toolUses = append(toolUses, kiroHistoryToolUse{
						ToolUseID: block.ID, Name: block.Name, Input: decodeKiroJSONValue(block.Input),
					})
				case "thinking", "redacted_thinking":
					if block.Signature != "" {
						assistant.ReasoningContent = &kiroReasoningContent{RedactedContent: block.RedactedThinking}
					}
				}
			}
			if len(toolUses) > 0 {
				assistant.ToolUses = toolUses
			}
			history = append(history, kiroHistoryEntry{AssistantResponseMessage: &assistant})
		}
	}

	// The final user message becomes the "current" user message. The Kiro
	// conversation envelope has no dedicated system field, so the flattened
	// Anthropic system prompt is carried as the leading user content so the
	// model still receives it as an instruction. Both the tool specifications
	// and any tool results on the final user turn are attached to the current
	// message's context so the active turn is self-contained.
	systemPrompt := normalizeKiroSystem(request.System)
	nextContent := ""
	var toolResults []kiroToolResult
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.UserInputMessage != nil {
			history = history[:len(history)-1]
			nextContent = last.UserInputMessage.Content
			if last.UserInputMessage.Context != nil {
				toolResults = last.UserInputMessage.Context.ToolResults
			}
		}
	}
	if systemPrompt != "" && nextContent != "" {
		nextContent = strings.TrimSpace(systemPrompt + "\n\n" + nextContent)
	} else if systemPrompt != "" {
		nextContent = systemPrompt
	}
	context := &kiroUserInputMessageContext{Tools: tools, ToolResults: toolResults}
	if len(toolResults) == 0 {
		context.ToolResults = nil
	}
	state.CurrentMessage.UserInputMessage = kiroUserInputMessage{
		Content: nextContent,
		ModelID: modelID,
		Origin:  kiroOriginCLI,
		Context: context,
		Images:  lastImages,
	}
	state.ConversationID = conversationID
	state.History = history

	effort := ""
	if request.Thinking != nil && strings.EqualFold(strings.TrimSpace(request.Thinking.Type), "enabled") {
		effort = "medium"
	}
	return state, effort
}

func newKiroMessageID() string {
	return randomKiroID()
}

func randomKiroID() string {
	return fmt.Sprintf("msg_%s", randomHex(8))
}

func randomHex(n int) string {
	const digits = "0123456789abcdef"
	bytes := make([]byte, n)
	for i := range bytes {
		bytes[i] = digits[int(randInt63())%len(digits)]
	}
	return string(bytes)
}

func jsonToolResultInMessage(content json.RawMessage) bool {
	var blocks []kiroAnthropicBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "tool_result" {
			return true
		}
	}
	return false
}

func extractKiroToolResults(content json.RawMessage) []kiroToolResult {
	var blocks []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	var results []kiroToolResult
	for _, block := range blocks {
		if block.Type != "tool_result" {
			continue
		}
		status := kiroToolResultSuccess
		if block.IsError {
			status = kiroToolResultError
		}
		result := kiroToolResult{ToolUseID: block.ToolUseID, Status: status}
		text, _ := extractKiroUserContent(block.Content)
		result.Content = append(result.Content, kiroToolResultContent{Text: text})
		results = append(results, result)
	}
	return results
}

func buildKiroToolEntries(tools []json.RawMessage) []kiroToolEntry {
	var entries []kiroToolEntry
	for _, rawTool := range tools {
		var tool kiroToolSpec
		if err := json.Unmarshal(rawTool, &tool); err != nil {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			schema = map[string]any{"type": "object"}
		}
		entries = append(entries, kiroToolEntry{
			ToolSpecification: &kiroToolSpecification{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: kiroInputSchema{JSON: schema},
			},
		})
	}
	return entries
}

func parseKiroContentBlocks(content json.RawMessage) ([]kiroAnthropicBlock, error) {
	var blocks []kiroAnthropicBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		// Fall back to a plain string.
		var text string
		if err2 := json.Unmarshal(content, &text); err2 == nil {
			return []kiroAnthropicBlock{{Type: "text", Text: text}}, nil
		}
		return nil, err
	}
	return blocks, nil
}

func decodeKiroJSONValue(raw json.RawMessage) any {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

// buildKiroPayload assembles the final Kiro request body and returns it.
func buildKiroPayload(request kiroRequest, profileARN string) ([]byte, error) {
	state, effort := kiroConversationFromRequest(request)
	payload := kiroPayload{
		ConversationState: state,
		ProfileARN:        profileARN,
	}
	if effort != "" {
		payload.AdditionalModelRequestFields = &kiroAdditionalModelRequestFields{
			OutputConfig: &kiroOutputConfig{Effort: effort},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Kiro payload: %w", err)
	}
	return body, nil
}

var _ = bytes.NewReader
