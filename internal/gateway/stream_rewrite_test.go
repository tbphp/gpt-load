package gateway

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestSSERewriteStreamUsesCompleteBoundedEvents(t *testing.T) {
	t.Run("one byte reads expose only a complete payload", func(t *testing.T) {
		input := "event: chunk\nid: 7\n: keep\ndata: {\"model\":\"provider\"}\n\n"
		body := &chunkedSSEBody{data: []byte(input), maxRead: 1}
		var payloads [][]byte
		stream := newSSERewriteStream(body, func(payload []byte) ([]byte, error) {
			payloads = append(payloads, bytes.Clone(payload))
			return []byte(`{"model":"public"}`), nil
		})

		output, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		want := "event: chunk\nid: 7\n: keep\ndata: {\"model\":\"public\"}\n\n"
		if string(output) != want {
			t.Fatalf("output = %q, want %q", output, want)
		}
		if len(payloads) != 1 || string(payloads[0]) != `{"model":"provider"}` {
			t.Fatalf("rewrite payloads = %q", payloads)
		}
	})

	t.Run("multiple events in one read are rewritten independently", func(t *testing.T) {
		input := "data: {\"n\":1}\n\ndata: {\"n\":2}\n\n"
		var payloads []string
		stream := newSSERewriteStream(io.NopCloser(strings.NewReader(input)), func(payload []byte) ([]byte, error) {
			payloads = append(payloads, string(payload))
			return bytes.ReplaceAll(payload, []byte(`"n"`), []byte(`"value"`)), nil
		})

		output, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		want := "data: {\"value\":1}\n\ndata: {\"value\":2}\n\n"
		if string(output) != want || !equalStrings(payloads, []string{`{"n":1}`, `{"n":2}`}) {
			t.Fatalf("output/payloads = %q / %#v", output, payloads)
		}
	})

	t.Run("CRLF fields and comments are retained", func(t *testing.T) {
		input := "event: update\r\nid: 9\r\n: comment\r\ndata: {\"model\":\"provider\"}\r\n\r\n"
		stream := newSSERewriteStream(&chunkedSSEBody{data: []byte(input), maxRead: 1}, func([]byte) ([]byte, error) {
			return []byte(`{"model":"public"}`), nil
		})

		output, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		want := "event: update\r\nid: 9\r\n: comment\r\ndata: {\"model\":\"public\"}\r\n\r\n"
		if string(output) != want {
			t.Fatalf("output = %q, want %q", output, want)
		}
	})

	t.Run("CR framing joins multiple data values once", func(t *testing.T) {
		input := "event: multi\rdata: {\"value\":\rdata: 1}\r\r"
		var payload []byte
		stream := newSSERewriteStream(&chunkedSSEBody{data: []byte(input), maxRead: 1}, func(value []byte) ([]byte, error) {
			payload = bytes.Clone(value)
			return []byte(`{"value":2}`), nil
		})

		output, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if string(payload) != "{\"value\":\n1}" {
			t.Fatalf("rewrite payload = %q", payload)
		}
		if want := "event: multi\rdata: {\"value\":2}\r\r"; string(output) != want {
			t.Fatalf("output = %q, want %q", output, want)
		}
	})

	t.Run("DONE and events without data remain byte exact", func(t *testing.T) {
		input := "event: ping\n: no data\n\ndata: [DONE]\n\n"
		calls := 0
		stream := newSSERewriteStream(io.NopCloser(strings.NewReader(input)), func(payload []byte) ([]byte, error) {
			calls++
			return payload, nil
		})

		output, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if string(output) != input || calls != 0 {
			t.Fatalf("output/calls = %q / %d, want byte exact and zero", output, calls)
		}
	})

	t.Run("unchanged payload preserves the complete original event", func(t *testing.T) {
		input := "event: untouched\r\ndata:  {\"other\":\r\ndata:\t1}\r\nid: 12\r\n\r\n"
		stream := newSSERewriteStream(io.NopCloser(strings.NewReader(input)), func(payload []byte) ([]byte, error) {
			return bytes.Clone(payload), nil
		})

		output, err := io.ReadAll(stream)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if string(output) != input {
			t.Fatalf("unchanged event = %q, want byte exact %q", output, input)
		}
	})

	t.Run("event size boundary is enforced", func(t *testing.T) {
		prefix, suffix := "data: ", "\n\n"
		exact := prefix + strings.Repeat("x", maxSSEEventBytes-len(prefix)-len(suffix)) + suffix
		stream := newSSERewriteStream(io.NopCloser(strings.NewReader(exact)), func(payload []byte) ([]byte, error) {
			return bytes.Clone(payload), nil
		})
		output, err := io.ReadAll(stream)
		if err != nil || string(output) != exact {
			t.Fatalf("exact boundary output/error = %d bytes / %v", len(output), err)
		}

		over := prefix + strings.Repeat("x", maxSSEEventBytes-len(prefix)-len(suffix)+1) + suffix
		stream = newSSERewriteStream(io.NopCloser(strings.NewReader(over)), func(payload []byte) ([]byte, error) {
			return payload, nil
		})
		if _, err := io.ReadAll(stream); !errors.Is(err, errSSEEventTooLarge) {
			t.Fatalf("oversized event error = %v, want %v", err, errSSEEventTooLarge)
		}

		crlfSuffix := "\r\n\r\n"
		exact = prefix + strings.Repeat("x", maxSSEEventBytes-len(prefix)-len(crlfSuffix)) + crlfSuffix
		stream = newSSERewriteStream(io.NopCloser(io.MultiReader(
			strings.NewReader(exact[:len(exact)-1]),
			strings.NewReader(exact[len(exact)-1:]),
		)), func(payload []byte) ([]byte, error) {
			return bytes.Clone(payload), nil
		})
		output, err = io.ReadAll(stream)
		if err != nil || string(output) != exact {
			t.Fatalf("split CRLF exact boundary output/error = %d bytes / %v", len(output), err)
		}

		over = prefix + strings.Repeat("x", maxSSEEventBytes-len(prefix)-len(crlfSuffix)+1) + crlfSuffix
		stream = newSSERewriteStream(io.NopCloser(io.MultiReader(
			strings.NewReader(over[:len(over)-1]),
			strings.NewReader(over[len(over)-1:]),
		)), func(payload []byte) ([]byte, error) {
			return payload, nil
		})
		if _, err := io.ReadAll(stream); !errors.Is(err, errSSEEventTooLarge) {
			t.Fatalf("split CRLF oversized event error = %v, want %v", err, errSSEEventTooLarge)
		}
	})

	t.Run("rewrite expansion is bounded", func(t *testing.T) {
		stream := newSSERewriteStream(io.NopCloser(strings.NewReader("data: {}\n\n")), func([]byte) ([]byte, error) {
			return bytes.Repeat([]byte("x"), maxSSEEventBytes), nil
		})
		if _, err := io.ReadAll(stream); !errors.Is(err, errSSEEventTooLarge) {
			t.Fatalf("expanded event error = %v, want %v", err, errSSEEventTooLarge)
		}
	})
}

func TestSSERewriteStreamSupportsMixedLineEndings(t *testing.T) {
	tests := []struct {
		name     string
		boundary string
	}{
		{name: "LF then LF", boundary: "\n\n"},
		{name: "LF then CR", boundary: "\n\r"},
		{name: "LF then CRLF", boundary: "\n\r\n"},
		{name: "CR then CR", boundary: "\r\r"},
		{name: "CR then CRLF", boundary: "\r\r\n"},
		{name: "CRLF then LF", boundary: "\r\n\n"},
		{name: "CRLF then CR", boundary: "\r\n\r"},
		{name: "CRLF then CRLF", boundary: "\r\n\r\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := "event: update" + test.boundary[:len(test.boundary)-len(lastLineEnding(test.boundary))] +
				"data: {\"model\":\"provider\"}" + test.boundary
			want := "event: update" + test.boundary[:len(test.boundary)-len(lastLineEnding(test.boundary))] +
				"data: {\"model\":\"public\"}" + test.boundary
			stream := newSSERewriteStream(&chunkedSSEBody{data: []byte(input), maxRead: 1}, func(payload []byte) ([]byte, error) {
				if string(payload) != `{"model":"provider"}` {
					t.Fatalf("rewrite payload = %q", payload)
				}
				return []byte(`{"model":"public"}`), nil
			})

			output, err := io.ReadAll(stream)
			if err != nil || string(output) != want {
				t.Fatalf("ReadAll() = %q, %v, want %q, nil", output, err, want)
			}
		})
	}
}

func TestSSEBoundaryScannerAdvancesLinearly(t *testing.T) {
	prefix, suffix := "data: ", "\n\n"
	event := []byte(prefix + strings.Repeat("x", maxSSEEventBytes-len(prefix)-len(suffix)) + suffix)
	pending := make([]byte, 0, len(event))
	scanner := sseRewriteBoundaryScanner{}
	previousOffset := 0
	for index, value := range event {
		pending = append(pending, value)
		end, found := scanner.Find(pending)
		if scanner.scanOffset < previousOffset || scanner.scanOffset < max(0, len(pending)-3) {
			t.Fatalf("byte %d scan offset = %d after %d bytes, previous %d", index, scanner.scanOffset, len(pending), previousOffset)
		}
		previousOffset = scanner.scanOffset
		if found && index != len(event)-1 {
			t.Fatalf("boundary found early at byte %d", index)
		}
		if index == len(event)-1 && (!found || end != len(event)) {
			t.Fatalf("final boundary = %d, %t, want %d, true", end, found, len(event))
		}
	}
}

func TestSSERewriteStreamRejectsIncompleteEventAtEOF(t *testing.T) {
	stream := newSSERewriteStream(io.NopCloser(strings.NewReader("data: {\"model\":\"provider\"}\n")), func(payload []byte) ([]byte, error) {
		return payload, nil
	})
	output, err := io.ReadAll(stream)
	if !errors.Is(err, errSSEEventIncomplete) || len(output) != 0 {
		t.Fatalf("ReadAll() = %q, %v, want no partial bytes and %v", output, err, errSSEEventIncomplete)
	}
	buffer := make([]byte, 1)
	if read, nextErr := stream.Read(buffer); read != 0 || !errors.Is(nextErr, io.EOF) {
		t.Fatalf("Read() after terminal error = %d, %v, want 0, EOF", read, nextErr)
	}
}

func TestSSERewriteStreamKeepsCROnlyProgressive(t *testing.T) {
	release := make(chan struct{})
	body := &gatedSSEBody{
		first:   []byte("data: {\"model\":\"provider\"}\r\r"),
		second:  []byte("data: {\"model\":\"later\"}\r\r"),
		release: release,
		closed:  make(chan struct{}),
	}
	stream := newSSERewriteStream(body, func(payload []byte) ([]byte, error) {
		return bytes.ReplaceAll(payload, []byte("provider"), []byte("public")), nil
	})
	t.Cleanup(func() { _ = stream.Close() })

	type readResult struct {
		body []byte
		err  error
	}
	result := make(chan readResult, 1)
	go func() {
		buffer := make([]byte, 1024)
		read, err := stream.Read(buffer)
		result <- readResult{body: bytes.Clone(buffer[:read]), err: err}
	}()

	select {
	case got := <-result:
		want := "data: {\"model\":\"public\"}\r\r"
		if got.err != nil || string(got.body) != want {
			t.Fatalf("first CR-only event = %q, %v, want %q, nil", got.body, got.err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("CR-only event waited for bytes from the next event")
	}
}

func TestSSERewriteStreamCloseClosesUnderlyingBody(t *testing.T) {
	body := &chunkedSSEBody{data: []byte("data: value\n\n"), maxRead: 1}
	stream := newSSERewriteStream(body, func(payload []byte) ([]byte, error) { return payload, nil })
	buffer := make([]byte, 1)
	_, _ = stream.Read(buffer)
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !body.closed {
		t.Fatal("Close() did not close the real upstream body")
	}
}

type chunkedSSEBody struct {
	data    []byte
	maxRead int
	closed  bool
}

type gatedSSEBody struct {
	first   []byte
	second  []byte
	release <-chan struct{}
	closed  chan struct{}
	stage   int
}

func (body *gatedSSEBody) Read(target []byte) (int, error) {
	switch body.stage {
	case 0:
		body.stage++
		return copy(target, body.first), nil
	case 1:
		select {
		case <-body.release:
			body.stage++
			return copy(target, body.second), nil
		case <-body.closed:
			return 0, io.ErrClosedPipe
		}
	default:
		return 0, io.EOF
	}
}

func (body *gatedSSEBody) Close() error {
	select {
	case <-body.closed:
	default:
		close(body.closed)
	}
	return nil
}

func (body *chunkedSSEBody) Read(target []byte) (int, error) {
	if len(body.data) == 0 {
		return 0, io.EOF
	}
	limit := len(target)
	if body.maxRead > 0 && limit > body.maxRead {
		limit = body.maxRead
	}
	if limit > len(body.data) {
		limit = len(body.data)
	}
	copy(target, body.data[:limit])
	body.data = body.data[limit:]
	return limit, nil
}

func (body *chunkedSSEBody) Close() error {
	body.closed = true
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func lastLineEnding(boundary string) string {
	if strings.HasSuffix(boundary, "\r\n") {
		return "\r\n"
	}
	return boundary[len(boundary)-1:]
}
