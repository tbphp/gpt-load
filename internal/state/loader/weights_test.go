package loader_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestLoaderMapsSchedulingWeights(t *testing.T) {
	db := openMigratedDatabase(t)
	groupWeight := 25
	group := models.Group{
		Name: "weighted", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models:    models.JSON(`[{"id":"gpt-weighted"}]`),
		Overrides: models.JSON(`{}`), Enabled: true, WeightManual: &groupWeight,
	}
	mustCreate(t, db, &group)
	keyWeight := 30
	credentials := []models.Credential{
		{GroupID: group.ID, Data: "cipher-manual", Fingerprint: "fingerprint-manual", Status: models.CredentialStatusActive, WeightManual: &keyWeight},
		{GroupID: group.ID, Data: "cipher-default", Fingerprint: "fingerprint-default", Status: models.CredentialStatusActive},
	}
	for index := range credentials {
		mustCreate(t, db, &credentials[index])
	}

	manager := state.NewManager()
	registry := state.NewCredentialRegistry()
	if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	view := manager.Current().Groups[group.ID]
	if view.WeightManual == nil || *view.WeightManual != groupWeight {
		t.Fatalf("GroupView.WeightManual = %v, want %d", view.WeightManual, groupWeight)
	}

	candidates := registry.CollectCredentialCandidates([]uint{group.ID}, nil, time.Time{})
	if len(candidates) != 2 {
		t.Fatalf("CollectCandidates() = %#v, want two credentials", candidates)
	}
	if candidates[0].WeightManual == nil || *candidates[0].WeightManual != keyWeight {
		t.Fatalf("first WeightManual = %v, want %d", candidates[0].WeightManual, keyWeight)
	}
	for _, candidate := range candidates {
		if candidate.WeightAuto != state.DefaultWeight {
			t.Errorf("credential %d WeightAuto = %d, want %d", candidate.ID, candidate.WeightAuto, state.DefaultWeight)
		}
	}
	if got := manager.Current().ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]["gpt-weighted"]; len(got) != 1 {
		t.Fatalf("route candidates = %#v, want one group", got)
	}
}

func TestLoaderPreservesManualWeightBoundaries(t *testing.T) {
	for _, weight := range []int{0, state.MaxWeight} {
		t.Run(fmt.Sprintf("weight_%d", weight), func(t *testing.T) {
			db := openMigratedDatabase(t)
			group := models.Group{
				Name: "boundary", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
				Models:    models.JSON(`[{"id":"gpt-boundary"}]`),
				Overrides: models.JSON(`{}`), Enabled: true, WeightManual: &weight,
			}
			mustCreate(t, db, &group)
			credential := models.Credential{
				GroupID: group.ID, Data: "cipher", Fingerprint: "fingerprint",
				Status: models.CredentialStatusActive, WeightManual: &weight,
			}
			mustCreate(t, db, &credential)

			manager := state.NewManager()
			registry := state.NewCredentialRegistry()
			if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			view := manager.Current().Groups[group.ID]
			if view.WeightManual == nil || *view.WeightManual != weight {
				t.Fatalf("GroupView.WeightManual = %v, want explicit %d", view.WeightManual, weight)
			}

			candidates := registry.CollectCredentialCandidates([]uint{group.ID}, nil, time.Time{})
			if len(candidates) != 1 || candidates[0].WeightManual == nil || *candidates[0].WeightManual != weight {
				t.Fatalf("CollectCandidates() = %#v, want explicit manual weight %d", candidates, weight)
			}
			if candidates[0].WeightAuto != state.DefaultWeight {
				t.Fatalf("WeightAuto = %d, want %d", candidates[0].WeightAuto, state.DefaultWeight)
			}
		})
	}
}
