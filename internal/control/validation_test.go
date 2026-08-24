package control

import (
	"bytes"
	"context"
	"encoding/json"
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

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
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
					protocol.OpenAICompletions,
				},
				" explicit-model ",
				nil,
			),
		}),
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{
		protocol: protocol.OpenAICompletions,
		model:    "explicit-model",
		apiKey:   "plain-key-7",
	}}; !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
}

func TestBuildGroupValidationTargetSkipsSubscriptionGroups(t *testing.T) {
	t.Parallel()

	group := validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil)
	group.ConnectionType = "subscription"
	if _, ok := buildGroupValidationTarget(group); ok {
		t.Fatal("buildGroupValidationTarget(subscription) ok = true")
	}
}

func TestValidationProtocolUsesFirstNativePresetProtocolThenConfiguredFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		channelID channel.ID
		params    json.RawMessage
		want      protocol.Protocol
		wantMode  channel.RouteMode
	}{
		{name: "OpenAI", channelID: channel.OpenAI, params: json.RawMessage(`{}`), want: protocol.OpenAICompletions, wantMode: channel.RouteNative},
		{name: "Anthropic", channelID: channel.Anthropic, params: json.RawMessage(`{}`), want: protocol.Anthropic, wantMode: channel.RouteNative},
		{name: "Gemini", channelID: channel.Gemini, params: json.RawMessage(`{}`), want: protocol.Gemini, wantMode: channel.RouteNative},
		{name: "OpenRouter", channelID: channel.OpenRouter, params: json.RawMessage(`{}`), want: protocol.OpenAICompletions, wantMode: channel.RouteNative},
		{name: "OpenAI compatible", channelID: channel.OpenAICompatible, params: json.RawMessage(`{"base_url":"https://upstream.example/v1"}`), want: protocol.OpenAICompletions, wantMode: channel.RouteNative},
		{name: "Azure", channelID: channel.AzureOpenAI, params: json.RawMessage(`{"endpoint":"https://example.openai.azure.com"}`), want: protocol.OpenAICompletions, wantMode: channel.RouteConverted},
		{name: "Bedrock", channelID: channel.AWSBedrock, params: json.RawMessage(`{"region":"us-east-1"}`), want: protocol.OpenAICompletions, wantMode: channel.RouteConverted},
		{name: "Vertex", channelID: channel.GoogleVertex, params: json.RawMessage(`{"location":"us-central1"}`), want: protocol.Gemini, wantMode: channel.RouteNative},
	}

	registry := channel.NewRegistry()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := registry.Resolve(test.channelID, test.params)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := validationProtocol(resolved, "gemini-2.5-pro")
			if !ok || got != test.want {
				t.Fatalf("validationProtocol() = %q/%t, want %q/true", got, ok, test.want)
			}
			mode, exists := resolved.ModeForModel(got, execution.OperationProbe, "gemini-2.5-pro")
			if !exists || mode != test.wantMode {
				t.Fatalf("probe mode = %q/%t, want %q/true", mode, exists, test.wantMode)
			}
		})
	}
}

func TestValidationWorkerFallsBackToFirstRealModelID(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{
			1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, " \t", []state.ModelConfig{{ID: "  real-model  ", Alias: "external-model"}}),
		}),
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{protocol: protocol.OpenAICompletions, model: "real-model", apiKey: "plain-key-7"}}; !sameValidationProbeCalls(got, want) {
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
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		probes,
	)

	worker.Validate(context.Background())

	want := []validationProbeCall{{
		protocol: protocol.OpenAICompletions,
		model:    "gpt-5",
		apiKey:   "plain-key-7",
	}}
	if got := probes.calls(); !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
}

func TestValidationWorkerProbesStructuredCloudCredential(t *testing.T) {
	group := validationGroup(nil, "anthropic.claude-test", nil)
	setValidationChannel(&group, channel.AWSBedrock, json.RawMessage(`{"region":"us-east-1"}`))
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: group}),
		[]state.CredentialRef{{
			ID: 7, GroupID: 1, Version: 1, IdentityGeneration: 7,
			Fingerprint: "bedrock-fingerprint", EncryptedValue: "bedrock-cipher",
		}},
		&validationProbeRecorder{},
	)
	worker.decryptor = fixedValidationDecryptor{
		plaintext: `{"access_key":"AKIA_TEST","secret_key":"bedrock-secret"}`,
	}
	var observed execution.AttemptSpec
	worker.executor = scriptedDiscoveryExecutor{execute: func(
		_ context.Context,
		spec execution.AttemptSpec,
	) execution.AttemptResult {
		observed = spec.Clone()
		return execution.AttemptResult{
			DispatchState:   execution.DispatchMaybeSent,
			ResponseStarted: true,
			StatusCode:      http.StatusOK,
			Header:          http.Header{},
		}
	}}

	worker.Validate(context.Background())

	if got, want := string(observed.Credential.Data()),
		`{"access_key":"AKIA_TEST","secret_key":"bedrock-secret"}`; got != want {
		t.Fatalf("credential = %s, want %s", got, want)
	}
	if observed.ChannelID != string(channel.AWSBedrock) ||
		observed.Operation != execution.OperationProbe ||
		observed.ClientProtocol != protocol.OpenAICompletions ||
		observed.UpstreamModel != "anthropic.claude-test" {
		t.Fatalf("attempt = %#v", observed)
	}
	if observed.Method != "" || observed.Path != "" || observed.RawQuery != "" ||
		len(observed.Query) != 0 || len(observed.Body) != 0 {
		t.Fatalf("probe attempt contains provider wire shape: %#v", observed)
	}
	if got, want := worker.recorder.events(), []string{
		fmt.Sprintf("registry.weight:7:%d", state.DefaultWeight), "registry.recover:7", "stats.reset:7",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recovery events = %#v, want %#v", got, want)
	}
}

func TestValidationWorkerUsesNativeGeminiProbeForVertexModel(t *testing.T) {
	group := validationGroup(nil, "gemini-2.5-pro", nil)
	setValidationChannel(&group, channel.GoogleVertex, json.RawMessage(`{}`))
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: group}),
		[]state.CredentialRef{{
			ID: 8, GroupID: 1, Version: 1, IdentityGeneration: 1,
			Fingerprint: "vertex-fingerprint", EncryptedValue: "vertex-cipher",
		}},
		&validationProbeRecorder{},
	)
	worker.decryptor = fixedValidationDecryptor{
		plaintext: `{"service_account_json":"{\"type\":\"service_account\",\"project_id\":\"project-one\",\"client_email\":\"svc@example.iam.gserviceaccount.com\",\"private_key\":\"secret\"}"}`,
	}
	var observed execution.AttemptSpec
	worker.executor = scriptedDiscoveryExecutor{execute: func(
		_ context.Context,
		spec execution.AttemptSpec,
	) execution.AttemptResult {
		observed = spec.Clone()
		return execution.AttemptResult{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: http.Header{},
		}
	}}

	worker.Validate(context.Background())

	if observed.ChannelID != string(channel.GoogleVertex) ||
		observed.ClientProtocol != protocol.Gemini ||
		observed.RouteMode != execution.RouteNative ||
		observed.UpstreamModel != "gemini-2.5-pro" ||
		string(observed.TargetConfig) != `{"location":"global"}` {
		t.Fatalf("Vertex probe attempt = %#v", observed)
	}
}

func TestValidationSignatureUsesCanonicalLengthPrefixedEncoding(t *testing.T) {
	group := validationGroup(
		[]protocol.Protocol{protocol.OpenAICompletions}, " model-a ", nil,
	)
	group.ID = 42
	group.HeaderRules = state.HeaderRules{
		Set: map[string]string{
			"X-Zeta":  "last",
			"x-alpha": "first",
		},
		Remove: []string{"X-Old", "x-beta"},
	}

	target, ok := buildGroupValidationTarget(group)
	if !ok {
		t.Fatal("buildGroupValidationTarget() ok = false, want true")
	}
	if target.protocol != protocol.OpenAICompletions || target.model != "model-a" {
		t.Fatalf("target protocol/model = %q/%q, want openai/model-a", target.protocol, target.model)
	}
	const want = "eb368678ae2d586451381ecad184474b772e646f6c75697e7f6a2a01ca2bc985"
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
		{name: "channel target", base: base, mutate: func(group *state.GroupView) {
			setValidationChannel(group, channel.OpenAICompatible, json.RawMessage(`{"base_url":"https://changed.example/v1"}`))
		}},
		{name: "provider family", base: base, mutate: func(group *state.GroupView) {
			setValidationChannel(group, channel.Anthropic, json.RawMessage(`{}`))
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
		2: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, " \t", nil),
		3: validationGroup([]protocol.Protocol{protocol.Gemini}, "model", nil),
	})
	worker := newValidationWorkerForTest(snapshot, []state.CredentialRef{
		{ID: 1, GroupID: 9, EncryptedValue: "key-1"},
		{ID: 2, GroupID: 1, EncryptedValue: "key-2"},
		{ID: 3, GroupID: 2, EncryptedValue: "key-3"},
		{ID: 4, GroupID: 3, EncryptedValue: "key-4"},
	}, probes)
	worker.executor = nil

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
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil)}),
		[]state.CredentialRef{
			{ID: 1, GroupID: 1, EncryptedValue: "decrypt-fails"},
			{ID: 2, GroupID: 1, EncryptedValue: "key-2"},
		},
		probes,
	)
	worker.decryptor = validationDecryptor{errors: map[string]error{"decrypt-fails": errors.New("decrypt failed")}}

	worker.Validate(context.Background())

	if got, want := probes.calls(), []validationProbeCall{{protocol: protocol.OpenAICompletions, model: "model", apiKey: "plain-key-2"}}; !sameValidationProbeCalls(got, want) {
		t.Fatalf("Probe calls = %#v, want %#v", got, want)
	}
	if got := worker.registry.(*validationRegistryRecorder).events(); len(got) != 0 {
		t.Fatalf("recovery events = %#v, want none", got)
	}
}

func TestValidationWorkerCoordinatesConditionalRecoveryAndStatsReset(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil)}),
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
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
		t.Fatalf("BlacklistedCredentials calls = %d, want 1", got)
	}
}

func TestValidationWorkerLogsSuccessfulRecovery(t *testing.T) {
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	previousOutput, previousFormatter, previousLevel := logger.Out, logger.Formatter, logger.GetLevel()
	logrus.SetOutput(&logs)
	logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.InfoLevel)
	t.Cleanup(func() {
		logrus.SetOutput(previousOutput)
		logrus.SetFormatter(previousFormatter)
		logrus.SetLevel(previousLevel)
	})

	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup(
			[]protocol.Protocol{protocol.OpenAICompletions}, "model", nil,
		)}),
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "cipher-secret"}},
		&validationProbeRecorder{},
	)

	worker.Validate(context.Background())

	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &entry); err != nil {
		t.Fatalf("decode recovery log: %v; output=%q", err, logs.String())
	}
	if entry["event"] != "credential_recovered" ||
		entry["credential_id"] != float64(7) ||
		entry["group_id"] != float64(1) ||
		entry["protocol"] != string(protocol.OpenAICompletions) ||
		entry["level"] != "info" ||
		entry["msg"] != "[CONTROL] Credential recovered" {
		t.Fatalf("recovery log = %#v", entry)
	}
	if output := logs.String(); strings.Contains(output, "cipher-secret") ||
		strings.Contains(output, "plain-cipher-secret") {
		t.Fatalf("recovery log leaked credential material: %q", output)
	}
}

func TestValidationWorkerRecoverIfMatchFailsDoesNotResetStats(t *testing.T) {
	probes := &validationProbeRecorder{}
	worker := newValidationWorkerForTest(
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil)}),
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
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
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 7, GroupID: 1, Version: 1, IdentityGeneration: 7, Fingerprint: "test-7", Status: state.CredentialStatusActive,
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
		channels:  channel.NewRegistry(),
		executor: &validationTestExecutor{
			probes: &validationProbeRecorder{probe: func(context.Context, protocol.Protocol, string, string) error {
				close(probeStarted)
				<-releaseProbe
				return nil
			}},
		},
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

	if got, want := registry.BlacklistedCredentials(), []state.CredentialRef{{
		ID: 7, GroupID: 1, Version: 1, IdentityGeneration: 7,
		Fingerprint: "test-7", EncryptedValue: "key-7", FailureGeneration: 1,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blacklisted keys = %#v, want stale recovery rejected as %#v", got, want)
	}
	if got, want := stats.Snapshot(7, now), (health.CredentialStats{
		Failure: 1, Problem: 1, ConsecutiveFailure: 1, ConsecutiveProblem: 1,
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
		{name: "channel target", mutate: func(group *state.GroupView) {
			setValidationChannel(group, channel.OpenAICompatible, json.RawMessage(`{"base_url":"https://changed.example/v1"}`))
		}},
		{name: "provider family", mutate: func(group *state.GroupView) {
			setValidationChannel(group, channel.Anthropic, json.RawMessage(`{}`))
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
				[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
				&validationProbeRecorder{},
			)
			current := cloneValidationGroup(captured)
			test.mutate(&current)
			worker.validationWorker.executor = &validationTestExecutor{
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
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
		&validationProbeRecorder{},
	)
	worker.validationWorker.executor = &validationTestExecutor{
		probes: &validationProbeRecorder{probe: func(context.Context, protocol.Protocol, string, string) error {
			worker.snapshots.(*validationSnapshotRecorder).setSnapshot(&state.ConfigSnapshot{
				Revision: 2,
				Groups: map[uint]state.GroupView{
					1: cloneValidationGroup(oldGroup),
					2: validationGroup([]protocol.Protocol{protocol.Anthropic}, "unrelated", []state.ModelConfig{{ID: "unrelated"}}),
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
				[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
				&validationProbeRecorder{},
			)
			worker.validationWorker.executor = &validationTestExecutor{
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
	delegate              *state.CredentialRegistry
	callbackActive        func() bool
	recoverCallbackActive chan bool
	recoverEntered        chan struct{}
	releaseRecover        chan struct{}
}

func (registry *publicationValidationRegistry) BlacklistedCredentials() []state.CredentialRef {
	return registry.delegate.BlacklistedCredentials()
}

func (registry *publicationValidationRegistry) RecoverIfMatch(ref state.CredentialRef, weight int) bool {
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
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 7, GroupID: 1, Version: 1, IdentityGeneration: 7, Fingerprint: "test-7", Status: state.CredentialStatusActive,
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
		channels:  channel.NewRegistry(),
		executor:  &validationTestExecutor{probes: probes},
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

	if got := registry.BlacklistedCredentials(); len(got) != 0 {
		t.Fatalf("blacklisted keys after recovery = %#v, want none", got)
	}
	if got := stats.Snapshot(7, now); got != (health.CredentialStats{}) {
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
	setValidationChannel(&oldGroup, channel.OpenAICompatible, json.RawMessage(`{"base_url":"https://sensitive.example.com/path"}`))
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
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "cipher-secret"}},
		&validationProbeRecorder{},
	)
	worker.validationWorker.executor = &validationTestExecutor{
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
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil)}),
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "key-7"}},
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
		mutate          func(t *testing.T, registry *state.CredentialRegistry)
		expectedCipher  string
		expectedEnabled bool
	}{
		{
			name: "disabled after sweep", expectedCipher: "cipher-original", mutate: func(t *testing.T, registry *state.CredentialRegistry) {
				t.Helper()
				if err := registry.SetCredentialStatus(7, state.CredentialStatusDisabled); err != nil {
					t.Fatalf("SetCredentialStatus() error = %v", err)
				}
			},
		},
		{
			name: "replaced after sweep", expectedCipher: "cipher-replaced", expectedEnabled: true, mutate: func(t *testing.T, registry *state.CredentialRegistry) {
				t.Helper()
				if err := registry.ReplaceCredentials([]state.CredentialEntry{{
					ID: 7, GroupID: 1, Version: 1, IdentityGeneration: 7, Fingerprint: "test-7", Status: state.CredentialStatusActive, Blacklisted: true,
					FailureCount: 5, WeightAuto: 17, EncryptedValue: "cipher-replaced",
				}}); err != nil {
					t.Fatalf("Replace() error = %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := state.NewCredentialRegistry()
			if err := registry.ReplaceCredentials([]state.CredentialEntry{{
				ID: 7, GroupID: 1, Version: 1, IdentityGeneration: 7, Fingerprint: "test-7", Status: state.CredentialStatusActive, Blacklisted: true,
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

			if got := len(registry.ActiveCredentialIDs()); test.expectedEnabled && got != 1 {
				t.Fatalf("active key count = %d, want 1", got)
			} else if !test.expectedEnabled && got != 0 {
				t.Fatalf("active key count = %d, want 0", got)
			}
			if !test.expectedEnabled {
				if err := registry.SetCredentialStatus(7, state.CredentialStatusActive); err != nil {
					t.Fatalf("SetCredentialStatus() error = %v", err)
				}
			}
			if got, want := registry.BlacklistedCredentials(), []state.CredentialRef{{
				ID: 7, GroupID: 1, Version: 1, IdentityGeneration: 7,
				Fingerprint: "test-7", EncryptedValue: test.expectedCipher, FailureGeneration: 0,
			}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("blacklisted keys after stale recovery = %#v, want %#v", got, want)
			}
			if got := registry.CollectCredentialCandidates([]uint{1}, nil, time.Time{}); len(got) != 0 {
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
		validationSnapshot(map[uint]state.GroupView{1: validationGroup(
			[]protocol.Protocol{protocol.OpenAICompletions}, "model", nil,
		)}),
		[]state.CredentialRef{{ID: 7, GroupID: 1, EncryptedValue: "cipher-secret"}},
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
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil)}),
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
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil)}),
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
		validationSnapshot(map[uint]state.GroupView{1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil)}),
		nil,
		probes,
	)
	jobs := make(chan state.CredentialRef, 1)
	jobs <- state.CredentialRef{ID: 7, GroupID: 1, EncryptedValue: "key-7"}
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
	refs            []state.CredentialRef
	blacklistedRead int
	recoveryOK      bool
	recorder        *validationEventRecorder
}

func (registry *validationRegistryRecorder) BlacklistedCredentials() []state.CredentialRef {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.blacklistedRead++
	return append([]state.CredentialRef(nil), registry.refs...)
}

func (registry *validationRegistryRecorder) RecoverIfMatch(ref state.CredentialRef, weight int) bool {
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
	encoded, err := json.Marshal(map[string]string{"api_key": "plain-" + value})
	return string(encoded), err
}

type fixedValidationDecryptor struct {
	plaintext string
}

func (decryptor fixedValidationDecryptor) Decrypt(string) (string, error) {
	return decryptor.plaintext, nil
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

type validationTestExecutor struct {
	probes *validationProbeRecorder
}

func (executor *validationTestExecutor) Execute(
	ctx context.Context,
	spec execution.AttemptSpec,
) execution.AttemptResult {
	var credential struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(spec.Credential.Data(), &credential); err != nil {
		return validationExecutionFailure(execution.ErrorKindInvalidRequest)
	}
	if err := executor.probes.invoke(ctx, spec.ClientProtocol, spec.UpstreamModel, credential.APIKey); err != nil {
		kind := execution.ErrorKindProvider
		if errors.Is(err, context.Canceled) {
			kind = execution.ErrorKindCanceled
		}
		return validationExecutionFailure(kind)
	}
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		StatusCode: http.StatusOK, Header: http.Header{},
	}
}

func (*validationTestExecutor) ExecuteStream(
	context.Context,
	execution.AttemptSpec,
	execution.StreamSink,
) execution.StreamResult {
	panic("unexpected stream execution")
}

func validationExecutionFailure(kind execution.ErrorKind) execution.AttemptResult {
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent,
		Error:         &execution.ErrorEvidence{Kind: kind, Summary: "test probe failed"},
	}
}

type validationTestWorker struct {
	*validationWorker
	recorder *validationEventRecorder
}

func newValidationWorkerForTest(snapshot *state.ConfigSnapshot, refs []state.CredentialRef, probes *validationProbeRecorder) *validationTestWorker {
	recorder := &validationEventRecorder{}
	registry := &validationRegistryRecorder{refs: refs, recoveryOK: true, recorder: recorder}
	return &validationTestWorker{
		validationWorker: &validationWorker{
			snapshots: &validationSnapshotRecorder{snapshot: snapshot},
			registry:  registry,
			stats:     &validationStatsRecorder{recorder: recorder},
			mutations: health.NewMutationCoordinator(),
			decryptor: validationDecryptor{},
			channels:  channel.NewRegistry(),
			executor:  &validationTestExecutor{probes: probes},
		},
		recorder: recorder,
	}
}

func newRealRegistryValidationWorker(registry *state.CredentialRegistry, probes *validationProbeRecorder) *validationWorker {
	return &validationWorker{
		snapshots: &validationSnapshotRecorder{snapshot: validationSnapshot(map[uint]state.GroupView{
			1: validationGroup([]protocol.Protocol{protocol.OpenAICompletions}, "model", nil),
		})},
		registry:  registry,
		stats:     health.NewStatsStore(),
		mutations: health.NewMutationCoordinator(),
		decryptor: validationDecryptor{},
		channels:  channel.NewRegistry(),
		executor:  &validationTestExecutor{probes: probes},
	}
}

func validationSnapshot(groups map[uint]state.GroupView) *state.ConfigSnapshot {
	return &state.ConfigSnapshot{Groups: groups}
}

func validationGroup(protocols []protocol.Protocol, validationModel string, models []state.ModelConfig) state.GroupView {
	group := state.GroupView{
		ValidationModel: validationModel,
		Models:          models,
	}
	if len(protocols) == 0 {
		return group
	}
	channelID := channel.OpenAI
	switch protocols[0] {
	case protocol.Anthropic:
		channelID = channel.Anthropic
	case protocol.Gemini:
		channelID = channel.Gemini
	}
	setValidationChannel(&group, channelID, json.RawMessage(`{}`))
	return group
}

func setValidationChannel(group *state.GroupView, channelID channel.ID, params json.RawMessage) {
	resolved, err := channel.NewRegistry().Resolve(channelID, params)
	if err != nil {
		panic(err)
	}
	group.ChannelID = channelID
	group.Params = append(json.RawMessage(nil), params...)
	group.ResolvedTarget = resolved
}

func validationSignatureGroup() state.GroupView {
	group := validationGroup(
		[]protocol.Protocol{protocol.OpenAICompletions},
		"model-a",
		[]state.ModelConfig{{ID: "model-a"}},
	)
	group.ID = 1
	group.HeaderRules = state.HeaderRules{
		Set: map[string]string{
			"X-Alpha": "first",
			"X-Zeta":  "last",
		},
		Remove: []string{"X-Old"},
	}
	return group
}

func cloneValidationGroup(group state.GroupView) state.GroupView {
	cloned := group
	cloned.Params = append(json.RawMessage(nil), group.Params...)
	cloned.ResolvedTarget.TargetConfig = append(json.RawMessage(nil), group.ResolvedTarget.TargetConfig...)
	cloned.Models = append([]state.ModelConfig(nil), group.Models...)
	cloned.HeaderRules.Set = make(map[string]string, len(group.HeaderRules.Set))
	for name, value := range group.HeaderRules.Set {
		cloned.HeaderRules.Set[name] = value
	}
	cloned.HeaderRules.Remove = append([]string(nil), group.HeaderRules.Remove...)
	return cloned
}

func validationManagerCompileInput(upstreamURL string) state.CompileInput {
	return state.CompileInput{ChannelRegistry: channel.NewRegistry(), Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1,
		Name:            "group",
		ChannelID:       channel.OpenAICompatible,
		Params:          json.RawMessage(`{"base_url":"` + upstreamURL + `"}`),
		ValidationModel: "model-a",
		Models:          []state.ModelConfig{{ID: "model-a"}},
		Enabled:         true,
	}}}
}

func validationRefs(count int) []state.CredentialRef {
	refs := make([]state.CredentialRef, count)
	for index := range refs {
		keyID := uint(index + 1)
		refs[index] = state.CredentialRef{ID: keyID, GroupID: 1, EncryptedValue: fmt.Sprintf("key-%d", keyID)}
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
