package gateway

import "bytes"

// sseEventScanner incrementally locates the first complete SSE event that has
// at least one non-empty data field. It reports wire offsets without rewriting
// the stream.
type sseEventScanner struct {
	line         []byte
	hasData      bool
	dataValues   [][]byte
	eventName    []byte
	skipLineFeed bool
	pendingEvent bool
	found        bool
	scannedBytes int
	hardLimit    int
}

func (scanner *sseEventScanner) Feed(chunk []byte) (int, bool) {
	if scanner.found {
		return 0, true
	}

	for index, value := range chunk {
		scanner.scannedBytes++
		if scanner.pendingEvent {
			scanner.pendingEvent = false
			scanner.found = true
			if value == '\n' {
				return index + 1, true
			}
			return index, true
		}
		if scanner.skipLineFeed {
			scanner.skipLineFeed = false
			if value == '\n' {
				continue
			}
		}

		switch value {
		case '\r':
			scanner.skipLineFeed = true
			if scanner.finishLine() {
				if scanner.hardLimit > 0 && scanner.scannedBytes == scanner.hardLimit {
					scanner.skipLineFeed = false
					scanner.pendingEvent = true
					continue
				}
				scanner.found = true
				return index + 1, true
			}
		case '\n':
			if scanner.finishLine() {
				scanner.found = true
				return index + 1, true
			}
		default:
			scanner.line = append(scanner.line, value)
		}
	}
	return 0, false
}

func (scanner *sseEventScanner) finishAtEOF() bool {
	if scanner.pendingEvent {
		scanner.pendingEvent = false
		scanner.found = true
	}
	return scanner.found
}

func (scanner *sseEventScanner) finishLine() bool {
	line := scanner.line
	scanner.line = scanner.line[:0]
	if len(line) == 0 {
		found := scanner.hasData
		if !found {
			scanner.resetEvent()
		}
		return found
	}
	if line[0] == ':' {
		return false
	}

	field := line
	var value []byte
	if separator := bytes.IndexByte(line, ':'); separator >= 0 {
		field = line[:separator]
		value = line[separator+1:]
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
	}
	if bytes.Equal(field, []byte("data")) && len(value) > 0 {
		scanner.hasData = true
	}
	if bytes.Equal(field, []byte("data")) {
		scanner.dataValues = append(scanner.dataValues, bytes.Clone(value))
	}
	if bytes.Equal(field, []byte("event")) {
		scanner.eventName = bytes.Clone(value)
	}
	return false
}

func (scanner *sseEventScanner) payload() []byte {
	return bytes.Join(scanner.dataValues, []byte{'\n'})
}

func (scanner *sseEventScanner) isProviderError() bool {
	return bytes.Equal(scanner.eventName, []byte("error")) ||
		isSSEErrorPayload(scanner.payload())
}

func (scanner *sseEventScanner) resetEvent() {
	scanner.hasData = false
	scanner.dataValues = nil
	scanner.eventName = nil
}
