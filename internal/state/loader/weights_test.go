package loader_test

import (
	"context"
	"testing"
	"time"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestLoaderMapsSchedulingWeights(t *testing.T) {
	db := openMigratedDatabase(t)
	groupWeight := 25
	group := models.Group{
		Name: "weighted", UpstreamURL: "https://weighted.example.com",
		Protocols: models.JSON(`["openai"]`), Models: models.JSON(`[{"id":"gpt-weighted"}]`),
		Config: models.JSON(`{}`), Enabled: true, WeightManual: &groupWeight,
	}
	mustCreate(t, db, &group)
	keyWeight := 30
	keys := []models.UpstreamKey{
		{GroupID: group.ID, KeyValue: "cipher-manual", KeyHash: "hash-manual", Status: models.UpstreamKeyStatusActive, WeightManual: &keyWeight},
		{GroupID: group.ID, KeyValue: "cipher-default", KeyHash: "hash-default", Status: models.UpstreamKeyStatusActive},
	}
	for index := range keys {
		mustCreate(t, db, &keys[index])
	}

	manager := state.NewManager()
	registry := state.NewKeyRegistry()
	if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	view := manager.Current().Groups[group.ID]
	if view.WeightManual == nil || *view.WeightManual != groupWeight {
		t.Fatalf("GroupView.WeightManual = %v, want %d", view.WeightManual, groupWeight)
	}

	candidates := registry.CollectCandidates([]uint{group.ID}, nil, time.Time{})
	if len(candidates) != 2 {
		t.Fatalf("CollectCandidates() = %#v, want two keys", candidates)
	}
	if candidates[0].WeightManual == nil || *candidates[0].WeightManual != keyWeight {
		t.Fatalf("first WeightManual = %v, want %d", candidates[0].WeightManual, keyWeight)
	}
	for _, candidate := range candidates {
		if candidate.WeightAuto != state.DefaultWeight {
			t.Errorf("key %d WeightAuto = %d, want %d", candidate.ID, candidate.WeightAuto, state.DefaultWeight)
		}
	}
	if got := manager.Current().Candidates[protocol.OpenAI]["gpt-weighted"]; len(got) != 1 {
		t.Fatalf("route candidates = %#v, want one group", got)
	}
}
