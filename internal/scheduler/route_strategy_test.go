package scheduler

import (
	"errors"
	"math/rand"
	"slices"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

type routeStrategyRandSource int64

func (source routeStrategyRandSource) Int63() int64 { return int64(source) }
func (routeStrategyRandSource) Seed(int64)          {}

func setSnapshotRouteStrategy(t *testing.T, snapshot *state.ConfigSnapshot, strategy string) {
	t.Helper()
	settings, err := state.ResolveRuntimeSettings(config.Settings{"route_strategy": strategy})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	snapshot.Settings = settings
}

func TestIteratorRouteStrategyUsesEffectiveWeightsAcrossModes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		strategy      string
		manualWeights bool
		tickets       int64
		wantConverted int
	}{
		{name: "native first", strategy: "native_first", manualWeights: true, tickets: 101},
		{name: "mixed manual weights", strategy: "weighted_mix", manualWeights: true, tickets: 101, wantConverted: 100},
		{name: "mixed default weights", strategy: "weighted_mix", tickets: 100, wantConverted: 50},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := channelSchedulerSnapshot(t)
			setSnapshotRouteStrategy(t, snapshot, test.strategy)
			if test.manualWeights {
				for groupID, weight := range map[uint]int{1: 100, 2: 1} {
					group := snapshot.Groups[groupID]
					group.WeightManual = &weight
					snapshot.Groups[groupID] = group
				}
			}
			source := fakeCredentialSource{keys: []state.CredentialMeta{
				{ID: 11, GroupID: 1, WeightAuto: 1},
				{ID: 21, GroupID: 2, WeightAuto: 1},
			}}
			converted := 0
			for ticket := range test.tickets {
				selection, err := New(snapshot, source, Query{
					ClientProtocol: protocol.OpenAICompletions,
					ExternalModel:  modelPointer("public"),
				}, rand.New(routeStrategyRandSource(ticket))).Next()
				if err != nil {
					t.Fatalf("ticket %d Next() error = %v", ticket, err)
				}
				wantMode, wantChannel, wantModel := channel.RouteNative, channel.OpenAI, "native-model"
				if selection.CredentialID == 11 {
					converted++
					wantMode, wantChannel, wantModel = channel.RouteConverted, channel.Anthropic, "converted-model"
				}
				if selection.RouteMode != wantMode || selection.ChannelID != wantChannel ||
					selection.ResolvedTarget.ChannelID != wantChannel ||
					selection.UpstreamModelID == nil || *selection.UpstreamModelID != wantModel {
					t.Fatalf("ticket %d Selection lost route target: %#v", ticket, selection)
				}
			}
			if converted != test.wantConverted {
				t.Fatalf("converted selections = %d/%d, want %d", converted, test.tickets, test.wantConverted)
			}
		})
	}
}

func TestIteratorWeightedMixFreezesStrategyAndPrefersEligibleConvertedCredential(t *testing.T) {
	t.Parallel()

	snapshot := channelSchedulerSnapshot(t)
	setSnapshotRouteStrategy(t, snapshot, "weighted_mix")
	iterator := New(snapshot, fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1}, {ID: 21, GroupID: 2},
	}}, Query{
		ClientProtocol:        protocol.OpenAICompletions,
		ExternalModel:         modelPointer("public"),
		PreferredCredentialID: 11,
	}, rand.New(routeStrategyRandSource(2500)))
	setSnapshotRouteStrategy(t, snapshot, "native_first")
	for index, want := range []uint{11, 21} {
		selection, err := iterator.Next()
		if err != nil || selection.CredentialID != want {
			t.Fatalf("Next() %d = (%#v, %v), want credential %d", index, selection, err, want)
		}
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() after exhaustion error = %v", err)
	}
}

func TestIteratorWeightedMixPreservesStoredResponsesPriority(t *testing.T) {
	t.Parallel()

	for _, preferred := range []uint{0, 31} {
		snapshot := responsesStoreSchedulerSnapshot(t, true)
		setSnapshotRouteStrategy(t, snapshot, "weighted_mix")
		weight := 100
		group := snapshot.Groups[3]
		group.WeightManual = &weight
		snapshot.Groups[3] = group
		iterator := New(snapshot, fakeCredentialSource{keys: []state.CredentialMeta{
			{ID: 11, GroupID: 1, WeightAuto: 1}, {ID: 21, GroupID: 2, WeightAuto: 1},
			{ID: 31, GroupID: 3, WeightAuto: 1}, {ID: 41, GroupID: 4, WeightAuto: 1},
		}}, Query{
			ClientProtocol:           protocol.OpenAIResponses,
			Operation:                execution.OperationResponsesCreate,
			ResponsesStorePreference: execution.ResponsesStorePreferencePreferStored,
			ExternalModel:            modelPointer("gpt"),
			PreferredCredentialID:    preferred,
		}, rand.New(routeStrategyRandSource(50)))
		for index, want := range []uint{21, 31} {
			selection, err := iterator.Next()
			if err != nil || selection.CredentialID != want || selection.ResponsesStoreDowngraded != (index > 0) {
				t.Fatalf("preferred %d Next() %d = (%#v, %v), want credential %d with preserved store semantics", preferred, index, selection, err, want)
			}
		}
	}
}

func TestIteratorWeightedMixKeepsNativeRequirement(t *testing.T) {
	t.Parallel()

	snapshot := responsesStoreSchedulerSnapshot(t, true)
	setSnapshotRouteStrategy(t, snapshot, "weighted_mix")
	query := Query{
		ClientProtocol:        protocol.OpenAIResponses,
		Operation:             execution.OperationResponsesCreate,
		RouteRequirement:      execution.RouteRequirementNative,
		ExternalModel:         modelPointer("gpt"),
		PreferredCredentialID: 31,
	}
	if got := CandidateGroupIDsForQuery(snapshot, query); !slices.Equal(got, []uint{2}) {
		t.Fatalf("CandidateGroupIDsForQuery() = %#v, want lifecycle-capable native group [2]", got)
	}
	iterator := New(snapshot, fakeCredentialSource{keys: []state.CredentialMeta{
		{ID: 11, GroupID: 1}, {ID: 21, GroupID: 2}, {ID: 31, GroupID: 3}, {ID: 41, GroupID: 4},
	}}, query, rand.New(zeroRandSource{}))
	selection, err := iterator.Next()
	if err != nil || selection.CredentialID != 21 || selection.RouteMode != channel.RouteNative {
		t.Fatalf("Next() = (%#v, %v), want lifecycle-capable native credential 21", selection, err)
	}
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() after native exhaustion error = %v", err)
	}
}

func TestIteratorWeightedMixHonorsLiveHealthAndRequestExclusions(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	snapshot := channelSchedulerSnapshot(t)
	setSnapshotRouteStrategy(t, snapshot, "weighted_mix")
	registry := state.NewCredentialRegistry()
	entries := make([]state.CredentialEntry, 0, 6)
	zero := 0
	for _, credential := range []struct {
		id, groupID uint
	}{
		{11, 1}, {12, 1}, {13, 1}, {14, 1}, {21, 2}, {22, 2},
	} {
		entry := state.CredentialEntry{
			ID: credential.id, GroupID: credential.groupID, Status: state.CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "test-fingerprint", EncryptedValue: "cipher",
		}
		if entry.ID == 14 {
			entry.WeightManual = &zero
		}
		entries = append(entries, entry)
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	allowed := map[uint]struct{}{11: {}, 12: {}, 14: {}, 21: {}, 22: {}}
	iterator := newWithClock(snapshot, registry, Query{
		ClientProtocol:       protocol.OpenAICompletions,
		ExternalModel:        modelPointer("public"),
		AllowedCredentialIDs: allowed,
	}, rand.New(zeroRandSource{}), func() time.Time { return now })
	allowed[13] = struct{}{}
	for index, want := range []uint{11, 21} {
		selection, err := iterator.Next()
		if err != nil || selection.CredentialID != want {
			t.Fatalf("Next() %d = (%#v, %v), want credential %d", index, selection, err, want)
		}
		if index == 0 && !registry.SetCooldown(12, now.Add(time.Minute)) {
			t.Fatal("SetCooldown() did not find converted credential 12")
		}
	}
	iterator.SkipGroup(2)
	if _, err := iterator.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("Next() after skip and exclusions error = %v", err)
	}
}
