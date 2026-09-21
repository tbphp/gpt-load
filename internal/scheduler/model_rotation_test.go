package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func modelRotationFixture(t *testing.T) (*state.ConfigSnapshot, *state.CredentialRegistry) {
	t.Helper()
	snapshot, err := state.Compile(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "multiple", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true,
				Models: []state.ModelConfig{{ID: "m1", Alias: "public"}, {ID: "m2", Alias: "public"}, {ID: "m3", Alias: "public"}}},
			{ID: 2, Name: "single", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Enabled: true,
				Models: []state.ModelConfig{{ID: "other", Alias: "public"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Revision = 1
	registry := state.NewCredentialRegistry()
	for _, entry := range []state.CredentialEntry{
		{ID: 1, GroupID: 1}, {ID: 2, GroupID: 1}, {ID: 3, GroupID: 2},
	} {
		entry.Version, entry.IdentityGeneration = 1, 1
		entry.Status, entry.Fingerprint, entry.EncryptedValue = state.CredentialStatusActive, "fixture", "cipher"
		if err := registry.ApplyCredentialImport(entry.GroupID, []state.CredentialEntry{entry}); err != nil {
			t.Fatal(err)
		}
	}
	return snapshot, registry
}

func modelRotationQuery(ids ...uint) Query {
	query := Query{ClientProtocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion, ExternalModel: modelPointer("public")}
	if len(ids) > 0 {
		query.AllowedCredentialIDs = make(map[uint]struct{}, len(ids))
		for _, id := range ids {
			query.AllowedCredentialIDs[id] = struct{}{}
		}
	}
	return query
}

func modelRotationPick(t *testing.T, snapshot *state.ConfigSnapshot, registry *state.CredentialRegistry, query Query) Selection {
	t.Helper()
	selection, err := New(snapshot, registry, query).Next()
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func TestModelRotationSharesProgressAcrossInterleavedRequestsAndCredentials(t *testing.T) {
	snapshot, registry := modelRotationFixture(t)
	if got := CandidateGroupIDsForQuery(snapshot, modelRotationQuery()); !reflect.DeepEqual(got, []uint{1, 2}) {
		t.Fatalf("candidate groups = %v", got)
	}
	for index, want := range []string{"m1", "m2", "m3", "m1", "m2", "m3"} {
		query := modelRotationQuery(uint(index%2 + 1))
		for range 9 {
			modelRotationPick(t, snapshot, registry, modelRotationQuery(3))
		}
		// 查询路由和配置重新发布不能消费或重置轮次。
		if _, err := Inspect(snapshot, registry.Snapshot(), query, time.Now()); err != nil {
			t.Fatal(err)
		}
		snapshot.Revision++
		selection := modelRotationPick(t, snapshot, registry, query)
		if *selection.UpstreamModelID != want {
			t.Fatalf("selection %d model = %q, want %q", index, *selection.UpstreamModelID, want)
		}
	}
}

func TestModelRotationPreservesCredentialShares(t *testing.T) {
	for _, strategy := range []state.RouteStrategy{state.RouteStrategyNativeFirst, state.RouteStrategyWeightedMix} {
		t.Run(string(strategy), func(t *testing.T) {
			snapshot, registry := modelRotationFixture(t)
			snapshot.Settings.RouteStrategy = strategy
			credentials := map[uint]int{}
			models := map[string]int{}
			for range 600 {
				selection := modelRotationPick(t, snapshot, registry, modelRotationQuery(1, 3))
				credentials[selection.CredentialID]++
				models[*selection.UpstreamModelID]++
			}
			if !reflect.DeepEqual(credentials, map[uint]int{1: 300, 3: 300}) ||
				!reflect.DeepEqual(models, map[string]int{"m1": 100, "m2": 100, "m3": 100, "other": 300}) {
				t.Fatalf("credential/model distribution = %v / %v", credentials, models)
			}
		})
	}
}

func TestModelRotationConcurrentRequestsRemainFair(t *testing.T) {
	snapshot, registry := modelRotationFixture(t)
	results := make(chan string, 300)
	var workers sync.WaitGroup
	for range 300 {
		workers.Go(func() {
			selection, err := New(snapshot, registry, modelRotationQuery(1, 2)).Next()
			if err != nil {
				results <- fmt.Sprint(err)
				return
			}
			results <- *selection.UpstreamModelID
		})
	}
	workers.Wait()
	close(results)
	counts := map[string]int{}
	for model := range results {
		counts[model]++
	}
	if !reflect.DeepEqual(counts, map[string]int{"m1": 100, "m2": 100, "m3": 100}) {
		t.Fatalf("concurrent distribution = %v", counts)
	}
}

func TestModelRotationSkipsCooldownWithoutLosingOtherModels(t *testing.T) {
	snapshot, registry := modelRotationFixture(t)
	ref, _ := registry.CredentialRef(1)
	query := modelRotationQuery(1)
	modelRotationPick(t, snapshot, registry, query)
	if ok, _ := registry.SetModelCooldown(ref, "m2", time.Now().Add(time.Hour), time.Now()); !ok {
		t.Fatal("set cooldown")
	}
	inspection, err := Inspect(snapshot, registry.Snapshot(), query, time.Now())
	if err != nil || !inspection.Routable {
		t.Fatalf("inspection = %+v, %v", inspection, err)
	}
	if got := *modelRotationPick(t, snapshot, registry, query).UpstreamModelID; got != "m3" {
		t.Fatalf("selected cooled model or lost rotation: %s", got)
	}
	registry.ClearModelCooldowns(1)
	for _, want := range []string{"m2", "m1", "m3", "m2", "m1", "m3"} {
		if got := *modelRotationPick(t, snapshot, registry, query).UpstreamModelID; got != want {
			t.Fatalf("recovery model=%s, want %s without repeated catch-up allocations", got, want)
		}
	}
}

func TestModelRotationLocalCooldownDoesNotStarveOtherCredentials(t *testing.T) {
	for _, test := range []struct {
		name     string
		cooldown map[uint][]string
		want     map[string]int
	}{
		{name: "all healthy", want: map[string]int{"m1": 20, "m2": 20, "m3": 20}},
		{name: "one available model", cooldown: map[uint][]string{1: {"m2", "m3"}}, want: map[string]int{"m1": 30, "m2": 15, "m3": 15}},
		{name: "overlapping model subsets", cooldown: map[uint][]string{1: {"m3"}, 2: {"m1"}}, want: map[string]int{"m1": 20, "m2": 20, "m3": 20}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, registry := modelRotationFixture(t)
			now := time.Now()
			for id, models := range test.cooldown {
				ref, exists := registry.CredentialRef(id)
				if !exists {
					t.Fatalf("credential %d missing", id)
				}
				for _, model := range models {
					if ok, _ := registry.SetModelCooldown(ref, model, now.Add(time.Hour), now); !ok {
						t.Fatal("set model cooldown")
					}
				}
			}
			models, credentials := map[string]int{}, map[uint]int{}
			for range 60 {
				selection := modelRotationPick(t, snapshot, registry, modelRotationQuery(1, 2))
				models[*selection.UpstreamModelID]++
				credentials[selection.CredentialID]++
			}
			if !reflect.DeepEqual(models, test.want) || !reflect.DeepEqual(credentials, map[uint]int{1: 30, 2: 30}) {
				t.Fatalf("models=%v credentials=%v, want models=%v and equal credential shares", models, credentials, test.want)
			}
		})
	}
}

func TestModelRotationCheckpointPreservesPendingOrderAfterLocalCooldown(t *testing.T) {
	snapshot, registry := modelRotationFixture(t)
	ref, _ := registry.CredentialRef(1)
	now := time.Now()
	for _, model := range []string{"m2", "m3"} {
		if ok, _ := registry.SetModelCooldown(ref, model, now.Add(time.Hour), now); !ok {
			t.Fatal("set model cooldown")
		}
	}
	for _, id := range []uint{1, 2, 1} {
		modelRotationPick(t, snapshot, registry, modelRotationQuery(id))
	}
	checkpoint := registry.SchedulingState().CaptureCheckpoint()
	// 检查点必须独立于后续分配对共享队列的修改。
	modelRotationPick(t, snapshot, registry, modelRotationQuery(2))
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	var saved state.SchedulingCheckpoint
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	_, restored := modelRotationFixture(t)
	restored.SchedulingState().SyncGroups(snapshot)
	restored.SchedulingState().RestoreCheckpoint(saved)
	for _, want := range []string{"m3", "m2", "m1"} {
		if got := *modelRotationPick(t, snapshot, restored, modelRotationQuery(2)).UpstreamModelID; got != want {
			t.Fatalf("restored model=%s, want %s", got, want)
		}
	}
}

func TestModelRotationPreservesRetryAndRefreshReplay(t *testing.T) {
	snapshot, registry := modelRotationFixture(t)
	query := modelRotationQuery(1)
	iterator := New(snapshot, registry, query)
	selection, err := iterator.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("ordinary retry repeated the credential: %v", err)
	}
	ref, _ := registry.CredentialRef(1)
	if !iterator.ChargeReplay(selection, ref) {
		t.Fatal("explicit refresh replay rejected")
	}
	if got := *modelRotationPick(t, snapshot, registry, query).UpstreamModelID; got != "m2" {
		t.Fatalf("refresh replay rerolled the model: %s", got)
	}
}

func TestModelRotationCheckpointResumesAndResourceRequestsDoNotAdvance(t *testing.T) {
	snapshot, registry := modelRotationFixture(t)
	modelRotationPick(t, snapshot, registry, modelRotationQuery(1))
	query := modelRotationQuery(1)
	query.ClientProtocol, query.Operation, query.ExternalModel = protocol.OpenAIResponses, execution.OperationResponsesRetrieve, nil
	if selection := modelRotationPick(t, snapshot, registry, query); selection.UpstreamModelID != nil {
		t.Fatal("resource request selected a model")
	}
	raw, err := json.Marshal(registry.SchedulingState().CaptureCheckpoint())
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint state.SchedulingCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	_, restored := modelRotationFixture(t)
	restored.SchedulingState().SyncGroups(snapshot)
	restored.SchedulingState().RestoreCheckpoint(checkpoint)
	if got := *modelRotationPick(t, snapshot, restored, modelRotationQuery(2)).UpstreamModelID; got != "m2" {
		t.Fatalf("restored model = %s, want m2", got)
	}
}

func TestModelRotationHonorsExistingRouteTiers(t *testing.T) {
	snapshot, registry := modelRotationFixture(t)
	routes := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]["public"]
	for index := range routes {
		if routes[index].UpstreamModelID == "m2" {
			routes[index].Mode = channel.RouteConverted
		}
	}
	for _, want := range []string{"m1", "m3", "m1", "m3"} {
		if got := *modelRotationPick(t, snapshot, registry, modelRotationQuery(1)).UpstreamModelID; got != want {
			t.Fatalf("native-first model = %s, want %s", got, want)
		}
	}
	snapshot.Settings.RouteStrategy = state.RouteStrategyWeightedMix
	for _, want := range []string{"m2", "m1", "m3"} {
		if got := *modelRotationPick(t, snapshot, registry, modelRotationQuery(1)).UpstreamModelID; got != want {
			t.Fatalf("weighted-mix model = %s, want %s", got, want)
		}
	}
}

func TestModelRotationContinuesAfterModelChangesAndCleansDeletedGroups(t *testing.T) {
	snapshot, registry := modelRotationFixture(t)
	query := modelRotationQuery(1)
	modelRotationPick(t, snapshot, registry, query)
	modelRotationPick(t, snapshot, registry, query)
	group := snapshot.Groups[1]
	group.Models = slices.DeleteFunc(group.Models, func(model state.ModelConfig) bool { return model.ID == "m2" })
	group.Models = append(group.Models, state.ModelConfig{ID: "m4", Alias: "public"})
	snapshot.Groups[1] = group
	byModel := snapshot.ExecutionCandidates[query.ClientProtocol][query.Operation]
	byModel["public"] = slices.DeleteFunc(byModel["public"], func(target state.RouteTarget) bool { return target.UpstreamModelID == "m2" })
	for _, target := range byModel["public"] {
		if target.GroupID == 1 {
			target.UpstreamModelID = "m4"
			byModel["public"] = append(byModel["public"], target)
			break
		}
	}
	snapshot.Revision++
	for _, want := range []string{"m3", "m1", "m4"} {
		if got := *modelRotationPick(t, snapshot, registry, query).UpstreamModelID; got != want {
			t.Fatalf("configuration change model=%s, want %s with existing order preserved", got, want)
		}
	}
	delete(snapshot.Groups, 1)
	delete(snapshot.GroupCatalog, 1)
	snapshot.Revision++
	registry.SchedulingState().SyncGroups(snapshot)
	if got := registry.SchedulingState().CaptureCheckpoint().ModelCursors; len(got) != 0 {
		t.Fatalf("deleted group retained cursors: %v", got)
	}
}
