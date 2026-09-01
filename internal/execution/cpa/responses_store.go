package cpa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"gpt-load/internal/execution"
)

func forceStatelessResponsesPayload(payload []byte) ([]byte, error) {
	object, err := decodeResponsesJSONObject(payload)
	if err != nil {
		return nil, fmt.Errorf("normalize stateless Responses request: %w", err)
	}
	object["store"] = json.RawMessage("false")
	return json.Marshal(object)
}

func forceStatelessResponsesBody(body []byte) ([]byte, error) {
	object, err := decodeResponsesJSONObject(body)
	if err != nil {
		return nil, fmt.Errorf("normalize stateless Responses response: %w", err)
	}
	object["store"] = json.RawMessage("false")
	return json.Marshal(object)
}

func decodeResponsesJSONObject(payload []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("body must be a JSON object")
	}
	return object, nil
}

func normalizeCPAResponsesStoreAttemptResult(
	spec execution.AttemptSpec,
	result *execution.AttemptResult,
) {
	if result == nil || !spec.ResponsesStoreDowngraded || result.Error != nil ||
		result.StatusCode < http.StatusOK || result.StatusCode >= http.StatusMultipleChoices {
		return
	}
	body, err := forceStatelessResponsesBody(result.Body)
	if err == nil {
		result.Body = body
		return
	}
	result.Body = nil
	result.Usage = nil
	result.Model = ""
	result.Error = &execution.ErrorEvidence{
		Kind:       execution.ErrorKindInternal,
		OriginHint: execution.ErrorOriginInternal,
		ScopeHint:  execution.ErrorScopeRequest,
		Code:       "stateless_responses_normalization_failed",
		Summary:    "Stateless Responses result could not be normalized.",
	}
}

func forceStatelessResponsesSSEEvent(event []byte) ([]byte, error) {
	lines := splitResponsesSSELines(event)
	dataValues := make([][]byte, 0, 1)
	firstDataLine := -1
	for index := range lines {
		if !lines[index].isData {
			continue
		}
		if firstDataLine < 0 {
			firstDataLine = index
		}
		dataValues = append(dataValues, lines[index].data)
	}
	if firstDataLine < 0 {
		return bytes.Clone(event), nil
	}
	payload := bytes.Join(dataValues, []byte{'\n'})
	if len(payload) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		return bytes.Clone(event), nil
	}
	object, err := decodeResponsesJSONObject(payload)
	if err != nil {
		return nil, fmt.Errorf("decode Responses SSE payload: %w", err)
	}
	rawResponse, exists := object["response"]
	if !exists || bytes.Equal(bytes.TrimSpace(rawResponse), []byte("null")) {
		return bytes.Clone(event), nil
	}
	response, err := decodeResponsesJSONObject(rawResponse)
	if err != nil {
		return nil, fmt.Errorf("decode Responses SSE response: %w", err)
	}
	response["store"] = json.RawMessage("false")
	object["response"], err = json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode Responses SSE response: %w", err)
	}
	rewritten, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode Responses SSE payload: %w", err)
	}

	var output bytes.Buffer
	output.Grow(len(event) - len(payload) + len(rewritten))
	for index, line := range lines {
		switch {
		case index == firstDataLine:
			_, _ = output.WriteString("data: ")
			_, _ = output.Write(rewritten)
			_, _ = output.Write(line.terminator)
		case line.isData:
			continue
		default:
			_, _ = output.Write(line.content)
			_, _ = output.Write(line.terminator)
		}
	}
	return output.Bytes(), nil
}

type responsesSSELine struct {
	content    []byte
	terminator []byte
	isData     bool
	data       []byte
}

func splitResponsesSSELines(event []byte) []responsesSSELine {
	lines := make([]responsesSSELine, 0, 4)
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
		content := event[start:end]
		line := responsesSSELine{
			content:    content,
			terminator: event[end:terminatorEnd],
		}
		if colon := bytes.IndexByte(content, ':'); colon >= 0 {
			if bytes.Equal(content[:colon], []byte("data")) {
				line.isData = true
				line.data = content[colon+1:]
				if len(line.data) > 0 && line.data[0] == ' ' {
					line.data = line.data[1:]
				}
			}
		} else if bytes.Equal(content, []byte("data")) {
			line.isData = true
		}
		lines = append(lines, line)
		start = terminatorEnd
	}
	return lines
}
