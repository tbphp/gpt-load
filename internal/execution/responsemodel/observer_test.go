package responsemodel

import (
	"strings"
	"testing"

	"gpt-load/internal/protocol"
)

func TestObserverSSEFramingAndTerminalBoundary(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(ending, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			wire := strings.Join([]string{
				": keepalive", "", `data: {"model":`, `data: "different"}`, "",
				`data: {"model":"requested"}`, "", "data: [DONE]", "",
				`data: {"model":"after-terminal"}`, "", "",
			}, ending)
			for split := 0; split <= len(wire); split++ {
				o := New(protocol.OpenAICompletions, "requested")
				o.ObserveSSE([]byte(wire[:split]))
				o.ObserveSSE([]byte(wire[split:]))
				o.FinishSSE()
				if o.Model != "different" {
					t.Fatalf("split=%d model=%q", split, o.Model)
				}
			}
		})
	}
}

func TestObserverMalformedOversizedAndFinalEvents(t *testing.T) {
	for _, tc := range []struct {
		name, wire, want string
		limit            int
	}{
		{"invalid JSON", "data: {broken}\n\ndata: {\"model\":\"reported\"}\n\n", "reported", 100},
		{"no final delimiter", "data: {\"model\":\"reported\"}", "reported", 100},
		{"blank model", "data: {\"model\":\"   \"}\n\n", "", 100},
		{"business field", "data: {\"tool\":{\"model\":\"business\"}}\n\n", "", 100},
		{"oversized event", "data: " + strings.Repeat("x", 100) + "\n\ndata: {\"model\":\"reported\"}\n\n", "", 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := New(protocol.OpenAICompletions, "requested")
			o.limit = tc.limit
			for _, b := range []byte(tc.wire) {
				o.ObserveSSE([]byte{b})
			}
			o.FinishSSE()
			if o.Model != tc.want {
				t.Fatalf("model=%q want=%q", o.Model, tc.want)
			}
		})
	}
}
