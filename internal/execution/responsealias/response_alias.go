// Package responsealias rewrites provider model identifiers back to the
// client-visible model without changing the surrounding response protocol.
package responsealias

import (
	"bytes"
	"fmt"

	"gpt-load/internal/dialect"
	"gpt-load/internal/protocol"
)

// Needs reports whether a selected upstream model differs from the model used
// by the client.
func Needs(clientModel, upstreamModel string) bool {
	return clientModel != "" && upstreamModel != "" && clientModel != upstreamModel
}

// RewriteJSON rewrites model fields in one protocol response object.
func RewriteJSON(clientProtocol protocol.Protocol, body []byte, clientModel string) ([]byte, error) {
	rewriter, err := modelRewriter(clientProtocol)
	if err != nil {
		return nil, err
	}
	rewritten, err := rewriter.RewriteResponseModel(body, clientModel)
	if err != nil {
		return nil, fmt.Errorf("rewrite response model: %w", err)
	}
	return rewritten, nil
}

// RewriteSSE rewrites every complete SSE event in data while preserving event
// names, comments, line endings and terminal markers. A final event without a
// blank-line delimiter is also accepted because some upstream bridges emit one
// logical event per chunk without retaining the delimiter.
func RewriteSSE(clientProtocol protocol.Protocol, data []byte, clientModel string) ([]byte, error) {
	var output bytes.Buffer
	remaining := data
	for len(remaining) > 0 {
		index, delimiterLength := firstSSEDelimiter(remaining)
		if index < 0 {
			rewritten, err := rewriteSSEEvent(clientProtocol, remaining, clientModel)
			if err != nil {
				return nil, err
			}
			_, _ = output.Write(rewritten)
			break
		}
		eventEnd := index + delimiterLength
		rewritten, err := rewriteSSEEvent(clientProtocol, remaining[:eventEnd], clientModel)
		if err != nil {
			return nil, err
		}
		_, _ = output.Write(rewritten)
		remaining = remaining[eventEnd:]
	}
	return output.Bytes(), nil
}

func modelRewriter(clientProtocol protocol.Protocol) (dialect.ModelRewriter, error) {
	switch clientProtocol {
	case protocol.OpenAICompletions:
		return dialect.NewOpenAI(), nil
	case protocol.OpenAIResponses:
		return dialect.NewOpenAIResponses(), nil
	case protocol.OpenAIImages:
		return dialect.NewOpenAIImages(), nil
	case protocol.OpenAIEmbeddings:
		return dialect.NewOpenAIEmbeddings(), nil
	case protocol.Anthropic:
		return dialect.NewAnthropic(), nil
	case protocol.Gemini:
		return dialect.NewGemini(), nil
	default:
		return nil, fmt.Errorf("unsupported client protocol")
	}
}

func rewriteSSEEvent(clientProtocol protocol.Protocol, event []byte, clientModel string) ([]byte, error) {
	lines := splitSSELines(event)
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
	rewritten, err := RewriteJSON(clientProtocol, payload, clientModel)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(rewritten, payload) {
		return bytes.Clone(event), nil
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

func firstSSEDelimiter(data []byte) (int, int) {
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

type sseLine struct {
	content    []byte
	terminator []byte
	isData     bool
	data       []byte
}

func splitSSELines(event []byte) []sseLine {
	lines := make([]sseLine, 0, 4)
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
		line := sseLine{content: event[start:end], terminator: event[end:terminatorEnd]}
		line.isData, line.data = parseSSEDataLine(line.content)
		lines = append(lines, line)
		start = terminatorEnd
	}
	return lines
}

func parseSSEDataLine(line []byte) (bool, []byte) {
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
