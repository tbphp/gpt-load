package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestUsageAPIExplicitTimeRange(t *testing.T) {
	t.Parallel()
	const hourMS int64 = 60 * 60 * 1000
	const dayMS = 24 * hourMS
	from := time.Date(2026, time.September, 8, 9, 15, 7, 123_000_000, time.UTC).UnixMilli()
	for _, test := range []struct {
		name  string
		span  int64
		width int64
		grain string
	}{
		{"one millisecond", 1, 5 * 60 * 1000, "minute"},
		{"one hour", hourMS, 5 * 60 * 1000, "minute"},
		{"over one hour", hourMS + 1, hourMS, "hour"},
		{"six hours", 6 * hourMS, hourMS, "hour"},
		{"one day", dayMS, hourMS, "hour"},
		{"three days", 3 * dayMS, 3 * hourMS, "hour"},
		{"seven days", 7 * dayMS, 6 * hourMS, "hour"},
		{"fifteen days", 15 * dayMS, 12 * hourMS, "hour"},
		{"thirty days", 30 * dayMS, dayMS, "day"},
		{"over thirty days", 30*dayMS + 1, 2 * dayMS, "day"},
		{"one year", 365 * dayMS, 13 * dayMS, "day"},
		{"future four hundred years", 400 * 365 * dayMS, 4867 * dayMS, "day"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &recordingUsageStatReader{}
			engine, _ := newUsageTestEngine(t, time.UnixMilli(from), reader)
			query := fmt.Sprintf("from_ms=%d&to_ms=%d&group_id=7&channel_id=openai&credential_id=11&upstream_model=usage-model", from, from+test.span)
			recorder := performUsageRequest(engine, "test-auth-key", query)
			if recorder.Code != http.StatusOK {
				t.Fatalf("explicit interval response = %d %s", recorder.Code, recorder.Body.String())
			}
			var envelope struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if _, exists := envelope.Data["range"]; exists {
				t.Fatal("explicit interval response must not expose a preset range")
			}
			var data usageResponse
			rawData, err := json.Marshal(envelope.Data)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(rawData, &data); err != nil {
				t.Fatal(err)
			}
			if data.FromMS != from || data.ToMS != from+test.span ||
				data.BucketWidthMS != test.width || string(data.Granularity) != test.grain {
				t.Fatalf("explicit interval changed: %+v", data)
			}
			if len(reader.queries) != 1 {
				t.Fatalf("reader calls = %d, want 1", len(reader.queries))
			}
			actual := reader.queries[0]
			if actual.FromMS != from || actual.ToMS != from+test.span ||
				actual.GroupID == nil || *actual.GroupID != 7 || actual.ChannelID != "openai" ||
				actual.CredentialID == nil || *actual.CredentialID != 11 || actual.UpstreamModel != "usage-model" {
				t.Fatalf("reader interval or filters changed: %+v", actual)
			}
		})
	}
}

func TestUsageAPIRequiresExplicitTimeRange(t *testing.T) {
	t.Parallel()
	for _, query := range []string{
		"", "from_ms=0", "to_ms=3600000", "range=1h", "range=24h&from_ms=0&to_ms=3600000",
		"from_ms=0&to_ms=0", "from_ms=2&to_ms=1", "from_ms=-1&to_ms=1",
		"from_ms=0&to_ms=9007199254740992", "from_ms=0&from_ms=1&to_ms=3600000",
	} {
		t.Run(query, func(t *testing.T) {
			reader := &recordingUsageStatReader{}
			engine, _ := newUsageTestEngine(t, time.Now(), reader)
			recorder := performUsageRequest(engine, "test-auth-key", query)
			if recorder.Code != http.StatusBadRequest || len(reader.queries) != 0 {
				t.Fatalf("invalid interval response = %d %s, reader calls %d", recorder.Code, recorder.Body.String(), len(reader.queries))
			}
		})
	}
}
