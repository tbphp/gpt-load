package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/state"
)

type priorityCredential struct {
	id       uint
	group    uint
	priority int32
	weight   *int
}

// credentialPriorityFixture 按给定分组优先级建组，凭据按 group 归属并带凭据优先级。
func credentialPriorityFixture(t *testing.T, groupPriorities []int32, credentials ...priorityCredential) (*state.ConfigSnapshot, *state.CredentialRegistry) {
	t.Helper()
	input := state.CompileInput{ChannelRegistry: channel.NewRegistry()}
	for index, priority := range groupPriorities {
		id := uint(index + 1)
		input.Groups = append(input.Groups, state.GroupConfig{
			ID: id, Name: fmt.Sprintf("group-%d", id), ChannelID: channel.OpenAI,
			ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true, Priority: priority,
			Models: []state.ModelConfig{{ID: "gpt-4o"}},
		})
	}
	snapshot, err := state.Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Revision = 1
	entries := make([]state.CredentialEntry, 0, len(credentials))
	for _, credential := range credentials {
		entries = append(entries, state.CredentialEntry{ID: credential.id, GroupID: credential.group, Version: 1,
			IdentityGeneration: 1, Status: state.CredentialStatusActive, Fingerprint: fmt.Sprint(credential.id),
			EncryptedValue: "cipher", Priority: credential.priority, WeightManual: credential.weight})
	}
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}
	return snapshot, registry
}

func TestCredentialPriorityLowerLevelReceivesNoSelectionsWhileHigherAvailable(t *testing.T) {
	snapshot, registry := credentialPriorityFixture(t, []int32{0},
		priorityCredential{id: 1, group: 1, priority: 1},
		priorityCredential{id: 2, group: 1, priority: 0, weight: new(100)},
		priorityCredential{id: 3, group: 1, priority: 0},
	)
	// 亲和偏好低档凭据、低档权重更高、单一高档凭据持续命中超过防饥饿阈值，均不能选到低档。
	for _, preferred := range []uint{2, 3, 0} {
		for request := range 500 {
			if got := fairnessPick(t, snapshot, registry, preferred); got != 1 {
				t.Fatalf("preferred %d request %d selected %d, want high-priority credential 1", preferred, request, got)
			}
		}
	}
}

func TestCredentialPriorityAffinityAppliesWithinSelectedLevel(t *testing.T) {
	snapshot, registry := credentialPriorityFixture(t, []int32{0},
		priorityCredential{id: 1, group: 1, priority: 5},
		priorityCredential{id: 2, group: 1, priority: 5},
		priorityCredential{id: 3, group: 1, priority: 0},
	)
	for range 50 {
		if got := fairnessPick(t, snapshot, registry, 2); got != 2 {
			t.Fatalf("same-level affinity selected %d, want 2", got)
		}
	}
}

func TestCredentialPriorityUnavailableHigherLevelFallsThroughInSameRequest(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, *state.CredentialRegistry)
		query   func(Query) Query
		want    uint
	}{
		{name: "cooldown", want: 2, prepare: func(t *testing.T, registry *state.CredentialRegistry) {
			registry.SetCooldown(1, now.Add(time.Hour))
		}},
		{name: "cooldown two levels", want: 3, prepare: func(t *testing.T, registry *state.CredentialRegistry) {
			registry.SetCooldown(1, now.Add(time.Hour))
			registry.SetCooldown(2, now.Add(time.Hour))
		}},
		{name: "blacklisted", want: 2, prepare: func(t *testing.T, registry *state.CredentialRegistry) {
			registry.SetBlacklisted(1)
		}},
		{name: "disabled", want: 2, prepare: func(t *testing.T, registry *state.CredentialRegistry) {
			if err := registry.SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "auth not ready", want: 2, prepare: func(t *testing.T, registry *state.CredentialRegistry) {
			registry.SetCredentialAuthState(1, state.CredentialAuthStateReauthorizationRequired)
		}},
		{name: "model cooldown", want: 2, prepare: func(t *testing.T, registry *state.CredentialRegistry) {
			ref, _ := registry.CredentialRef(1)
			if ok, _ := registry.SetModelCooldown(ref, "gpt-4o", now.Add(time.Hour), now); !ok {
				t.Fatal("model cooldown was not applied")
			}
		}},
		{name: "not allowed", want: 2, query: func(query Query) Query {
			query.AllowedCredentialIDs = map[uint]struct{}{2: {}, 3: {}}
			return query
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, registry := credentialPriorityFixture(t, []int32{0},
				priorityCredential{id: 1, group: 1, priority: 2},
				priorityCredential{id: 2, group: 1, priority: 1},
				priorityCredential{id: 3, group: 1, priority: 0},
			)
			if test.prepare != nil {
				test.prepare(t, registry)
			}
			query := fairnessQuery(1)
			if test.query != nil {
				query = test.query(query)
			}
			for range 20 {
				selected, err := New(snapshot, registry, query).Next()
				if err != nil || selected.CredentialID != test.want {
					t.Fatalf("selected %d, want %d, error=%v", selected.CredentialID, test.want, err)
				}
			}
		})
	}
}

func TestCredentialPriorityTriedHigherLevelReleasesNextLevel(t *testing.T) {
	snapshot, registry := credentialPriorityFixture(t, []int32{0},
		priorityCredential{id: 1, group: 1, priority: 1},
		priorityCredential{id: 2, group: 1, priority: 0},
	)
	iterator := New(snapshot, registry, fairnessQuery(0))
	for _, want := range []uint{1, 2} {
		selected, err := iterator.Next()
		if err != nil || selected.CredentialID != want {
			t.Fatalf("selected %d, want %d, error=%v", selected.CredentialID, want, err)
		}
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("expected exhaustion, got %v", err)
	}
}

func TestCredentialPriorityOneGroupMatchesEquivalentGroupTiers(t *testing.T) {
	retryOrder := func(snapshot *state.ConfigSnapshot, registry *state.CredentialRegistry, skipFailedGroup bool) []uint {
		iterator := New(snapshot, registry, fairnessQuery(0))
		var order []uint
		for {
			selected, err := iterator.Next()
			if err != nil {
				return order
			}
			order = append(order, selected.CredentialID)
			if skipFailedGroup {
				iterator.SkipGroup(selected.GroupID)
			}
			iterator.AdvancePriority(selected)
		}
	}
	split, splitRegistry := credentialPriorityFixture(t, []int32{3, 2, 1},
		priorityCredential{id: 1, group: 1}, priorityCredential{id: 2, group: 1},
		priorityCredential{id: 3, group: 2}, priorityCredential{id: 4, group: 3}, priorityCredential{id: 5, group: 3},
	)
	merged, mergedRegistry := credentialPriorityFixture(t, []int32{0},
		priorityCredential{id: 1, group: 1, priority: 3}, priorityCredential{id: 2, group: 1, priority: 3},
		priorityCredential{id: 3, group: 1, priority: 2}, priorityCredential{id: 4, group: 1, priority: 1},
		priorityCredential{id: 5, group: 1, priority: 1},
	)
	want := []uint{1, 3, 4, 5}
	if got := retryOrder(split, splitRegistry, false); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("group tiers retry order %v, want %v", got, want)
	}
	if got := retryOrder(merged, mergedRegistry, false); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("credential tiers retry order %v, want %v", got, want)
	}
}

func TestCredentialPriorityComparesAcrossGroupsInSameGroupTier(t *testing.T) {
	snapshot, registry := credentialPriorityFixture(t, []int32{5, 5, 4},
		priorityCredential{id: 1, group: 1, priority: 1},
		priorityCredential{id: 2, group: 1, priority: 0},
		priorityCredential{id: 3, group: 2, priority: 0},
		priorityCredential{id: 4, group: 3, priority: 100},
	)
	for range 200 {
		if got := fairnessPick(t, snapshot, registry, 3); got != 1 {
			t.Fatalf("selected %d, want highest credential tier inside highest group tier", got)
		}
	}
	iterator := New(snapshot, registry, fairnessQuery(0))
	first, err := iterator.Next()
	if err != nil || first.CredentialID != 1 || first.CredentialPriority != 1 {
		t.Fatalf("first selection %+v, %v", first, err)
	}
	iterator.AdvancePriority(first)
	second, err := iterator.Next()
	if err != nil || (second.CredentialID != 2 && second.CredentialID != 3) {
		t.Fatalf("retry selected %d, want next credential tier of the same group tier, error=%v", second.CredentialID, err)
	}
	iterator.AdvancePriority(second)
	third, err := iterator.Next()
	if err != nil || third.CredentialID != 4 {
		t.Fatalf("retry after group tier selected %d, want lower group tier 4 despite its higher credential priority, error=%v", third.CredentialID, err)
	}
}

func TestCredentialPrioritySkippedGroupFallsToLowerGroupTier(t *testing.T) {
	snapshot, registry := credentialPriorityFixture(t, []int32{0, -1},
		priorityCredential{id: 1, group: 1, priority: 1},
		priorityCredential{id: 2, group: 1, priority: 0},
		priorityCredential{id: 3, group: 2, priority: 0},
	)
	iterator := New(snapshot, registry, fairnessQuery(0))
	selected, err := iterator.Next()
	if err != nil || selected.CredentialID != 1 {
		t.Fatalf("first selection %d, %v", selected.CredentialID, err)
	}
	iterator.SkipGroup(selected.GroupID)
	iterator.AdvancePriority(selected)
	selected, err = iterator.Next()
	if err != nil || selected.CredentialID != 3 {
		t.Fatalf("skipped group retry selected %d, want 3, error=%v", selected.CredentialID, err)
	}
}

func TestCredentialPriorityEqualLevelsKeepWeightedDistribution(t *testing.T) {
	snapshot, registry := credentialPriorityFixture(t, []int32{0},
		priorityCredential{id: 1, group: 1, priority: 5, weight: new(1)},
		priorityCredential{id: 2, group: 1, priority: 5, weight: new(3)},
		priorityCredential{id: 3, group: 1, priority: 0, weight: new(100)},
	)
	counts := make(map[uint]int)
	for range 160 {
		counts[fairnessPick(t, snapshot, registry, 0)]++
	}
	if counts[1] != 40 || counts[2] != 120 || counts[3] != 0 {
		t.Fatalf("equal-priority weighted allocation = %v", counts)
	}
}

func TestCredentialPriorityUniformLevelKeepsExistingSequence(t *testing.T) {
	sequence := func(priority int32) []uint {
		snapshot, registry := credentialPriorityFixture(t, []int32{0, 0},
			priorityCredential{id: 1, group: 1, priority: priority, weight: new(1)},
			priorityCredential{id: 2, group: 1, priority: priority, weight: new(4)},
			priorityCredential{id: 3, group: 2, priority: priority},
		)
		var picks []uint
		for request := range 400 {
			preferred := uint(0)
			if request < 150 {
				preferred = 1
			}
			picks = append(picks, fairnessPick(t, snapshot, registry, preferred))
		}
		return picks
	}
	baseline := sequence(0)
	for _, priority := range []int32{7, -7} {
		if got := sequence(priority); fmt.Sprint(got) != fmt.Sprint(baseline) {
			t.Fatalf("uniform credential priority %d changed selection sequence", priority)
		}
	}
}

func TestCredentialPriorityChargeReplayKeepsSelectedCredential(t *testing.T) {
	snapshot, registry := credentialPriorityFixture(t, []int32{0},
		priorityCredential{id: 1, group: 1, priority: 1},
		priorityCredential{id: 2, group: 1, priority: 0},
	)
	iterator := New(snapshot, registry, fairnessQuery(0))
	selected, err := iterator.Next()
	if err != nil || selected.CredentialID != 1 {
		t.Fatalf("selected %d, %v", selected.CredentialID, err)
	}
	ref, _ := registry.CredentialRef(1)
	if !iterator.ChargeReplay(selected, ref) {
		t.Fatal("replay on the selected high-priority credential was not charged")
	}
}

func TestCredentialPriorityChangeReentersMembersWithoutCatchUpBurst(t *testing.T) {
	snapshot, registry := credentialPriorityFixture(t, []int32{0},
		priorityCredential{id: 1, group: 1, priority: 1},
		priorityCredential{id: 2, group: 1, priority: 0},
	)
	for range 1000 {
		if got := fairnessPick(t, snapshot, registry, 0); got != 1 {
			t.Fatalf("selected %d before priority change", got)
		}
	}
	if err := registry.UpdateCredentialConfig(1, state.CredentialStatusActive, nil, 0); err != nil {
		t.Fatal(err)
	}
	registry.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		for _, member := range d.Members {
			if !member.Pending {
				t.Fatal("credential priority change did not mark every member pending")
			}
		}
	})
	counts := make(map[uint]int)
	for range 100 {
		counts[fairnessPick(t, snapshot, registry, 0)]++
	}
	if counts[1] != 50 || counts[2] != 50 {
		t.Fatalf("credential priority change caused catch-up burst: %v", counts)
	}
	if err := registry.UpdateCredentialConfig(1, state.CredentialStatusActive, nil, 0); err != nil {
		t.Fatal(err)
	}
	registry.SchedulingState().WithLock(func(d *state.SchedulingLedger) {
		if d.Members[1].Pending || d.Members[2].Pending {
			t.Fatal("unchanged credential priority reset history")
		}
	})
}
