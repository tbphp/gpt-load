package embedded

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

func TestMuseStreamTerminalPreservesBytesAndParentObserver(t *testing.T) {
	for _, test := range []struct {
		name, wire       string
		seen, incomplete bool
	}{
		{"complete without newline", `data: {"type":"response.completed","response":{"status":"completed"}}`, true, false},
		{"incomplete", "event: response.incomplete\r\ndata: {\"type\":\"response.incomplete\"}\r\n\r\n", true, true},
		{"text is not a terminal", `data: {"type":"response.output_text.delta","delta":"response.completed"}`, false, false},
		{"truncated terminal", `data: {"type":"response.completed","response":`, false, false},
		{"event name alone", "event: response.completed\n\n", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			parentCalled := false
			ctx := WithStreamBodyObserver(t.Context(), func(body io.ReadCloser, _ http.Header) io.ReadCloser { parentCalled = true; return body })
			terminal := &museStreamTerminal{}
			ctx = withMuseStreamTerminal(ctx, terminal)
			wrap := ctx.Value(streamBodyObserverKey{}).(func(io.ReadCloser, http.Header) io.ReadCloser)
			body := wrap(io.NopCloser(iotest.OneByteReader(strings.NewReader(test.wire))), nil)
			got, err := io.ReadAll(body)
			if err != nil {
				t.Fatal(err)
			}
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
			if !parentCalled || !bytes.Equal(got, []byte(test.wire)) || terminal.seen != test.seen || terminal.incomplete != test.incomplete {
				t.Fatalf("parent=%t bytes preserved=%t terminal=%t incomplete=%t", parentCalled, bytes.Equal(got, []byte(test.wire)), terminal.seen, terminal.incomplete)
			}
		})
	}
}
