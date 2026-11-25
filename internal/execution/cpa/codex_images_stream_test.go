package cpa

import (
	"slices"
	"testing"
)

func TestCodexImagesSSEAssemblerPreservesWireBoundaries(t *testing.T) {
	t.Parallel()
	const first = ": keepalive\r\n\r\n"
	const second = "event: image_generation.completed\r\ndata: {}\r\n\r\n"
	const third = "data: [DONE]\n\n"
	wire := first + second + third
	for split := 0; split <= len(wire); split++ {
		var s codexImagesSSEAssembler
		var got []string
		for _, chunk := range []string{wire[:split], wire[split:]} {
			events, err := s.push([]byte(chunk), false)
			if err != nil {
				t.Fatalf("split %d: %v", split, err)
			}
			for _, event := range events {
				got = append(got, string(event))
			}
		}
		if events, err := s.push(nil, true); err != nil || len(events) != 0 {
			t.Fatalf("split %d: EOF returned %q, %v", split, events, err)
		}
		if !slices.Equal(got, []string{first, second, third}) {
			t.Fatalf("split %d: events = %q", split, got)
		}
	}
}

func TestCodexImagesSSEAssemblerRejectsTruncatedEvent(t *testing.T) {
	t.Parallel()
	var s codexImagesSSEAssembler
	if events, err := s.push([]byte("data: {\"type\":\"image_generation.completed\""), false); err != nil || len(events) != 0 {
		t.Fatalf("partial event returned %q, %v", events, err)
	}
	if events, err := s.push(nil, true); err == nil || len(events) != 0 {
		t.Fatalf("EOF returned %q, %v; want a truncation error", events, err)
	}
}
