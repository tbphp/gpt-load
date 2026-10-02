package requestlog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestMapEventClientIP(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"", ""}, {"192.0.2.1", "192.0.2.1"},
		{"::ffff:192.0.2.1", "192.0.2.1"}, {"2001:0db8:0:0::1", "2001:db8::1"},
	} {
		event := telemetry.RequestEvent{
			RequestID: "resource", CompletedAt: time.Now(), ClientIP: test.input,
			Status: telemetry.RequestStatusSuccess,
			Usage: telemetry.UsageObservation{
				Result:  usage.Result{State: usage.StateNotApplicable},
				Pricing: telemetry.PricingObservation{CostState: string(pricing.CostStateNotApplicable), PricingCompleteness: string(pricing.CompletenessNotApplicable)},
			},
		}
		row, err := mapEvent(redact.New(), event)
		if err != nil {
			t.Fatal(err)
		}
		if test.want == "" {
			if row.ClientIP != nil {
				t.Fatal("unknown IP must be NULL")
			}
		} else if row.ClientIP == nil || *row.ClientIP != test.want {
			t.Fatalf("mapped IP = %v, want %s", row.ClientIP, test.want)
		}
	}
}

func TestServiceListFiltersExactClientIPBeforePagination(t *testing.T) {
	db := openRequestLogQueryDB(t)
	completed := time.Now()
	for index, ip := range []string{"2001:db8::1", "2001:db8::10", "2001:db8::1", ""} {
		row := requestLogQueryRow(fmt.Sprintf("00000000-0000-4000-8000-%012d", index+801), completed, 1, "model", nil)
		if ip != "" {
			row.ClientIP = &ip
		}
		createRequestLogQueryRow(t, db, row)
	}
	service := newRequestLogTestService(db)
	query := ListQuery{ClientIP: "2001:db8::1", Limit: 1}
	for index, suffix := range []string{"803", "801"} {
		page, err := service.List(context.Background(), query)
		if err != nil || len(page.Items) != 1 || page.Items[0].RequestID != "00000000-0000-4000-8000-000000000"+suffix || page.Items[0].ClientIP != query.ClientIP {
			t.Fatalf("page = %#v, error = %v", page, err)
		}
		if (page.NextCursor == nil) != (index == 1) {
			t.Fatal("incorrect IP pagination")
		}
		query.Cursor = page.NextCursor
	}
	query.ClientModel = "other-model"
	query.Cursor = nil
	page, err := service.List(context.Background(), query)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("combined filters = %#v, error %v", page, err)
	}
}
