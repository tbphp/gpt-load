package bifrost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gpt-load/internal/channel/spec"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/responsealias"
	"gpt-load/internal/protocol"
)

const maxNativeAliasSSEEventBytes = execution.DefaultSSEEventLimitBytes

const maxNativeFirstSSEEventBytes = maxNativeAliasSSEEventBytes

type nativeFirstSSEEventGate struct {
	pending       []byte
	scanStart     int
	ready         bool
	maxEventBytes int
}

func newNativeFirstSSEEventGate(spec execution.AttemptSpec) *nativeFirstSSEEventGate {
	return &nativeFirstSSEEventGate{
		maxEventBytes: execution.SSEEventLimit(spec.ClientProtocol),
	}
}

func (g *nativeFirstSSEEventGate) eventLimit() int {
	if g != nil && g.maxEventBytes > 0 {
		return g.maxEventBytes
	}
	return maxNativeFirstSSEEventBytes
}

func (g *nativeFirstSSEEventGate) push(chunk []byte) ([]byte, error) {
	if g == nil || g.ready {
		return append([]byte(nil), chunk...), nil
	}
	g.pending = append(g.pending, chunk...)
	if len(g.pending) > g.eventLimit() {
		return nil, fmt.Errorf("first native SSE event exceeds limit")
	}
	for {
		eventEnd, found := firstCompleteNativeSSEEvent(g.pending, g.scanStart)
		if !found {
			return nil, nil
		}
		event := g.pending[g.scanStart:eventEnd]
		g.scanStart = eventEnd
		if !nativeSSEEventHasData(event) {
			continue
		}
		g.ready = true
		output := append([]byte(nil), g.pending...)
		g.pending = nil
		g.scanStart = 0
		return output, nil
	}
}

func (g *nativeFirstSSEEventGate) finish() error {
	if g == nil || g.ready {
		return nil
	}
	return fmt.Errorf("native SSE stream ended before the first data event")
}

func firstCompleteNativeSSEEvent(data []byte, start int) (int, bool) {
	if start < 0 || start > len(data) {
		return 0, false
	}
	lineStart := start
	for index := start; index < len(data); {
		if data[index] != '\r' && data[index] != '\n' {
			index++
			continue
		}
		terminatorStart := index
		index++
		if data[terminatorStart] == '\r' && index < len(data) && data[index] == '\n' {
			index++
		}
		if terminatorStart == lineStart {
			return index, true
		}
		lineStart = index
	}
	return 0, false
}

func nativeSSEEventHasData(event []byte) bool {
	for _, line := range splitNativeSSELines(event) {
		isData, value := parseNativeSSEDataLine(line.content)
		if isData && len(value) > 0 {
			return true
		}
	}
	return false
}

func needsClientModelAlias(spec execution.AttemptSpec) bool {
	return responsealias.Needs(spec.ClientModel, spec.UpstreamModel)
}

// parseRequestReasoningAliasMode resolves the request reasoning alias
// parameter. Unset, empty, invalid and the response-only duplicate value
// select off.
func parseRequestReasoningAliasMode(raw json.RawMessage) responsealias.ReasoningMode {
	text, ok := reasoningAliasText(raw)
	if !ok {
		return responsealias.ReasoningModeOff
	}
	switch text {
	case spec.ReasoningAliasReasoningToContent:
		return responsealias.ReasoningModeReasoningToContent
	case spec.ReasoningAliasContentToReasoning:
		return responsealias.ReasoningModeContentToReasoning
	default:
		return responsealias.ReasoningModeOff
	}
}

// parseResponseReasoningAliasMode resolves the response reasoning alias
// parameter. Unset, empty and invalid values select off.
func parseResponseReasoningAliasMode(raw json.RawMessage) responsealias.ReasoningMode {
	text, ok := reasoningAliasText(raw)
	if !ok {
		return responsealias.ReasoningModeOff
	}
	switch text {
	case spec.ReasoningAliasDuplicate:
		return responsealias.ReasoningModeDuplicate
	default:
		return responsealias.ReasoningModeOff
	}
}

// reasoningAliasText decodes one stored alias parameter into the trimmed,
// lowercased text the mode parsers compare. Unset and undecodable payloads
// are not ok, which selects off.
func reasoningAliasText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(text)), true
}

// reasoningAliasModes reads both reasoning alias directions from the resolved
// target configuration. The reasoning_content_alias key name is fixed by
// stored group params and must not be renamed.
func reasoningAliasModes(spec execution.AttemptSpec) (responseMode, requestMode responsealias.ReasoningMode) {
	if len(spec.TargetConfig) == 0 {
		return responsealias.ReasoningModeOff, responsealias.ReasoningModeOff
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(spec.TargetConfig, &config); err != nil {
		return responsealias.ReasoningModeOff, responsealias.ReasoningModeOff
	}
	return parseResponseReasoningAliasMode(config["reasoning_content_alias"]),
		parseRequestReasoningAliasMode(config["request_reasoning_alias"])
}

func needsResponseReasoningAlias(spec execution.AttemptSpec) bool {
	responseMode, _ := reasoningAliasModes(spec)
	return spec.ClientProtocol == protocol.OpenAICompletions &&
		responseMode != responsealias.ReasoningModeOff
}

func responseReasoningAliasMode(spec execution.AttemptSpec) responsealias.ReasoningMode {
	if !needsResponseReasoningAlias(spec) {
		return responsealias.ReasoningModeOff
	}
	responseMode, _ := reasoningAliasModes(spec)
	return responseMode
}

// needsRequestReasoningAlias gates the outbound chat completions body
// rewrite. Only the native OpenAI chat completions route forwards client
// message objects verbatim, so other protocols never see this rewrite.
func needsRequestReasoningAlias(spec execution.AttemptSpec) bool {
	_, requestMode := reasoningAliasModes(spec)
	return spec.ClientProtocol == protocol.OpenAICompletions &&
		requestMode != responsealias.ReasoningModeOff
}

func requestReasoningAliasMode(spec execution.AttemptSpec) responsealias.ReasoningMode {
	if !needsRequestReasoningAlias(spec) {
		return responsealias.ReasoningModeOff
	}
	_, requestMode := reasoningAliasModes(spec)
	return requestMode
}

func rewriteClientResponseModel(clientProtocol protocol.Protocol, body []byte, clientModel string) ([]byte, error) {
	return responsealias.RewriteJSON(clientProtocol, body, clientModel)
}

// rewriteClientResponseAlias rewrites a native response, optionally with the
// client model name and both reasoning spellings. An empty clientModel skips
// the model rewrite and ReasoningModeOff skips the reasoning rewrite.
func rewriteClientResponseAlias(
	clientProtocol protocol.Protocol,
	body []byte,
	clientModel string,
	mode responsealias.ReasoningMode,
) ([]byte, error) {
	return responsealias.RewriteJSONReasoning(clientProtocol, body, clientModel, mode)
}

type nativeAliasSSERewriter struct {
	clientProtocol protocol.Protocol
	clientModel    string
	reasoningMode  responsealias.ReasoningMode
	pending        []byte
	maxEventBytes  int
}

func newNativeAliasSSERewriter(spec execution.AttemptSpec) *nativeAliasSSERewriter {
	needsModelAlias := needsClientModelAlias(spec)
	reasoningMode := responseReasoningAliasMode(spec)
	if !needsModelAlias && reasoningMode == responsealias.ReasoningModeOff {
		return nil
	}
	clientModel := ""
	if needsModelAlias {
		clientModel = spec.ClientModel
	}
	return &nativeAliasSSERewriter{
		clientProtocol: spec.ClientProtocol,
		clientModel:    clientModel,
		reasoningMode:  reasoningMode,
		maxEventBytes:  execution.SSEEventLimit(spec.ClientProtocol),
	}
}

func (r *nativeAliasSSERewriter) eventLimit() int {
	if r != nil && r.maxEventBytes > 0 {
		return r.maxEventBytes
	}
	return maxNativeAliasSSEEventBytes
}

func (r *nativeAliasSSERewriter) push(chunk []byte) ([]byte, error) {
	if r == nil || len(chunk) == 0 {
		return append([]byte(nil), chunk...), nil
	}
	r.pending = append(r.pending, chunk...)
	var output bytes.Buffer
	for {
		index, delimiterLength := firstNativeSSEDelimiter(r.pending)
		if index < 0 {
			if len(r.pending) > r.eventLimit() {
				return nil, fmt.Errorf("native SSE event exceeds limit")
			}
			return output.Bytes(), nil
		}
		eventEnd := index + delimiterLength
		if eventEnd > r.eventLimit() {
			return nil, fmt.Errorf("native SSE event exceeds limit")
		}
		event := append([]byte(nil), r.pending[:eventEnd]...)
		r.pending = r.pending[eventEnd:]
		rewritten, err := rewriteClientSSEEventAlias(event, r.clientProtocol, r.clientModel, r.reasoningMode)
		if err != nil {
			return nil, err
		}
		_, _ = output.Write(rewritten)
	}
}

func (r *nativeAliasSSERewriter) finish() ([]byte, error) {
	if r == nil || len(r.pending) == 0 {
		return nil, nil
	}
	if len(r.pending) > r.eventLimit() {
		return nil, fmt.Errorf("native SSE event exceeds limit")
	}
	event := append([]byte(nil), r.pending...)
	r.pending = nil
	return rewriteClientSSEEventAlias(event, r.clientProtocol, r.clientModel, r.reasoningMode)
}

func firstNativeSSEDelimiter(data []byte) (int, int) {
	indexLF := bytes.Index(data, []byte("\n\n"))
	indexCRLF := bytes.Index(data, []byte("\r\n\r\n"))
	switch {
	case indexLF < 0 && indexCRLF < 0:
		return -1, 0
	case indexLF < 0:
		return indexCRLF, 4
	case indexCRLF < 0:
		return indexLF, 2
	case indexCRLF < indexLF:
		return indexCRLF, 4
	default:
		return indexLF, 2
	}
}

type nativeSSELine struct {
	content    []byte
	terminator []byte
	isData     bool
	data       []byte
}

// rewriteClientSSEEventAlias rewrites one native SSE event, optionally with
// the client model name and both reasoning spellings on each data payload.
func rewriteClientSSEEventAlias(
	event []byte,
	clientProtocol protocol.Protocol,
	clientModel string,
	mode responsealias.ReasoningMode,
) ([]byte, error) {
	return responsealias.RewriteSSEReasoning(clientProtocol, event, clientModel, mode)
}

func rewriteClientSSEEvent(event []byte, clientProtocol protocol.Protocol, clientModel string) ([]byte, error) {
	return responsealias.RewriteSSE(clientProtocol, event, clientModel)
}

func splitNativeSSELines(event []byte) []nativeSSELine {
	lines := make([]nativeSSELine, 0, 4)
	for start := 0; start < len(event); {
		end := start
		for end < len(event) && event[end] != '\n' && event[end] != '\r' {
			end++
		}
		terminatorEnd := end
		if terminatorEnd < len(event) {
			terminatorEnd++
			if event[end] == '\r' && terminatorEnd < len(event) && event[terminatorEnd] == '\n' {
				terminatorEnd++
			}
		}
		line := nativeSSELine{content: event[start:end], terminator: event[end:terminatorEnd]}
		line.isData, line.data = parseNativeSSEDataLine(line.content)
		lines = append(lines, line)
		start = terminatorEnd
	}
	return lines
}

func parseNativeSSEDataLine(line []byte) (bool, []byte) {
	if len(line) == 0 || line[0] == ':' {
		return false, nil
	}
	separator := bytes.IndexByte(line, ':')
	field := line
	var value []byte
	if separator >= 0 {
		field = line[:separator]
		value = line[separator+1:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
	}
	if !bytes.Equal(field, []byte("data")) {
		return false, nil
	}
	return true, value
}
