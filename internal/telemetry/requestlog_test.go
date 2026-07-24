package telemetry

import (
	"reflect"
	"testing"
	"time"
)

func TestNoopRequestLogSinkDropsSafely(t *testing.T) {
	NoopRequestLogSink{}.Emit(RequestEvent{
		RequestID:   "00000000-0000-4000-8000-000000000001",
		CompletedAt: time.Unix(1, 0),
		Attempts:    []Attempt{{Sequence: 1}},
	})
}

func TestRequestTelemetryContractUsesExactFieldAllowlist(t *testing.T) {
	allowlists := map[reflect.Type][]string{
		reflect.TypeOf(RequestEvent{}): {
			"RequestID",
			"CompletedAt",
			"AccessKeyID",
			"Protocol",
			"ClientModel",
			"UpstreamModel",
			"Status",
			"StatusCode",
			"ErrorCode",
			"ErrorSummary",
			"DurationMs",
			"AffinityHit",
			"Attempts",
		},
		reflect.TypeOf(Attempt{}): {
			"Sequence",
			"GroupID",
			"GroupName",
			"KeyID",
			"KeyMask",
			"UpstreamModel",
			"StatusCode",
			"DurationMs",
			"FailureCategory",
			"Action",
			"WillRetry",
			"ErrorCode",
			"ErrorSummary",
			"Committed",
		},
	}

	for typ, want := range allowlists {
		got := make([]string, 0, typ.NumField())
		for index := 0; index < typ.NumField(); index++ {
			got = append(got, typ.Field(index).Name)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s fields = %v, want exact allowlist %v", typ.Name(), got, want)
		}
	}
}

func TestTelemetryEnumValuesAreStable(t *testing.T) {
	if RequestStatusSuccess != "success" || RequestStatusIncomplete != "incomplete" {
		t.Fatalf("request status values changed")
	}
	if FailureCategoryRateLimited != "rate_limited" || ActionSkipGroup != "skip_group" {
		t.Fatalf("attempt enum values changed")
	}
}
