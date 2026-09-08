package state

import "testing"

func TestSchedulingProgressRetainsSmallStepsAtLargeValues(t *testing.T) {
	p := SchedulingProgress{Whole: 1 << 60, Fraction: ^uint64(0) - 10}
	next, ok := p.Advance(10000)
	if !ok || next.Compare(p) <= 0 || next.Whole != p.Whole+1 {
		t.Fatalf("small step lost or carry broken: %+v -> %+v (%v)", p, next, ok)
	}
	if _, ok := (SchedulingProgress{Whole: ^uint64(0), Fraction: ^uint64(0)}).Advance(1); ok {
		t.Fatal("overflow was not detected")
	}
}

func TestSchedulingCheckpointRejectsIncompatibleCoordinates(t *testing.T) {
	s := NewSchedulingState()
	for _, checkpoint := range []SchedulingCheckpoint{{Version: 2}, {Version: 1, Sequence: ^uint64(0)},
		{Version: 1, Watermark: SchedulingProgress{Whole: ^uint64(0)}}} {
		if got := s.RestoreCheckpoint(checkpoint); got != 0 {
			t.Fatalf("incompatible checkpoint restored %d members", got)
		}
	}
}
