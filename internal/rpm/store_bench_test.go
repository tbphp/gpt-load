package rpm

import (
	"testing"
	"time"
)

func BenchmarkRPMRecord(b *testing.B) {
	s := NewStore()
	at := time.Now()
	s.Record(AccessKey, 1, false, at)
	b.ReportAllocs()
	for b.Loop() {
		s.Record(AccessKey, 1, false, at)
	}
}
