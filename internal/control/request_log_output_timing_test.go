package control

import (
	"encoding/json"
	"testing"

	"gpt-load/internal/requestlog"
)

func TestRequestLogOutputTimingAPI(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		record := requestlog.Record{Stream: true, DurationMs: 1000}
		first, last := int64(100), int64(800)
		if !legacy {
			record.FirstOutputMs, record.LastOutputMs = &first, &last
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
		for name, want := range map[string]int64{"first_output_ms": first, "last_output_ms": last} {
			value, present := fields[name]
			if !present || (legacy && value != nil) || (!legacy && value != float64(want)) {
				t.Fatalf("%s = %v (present %v), legacy=%v", name, value, present, legacy)
			}
		}
	}
}
