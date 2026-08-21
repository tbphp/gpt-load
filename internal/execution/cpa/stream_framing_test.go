package cpa

import "testing"

func TestNativeResponsesSSEAssemblerHandlesFragmentedAndCompleteEvents(t *testing.T) {
	assembler := &nativeResponsesSSEAssembler{}
	for _, chunk := range []string{
		"event: response.created",
		`data: {"type":"response.created"}`,
	} {
		events, err := assembler.push([]byte(chunk))
		if err != nil || len(events) != 0 {
			t.Fatalf("push(%q) = %q, %v", chunk, events, err)
		}
	}
	events, err := assembler.push(nil)
	if err != nil || len(events) != 1 || string(events[0]) != "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" {
		t.Fatalf("fragmented event = %q, %v", events, err)
	}

	events, err = assembler.push([]byte(
		"event: response.in_progress\r\ndata: {\"type\":\"response.in_progress\"}\r\n\r\n" +
			"event: response.completed\r\ndata: {\"type\":\"response.completed\"}\r\n\r\n",
	))
	if err != nil || len(events) != 2 {
		t.Fatalf("complete events = %q, %v", events, err)
	}
	if trailing, err := assembler.finish(); err != nil || len(trailing) != 0 {
		t.Fatal(err)
	}
}

func TestNativeResponsesSSEAssemblerIgnoresEventsWithoutData(t *testing.T) {
	assembler := &nativeResponsesSSEAssembler{}
	events, err := assembler.push([]byte(": keepalive\n\nevent: response.created\n\n"))
	if err != nil || len(events) != 0 {
		t.Fatalf("events without data = %q, %v", events, err)
	}
}

func TestNativeResponsesSSEAssemblerRejectsIncompleteEOF(t *testing.T) {
	assembler := &nativeResponsesSSEAssembler{}
	if events, err := assembler.push([]byte("event: response.created")); err != nil || len(events) != 0 {
		t.Fatalf("push() = %q, %v", events, err)
	}
	if _, err := assembler.finish(); err == nil {
		t.Fatal("finish() error = nil, want incomplete event error")
	}
}

func TestNativeResponsesSSEAssemblerCompletesDataEventAtEOF(t *testing.T) {
	assembler := &nativeResponsesSSEAssembler{}
	for _, chunk := range []string{
		"event: response.completed",
		`data: {"type":"response.completed"}`,
	} {
		if events, err := assembler.push([]byte(chunk)); err != nil || len(events) != 0 {
			t.Fatalf("push(%q) = %q, %v", chunk, events, err)
		}
	}
	events, err := assembler.finish()
	if err != nil || len(events) != 1 || string(events[0]) != "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n" {
		t.Fatalf("finish() = %q, %v", events, err)
	}
}
