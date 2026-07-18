package gateway

import "bytes"

// sseEventScanner incrementally locates the first complete SSE event that has
// at least one non-empty data field. It reports wire offsets without rewriting
// the stream.
type sseEventScanner struct {
	line         []byte
	hasData      bool
	skipLineFeed bool
	found        bool
}

func (scanner *sseEventScanner) Feed(chunk []byte) (int, bool) {
	if scanner.found {
		return 0, true
	}

	for index, value := range chunk {
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

func (scanner *sseEventScanner) finishLine() bool {
	line := scanner.line
	scanner.line = scanner.line[:0]
	if len(line) == 0 {
		found := scanner.hasData
		scanner.hasData = false
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
	return false
}
