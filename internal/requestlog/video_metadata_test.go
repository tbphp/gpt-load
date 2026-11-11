package requestlog

import (
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
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

func TestMapEventProjectsBoundVideoContent(t *testing.T) {
	t.Parallel()
	event := telemetry.RequestEvent{
		RequestID: "video-content", CompletedAt: time.Now(), Protocol: protocol.OpenAIVideos,
		Operation: execution.OperationVideosContent, ClientModel: "sora-2",
		UpstreamModel: "sora-2", ModelConsistency: telemetry.ModelConsistencyUnknown,
		Status: telemetry.RequestStatusSuccess, StatusCode: 206, DurationMs: 4,
		Attempts: []telemetry.Attempt{{
			Sequence: 1, GroupID: 1, ChannelID: channel.OpenAICompatible, CredentialID: 2,
			Operation: execution.OperationVideosContent, RouteMode: channel.RouteNative,
			UpstreamModel: "sora-2", DispatchState: execution.DispatchMaybeSent,
			ResponseStarted: true, UpstreamProtocol: protocol.OpenAIVideos, StatusCode: 206, DurationMs: 4,
			FailureCategory: telemetry.FailureCategoryOK, FailureOrigin: execution.ErrorOriginUpstream,
			FailureScope: execution.ErrorScopeRequest, RetryDirective: "none", Effect: "none", Action: "terminate",
		}},
		Usage: telemetry.UsageObservation{
			Result:  usage.Result{State: usage.StateNotApplicable},
			GroupID: 1, ChannelID: channel.OpenAICompatible, CredentialID: 2, AttemptSequence: 1,
			Pricing: telemetry.PricingObservation{
				UpstreamModel: "sora-2", CostState: string(pricing.CostStateNotApplicable),
				PricingCompleteness: string(pricing.CompletenessNotApplicable),
			},
		},
		Video: &telemetry.VideoObservation{
			GatewayTaskID: "video_01", UpstreamTaskID: "task_01", Model: "sora-2",
			Mode: "text", Seconds: "5", Size: "720P", AspectRatio: "16:9", Phase: "content", Status: "completed",
		},
	}
	row, err := mapEvent(redact.New(), event)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(row.VideoMetadata), `"phase":"content"`) || len(row.AttemptRows) != 1 {
		t.Fatalf("metadata = %s attempts = %d", row.VideoMetadata, len(row.AttemptRows))
	}
}
