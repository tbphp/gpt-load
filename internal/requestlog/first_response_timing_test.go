package requestlog

import (
	"context"
	"testing"
	"time"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/storage/models"
)

func TestFirstResponseFilterUsesUpstreamResponseTime(t *testing.T) {
	db := openRequestLogQueryDB(t)
	first, raw := int64(200), int64(50)
	for _, id := range []string{"legacy", "current"} {
		row := requestLogQueryRow(id, time.Now(), 1, "model", nil)
		row.Stream, row.DurationMs, row.FirstResponseMs = true, 1000, &raw
		if id == "current" {
			row.FirstResponseMs = &first
		}
		createRequestLogQueryRow(t, db, row)
	}
	minimum, maximum := int64(150), int64(250)
	page, err := newRequestLogTestService(db).List(context.Background(), ListQuery{Limit: 50, FirstResponseMinMS: &minimum, FirstResponseMaxMS: &maximum})
	if err != nil || len(page.Items) != 1 || page.Items[0].RequestID != "current" {
		t.Fatalf("upstream first response filter: items=%v, error=%v", requestIDs(page.Items), err)
	}
}

func TestFirstResponseSurvivesFrozenLogRoundTrip(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		event := testEvent("first-response")
		event.Stream = true
		first := int64(5)
		if !legacy {
			event.FirstResponseMs = &first
		}
		frozen := cloneEvent(event)
		first = 7
		row := mustMapEvent(t, redact.New(), frozen)
		records, err := decodeRequestLogRows([]models.RequestLog{row})
		if err != nil || len(records) != 1 {
			t.Fatalf("decode: %v, %+v", err, records)
		}
		got := records[0]
		if legacy {
			if got.FirstResponseMs != nil {
				t.Fatal("legacy timing was fabricated")
			}
		} else if got.FirstResponseMs == nil || *got.FirstResponseMs != 5 {
			t.Fatalf("first response lost or mutated: %+v", got)
		}
	}
}
