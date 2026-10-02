package control

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/requestlog"
)

func TestRequestLogFirstResponseTimingAPI(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		record := requestlog.Record{Stream: true, DurationMs: 1000}
		first := int64(100)
		if !legacy {
			record.FirstResponseMs = &first
		}
		response, err := mapRequestLogItemResponse(record, requestLogUsageCostResponse{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"first_output_ms", "last_output_ms"} {
			if _, exists := fields[name]; exists {
				t.Fatalf("obsolete field %s remains", name)
			}
		}
		for name, want := range map[string]int64{"first_response_ms": first} {
			value, present := fields[name]
			if !present || (legacy && value != nil) || (!legacy && value != float64(want)) {
				t.Fatalf("%s = %v (present %v), legacy=%v", name, value, present, legacy)
			}
		}
	}
}
