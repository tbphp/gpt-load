package requestlog

import (
	"testing"
	"time"

	"gpt-load/internal/rpm"
	"gpt-load/internal/storage/models"
)

func TestRPMQueriesKeepPeaksAndMissingKeysSeparate(t *testing.T) {
	db := openRequestLogQueryDB(t)
	s := newRequestLogTestService(db)
	store := rpm.NewStore()
	s.SetRPMStore(store)
	now := time.Now().Truncate(time.Minute)
	for _, row := range []models.RPMStat{
		{Kind: "access_key", KeyID: 1, MinuteMS: now.Add(-2 * time.Hour).UnixMilli(), SessionID: "old", Requests: 2, Peak: 100},
		{Kind: "access_key", KeyID: 1, MinuteMS: now.Add(-2 * time.Minute).UnixMilli(), SessionID: "old", Requests: 2, Peak: 20},
		{Kind: "credential", KeyID: 1, MinuteMS: now.Add(-time.Minute).UnixMilli(), SessionID: "old", Requests: 2, Peak: 50},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	store.Record(rpm.AccessKey, 2, false, now)
	peaks, err := s.RPMPeaks(t.Context(), rpm.AccessKey, []uint{1, 2, 3}, now)
	if err != nil || len(peaks) != 2 || peaks[1] != 20 || peaks[2] != 1 {
		t.Fatalf("peaks=%v error=%v", peaks, err)
	}
	report, err := s.QueryRPM(t.Context(), rpm.AccessKey, 1, now)
	if err != nil || report.Peak == nil || *report.Peak != 20 || report.BucketWidthMS != 60_000 || len(report.Points) != 1 || report.FromMS != now.Add(-time.Hour).UnixMilli() {
		t.Fatalf("report=%+v error=%v", report, err)
	}
	missing, err := s.QueryRPM(t.Context(), rpm.AccessKey, 3, now)
	if err != nil || missing.Peak != nil || missing.Current != nil || len(missing.Points) != 0 {
		t.Fatalf("missing was converted to zero: %+v %v", missing, err)
	}
}
