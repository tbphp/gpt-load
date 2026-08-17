package gateway

import (
	"bytes"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/affinity"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

type affinityFixedRandSource struct {
	value int64
}

const affinitySecondCredentialRand int64 = 3000

func (source affinityFixedRandSource) Int63() int64 { return source.value }
func (affinityFixedRandSource) Seed(int64)          {}

func TestHandlerLearnsAndReusesAutomaticSoftAffinity(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(2)}
	handler, _, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	useAffinityRandomValues(handler, 0, affinitySecondCredentialRand)
	engine := newAffinityTestEngine(t, handler)

	serveAffinityRequest(t, engine, `{
		"model":"gpt-4o","temperature":0.1,
		"messages":[
			{"role":"system","content":"Be helpful"},
			{"role":"user","content":"Hello"}
		]
	}`)
	serveAffinityRequest(t, engine, `{
		"model":"gpt-4o","temperature":1,
		"messages":[
			{"role":"system","content":[{"type":"text","text":"Be helpful"}]},
			{"role":"user","content":[{"type":"input_text","text":"Hello"}]},
			{"role":"assistant","content":"prior answer"},
			{"role":"user","content":"later turn"}
		]
	}`)

	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, true})
}

func TestHandlerReusesSoftAffinityAcrossModelsWithSameCandidateRange(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(2)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	addAffinityModelRoute(t, manager.Current(), "gpt-4o-mini", 1)
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	useAffinityRandomValues(handler, 0, affinitySecondCredentialRand)
	engine := newAffinityTestEngine(t, handler)

	serveAffinityModelRequest(t, engine, "gpt-4o")
	serveAffinityModelRequest(t, engine, "gpt-4o-mini")

	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, true})
}

func TestHandlerRelearnsSoftAffinityWhenModelCandidateRangeChanges(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(4)}
	handler, manager, registry := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	moveSecondAffinityCredentialToGroup(t, manager.Current(), registry)
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	useAffinityRandomValues(handler, 0, 0, 0, 0)
	engine := newAffinityTestEngine(t, handler)

	serveAffinityModelRequest(t, engine, "gpt-4o")
	serveAffinityModelRequest(t, engine, "gpt-4o-mini")
	serveAffinityModelRequest(t, engine, "gpt-4o-mini")
	serveAffinityModelRequest(t, engine, "gpt-4o")

	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two", "sk-two", "sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false, true, false})
	assertAffinityUpstreamModels(t, forwarder.inputs, []string{"gpt-4o", "gpt-4o-mini", "gpt-4o-mini", "gpt-4o"})
}

func TestHandlerDoesNotLearnAffinityForNonParticipatingGroup(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(2)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	snapshot := manager.Current()
	group := snapshot.Groups[1]
	group.AffinityEnabled = false
	snapshot.Groups[1] = group
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	useAffinityRandomValues(handler, 0, affinitySecondCredentialRand)
	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable conversation"}]}`

	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)

	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false})
}

func TestHandlerGroupAffinityOverrideWinsOverGlobalSetting(t *testing.T) {
	tests := []struct {
		name           string
		globalEnabled  bool
		groupOverrides config.Settings
		wantKeys       []string
		wantHits       []bool
	}{
		{
			name: "group enables when global is disabled", globalEnabled: false,
			groupOverrides: config.Settings{state.SettingAffinityEnabled: true},
			wantKeys:       []string{"sk-one", "sk-one"}, wantHits: []bool{false, true},
		},
		{
			name: "group disables when global is enabled", globalEnabled: true,
			groupOverrides: config.Settings{state.SettingAffinityEnabled: false},
			wantKeys:       []string{"sk-one", "sk-two"}, wantHits: []bool{false, false},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: successfulAffinityResults(2)}
			handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
			current := manager.Current()
			group := current.Groups[1]
			resolved, err := state.ResolveGroupRuntimeSettings(state.RuntimeSettings{
				AffinityEnabled:  test.globalEnabled,
				AffinityTTL:      time.Hour,
				AffinityCapacity: 10_000,
			}, test.groupOverrides)
			if err != nil {
				t.Fatal(err)
			}
			group.AffinityEnabled = resolved.AffinityEnabled
			current.Settings.AffinityEnabled = test.globalEnabled
			current.Groups[1] = group
			sink := &recordingRequestLogSink{}
			handler.requestLogSink = sink
			useAffinityRandomValues(handler, 0, affinitySecondCredentialRand)
			engine := newAffinityTestEngine(t, handler)
			body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable conversation"}]}`

			serveAffinityRequest(t, engine, body)
			serveAffinityRequest(t, engine, body)

			assertAffinityAttemptKeys(t, forwarder.inputs, test.wantKeys)
			assertAffinityHits(t, sink.snapshot(), test.wantHits)
		})
	}
}

func TestHandlerSoftAffinityRetriesAndRebindsAfterFallbackSuccess(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		successfulAffinityResult(),
		{
			StatusCode:         http.StatusTooManyRequests,
			Header:             http.Header{"Retry-After": {"30"}},
			Body:               []byte(`{"error":"rate_limit"}`),
			ClassificationBody: []byte(`{"error":"rate_limit"}`),
			RequestWritten:     true,
		},
		successfulAffinityResult(),
		successfulAffinityResult(),
	}}
	handler, _, registry := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	useAffinityRandomValues(handler, 0, affinitySecondCredentialRand, 0)
	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable conversation"}]}`

	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)
	if !registry.RestoreRuntimeState(1, state.DefaultWeight) {
		t.Fatal("RestoreRuntimeState() = false, want credential 1 restored")
	}
	serveAffinityRequest(t, engine, body)

	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one", "sk-two", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, true, true})
}

func TestHandlerSoftAffinitySkipsDisabledCredentialAndLearnsReplacement(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(3)}
	handler, _, registry := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	useAffinityRandomValues(handler, 0, 0, 0)
	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable conversation"}]}`

	serveAffinityRequest(t, engine, body)
	if err := registry.SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
		t.Fatalf("SetCredentialStatus(disabled) error = %v", err)
	}
	serveAffinityRequest(t, engine, body)
	if err := registry.SetCredentialStatus(1, state.CredentialStatusActive); err != nil {
		t.Fatalf("SetCredentialStatus(active) error = %v", err)
	}
	serveAffinityRequest(t, engine, body)

	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false, true})
}

func TestHandlerDoesNotApplyAffinityWithoutInitialUserText(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(2)}
	handler, _, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	useAffinityRandomValues(handler, 0, affinitySecondCredentialRand)
	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","messages":[{"role":"system","content":"shared instruction"}]}`

	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)

	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false})
}

func TestHandlerLearnsAffinityOnlyFromCleanCompletedStream(t *testing.T) {
	forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
		{StatusCode: http.StatusOK, Committed: true, Stream: StreamObservation{EndReason: StreamEndProviderIncomplete}},
		{StatusCode: http.StatusOK, Committed: true, Stream: StreamObservation{EndReason: StreamEndCleanEOF}},
		{StatusCode: http.StatusOK, Committed: true, Stream: StreamObservation{EndReason: StreamEndCleanEOF}},
	}}
	handler, _, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	useAffinityRandomValues(handler, 0, affinitySecondCredentialRand, 0)
	engine := newAffinityTestEngine(t, handler)
	body := `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"stable stream"}]}`

	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)
	serveAffinityRequest(t, engine, body)

	assertAffinityAttemptKeys(t, forwarder.streamInputs, []string{"sk-one", "sk-two", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false, true})
}

func TestHandlerIgnoresAffinityAfterCredentialIdentityChanges(t *testing.T) {
	handler, manager, _ := newHandlerForTest(t, &scriptedForwarder{}, "sk-one")
	snapshot := manager.Current()
	prefix := []byte(`{"v":1,"user":["hello"]}`)
	oldRef := state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1}
	initial := handler.resolveRequestAffinity(
		snapshot,
		1,
		protocol.OpenAICompletions,
		prefix,
		map[uint]state.CredentialRef{1: oldRef},
	)
	if initial.preferredCredentialID != 0 || !initial.key.Valid() {
		t.Fatalf("initial affinity = %#v, want valid miss", initial)
	}
	if !handler.affinityCache.RecordSuccess(
		initial.key,
		initial.observation,
		affinity.Target{GroupID: 1, CredentialID: 1, IdentityGeneration: 1},
	) {
		t.Fatal("RecordSuccess() = false, want cached identity")
	}

	hit := handler.resolveRequestAffinity(
		snapshot,
		1,
		protocol.OpenAICompletions,
		prefix,
		map[uint]state.CredentialRef{1: oldRef},
	)
	if hit.preferredCredentialID != 1 {
		t.Fatalf("preferred credential = %d, want 1", hit.preferredCredentialID)
	}
	changedRef := oldRef
	changedRef.IdentityGeneration = 2
	stale := handler.resolveRequestAffinity(
		snapshot,
		1,
		protocol.OpenAICompletions,
		prefix,
		map[uint]state.CredentialRef{1: changedRef},
	)
	if stale.preferredCredentialID != 0 {
		t.Fatalf("preferred credential after identity change = %d, want 0", stale.preferredCredentialID)
	}
}

func TestHandlerDerivesPrivateContinuityWithoutReenablingDisabledAffinity(t *testing.T) {
	handler, manager, _ := newHandlerForTest(t, &scriptedForwarder{}, "sk-one")
	snapshot := manager.Current()
	snapshot.Settings.AffinityCapacity = 0
	resolved := handler.resolveRequestAffinity(
		snapshot,
		1,
		protocol.OpenAICompletions,
		[]byte(`{"v":1,"user":["hello"]}`),
		map[uint]state.CredentialRef{1: {ID: 1, GroupID: 1, IdentityGeneration: 1}},
	)
	if resolved.key.Valid() || resolved.preferredCredentialID != 0 || resolved.continuityKey == "" {
		t.Fatalf("disabled affinity resolution = %#v", resolved)
	}
}

func successfulAffinityResults(count int) []UpstreamResult {
	results := make([]UpstreamResult, count)
	for index := range results {
		results[index] = successfulAffinityResult()
	}
	return results
}

func successfulAffinityResult() UpstreamResult {
	return UpstreamResult{
		StatusCode:     http.StatusOK,
		Header:         make(http.Header),
		Body:           []byte(`{"ok":true}`),
		RequestWritten: true,
	}
}

func useAffinityRandomValues(handler *Handler, values ...int64) {
	index := 0
	handler.newRandom = func() *rand.Rand {
		value := int64(0)
		if index < len(values) {
			value = values[index]
			index++
		}
		return rand.New(affinityFixedRandSource{value: value})
	}
}

func addAffinityModelRoute(
	t *testing.T,
	snapshot *state.ConfigSnapshot,
	externalModel string,
	groupID uint,
) {
	t.Helper()
	byOperation := snapshot.ExecutionCandidates[protocol.OpenAICompletions]
	if byOperation == nil {
		t.Fatal("OpenAI Completions execution candidates are missing")
	}
	byModel := byOperation[execution.OperationChatCompletion]
	base := byModel["gpt-4o"]
	if len(base) != 1 {
		t.Fatalf("gpt-4o routes = %d, want 1", len(base))
	}
	route := base[0]
	route.GroupID = groupID
	route.UpstreamModelID = externalModel
	byModel[externalModel] = []state.RouteTarget{route}
}

func moveSecondAffinityCredentialToGroup(
	t *testing.T,
	snapshot *state.ConfigSnapshot,
	registry *state.CredentialRegistry,
) {
	t.Helper()
	group := snapshot.Groups[1]
	group.ID = 2
	group.Name = "openai-two"
	group.Models = []state.ModelConfig{{ID: "gpt-4o-mini"}}
	snapshot.Groups[2] = group
	catalog := snapshot.GroupCatalog[1]
	catalog.ID = 2
	catalog.Name = group.Name
	snapshot.GroupCatalog[2] = catalog
	addAffinityModelRoute(t, snapshot, "gpt-4o-mini", 2)

	refs := registry.CaptureActiveCredentialRefs([]uint{1})
	if len(refs) != 2 {
		t.Fatalf("captured credential refs = %d, want 2", len(refs))
	}
	entries := make([]state.CredentialEntry, 0, len(refs))
	for _, ref := range refs {
		groupID := uint(1)
		if ref.ID == 2 {
			groupID = 2
		}
		entries = append(entries, state.CredentialEntry{
			ID: ref.ID, GroupID: groupID, Version: ref.Version,
			IdentityGeneration: ref.IdentityGeneration, Fingerprint: ref.Fingerprint,
			Status: state.CredentialStatusActive, EncryptedValue: ref.EncryptedValue,
		})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
}

func serveAffinityModelRequest(t *testing.T, engine http.Handler, model string) {
	t.Helper()
	serveAffinityRequest(
		t,
		engine,
		`{"model":"`+model+`","messages":[{"role":"user","content":"stable conversation"}]}`,
	)
}

func newAffinityTestEngine(t *testing.T, handler *Handler) http.Handler {
	t.Helper()
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine
}

func serveAffinityRequest(t *testing.T, engine http.Handler, body string) {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(body),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("response = %d %s, want 200", response.Code, response.Body.String())
	}
}

func assertAffinityAttemptKeys(t *testing.T, inputs []ForwardInput, want []string) {
	t.Helper()
	got := make([]string, 0, len(inputs))
	for _, input := range inputs {
		got = append(got, input.APIKey)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attempt keys = %#v, want %#v", got, want)
	}
}

func assertAffinityHits(t *testing.T, events []telemetry.RequestEvent, want []bool) {
	t.Helper()
	got := make([]bool, 0, len(events))
	for _, event := range events {
		got = append(got, event.AffinityHit)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("affinity hits = %#v, want %#v", got, want)
	}
}

func assertAffinityUpstreamModels(t *testing.T, inputs []ForwardInput, want []string) {
	t.Helper()
	got := make([]string, 0, len(inputs))
	for _, input := range inputs {
		got = append(got, input.UpstreamModelID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upstream models = %#v, want %#v", got, want)
	}
}
