package embedded

import (
	"net/http"
	"testing"
	"time"
)

func TestBoundedOAuthRetryAfter(t *testing.T) {
	now := time.Date(2026, time.August, 30, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		values []string
		want   time.Duration
	}{
		{name: "delta seconds", values: []string{"1800"}, want: 30 * time.Minute},
		{name: "http date", values: []string{now.Add(45 * time.Minute).Format(http.TimeFormat)}, want: 45 * time.Minute},
		{name: "multiple uses longest valid", values: []string{"300", "1800"}, want: 30 * time.Minute},
		{name: "exact limit", values: []string{"3600"}, want: time.Hour},
		{name: "delta over limit is clamped", values: []string{"3601"}, want: time.Hour},
		{name: "delta overflow is clamped", values: []string{"999999999999999999999999"}, want: time.Hour},
		{name: "date over limit is clamped", values: []string{now.Add(2 * time.Hour).Format(http.TimeFormat)}, want: time.Hour},
		{name: "zero", values: []string{"0"}},
		{name: "negative", values: []string{"-1"}},
		{name: "past date", values: []string{now.Add(-time.Minute).Format(http.TimeFormat)}},
		{name: "invalid", values: []string{"later"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{"Retry-After": test.values}
			if got := boundedOAuthRetryAfter(header, now); got != test.want {
				t.Fatalf("boundedOAuthRetryAfter() = %s, want %s", got, test.want)
			}
		})
	}
}
