package rpm

import (
	"sync"
	"testing"
	"time"
)

func TestRecordCountsArrivalAndRejectionIndependentlyOfUpstream(t *testing.T) {
	s := NewStore()
	at := time.Unix(120, 0)
	s.Record(AccessKey, 1, false, at)
	s.Record(AccessKey, 1, true, at)
	s.Record(Credential, 1, false, at)
	s.Record(Credential, 2, false, at)
	if got := s.Current(AccessKey, 1, at); got != (Counter{Requests: 2, Rejected: 1}) {
		t.Fatalf("access RPM = %+v", got)
	}
	if got := s.Current(Credential, 1, at); got != (Counter{Requests: 1}) {
		t.Fatalf("credential RPM = %+v", got)
	}
	if got := s.Current(AccessKey, 1, at.Add(time.Minute)); got != (Counter{}) {
		t.Fatalf("expired RPM = %+v", got)
	}
}

func TestMinutePeakUsesRollingWindowAndSurvivesIdleBoundary(t *testing.T) {
	s := NewStore()
	s.Record(AccessKey, 1, false, time.Unix(179, 0))
	s.Record(AccessKey, 1, true, time.Unix(180, 0))
	rows := s.Snapshots(time.Unix(240, 0), false, 0)
	peaks := map[int64]int64{}
	for _, row := range rows {
		peaks[row.MinuteMS] = row.Peak
	}
	if peaks[120_000] != 1 || peaks[180_000] != 2 {
		t.Fatalf("minute peaks = %v", peaks)
	}
	if got := s.Current(AccessKey, 1, time.Unix(240, 0)); got.Requests != 0 {
		t.Fatalf("RPM after expiry = %+v", got)
	}

	s = NewStore()
	s.Record(Credential, 4, false, time.Unix(179, 0))
	rows = s.Snapshots(time.Unix(181, 0), false, 0)
	if len(rows) != 2 || rows[1].MinuteMS != 180_000 || rows[1].Peak != 1 || rows[1].Requests != 0 {
		t.Fatalf("idle boundary lost rolling peak: %+v", rows)
	}
}

func TestCheckpointAckDoesNotLoseConcurrentUpdates(t *testing.T) {
	s := NewStore()
	at := time.Unix(120, 0)
	s.Record(AccessKey, 2, false, at)
	first := s.Snapshots(at, true, 10)
	if len(first) != 1 {
		t.Fatalf("pending = %+v", first)
	}
	s.Record(AccessKey, 2, true, at.Add(time.Second))
	s.Ack(first)
	second := s.Snapshots(at.Add(time.Second), true, 10)
	if len(second) != 1 || second[0].Requests != 2 || second[0].Rejected != 1 || second[0].Version <= first[0].Version {
		t.Fatalf("concurrent update lost: %+v", second)
	}
	s.Ack(second)
	if dirty := s.Snapshots(at.Add(time.Second), true, 10); len(dirty) != 0 {
		t.Fatalf("acked data remains dirty: %+v", dirty)
	}
	if visible := s.Snapshots(at.Add(time.Second), false, 0); len(visible) != 1 {
		t.Fatalf("live read lost acked minute: %+v", visible)
	}
}

func TestConcurrentRecordsAndRetention(t *testing.T) {
	s := NewStore()
	at := time.Unix(120, 0)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 100 {
				s.Record(AccessKey, 1, false, at)
			}
		})
	}
	wg.Wait()
	if got := s.Current(AccessKey, 1, at).Requests; got != 2000 {
		t.Fatalf("requests = %d", got)
	}
	s.DiscardBefore(180_000)
	if got := s.Snapshots(at, false, 0); len(got) != 0 {
		t.Fatalf("retained expired buckets: %+v", got)
	}
}
