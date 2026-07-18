package gateway

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"gpt-load/internal/testutil/fakeupstream"
)

func TestSSEScannerCommitsOnlyAtCompleteNonEmptyDataEvent(t *testing.T) {
	tests := []struct {
		name           string
		chunks         []string
		wantFoundChunk int
	}{
		{name: "half event", chunks: []string{"data: {\"ok\":true}\n"}},
		{name: "comment and blank", chunks: []string{": keepalive\n", "\n"}},
		{name: "leading blanks", chunks: []string{"\n\n", "event: message\n"}},
		{name: "empty data", chunks: []string{"data:\n\n"}},
		{name: "space-only data", chunks: []string{"data: \n\n"}},
		{name: "data without colon", chunks: []string{"data\n\n"}},
		{name: "complete LF", chunks: []string{"data: x\n", "\n"}, wantFoundChunk: 2},
		{name: "complete CRLF split", chunks: []string{"data: x\r", "\n\r", "\n"}, wantFoundChunk: 2},
		{name: "complete CR", chunks: []string{"data: x\r\r"}, wantFoundChunk: 1},
		{name: "event field plus data", chunks: []string{"event: message_start\ndata: {}\n\n"}, wantFoundChunk: 1},
		{name: "tab data is nonempty", chunks: []string{"data:\tx\n\n"}, wantFoundChunk: 1},
		{name: "unknown field does not interfere", chunks: []string{"retry: 1000\ndata: x\n\n"}, wantFoundChunk: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := &sseEventScanner{}
			foundChunk := 0
			for index, chunk := range tt.chunks {
				_, found := scanner.Feed([]byte(chunk))
				if found {
					foundChunk = index + 1
					break
				}
			}
			if foundChunk != tt.wantFoundChunk {
				t.Fatalf("Feed() found after chunk %d, want %d", foundChunk, tt.wantFoundChunk)
			}
		})
	}
}

func TestSSEScannerReportsExactFirstEventBoundary(t *testing.T) {
	const first = "data: first\n\n"
	const second = "data: second\n\n"

	tests := []struct {
		name   string
		chunks [][]byte
	}{
		{name: "single chunk", chunks: [][]byte{[]byte(first + second)}},
		{name: "one byte chunks", chunks: splitBytes([]byte(first+second), 1)},
		{name: "uneven chunks", chunks: splitBytes([]byte(first+second), 5)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			boundary, found := scanSSEChunks(tt.chunks)
			if !found {
				t.Fatal("scanner did not find first event")
			}
			if boundary != len(first) {
				t.Fatalf("boundary = %d, want %d", boundary, len(first))
			}
		})
	}
}

func TestSSEScannerRecognizesThreeDialectFixtures(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "openai", path: "/v1/chat/completions"},
		{name: "anthropic", path: "/v1/messages"},
		{name: "gemini", path: "/v1beta/models/gemini-2.5-pro:streamGenerateContent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := fakeupstream.New(fakeupstream.Step{Stream: true, Fixture: "stream.sse"})
			defer upstream.Close()

			request, err := http.NewRequest(http.MethodPost, upstream.URL+tt.path, bytes.NewReader([]byte(`{}`)))
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			response, err := upstream.Client().Do(request)
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatalf("ReadAll() error = %v", readErr)
			}
			separator := bytes.Index(body, []byte("\n\n"))
			if separator < 0 {
				t.Fatalf("fixture has no complete first event: %q", body)
			}
			wantBoundary := separator + len("\n\n")

			for _, chunkSize := range []int{1, 7, len(body)} {
				boundary, found := scanSSEChunks(splitBytes(body, chunkSize))
				if !found || boundary != wantBoundary {
					t.Fatalf("chunk size %d: boundary/found = %d/%t, want %d/true", chunkSize, boundary, found, wantBoundary)
				}
			}
		})
	}
}

func scanSSEChunks(chunks [][]byte) (int, bool) {
	scanner := &sseEventScanner{}
	consumed := 0
	for _, chunk := range chunks {
		boundary, found := scanner.Feed(chunk)
		if found {
			return consumed + boundary, true
		}
		consumed += len(chunk)
	}
	return 0, false
}

func splitBytes(input []byte, size int) [][]byte {
	if size <= 0 {
		panic("chunk size must be positive")
	}
	chunks := make([][]byte, 0, (len(input)+size-1)/size)
	for len(input) > 0 {
		end := min(size, len(input))
		chunks = append(chunks, bytes.Clone(input[:end]))
		input = input[end:]
	}
	return chunks
}
