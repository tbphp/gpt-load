package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/health"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
)

func TestValidationWorkerUsesExplicitModelAndCanonicalRepresentativeProtocol(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{
			1: validationGroup(
				[]protocol.Protocol{
					protocol.OpenAIResponses,
					protocol.OpenAIChatCompletions,
				},
				" explicit-model ",
				nil,
			),
		}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{
		protocol: protocol.OpenAIChatCompletions,
		model:    "explicit-model",
		apiKey:   "plain-key-7",
	}}; !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
}

func TestValidationWorkerFallsBackToFirstRealModelID(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{
			1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, " \t", []state.ModelConfig{{ID: "  real-model  ", Alias: "external-model"}}),
		}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{protocol: protocol.OpenAIChatCompletions, model: "real-model", apiKey: "plain-key-7"}}; !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
}

func TestValidationWorkerUsesResponsesProbeForResponsesOnlyGroup(t *testing.T) {
	t.Parallel()

	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{
			1: validationGroup(
				[]protocol.Protocol{protocol.OpenAIResponses},
				"gpt-5",
				nil,
			),
		}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	want := []validationProbeCall{{
		protocol: protocol.OpenAIResponses,
		model:    "gpt-5",
		apiKey:   "plain-key-7",
	}}
	if got := probes.calls(); !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
}

func TestValidationSignatureUsesCanonicalLengthPrefixedEncoding(t *testing.T) {
	group := state.GroupView{
		ID:              42,
		UpstreamURL:     "https://upstream.example.com/v1",
		Protocols:       []protocol.Protocol{protocol.OpenAIChatCompletions},
		ValidationModel: " model-a ",
		HeaderRules: state.HeaderRules{
			Set: map[string]string{
				"X-Zeta":  "last",
				"x-alpha": "first",
			},
			Remove: []string{"X-Old", "x-beta"},
		},
	}

	target, ok := buildGroupValidationTarget(group)
	if !ok {
		t.Fatal("buildGroupValidationTarget() ok = false, want true")
	}
	if target.protocol != protocol.OpenAIChatCompletions || target.model != "model-a" {
		t.Fatalf("target protocol/model = %q/%q, want openai/model-a", target.protocol, target.model)
	}
	const want = "818ce5cc7bbead79d23f5f0d03cdaef243ac2fbe5bf303068eb78f4d2abb894c"
	if got := fmt.Sprintf("%x", target.signature); got != want {
		t.Fatalf("validation signature = %s, want canonical digest %s", got, want)
	}
}

func TestValidationSignatureChangesForEveryCoveredInput(t *testing.T) {
	base := validationSignatureGroup()
	tests := []struct {
		name   string
		base   state.GroupView
		mutate func(*state.GroupView)
	}{
		{name: "group id", base: base, mutate: func(group *state.GroupView) {
			group.ID++
		}},
		{name: "upstream URL", base: base, mutate: func(group *state.GroupView) {
			group.UpstreamURL += "/changed"
		}},
		{name: "selected protocol", base: base, mutate: func(group *state.GroupView) {
			group.Protocols[0] = protocol.Anthropic
		}},
		{name: "explicit validation model", base: base, mutate: func(group *state.GroupView) {
			group.ValidationModel = "model-b"
		}},
		{name: "fallback first model", base: func() state.GroupView {
			group := base
			group.ValidationModel = ""
			return group
		}(), mutate: func(group *state.GroupView) {
			group.Models[0].ID = "model-b"
		}},
		{name: "header set name", base: base, mutate: func(group *state.GroupView) {
			value := group.HeaderRules.Set["X-Alpha"]
			delete(group.HeaderRules.Set, "X-Alpha")
			group.HeaderRules.Set["X-Renamed"] = value
		}},
		{name: "header set value", base: base, mutate: func(group *state.GroupView) {
			group.HeaderRules.Set["X-Alpha"] = "changed"
		}},
		{name: "header remove", base: base, mutate: func(group *state.GroupView) {
			group.HeaderRules.Remove[0] = "X-Changed"
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := cloneValidationGroup(test.base)
			after := cloneValidationGroup(test.base)
			test.mutate(&after)

			beforeTarget, beforeOK := buildGroupValidationTarget(before)
			afterTarget, afterOK := buildGroupValidationTarget(after)
			if !beforeOK || !afterOK {
				t.Fatalf("build target ok = %t/%t, want true/true", beforeOK, afterOK)
			}
			if beforeTarget.signature == afterTarget.signature {
				t.Fatalf("signature did not change after mutating %s", test.name)
			}
		})
	}
}

func TestValidationSignatureIsStableAcrossNormalizedHeaderOrder(t *testing.T) {
	first := validationSignatureGroup()
	first.HeaderRules.Set = map[string]string{
		"X-Alpha": "first",
		"X-Zeta":  "last",
	}
	first.HeaderRules.Remove = []string{"X-Beta", "X-Old"}

	second := validationSignatureGroup()
	second.HeaderRules.Set = map[string]string{
		"x-zeta":  "last",
		"x-alpha": "first",
	}
	second.HeaderRules.Remove = []string{"x-old", "x-beta"}

	firstTarget, firstOK := buildGroupValidationTarget(first)
	secondTarget, secondOK := buildGroupValidationTarget(second)
	if !firstOK || !secondOK {
		t.Fatalf("build target ok = %t/%t, want true/true", firstOK, secondOK)
	}
	if firstTarget.signature != secondTarget.signature {
		t.Fatalf(
			"semantically equal HeaderRules signatures differ: %x != %x",
			firstTarget.signature,
			secondTarget.signature,
		)
	}
}

func TestValidationWorkerSkipsMissingGroupProtocolModelAndDialect(t *testing.T) {
	probes := &validationProbeRecorder{}
	snapshot := validationSnapshot(map[uint]state.GroupView{
		1: validationGroup(nil, "", nil),
		2: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, " \t", nil),
		3: validationGroup([]protocol.Protocol{protocol.Gemini}, "model", nil),
	})
	worker := newValidationWorkerForTest(snapshot, []state.KeyRef{
		{ID: 1, GroupID: 9, EncryptedValue: "key-1"},
		{ID: 2, GroupID: 1, EncryptedValue: "key-2"},
		{ID: 3, GroupID: 2, EncryptedValue: "key-3"},
		{ID: 4, GroupID: 3, EncryptedValue: "key-4"},
	}, probes)

	worker.Validate(context.Background())

	if got := probes.calls(); len(got) != 0 {
		t.Fatalf("Probe calls = %#v, want none", got)
	}
	if got := worker.registry.(*validationRegistryRecorder).events(); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none", got)
	}
}

func TestValidationWorkerKeepsKeyBlacklistedOnDecryptOrProbeFailure(t *testing.T) {
	probes := &validationProbeRecorder{errByKey: map[string]error{"plain-key-2": errors.New("probe failed")}}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, "model", nil)}),
		[]state.KeyRef{
			{ID: 1, GroupID: 1, EncryptedValue: "decrypt-fails"},
			{ID: 2, GroupID: 1, EncryptedValue: "key-2"},
		},
		probes,
	)
	worker.decryptor = validationDecryptor{errors: map[string]error{"decrypt-fails": errors.New("decrypt failed")}}

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{protocol: protocol.OpenAIChatCompletions, model: "model", apiKey: "plain-key-2"}}; !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
	if got := worker.registry.(*validationRegistryRecorder).events(); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none", got)
	}
}

func TestValidationWorkerCoordinatesConditionalRecoveryAndStatsReset(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, "model", nil)}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)
	coordinator := &barrierValidationMutationCoordinator{
		entered: make(chan struct{}), releaseEntry: make(chan struct{}),
		observe: worker.recorder.events, observed: make(chan []string, 1),
		releaseExit: make(chan struct{}),
	}
	worker.mutations = coordinator

	done := make(chan struct{})
	go func() {
		worker.Validate(context.Background())
		close(done)
	}()

	awaitSignal(t, coordinator.entered)
	if got := worker.recorder.events(); len(got) != 0 {
		t.Fatalf("recovery events before coordinator callback = %#v, want none", got)
	}
	close(coordinator.releaseEntry)
	if got, want := awaitValue(t, coordinator.observed), []string{"registry.weight:7:50", "registry.recover:7", "stats.reset:7"}; !sameValidationEvents(got, want) {
		t.Fatalf("recovery events = %#v, want %#v", got, want)
	}
	select {
	case <-done:
		t.Fatal("validation returned before coordinator interval was released")
	default:
	}
	close(coordinator.releaseExit)
	awaitSignal(t, done)
	if got := worker.snapshots.(*validationSnapshotRecorder).calls(); got != 1 {
		t.Fatalf("snapshot reads = %d, want 1", got)
	}
	if got := worker.registry.(*validationRegistryRecorder).blacklistedCalls(); got != 1 {
		t.Fatalf("BlacklistedKeys calls = %d, want 1", got)
	}
}

func TestValidationWorkerRecoverIfMatchFailsDoesNotResetStats(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, "model", nil)}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)
	worker.registry.(*validationRegistryRecorder).recoveryOK = false

	worker.Validate(context.Background())

	if got := worker.recorder.events(); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none", got)
	}
}

func TestValidationWorkerFailureGenerationChangesDuringProbeRejectsRecovery(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	registry := state.NewKeyRegistry()
	if err := registry.Replace([]state.KeyEntry{{
		ID: 7, GroupID: 1, Status: state.KeyStatusActive,
		Blacklisted: true, FailureCount: 3, EncryptedValue: "key-7",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	stats := health.NewStatsStore()
	stats.RecordFailure(7, health.FailureCategoryAmbiguous, 0, now)
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	worker := &validationWorker{
		snapshots: &validationSnapshotRecorder{snapshot: validationSnapshot(map[uint]state.GroupView{
			1: validationSignatureGroup(),
		})},
		registry:  registry,
		stats:     stats,
		mutations: health.NewMutationCoordinator(),
		decryptor: validationDecryptor{},
		dialects: dialect.Set{protocol.OpenAIChatCompletions: &validationTestDialect{
			protocol: protocol.OpenAIChatCompletions,
			probes: &validationProbeRecorder{probe: func(context.Context, protocol.Protocol, string, string) error {
				close(probeStarted)
				<-releaseProbe
				return nil
			}},
		}},
	}

	done := make(chan struct{})
	go func() {
		worker.Validate(context.Background())
		close(done)
	}()
	awaitSignal(t, probeStarted)
	if count, ok := registry.IncrFailure(7); !ok || count != 4 {
		t.Fatalf("IncrFailure() = %d/%t, want 4/true", count, ok)
	}
	close(releaseProbe)
	awaitValidationDone(t, done)

	if got, want := registry.BlacklistedKeys(), []state.KeyRef{{
		ID: 7, GroupID: 1, EncryptedValue: "key-7", FailureGeneration: 1,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blacklisted keys = %#v, want stale recovery rejected as %#v", got, want)
	}
	if got, want := stats.Snapshot(7, now), (health.KeyStats{
		Failure: 1, ConsecutiveFailure: 1, ConsecutiveProblem: 1,
	}); got != want {
		t.Fatalf("stats after stale recovery = %#v, want %#v", got, want)
	}
}

func TestValidationSignatureChangesDuringProbeRejectRecovery(t *testing.T) {
	oldGroup := validationSignatureGroup()
	tests := []struct {
		name    string
		prepare func(*state.GroupView)
		mutate  func(*state.GroupView)
	}{
		{name: "upstream URL", mutate: func(group *state.GroupView) {
			group.UpstreamURL += "/changed"
		}},
		{name: "protocol", mutate: func(group *state.GroupView) {
			group.Protocols[0] = protocol.Anthropic
		}},
		{name: "explicit model", mutate: func(group *state.GroupView) {
			group.ValidationModel = "model-b"
		}},
		{name: "fallback model", prepare: func(group *state.GroupView) {
			group.ValidationModel = ""
		}, mutate: func(group *state.GroupView) {
			group.Models[0].ID = "model-b"
		}},
		{name: "HeaderRules set", mutate: func(group *state.GroupView) {
			group.HeaderRules.Set["X-Alpha"] = "changed-secret"
		}},
		{name: "HeaderRules remove", mutate: func(group *state.GroupView) {
			group.HeaderRules.Remove[0] = "X-Changed"
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			captured := cloneValidationGroup(oldGroup)
			if test.prepare != nil {
				test.prepare(&captured)
			}
			worker := newValidationWorkerForTest(
				validationSnapshot(map[uint]state.GroupView{1: captured}),
				[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
				&validationProbeRecorder{},
			)
			current := cloneValidationGroup(captured)
			test.mutate(&current)
			worker.validationWorker.dialects[protocol.OpenAIChatCompletions] = &validationTestDialect{
				protocol: protocol.OpenAIChatCompletions,
				probes: &validationProbeRecorder{probe: func(context.Context, protocol.Protocol, string, string) error {
					worker.snapshots.(*validationSnapshotRecorder).setSnapshot(
						validationSnapshot(map[uint]state.GroupView{1: current}),
					)
					return nil
				}},
			}

			worker.Validate(context.Background())

			if got := worker.recorder.events(); len(got) != 0 {
				t.Fatalf("recovery events after %s change = %#v, want none", test.name, got)
			}
		})
	}
}

func TestValidationWorkerUnrelatedSnapshotRevisionAllowsRecovery(t *testing.T) {
	oldGroup := validationSignatureGroup()
	worker := newValidationWorkerForTest(
		&state.ConfigSnapshot{
			Revision: 1,
			Groups:   map[uint]state.GroupView{1: cloneValidationGroup(oldGroup)},
		},
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		&validationProbeRecorder{},
	)
	worker.validationWorker.dialects[protocol.OpenAIChatCompletions] = &validationTestDialect{
		protocol: protocol.OpenAIChatCompletions,
		probes: &validationProbeRecorder{probe: func(context.Context, protocol.Protocol, string, string) error {
			worker.snapshots.(*validationSnapshotRecorder).setSnapshot(&state.ConfigSnapshot{
				Revision: 2,
				Groups: map[uint]state.GroupView{
					1: cloneValidationGroup(oldGroup),
					2: {
						ID: 2, UpstreamURL: "https://unrelated.example.com",
						Protocols: []protocol.Protocol{protocol.Anthropic},
						Models:    []state.ModelConfig{{ID: "unrelated"}},
					},
				},
			})
			return nil
		}},
	}

	worker.Validate(context.Background())

	if got, want := worker.recorder.events(), []string{
		"registry.weight:7:50",
		"registry.recover:7",
		"stats.reset:7",
	}; !sameValidationEvents(got, want) {
		t.Fatalf("recovery events = %#v, want unrelated revision allowed %#v", got, want)
	}
}

func TestValidationSignatureRemovedOrDisabledGroupRejectsRecovery(t *testing.T) {
	for _, name := range []string{"removed", "disabled"} {
		t.Run(name, func(t *testing.T) {
			worker := newValidationWorkerForTest(
				validationSnapshot(map[uint]state.GroupView{1: validationSignatureGroup()}),
				[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
				&validationProbeRecorder{},
			)
			worker.validationWorker.dialects[protocol.OpenAIChatCompletions] = &validationTestDialect{
				protocol: protocol.OpenAIChatCompletions,
				probes: &validationProbeRecorder{probe: func(context.Context, protocol.Protocol, string, string) error {
					// Disabled Groups are omitted from ConfigSnapshot.Groups, as are removed Groups.
					worker.snapshots.(*validationSnapshotRecorder).setSnapshot(validationSnapshot(nil))
					return nil
				}},
			}

			worker.Validate(context.Background())

			if got := worker.recorder.events(); len(got) != 0 {
				t.Fatalf("recovery events for %s Group = %#v, want none", name, got)
			}
		})
	}
}

type observableValidationSnapshotSource struct {
	manager        *state.Manager
	callbackActive atomic.Bool
}

func (source *observableValidationSnapshotSource) Current() *state.ConfigSnapshot {
	return source.manager.Current()
}

func (source *observableValidationSnapshotSource) WithCurrentSnapshot(
	fn func(*state.ConfigSnapshot) bool,
) bool {
	return source.manager.WithCurrentSnapshot(func(snapshot *state.ConfigSnapshot) bool {
		if !source.callbackActive.CompareAndSwap(false, true) {
			panic("nested validation publication callback")
		}
		defer source.callbackActive.Store(false)
		return fn(snapshot)
	})
}

func (source *observableValidationSnapshotSource) active() bool {
	return source.callbackActive.Load()
}

type publicationValidationRegistry struct {
	delegate              *state.KeyRegistry
	callbackActive        func() bool
	recoverCallbackActive chan bool
	recoverEntered        chan struct{}
	releaseRecover        chan struct{}
}

func (registry *publicationValidationRegistry) BlacklistedKeys() []state.KeyRef {
	return registry.delegate.BlacklistedKeys()
}

func (registry *publicationValidationRegistry) RecoverIfMatch(ref state.KeyRef, weight int) bool {
	registry.recoverCallbackActive <- registry.callbackActive()
	close(registry.recoverEntered)
	<-registry.releaseRecover
	return registry.delegate.RecoverIfMatch(ref, weight)
}

type publicationValidationStats struct {
	delegate            *health.StatsStore
	callbackActive      func() bool
	resetCallbackActive chan bool
	resetEntered        chan struct{}
	releaseReset        chan struct{}
}

func (stats *publicationValidationStats) Reset(keyID uint) {
	stats.delegate.Reset(keyID)
	stats.resetCallbackActive <- stats.callbackActive()
	close(stats.resetEntered)
	<-stats.releaseReset
}

func TestValidationWorkerPublicationBoundaryBlocksPublishThroughRecoverAndReset(t *testing.T) {
	manager := state.NewManager()
	if _, err := manager.Publish(validationManagerCompileInput("https://upstream.example.com")); err != nil {
		t.Fatalf("initial Publish() error = %v", err)
	}
	registry := state.NewKeyRegistry()
	if err := registry.Replace([]state.KeyEntry{{
		ID: 7, GroupID: 1, Status: state.KeyStatusActive,
		Blacklisted: true, FailureCount: 3, EncryptedValue: "key-7",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	stats := health.NewStatsStore()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	stats.RecordFailure(7, health.FailureCategoryAmbiguous, 0, now)
	snapshots := &observableValidationSnapshotSource{manager: manager}
	blockingRegistry := &publicationValidationRegistry{
		delegate:              registry,
		callbackActive:        snapshots.active,
		recoverCallbackActive: make(chan bool, 1),
		recoverEntered:        make(chan struct{}),
		releaseRecover:        make(chan struct{}),
	}
	blockingStats := &publicationValidationStats{
		delegate:            stats,
		callbackActive:      snapshots.active,
		resetCallbackActive: make(chan bool, 1),
		resetEntered:        make(chan struct{}),
		releaseReset:        make(chan struct{}),
	}
	probes := &validationProbeRecorder{}
	worker := &validationWorker{
		snapshots: snapshots,
		registry:  blockingRegistry,
		stats:     blockingStats,
		mutations: health.NewMutationCoordinator(),
		decryptor: validationDecryptor{},
		dialects: dialect.Set{protocol.OpenAIChatCompletions: &validationTestDialect{
			protocol: protocol.OpenAIChatCompletions,
			probes:   probes,
		}},
	}

	validationDone := make(chan struct{})
	go func() {
		worker.Validate(context.Background())
		close(validationDone)
	}()
	awaitSignal(t, blockingRegistry.recoverEntered)
	if active := awaitValue(t, blockingRegistry.recoverCallbackActive); !active {
		t.Fatal("RecoverIfMatch ran outside the active Manager snapshot callback")
	}

	publishAttempted := make(chan struct{})
	publishDone := make(chan error, 1)
	go func() {
		close(publishAttempted)
		_, err := manager.Publish(validationManagerCompileInput("https://changed.example.com"))
		publishDone <- err
	}()
	awaitSignal(t, publishAttempted)
	assertValidationPublishBlocked(t, publishDone, "RecoverIfMatch")

	close(blockingRegistry.releaseRecover)
	awaitSignal(t, blockingStats.resetEntered)
	if active := awaitValue(t, blockingStats.resetCallbackActive); !active {
		t.Fatal("Stats.Reset ran outside the active Manager snapshot callback")
	}
	assertValidationPublishBlocked(t, publishDone, "Stats.Reset")

	close(blockingStats.releaseReset)
	awaitValidationDone(t, validationDone)
	select {
	case err := <-publishDone:
		if err != nil {
			t.Fatalf("concurrent Publish() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Publish after validation callback returned")
	}

	if got := registry.BlacklistedKeys(); len(got) != 0 {
		t.Fatalf("blacklisted keys after recovery = %#v, want none", got)
	}
	if got := stats.Snapshot(7, now); got != (health.KeyStats{}) {
		t.Fatalf("stats after recovery = %#v, want reset", got)
	}
	if snapshots.active() {
		t.Fatal("Manager snapshot callback remained active after validation returned")
	}
}

func TestValidationSignatureMismatchLogDoesNotLeakSensitiveInputs(t *testing.T) {
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter, previousLevel := logger.Out, logger.Formatter, logger.GetLevel()
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.WarnLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	oldGroup := validationSignatureGroup()
	oldGroup.UpstreamURL = "https://sensitive.example.com/path"
	oldGroup.HeaderRules.Set["X-Secret"] = "sensitive-header-value"
	oldTarget, ok := buildGroupValidationTarget(oldGroup)
	if !ok {
		t.Fatal("build old validation target failed")
	}
	currentGroup := cloneValidationGroup(oldGroup)
	currentGroup.HeaderRules.Set["X-Secret"] = "changed-sensitive-header-value"
	currentTarget, ok := buildGroupValidationTarget(currentGroup)
	if !ok {
		t.Fatal("build current validation target failed")
	}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: oldGroup}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "cipher-secret"}},
		&validationProbeRecorder{},
	)
	worker.validationWorker.dialects[protocol.OpenAIChatCompletions] = &validationTestDialect{
		protocol: protocol.OpenAIChatCompletions,
		probes: &validationProbeRecorder{probe: func(context.Context, protocol.Protocol, string, string) error {
			worker.snapshots.(*validationSnapshotRecorder).setSnapshot(
				validationSnapshot(map[uint]state.GroupView{1: currentGroup}),
			)
			return nil
		}},
	}

	worker.Validate(context.Background())

	output := logs.String()
	if !strings.Contains(output, `"stage":"conditional_recover"`) {
		t.Fatalf("log output = %q, want conditional_recover stage", output)
	}
	for _, forbidden := range []string{
		"cipher-secret",
		"plain-cipher-secret",
		"sensitive-header-value",
		"changed-sensitive-header-value",
		"https://sensitive.example.com",
		fmt.Sprintf("%x", oldTarget.signature),
		fmt.Sprintf("%x", currentTarget.signature),
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("log output leaked %q: %q", forbidden, output)
		}
	}
}

func assertValidationPublishBlocked(t *testing.T, publishDone <-chan error, stage string) {
	t.Helper()
	select {
	case err := <-publishDone:
		t.Fatalf("Publish() returned during %s: %v", stage, err)
	default:
	}
}

type barrierValidationMutationCoordinator struct {
	entered      chan struct{}
	releaseEntry chan struct{}
	observe      func() []string
	observed     chan []string
	releaseExit  chan struct{}
}

func (coordinator *barrierValidationMutationCoordinator) Do(_ uint, fn func()) {
	close(coordinator.entered)
	<-coordinator.releaseEntry
	fn()
	coordinator.observed <- coordinator.observe()
	<-coordinator.releaseExit
}

func TestValidationWorkerConditionalRecoveryFailureCompletesCoordinatorInterval(t *testing.T) {
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, "model", nil)}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		&validationProbeRecorder{},
	)
	worker.registry.(*validationRegistryRecorder).recoveryOK = false
	coordinator := &barrierValidationMutationCoordinator{
		entered: make(chan struct{}), releaseEntry: make(chan struct{}),
		observe: worker.recorder.events, observed: make(chan []string, 1),
		releaseExit: make(chan struct{}),
	}
	worker.mutations = coordinator

	done := make(chan struct{})
	go func() {
		worker.Validate(context.Background())
		close(done)
	}()
	awaitSignal(t, coordinator.entered)
	if got := worker.recorder.events(); len(got) != 0 {
		t.Fatalf("recovery events before coordinator callback = %#v, want none", got)
	}
	close(coordinator.releaseEntry)
	if got := awaitValue(t, coordinator.observed); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none", got)
	}
	close(coordinator.releaseExit)
	awaitSignal(t, done)
}

func TestValidationWorkerDoesNotRecoverDisabledOrReplacedKeyRef(t *testing.T) {
	tests := []struct {
		name            string
		mutate          func(t *testing.T, registry *state.KeyRegistry)
		expectedCipher  string
		expectedEnabled bool
	}{
		{
			name: "disabled after sweep", expectedCipher: "cipher-original", mutate: func(t *testing.T, registry *state.KeyRegistry) {
				t.Helper()
				if err := registry.SetKeyStatus(7, state.KeyStatusDisabled); err != nil {
					t.Fatalf("SetKeyStatus() error = %v", err)
				}
			},
		},
		{
			name: "replaced after sweep", expectedCipher: "cipher-replaced", expectedEnabled: true, mutate: func(t *testing.T, registry *state.KeyRegistry) {
				t.Helper()
				if err := registry.Replace([]state.KeyEntry{{
					ID: 7, GroupID: 1, Status: state.KeyStatusActive, Blacklisted: true,
					FailureCount: 5, WeightAuto: 17, EncryptedValue: "cipher-replaced",
				}}); err != nil {
					t.Fatalf("Replace() error = %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := state.NewKeyRegistry()
			if err := registry.Replace([]state.KeyEntry{{
				ID: 7, GroupID: 1, Status: state.KeyStatusActive, Blacklisted: true,
				FailureCount: 3, WeightAuto: 17, EncryptedValue: "cipher-original",
			}}); err != nil {
				t.Fatalf("Replace() error = %v", err)
			}
			probeStarted := make(chan struct{})
			releaseProbe := make(chan struct{})
			worker := newRealRegistryValidationWorker(registry, &validationProbeRecorder{probe: func(ctx context.Context, _ protocol.Protocol, _ string, _ string) error {
				close(probeStarted)
				select {
				case <-releaseProbe:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}})
			done := make(chan struct{})
			go func() {
				worker.Validate(context.Background())
				close(done)
			}()
			awaitSignal(t, probeStarted)
			test.mutate(t, registry)
			close(releaseProbe)
			awaitValidationDone(t, done)

			if got := len(registry.ActiveKeyIDs()); test.expectedEnabled && got != 1 {
				t.Fatalf("active key count = %d, want 1", got)
			} else if !test.expectedEnabled && got != 0 {
				t.Fatalf("active key count = %d, want 0", got)
			}
			if !test.expectedEnabled {
				if err := registry.SetKeyStatus(7, state.KeyStatusActive); err != nil {
					t.Fatalf("SetKeyStatus() error = %v", err)
				}
			}
			if got, want := registry.BlacklistedKeys(), []state.KeyRef{{
				ID: 7, GroupID: 1, EncryptedValue: test.expectedCipher, FailureGeneration: 0,
			}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("blacklisted keys after stale recovery = %#v, want %#v", got, want)
			}
			if got := registry.CollectCandidates([]uint{1}, nil, time.Time{}); len(got) != 0 {
				t.Fatalf("candidates after stale recovery = %#v, want none", got)
			}
		})
	}
}

func TestValidationWorkerFailureLogUsesSafeStructuredFields(t *testing.T) {
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter, previousLevel := logger.Out, logger.Formatter, logger.GetLevel()
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.WarnLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: {
			UpstreamURL:     "https://sensitive.example.com/path",
			Protocols:       []protocol.Protocol{protocol.OpenAIChatCompletions},
			ValidationModel: "model",
		}}),
		[]state.KeyRef{{ID: 7, GroupID: 1, EncryptedValue: "cipher-secret"}},
		&validationProbeRecorder{},
	)
	worker.decryptor = validationDecryptor{errors: map[string]error{"cipher-secret": errors.New("plain-secret underlying failure")}}

	worker.Validate(context.Background())

	output := logs.String()
	if !strings.Contains(output, `"stage":"decrypt"`) {
		t.Fatalf("log output = %q, want decrypt stage", output)
	}
	for _, forbidden := range []string{"cipher-secret", "plain-secret", "https://sensitive.example.com", "underlying failure"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("log output leaked %q: %q", forbidden, output)
		}
	}
}

func TestValidationWorkerLimitsGlobalConcurrencyToEight(t *testing.T) {
	started := make(chan uint, 9)
	release := make(chan struct{})
	probes := &validationProbeRecorder{
		probe: func(ctx context.Context, _ protocol.Protocol, _ string, apiKey string) error {
			keyID := validationKeyID(apiKey)
			started <- keyID
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, "model", nil)}),
		validationRefs(9),
		probes,
	)
	done := make(chan struct{})
	go func() {
		worker.Validate(context.Background())
		close(done)
	}()

	for range validationConcurrency {
		awaitValidationStart(t, started)
	}
	if got := probes.maxActive(); got != validationConcurrency {
		t.Fatalf("maximum active probes = %d, want %d", got, validationConcurrency)
	}
	select {
	case keyID := <-started:
		t.Fatalf("key %d started before a worker was released", keyID)
	default:
	}

	close(release)
	awaitValidationDone(t, done)
	if got, want := len(probes.calls()), 9; got != want {
		t.Fatalf("Probe calls = %d, want %d", got, want)
	}
}

func TestValidationWorkerCancellationStopsDispatchAndInFlightProbes(t *testing.T) {
	started := make(chan uint, 9)
	probes := &validationProbeRecorder{
		probe: func(ctx context.Context, _ protocol.Protocol, _ string, apiKey string) error {
			started <- validationKeyID(apiKey)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, "model", nil)}),
		validationRefs(9),
		probes,
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Validate(ctx)
		close(done)
	}()

	for range validationConcurrency {
		awaitValidationStart(t, started)
	}
	cancel()
	awaitValidationDone(t, done)
	select {
	case keyID := <-started:
		t.Fatalf("key %d started after cancellation", keyID)
	default:
	}
	if got, want := len(probes.calls()), validationConcurrency; got != want {
		t.Fatalf("Probe calls = %d, want %d", got, want)
	}
}

func TestValidationWorkerDoesNotProbeQueuedJobAfterCancellation(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, "model", nil)}),
		nil,
		probes,
	)
	jobs := make(chan state.KeyRef, 1)
	jobs <- state.KeyRef{ID: 7, GroupID: 1, EncryptedValue: "key-7"}
	close(jobs)
	ctx := &validationCancelAfterJobContext{
		done:    make(chan struct{}),
		checked: make(chan uint, 1),
		release: make(chan struct{}),
	}
	finished := make(chan struct{})
	go func() {
		worker.consumeValidationJobs(ctx, worker.snapshots.Current(), jobs)
		close(finished)
	}()
	awaitValidationStart(t, ctx.checked)
	close(ctx.done)
	close(ctx.release)
	awaitValidationDone(t, finished)

	if got := probes.calls(); len(got) != 0 {
		t.Fatalf("Probe calls = %#v, want none after cancellation", got)
	}
	if got := worker.recorder.events(); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none after cancellation", got)
	}
}

type validationCancelAfterJobContext struct {
	done    chan struct{}
	checked chan uint
	release chan struct{}
}

func (*validationCancelAfterJobContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (ctx *validationCancelAfterJobContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *validationCancelAfterJobContext) Err() error {
	ctx.checked <- 1
	<-ctx.release
	return context.Canceled
}

func (*validationCancelAfterJobContext) Value(any) any {
	return nil
}

type validationSnapshotRecorder struct {
	mu       sync.Mutex
	snapshot *state.ConfigSnapshot
	read     int
}

func (source *validationSnapshotRecorder) Current() *state.ConfigSnapshot {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.read++
	return source.snapshot
}

func (source *validationSnapshotRecorder) WithCurrentSnapshot(
	fn func(*state.ConfigSnapshot) bool,
) bool {
	if fn == nil {
		return false
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	return fn(source.snapshot)
}

func (source *validationSnapshotRecorder) setSnapshot(snapshot *state.ConfigSnapshot) {
	source.mu.Lock()
	source.snapshot = snapshot
	source.mu.Unlock()
}

func (source *validationSnapshotRecorder) calls() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.read
}

type validationEventRecorder struct {
	mu    sync.Mutex
	items []string
}

func (recorder *validationEventRecorder) add(event string) {
	recorder.mu.Lock()
	recorder.items = append(recorder.items, event)
	recorder.mu.Unlock()
}

func (recorder *validationEventRecorder) events() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.items...)
}

type validationRegistryRecorder struct {
	mu              sync.Mutex
	refs            []state.KeyRef
	blacklistedRead int
	recoveryOK      bool
	recorder        *validationEventRecorder
}

func (registry *validationRegistryRecorder) BlacklistedKeys() []state.KeyRef {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.blacklistedRead++
	return append([]state.KeyRef(nil), registry.refs...)
}

func (registry *validationRegistryRecorder) RecoverIfMatch(ref state.KeyRef, weight int) bool {
	registry.mu.Lock()
	recoveryOK := registry.recoveryOK
	registry.mu.Unlock()
	if !recoveryOK {
		return false
	}
	registry.recorder.add(fmt.Sprintf("registry.weight:%d:%d", ref.ID, weight))
	registry.recorder.add(fmt.Sprintf("registry.recover:%d", ref.ID))
	return true
}

func (registry *validationRegistryRecorder) events() []string {
	return registry.recorder.events()
}

func (registry *validationRegistryRecorder) blacklistedCalls() int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.blacklistedRead
}

type validationStatsRecorder struct {
	recorder *validationEventRecorder
}

func (stats *validationStatsRecorder) Reset(keyID uint) {
	stats.recorder.add(fmt.Sprintf("stats.reset:%d", keyID))
}

type validationDecryptor struct {
	errors map[string]error
}

func (decryptor validationDecryptor) Decrypt(value string) (string, error) {
	if err := decryptor.errors[value]; err != nil {
		return "", err
	}
	return "plain-" + value, nil
}

type validationProbeCall struct {
	protocol protocol.Protocol
	model    string
	apiKey   string
}

type validationProbeRecorder struct {
	mu       sync.Mutex
	items    []validationProbeCall
	active   int
	maximum  int
	errByKey map[string]error
	probe    func(context.Context, protocol.Protocol, string, string) error
}

func (recorder *validationProbeRecorder) calls() []validationProbeCall {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]validationProbeCall(nil), recorder.items...)
}

func (recorder *validationProbeRecorder) maxActive() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.maximum
}

func (recorder *validationProbeRecorder) invoke(ctx context.Context, p protocol.Protocol, model, apiKey string) error {
	recorder.mu.Lock()
	recorder.items = append(recorder.items, validationProbeCall{protocol: p, model: model, apiKey: apiKey})
	recorder.active++
	if recorder.active > recorder.maximum {
		recorder.maximum = recorder.active
	}
	recorder.mu.Unlock()
	defer func() {
		recorder.mu.Lock()
		recorder.active--
		recorder.mu.Unlock()
	}()
	if recorder.probe != nil {
		return recorder.probe(ctx, p, model, apiKey)
	}
	return recorder.errByKey[apiKey]
}

type validationTestDialect struct {
	protocol protocol.Protocol
	probes   *validationProbeRecorder
}

func (dialect *validationTestDialect) Protocol() protocol.Protocol {
	return dialect.protocol
}

func (*validationTestDialect) InspectRequest(
	*dialect.ParsedRequest,
) (dialect.RequestMetadata, error) {
	return dialect.RequestMetadata{}, nil
}

func (*validationTestDialect) BuildUpstreamURL(string, *dialect.ParsedRequest) (string, error) {
	return "", nil
}

func (*validationTestDialect) InjectCredential(http.Header, string) {}

func (*validationTestDialect) ListModels(context.Context, string, string, state.HeaderRules) ([]string, error) {
	return nil, nil
}

func (dialect *validationTestDialect) Probe(ctx context.Context, _ string, apiKey string, _ state.HeaderRules, model string) error {
	return dialect.probes.invoke(ctx, dialect.protocol, model, apiKey)
}

func (*validationTestDialect) ClassifyStatus(int, []byte) health.FailureCategory {
	return health.FailureCategoryAmbiguous
}

type validationTestWorker struct {
	*validationWorker
	recorder *validationEventRecorder
}

func newValidationWorkerForTest(snapshot *state.ConfigSnapshot, refs []state.KeyRef, probes *validationProbeRecorder) *validationTestWorker {
	recorder := &validationEventRecorder{}
	registry := &validationRegistryRecorder{refs: refs, recoveryOK: true, recorder: recorder}
	dialects := dialect.Set{}
	for _, p := range []protocol.Protocol{
		protocol.OpenAIChatCompletions,
		protocol.OpenAIResponses,
		protocol.Anthropic,
	} {
		dialects[p] = &validationTestDialect{protocol: p, probes: probes}
	}
	return &validationTestWorker{
		validationWorker: &validationWorker{
			snapshots: &validationSnapshotRecorder{snapshot: snapshot},
			registry:  registry,
			stats:     &validationStatsRecorder{recorder: recorder},
			mutations: health.NewMutationCoordinator(),
			decryptor: validationDecryptor{},
			dialects:  dialects,
		},
		recorder: recorder,
	}
}

func newRealRegistryValidationWorker(registry *state.KeyRegistry, probes *validationProbeRecorder) *validationWorker {
	return &validationWorker{
		snapshots: &validationSnapshotRecorder{snapshot: validationSnapshot(map[uint]state.GroupView{
			1: validationGroup([]protocol.Protocol{protocol.OpenAIChatCompletions}, "model", nil),
		})},
		registry:  registry,
		stats:     health.NewStatsStore(),
		mutations: health.NewMutationCoordinator(),
		decryptor: validationDecryptor{},
		dialects:  dialect.Set{protocol.OpenAIChatCompletions: &validationTestDialect{protocol: protocol.OpenAIChatCompletions, probes: probes}},
	}
}

func validationSnapshot(groups map[uint]state.GroupView) *state.ConfigSnapshot {
	return &state.ConfigSnapshot{Groups: groups}
}

func validationGroup(protocols []protocol.Protocol, validationModel string, models []state.ModelConfig) state.GroupView {
	return state.GroupView{
		UpstreamURL:     "https://upstream.example.com",
		Protocols:       protocols,
		ValidationModel: validationModel,
		Models:          models,
	}
}

func validationSignatureGroup() state.GroupView {
	return state.GroupView{
		ID:              1,
		UpstreamURL:     "https://upstream.example.com",
		Protocols:       []protocol.Protocol{protocol.OpenAIChatCompletions},
		ValidationModel: "model-a",
		Models:          []state.ModelConfig{{ID: "model-a"}},
		HeaderRules: state.HeaderRules{
			Set: map[string]string{
				"X-Alpha": "first",
				"X-Zeta":  "last",
			},
			Remove: []string{"X-Old"},
		},
	}
}

func cloneValidationGroup(group state.GroupView) state.GroupView {
	cloned := group
	cloned.Protocols = append([]protocol.Protocol(nil), group.Protocols...)
	cloned.Models = append([]state.ModelConfig(nil), group.Models...)
	cloned.HeaderRules.Set = make(map[string]string, len(group.HeaderRules.Set))
	for name, value := range group.HeaderRules.Set {
		cloned.HeaderRules.Set[name] = value
	}
	cloned.HeaderRules.Remove = append([]string(nil), group.HeaderRules.Remove...)
	return cloned
}

func validationManagerCompileInput(upstreamURL string) state.CompileInput {
	return state.CompileInput{Groups: []state.GroupConfig{{
		ID:              1,
		Name:            "group",
		UpstreamURL:     upstreamURL,
		ValidationModel: "model-a",
		Protocols:       []protocol.Protocol{protocol.OpenAIChatCompletions},
		Models:          []state.ModelConfig{{ID: "model-a"}},
		Enabled:         true,
	}}}
}

func validationRefs(count int) []state.KeyRef {
	refs := make([]state.KeyRef, count)
	for index := range refs {
		keyID := uint(index + 1)
		refs[index] = state.KeyRef{ID: keyID, GroupID: 1, EncryptedValue: fmt.Sprintf("key-%d", keyID)}
	}
	return refs
}

func validationKeyID(apiKey string) uint {
	var keyID uint
	if _, err := fmt.Sscanf(apiKey, "plain-key-%d", &keyID); err != nil {
		panic(fmt.Sprintf("parse key ID from %q: %v", apiKey, err))
	}
	return keyID
}

func awaitValidationStart(t *testing.T, started <-chan uint) uint {
	t.Helper()
	select {
	case keyID := <-started:
		return keyID
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a probe to start")
		return 0
	}
}

func awaitValidationDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for validation worker to return")
	}
}

func sameValidationProbeCalls(got, want []validationProbeCall) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func sameValidationEvents(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
