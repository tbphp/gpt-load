package health

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestParseRateLimitReset(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		header http.Header
		want   time.Time
		wantOK bool
	}{
		{
			name:   "Retry-After delta seconds",
			header: http.Header{"Retry-After": {"30"}},
			want:   now.Add(30 * time.Second),
			wantOK: true,
		},
		{
			name:   "Retry-After HTTP date",
			header: http.Header{"Retry-After": {now.Add(45 * time.Second).Format(http.TimeFormat)}},
			want:   now.Add(45 * time.Second),
			wantOK: true,
		},
		{
			name: "Retry-After wins over lower families",
			header: http.Header{
				"Retry-After":                      {"15"},
				"Anthropic-Ratelimit-Tokens-Reset": {now.Add(30 * time.Second).Format(time.RFC3339)},
			},
			want:   now.Add(15 * time.Second),
			wantOK: true,
		},
		{
			name: "invalid Retry-After falls back to Anthropic",
			header: http.Header{
				"Retry-After":                      {"invalid"},
				"Anthropic-Ratelimit-Tokens-Reset": {now.Add(20 * time.Second).Format(time.RFC3339)},
			},
			want:   now.Add(20 * time.Second),
			wantOK: true,
		},
		{
			name: "Anthropic reset wins over x-ratelimit family",
			header: http.Header{
				"Anthropic-Ratelimit-Tokens-Reset": {now.Add(20 * time.Second).Format(time.RFC3339)},
				"X-Ratelimit-Reset-Tokens":         {"45s"},
			},
			want:   now.Add(20 * time.Second),
			wantOK: true,
		},
		{
			name: "invalid Anthropic reset falls back to x-ratelimit family",
			header: http.Header{
				"Anthropic-Ratelimit-Tokens-Reset": {"invalid"},
				"X-Ratelimit-Reset-Tokens":         {"45s"},
			},
			want:   now.Add(45 * time.Second),
			wantOK: true,
		},
		{
			name:   "Anthropic Unix seconds",
			header: http.Header{"Anthropic-Ratelimit-Requests-Reset": {strconv.FormatInt(now.Add(2*time.Minute).Unix(), 10)}},
			want:   now.Add(2 * time.Minute),
			wantOK: true,
		},
		{
			name:   "Anthropic Unix milliseconds",
			header: http.Header{"Anthropic-Ratelimit-Tokens-Reset": {strconv.FormatInt(now.Add(90*time.Second).UnixMilli(), 10)}},
			want:   now.Add(90 * time.Second),
			wantOK: true,
		},
		{
			name:   "x-ratelimit duration",
			header: http.Header{"X-Ratelimit-Reset-Tokens": {"75s"}},
			want:   now.Add(75 * time.Second),
			wantOK: true,
		},
		{
			name: "same family chooses latest valid reset",
			header: http.Header{
				"X-Ratelimit-Reset-Requests": {"10s"},
				"X-Ratelimit-Reset-Tokens":   {"25s"},
			},
			want:   now.Add(25 * time.Second),
			wantOK: true,
		},
		{
			name:   "case-insensitive raw header name",
			header: http.Header{"aNtHrOpIc-RaTeLiMiT-ToKeNs-ReSeT": {now.Add(time.Minute).Format(time.RFC3339)}},
			want:   now.Add(time.Minute),
			wantOK: true,
		},
		{
			name:   "upper boundary accepted",
			header: http.Header{"X-Ratelimit-Reset": {"1h"}},
			want:   now.Add(time.Hour),
			wantOK: true,
		},
		{name: "empty headers", header: http.Header{}},
		{name: "malformed text", header: http.Header{"Retry-After": {"tomorrow"}}},
		{name: "NaN rejected", header: http.Header{"X-Ratelimit-Reset": {"NaN"}}},
		{name: "Inf rejected", header: http.Header{"X-Ratelimit-Reset": {"Inf"}}},
		{
			name: "equal now rejected",
			header: http.Header{
				"Anthropic-Ratelimit-Tokens-Reset": {now.Format(time.RFC3339)},
			},
		},
		{
			name: "past rejected",
			header: http.Header{
				"Anthropic-Ratelimit-Tokens-Reset": {now.Add(-time.Second).Format(time.RFC3339)},
			},
		},
		{
			name: "over one hour rejected",
			header: http.Header{
				"X-Ratelimit-Reset": {"1h1ns"},
			},
		},
		{name: "negative Retry-After rejected", header: http.Header{"Retry-After": {"-1"}}},
		{
			name: "integer overflow rejected",
			header: http.Header{
				"X-Ratelimit-Reset": {"9223372036854775808"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotOK := ParseRateLimitReset(tt.header, now)
			if gotOK != tt.wantOK {
				t.Fatalf("ParseRateLimitReset() ok = %t, want %t", gotOK, tt.wantOK)
			}
			if !gotOK {
				if !got.IsZero() {
					t.Fatalf("ParseRateLimitReset() reset = %v, want zero time", got)
				}
				return
			}
			if !got.Equal(tt.want) {
				t.Fatalf("ParseRateLimitReset() reset = %v, want %v", got, tt.want)
			}
		})
	}
}
