package gateway

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
)

const maxSSEEventBytes = 1 << 20

var (
	errSSEEventTooLarge   = errors.New("SSE event exceeds size limit")
	errSSEEventIncomplete = errors.New("stream ended with an incomplete SSE event")
)

type sseRewriteStream struct {
	readMu sync.Mutex
	mu     sync.Mutex

	body    io.ReadCloser
	rewrite func([]byte) ([]byte, error)

	pending     []byte
	output      []byte
	scratch     []byte
	scanner     sseRewriteBoundaryScanner
	deferredErr error
	finished    bool
	closed      bool
}

func newSSERewriteStream(
	body io.ReadCloser,
	rewrite func([]byte) ([]byte, error),
) io.ReadCloser {
	return &sseRewriteStream{
		body: body, rewrite: rewrite,
		scratch: make([]byte, streamReadBufferSize),
	}
}

func (stream *sseRewriteStream) Read(target []byte) (int, error) {
	if len(target) == 0 {
		return 0, nil
	}
	stream.readMu.Lock()
	defer stream.readMu.Unlock()

	zeroReads := 0
	for {
		stream.mu.Lock()
		if stream.closed {
			stream.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if stream.finished {
			stream.mu.Unlock()
			return 0, io.EOF
		}
		if len(stream.output) > 0 {
			read := copy(target, stream.output)
			stream.output = stream.output[read:]
			if len(stream.output) == 0 {
				stream.output = nil
			}
			stream.mu.Unlock()
			return read, nil
		}
		optionalLF, overflow := stream.scanner.ConsumeOptionalLineFeed(
			stream.pending,
			stream.deferredErr != nil,
		)
		if overflow {
			stream.finishLocked()
			stream.mu.Unlock()
			return 0, errSSEEventTooLarge
		}
		if optionalLF > 0 {
			stream.pending = stream.pending[optionalLF:]
			if len(stream.pending) == 0 {
				stream.pending = nil
			}
			stream.output = []byte{'\n'}
			stream.mu.Unlock()
			continue
		}

		eventEnd, complete := stream.scanner.Find(stream.pending)
		if complete {
			if eventEnd > maxSSEEventBytes {
				stream.finishLocked()
				stream.mu.Unlock()
				return 0, errSSEEventTooLarge
			}
			event := bytes.Clone(stream.pending[:eventEnd])
			stream.pending = stream.pending[eventEnd:]
			if len(stream.pending) == 0 {
				stream.pending = nil
			}
			rewrite := stream.rewrite
			stream.mu.Unlock()

			rewritten, err := rewriteSSEEvent(event, rewrite)
			if err != nil {
				stream.mu.Lock()
				stream.finishLocked()
				stream.mu.Unlock()
				return 0, err
			}
			if len(rewritten) > maxSSEEventBytes {
				stream.mu.Lock()
				stream.finishLocked()
				stream.mu.Unlock()
				return 0, errSSEEventTooLarge
			}
			stream.mu.Lock()
			if stream.closed {
				stream.mu.Unlock()
				return 0, io.ErrClosedPipe
			}
			stream.scanner.AfterEvent(eventEnd, len(rewritten))
			stream.output = rewritten
			stream.mu.Unlock()
			continue
		}
		if len(stream.pending) > maxSSEEventBytes {
			stream.finishLocked()
			stream.mu.Unlock()
			return 0, errSSEEventTooLarge
		}
		if stream.deferredErr != nil {
			err := stream.deferredErr
			stream.deferredErr = nil
			if errors.Is(err, io.EOF) {
				if len(stream.pending) > 0 {
					err = errSSEEventIncomplete
				} else {
					err = io.EOF
				}
			} else {
				err = fmt.Errorf("read SSE stream: %w", err)
			}
			stream.finishLocked()
			stream.mu.Unlock()
			return 0, err
		}
		if stream.body == nil || stream.rewrite == nil {
			err := fmt.Errorf("SSE rewrite stream body and callback are required")
			stream.finishLocked()
			stream.mu.Unlock()
			return 0, err
		}
		body := stream.body
		readLimit := min(streamReadBufferSize, maxSSEEventBytes+1-len(stream.pending))
		chunk := stream.scratch[:readLimit]
		stream.mu.Unlock()

		read, err := body.Read(chunk)

		stream.mu.Lock()
		if stream.closed {
			stream.mu.Unlock()
			return 0, io.ErrClosedPipe
		}
		if read > 0 {
			stream.pending = append(stream.pending, chunk[:read]...)
			zeroReads = 0
		} else if err == nil {
			zeroReads++
		}
		if err != nil {
			stream.deferredErr = err
		}
		stream.mu.Unlock()
		if zeroReads >= 100 {
			stream.mu.Lock()
			stream.finishLocked()
			stream.mu.Unlock()
			return 0, io.ErrNoProgress
		}
	}
}

func (stream *sseRewriteStream) Close() error {
	stream.mu.Lock()
	if stream.closed {
		stream.mu.Unlock()
		return nil
	}
	stream.closed = true
	body := stream.body
	stream.body = nil
	stream.rewrite = nil
	stream.pending = nil
	stream.output = nil
	stream.scratch = nil
	stream.scanner.Reset()
	stream.deferredErr = nil
	stream.finished = true
	stream.mu.Unlock()
	if body == nil {
		return nil
	}
	return body.Close()
}

func (stream *sseRewriteStream) finishLocked() {
	stream.pending = nil
	stream.output = nil
	stream.deferredErr = nil
	stream.finished = true
	stream.scanner.Reset()
}

type sseRewriteBoundaryScanner struct {
	scanOffset          int
	lineHasContent      bool
	skipLineFeed        bool
	optionalLineFeed    bool
	previousInputBytes  int
	previousOutputBytes int
}

func (scanner *sseRewriteBoundaryScanner) Find(data []byte) (int, bool) {
	start := min(scanner.scanOffset, len(data))
	for index := start; index < len(data); {
		value := data[index]
		if scanner.skipLineFeed {
			scanner.skipLineFeed = false
			if value == '\n' {
				index++
				scanner.scanOffset = index
				continue
			}
		}

		switch value {
		case '\r':
			blankLine := !scanner.lineHasContent
			index++
			if index < len(data) && data[index] == '\n' {
				index++
			} else {
				scanner.skipLineFeed = true
			}
			scanner.lineHasContent = false
			scanner.scanOffset = index
			if blankLine {
				return index, true
			}
		case '\n':
			blankLine := !scanner.lineHasContent
			index++
			scanner.lineHasContent = false
			scanner.scanOffset = index
			if blankLine {
				return index, true
			}
		default:
			scanner.lineHasContent = true
			index++
			scanner.scanOffset = index
		}
	}
	return 0, false
}

func (scanner *sseRewriteBoundaryScanner) AfterEvent(inputBytes, outputBytes int) {
	scanner.optionalLineFeed = scanner.skipLineFeed
	scanner.previousInputBytes = inputBytes
	scanner.previousOutputBytes = outputBytes
	scanner.scanOffset = 0
	scanner.lineHasContent = false
	scanner.skipLineFeed = false
}

func (scanner *sseRewriteBoundaryScanner) ConsumeOptionalLineFeed(data []byte, final bool) (int, bool) {
	if !scanner.optionalLineFeed {
		return 0, false
	}
	if len(data) == 0 {
		if final {
			scanner.clearOptionalLineFeed()
		}
		return 0, false
	}
	scanner.optionalLineFeed = false
	if data[0] != '\n' {
		scanner.previousInputBytes = 0
		scanner.previousOutputBytes = 0
		return 0, false
	}
	overflow := scanner.previousInputBytes >= maxSSEEventBytes ||
		scanner.previousOutputBytes >= maxSSEEventBytes
	scanner.previousInputBytes = 0
	scanner.previousOutputBytes = 0
	return 1, overflow
}

func (scanner *sseRewriteBoundaryScanner) clearOptionalLineFeed() {
	scanner.optionalLineFeed = false
	scanner.previousInputBytes = 0
	scanner.previousOutputBytes = 0
}

func (scanner *sseRewriteBoundaryScanner) Reset() {
	*scanner = sseRewriteBoundaryScanner{}
}

type sseEventLine struct {
	content    []byte
	terminator []byte
	isData     bool
	data       []byte
}

func rewriteSSEEvent(event []byte, rewrite func([]byte) ([]byte, error)) ([]byte, error) {
	if rewrite == nil {
		return nil, fmt.Errorf("SSE rewrite callback is required")
	}
	lines := splitSSEEventLines(event)
	dataValues := make([][]byte, 0)
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
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return bytes.Clone(event), nil
	}
	rewritten, err := rewrite(payload)
	if err != nil {
		return nil, fmt.Errorf("rewrite SSE event payload: %w", err)
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

func splitSSEEventLines(event []byte) []sseEventLine {
	lines := make([]sseEventLine, 0, 4)
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
		line := sseEventLine{content: content, terminator: event[end:terminatorEnd]}
		line.isData, line.data = parseSSEDataLine(content)
		lines = append(lines, line)
		start = terminatorEnd
	}
	return lines
}

func parseSSEDataLine(line []byte) (bool, []byte) {
	if len(line) == 0 || line[0] == ':' {
		return false, nil
	}
	colon := bytes.IndexByte(line, ':')
	field := line
	value := []byte(nil)
	if colon >= 0 {
		field = line[:colon]
		value = line[colon+1:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
	}
	if !bytes.Equal(field, []byte("data")) {
		return false, nil
	}
	return true, value
}
