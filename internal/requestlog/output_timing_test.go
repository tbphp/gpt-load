package requestlog

import (
	"context"
	"testing"
	"time"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
)

func TestFirstResponseFilterUsesEffectiveOutputTime(t *testing.T) {
	db := openRequestLogQueryDB(t)
	first, last, raw := int64(200), int64(800), int64(50)
	for _, id := range []string{"legacy", "current"} {
		row := requestLogQueryRow(id, time.Now(), 1, "model", nil)
		row.Stream, row.DurationMs, row.FirstResponseMs = true, 1000, &raw
		if id == "current" {
			row.FirstOutputMs, row.LastOutputMs = &first, &last
		}
		createRequestLogQueryRow(t, db, row)
	}
	minimum, maximum := int64(150), int64(250)
	page, err := newRequestLogTestService(db).List(context.Background(), ListQuery{Limit: 50, FirstResponseMinMS: &minimum, FirstResponseMaxMS: &maximum})
	if err != nil || len(page.Items) != 1 || page.Items[0].RequestID != "current" {
		t.Fatalf("effective first response filter: items=%v, error=%v", requestIDs(page.Items), err)
	}
}

func TestOutputTimingSurvivesFrozenLogRoundTrip(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		event := testEvent("output-timing")
		event.Stream = true
		first, last := int64(5), int64(20)
		if !legacy {
			event.FirstOutputMs, event.LastOutputMs = &first, &last
		}
		frozen := cloneEvent(event)
		first, last = 7, 22
		row := mustMapEvent(t, redact.New(), frozen)
		records, err := decodeRequestLogRows([]models.RequestLog{row})
		if err != nil || len(records) != 1 {
			t.Fatalf("decode: %v, %+v", err, records)
		}
		got := records[0]
		if legacy {
			if got.FirstOutputMs != nil || got.LastOutputMs != nil {
				t.Fatal("legacy timing was fabricated")
			}
		} else if got.FirstOutputMs == nil || got.LastOutputMs == nil || *got.FirstOutputMs != 5 || *got.LastOutputMs != 20 {
			t.Fatalf("output timing lost or mutated: %+v", got)
		}
	}
}

func TestOutputTimingRejectsInvalidObservation(t *testing.T) {
	for _, pair := range [][2]int64{{-1, 20}, {20, 5}, {5, 26}} {
		event := testEvent("invalid-output-timing")
		event.Stream = true
		event.FirstOutputMs, event.LastOutputMs = &pair[0], &pair[1]
		if _, err := mapEvent(redact.New(), event); err == nil {
			t.Fatalf("invalid timing accepted: %v", pair)
		}
	}
	for _, stream := range []bool{false, true} {
		event := testEvent("incomplete-output-timing")
		event.Stream = stream
		first, last := int64(5), int64(20)
		event.FirstOutputMs = &first
		if !stream {
			event.LastOutputMs = &last
		}
		if _, err := mapEvent(redact.New(), event); err == nil {
			t.Fatal("nonstream or incomplete timing accepted")
		}
	}
}
