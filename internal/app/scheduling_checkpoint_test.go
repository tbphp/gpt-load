package app

import (
	"context"
	"testing"

	"gpt-load/internal/state"
)

func TestRuntimeCheckpointRestoresOnlyMatchingSchedulingIdentities(t *testing.T) {
	makeRegistry := func(generations map[uint]uint64) *state.CredentialRegistry {
		r := state.NewCredentialRegistry()
		var entries []state.CredentialEntry
		for id, generation := range generations {
			entries = append(entries, state.CredentialEntry{ID: id, GroupID: 1, Version: 1,
				IdentityGeneration: generation, Fingerprint: "fixture", EncryptedValue: "cipher",
				Status: state.CredentialStatusActive})
		}
		if err := r.ReplaceCredentials(entries); err != nil {
			t.Fatal(err)
		}
		return r
	}
	dir := t.TempDir()
	original := makeRegistry(map[uint]uint64{1: 1, 2: 1})
	original.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		d.Started, d.Sequence, d.LastMember, d.Consecutive = true, 8, 2, 3
		d.Watermark = state.SchedulingProgress{Whole: 321}
		d.Members[1].Progress = state.SchedulingProgress{Whole: 123, Fraction: 456}
		d.Members[1].LastSelected = 5
		d.Members[2].Progress = d.Watermark
		d.Members[2].LastSelected = 8
	})
	if err := NewFileRuntimeStateCheckpoint(dir, original, nil, nil).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded := makeRegistry(map[uint]uint64{1: 1, 2: 2, 3: 1})
	if err := NewFileRuntimeStateCheckpoint(dir, loaded, nil, nil).Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		if d.Sequence != 8 || d.Members[1].Progress != (state.SchedulingProgress{Whole: 123, Fraction: 456}) {
			t.Fatalf("matching progress was not restored: sequence=%d progress=%+v", d.Sequence, d.Members[1].Progress)
		}
		if !d.Members[2].Pending || !d.Members[3].Pending || d.LastMember != 0 {
			t.Fatal("new/replaced identities inherited old progress or streak")
		}
	})
}
