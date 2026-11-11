package requestlog

import (
	"strings"
	"testing"
	"time"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestMapEventRecordsVideoMetadata(t *testing.T) {
	t.Parallel()
	event := telemetry.RequestEvent{
		RequestID: "video-metadata-request", CompletedAt: time.Now(),
		Status: telemetry.RequestStatusSuccess,
		Usage: telemetry.UsageObservation{
			Result:  usage.Result{State: usage.StateNotApplicable},
			Pricing: telemetry.PricingObservation{CostState: string(pricing.CostStateNotApplicable), PricingCompleteness: string(pricing.CompletenessNotApplicable)},
		},
		Video: &telemetry.VideoObservation{
			GatewayTaskID: "video_01", UpstreamTaskID: "task_01", Model: "agnes-video-2.5-flash",
			Mode: "text", Seconds: "5", Size: "720P", AspectRatio: "16:9", Phase: "create", Status: "queued",
		},
	}
	row, err := mapEvent(redact.New(), event)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"gateway_task_id":"video_01"`, `"upstream_task_id":"task_01"`, `"seconds":"5"`} {
		if !strings.Contains(string(row.VideoMetadata), field) {
			t.Fatalf("video metadata %s missing %s", row.VideoMetadata, field)
		}
	}
}
