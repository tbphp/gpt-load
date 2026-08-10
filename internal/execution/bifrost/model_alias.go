package bifrost

import (
	"bytes"
	"fmt"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

const maxNativeAliasSSEEventBytes = 10 << 20

const maxNativeFirstSSEEventBytes = 1 << 20

type nativeFirstSSEEventGate struct {
	pending   []byte
	scanStart int
	ready     bool
}

func (g *nativeFirstSSEEventGate) push(chunk []byte) ([]byte, error) {
	if g == nil || g.ready {
		return append([]byte(nil), chunk...), nil
	}
	g.pending = append(g.pending, chunk...)
	if len(g.pending) > maxNativeFirstSSEEventBytes {
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
	return spec.ClientModel != "" && spec.UpstreamModel != "" && spec.ClientModel != spec.UpstreamModel
}

func rewriteClientResponseModel(clientProtocol protocol.Protocol, body []byte, clientModel string) ([]byte, error) {
	var rewriter dialect.ModelRewriter
	switch clientProtocol {
	case protocol.OpenAICompletions:
		rewriter = dialect.NewOpenAI()
	case protocol.OpenAIResponses:
		rewriter = dialect.NewOpenAIResponses()
	case protocol.Anthropic:
		rewriter = dialect.NewAnthropic()
	case protocol.Gemini:
		rewriter = dialect.NewGemini()
	default:
		return nil, fmt.Errorf("unsupported client protocol")
	}
	rewritten, err := rewriter.RewriteResponseModel(body, clientModel)
	if err != nil {
		return nil, fmt.Errorf("rewrite native response model")
	}
	return rewritten, nil
}

type nativeAliasSSERewriter struct {
	clientProtocol protocol.Protocol
	clientModel    string
	pending        []byte
}

func newNativeAliasSSERewriter(spec execution.AttemptSpec) *nativeAliasSSERewriter {
	if !needsClientModelAlias(spec) {
		return nil
	}
	return &nativeAliasSSERewriter{
		clientProtocol: spec.ClientProtocol,
		clientModel:    spec.ClientModel,
	}
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
			if len(r.pending) > maxNativeAliasSSEEventBytes {
				return nil, fmt.Errorf("native SSE event exceeds limit")
			}
			return output.Bytes(), nil
		}
		eventEnd := index + delimiterLength
		if eventEnd > maxNativeAliasSSEEventBytes {
			return nil, fmt.Errorf("native SSE event exceeds limit")
		}
		event := append([]byte(nil), r.pending[:eventEnd]...)
		r.pending = r.pending[eventEnd:]
		rewritten, err := rewriteClientSSEEvent(event, r.clientProtocol, r.clientModel)
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
	if len(r.pending) > maxNativeAliasSSEEventBytes {
		return nil, fmt.Errorf("native SSE event exceeds limit")
	}
	event := append([]byte(nil), r.pending...)
	r.pending = nil
	return rewriteClientSSEEvent(event, r.clientProtocol, r.clientModel)
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

func rewriteClientSSEEvent(event []byte, clientProtocol protocol.Protocol, clientModel string) ([]byte, error) {
	lines := splitNativeSSELines(event)
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
		return append([]byte(nil), event...), nil
	}
	payload := bytes.Join(dataValues, []byte{'\n'})
	if len(payload) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		return append([]byte(nil), event...), nil
	}
	rewritten, err := rewriteClientResponseModel(clientProtocol, payload, clientModel)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(rewritten, payload) {
		return append([]byte(nil), event...), nil
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
