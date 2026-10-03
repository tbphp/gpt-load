package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/affinity"
	"gpt-load/internal/channel"
	"gpt-load/internal/policy"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

// 1. Policy-only save retains eligible soft affinity
func TestPolicyRouting_PolicyOnlySaveRetainsEligibleAffinity(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(3)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := newAffinityTestEngine(t, handler)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"stable affinity conversation"}]}`

	// Request 1: learns affinity to sk-one (credential 1)
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false})

	// Publish policy-only change: rule applies to another model or credential
	ruleJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "unrelated-rule",
				"name": "Block unrelated model",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "non-existent-model"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "group", GroupID: 1, Config: ruleJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	currentSnap := manager.Current()
	compileInput := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "openai", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{
			{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"},
			{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"},
		},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
		Policies: pv,
	}

	newSnap, err := manager.Publish(compileInput)
	if err != nil {
		t.Fatalf("manager.Publish error = %v", err)
	}
	if newSnap.Revision != currentSnap.Revision+1 {
		t.Fatalf("Revision = %d, want %d", newSnap.Revision, currentSnap.Revision+1)
	}
	if newSnap.AffinityRevision != currentSnap.AffinityRevision {
		t.Fatalf("AffinityRevision = %d, want preserved %d", newSnap.AffinityRevision, currentSnap.AffinityRevision)
	}

	// Request 2: under new snapshot revision, affinity to sk-one MUST be retained!
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, true})
}

// 2. Incompatible configuration change invalidates cached affinity
func TestPolicyRouting_IncompatibleConfigInvalidatesAffinity(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(3)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := newAffinityTestEngine(t, handler)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"session to invalidate"}]}`

	// Request 1: learns affinity to sk-one
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one"})
	assertAffinityHits(t, sink.snapshot(), []bool{false})

	// Publish incompatible change: group name/configuration changed
	currentSnap := manager.Current()
	compileInput := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "openai-modified", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{
			{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"},
			{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"},
		},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}

	newSnap, err := manager.Publish(compileInput)
	if err != nil {
		t.Fatalf("manager.Publish error = %v", err)
	}
	if newSnap.AffinityRevision == currentSnap.AffinityRevision {
		t.Fatalf("AffinityRevision should have incremented on incompatible config, got %d", newSnap.AffinityRevision)
	}

	// Request 2: affinity must have been invalidated (no hit), normal schedule picks next candidate (sk-two)
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false})
}

// 3. Stale snapshot/cache writes are rejected
func TestPolicyRouting_StaleSnapshotCacheWritesAreRejected(t *testing.T) {
	cache := affinity.NewCache()
	target := affinity.Target{GroupID: 1, CredentialID: 1, IdentityGeneration: 1}

	// Configure at Revision 1, AffinityRevision 1
	if !cache.Configure(1, 1, 10, time.Hour) {
		t.Fatal("Configure(1, 1) failed")
	}

	key := affinity.Key("test-key")
	obsRev1 := cache.Lookup(key)
	if obsRev1.Found() {
		t.Fatal("expected miss on rev 1")
	}

	// Move to Revision 2, AffinityRevision 1 (policy-only publish)
	if !cache.Configure(2, 1, 10, time.Hour) {
		t.Fatal("Configure(2, 1) failed")
	}

	// An in-flight request that started on Revision 1 attempts to write back:
	// MUST be rejected because its observation revision (1) != current cache revision (2)!
	if cache.RecordSuccess(key, obsRev1, target) {
		t.Fatal("stale write with revision 1 observation succeeded, want rejection")
	}

	// A request on Revision 2 can write back successfully:
	obsRev2 := cache.Lookup(key)
	if !cache.RecordSuccess(key, obsRev2, target) {
		t.Fatal("valid write on revision 2 failed")
	}
}

// 4. Denied bound candidate (previous_response_id) cannot bypass policy via replay or fallback
func TestPolicyRouting_DeniedBoundCandidateCannotBypassPolicyViaReplayOrFallback(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		storedResponse("initial-resp"),
		storedResponse("must-not-be-reached"),
	}}
	handler, engine, sink := newContinuationFixture(t, forwarder)

	// Step 1: Initial request binds response "initial-resp" to credential 1
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"initial","store":true}`, http.StatusOK)
	if len(forwarder.inputs) != 1 || forwarder.inputs[0].APIKey != "sk-one" {
		t.Fatalf("initial request did not route to sk-one, got %v", forwarder.inputs)
	}

	// Step 2: Publish policy that excludes credential 1 for gpt-4o
	ruleJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "block-cred-1",
				"name": "Block credential 1",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 1, Config: ruleJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	// Update snapshot with policy
	snap := handler.manager.Current()
	compileInput := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "openai", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{
			{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"},
			{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"},
		},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
		Policies: pv,
	}
	if _, err := handler.manager.Publish(compileInput); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	// Step 3: Continuation request referencing previous_response_id: "initial-resp"
	// Credential 1 is bound, but excluded by policy.
	// It MUST NOT fall back to credential 2 (sk-two)!
	// It MUST return 503 (no_available_candidate).
	serveContinuation(t, engine, "gl-client", `{"model":"gpt-4o","input":"continue","previous_response_id":"initial-resp"}`, http.StatusServiceUnavailable)

	// Verify no new attempt was dispatched to any upstream!
	if len(forwarder.inputs) != 1 {
		t.Fatalf("continuation escaped or fell back to another credential: %d inputs", len(forwarder.inputs))
	}
	_ = sink
	_ = snap
}

// 5. Alternate targets remain usable when preferred soft affinity candidate is denied by policy
func TestPolicyRouting_AlternateTargetsRemainUsableWhenPreferredDenied(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(3)}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := newAffinityTestEngine(t, handler)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"fallback conversation"}]}`

	// Request 1: learns affinity to sk-one (credential 1)
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one"})

	// Publish policy excluding credential 1
	ruleJSON := []byte(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "block-cred-1",
				"name": "Block credential 1",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "gpt-4o"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 1, Config: ruleJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	compileInput := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key", ID: 1, Name: "openai", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{
			{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"},
			{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"},
		},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
		Policies: pv,
	}
	if _, err := manager.Publish(compileInput); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	// Request 2: preferred credential 1 is denied by policy, so scheduler must fall back
	// to alternate candidate (sk-two) without failure!
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two"})

	// Request 3: now sk-two was learned by soft affinity, next request hits sk-two
	serveAffinityRequest(t, engine, body)
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-two", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, false, true})
}

// 6. No-policy behavior remains completely consistent
func TestPolicyRouting_NoPolicyBehaviorRemainsConsistent(t *testing.T) {
	forwarder := &scriptedForwarder{results: successfulAffinityResults(4)}
	handler, _, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	engine := newAffinityTestEngine(t, handler)

	// Conversation A
	bodyA := `{"model":"gpt-4o","messages":[{"role":"user","content":"conversation alpha"}]}`
	serveAffinityRequest(t, engine, bodyA)
	serveAffinityRequest(t, engine, bodyA)

	// Conversation B
	bodyB := `{"model":"gpt-4o","messages":[{"role":"user","content":"conversation beta"}]}`
	serveAffinityRequest(t, engine, bodyB)
	serveAffinityRequest(t, engine, bodyB)

	// Both conversations learn affinity and reuse their chosen credentials consistently
	assertAffinityAttemptKeys(t, forwarder.inputs, []string{"sk-one", "sk-one", "sk-two", "sk-two"})
	assertAffinityHits(t, sink.snapshot(), []bool{false, true, false, true})
}

// 7. Live policy admission denies excluded candidates while preserving hangup/cleanup
func TestPolicyRouting_LivePolicyAdmissionPreservesHangupWithoutMigration(t *testing.T) {
	forwarder := &scriptedForwarder{}
	handler, manager, registry := newHandlerForTest(t, forwarder, "sk-one")

	liveModel := channel.CodexLiveModelID
	call := &liveCallSession{
		id:          "call-1",
		keyID:       1,
		keyHash:     handler.encryption.Hash("gl-client"),
		groupID:     1,
		clientModel: liveModel,
		model:       liveModel,
		ref:         state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1},
	}

	// 1. Initial snapshot with Codex channel
	initialInput := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "subscription", ID: 1, Name: "codex", ChannelID: channel.Codex,
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: liveModel}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{
			{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"},
		},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}
	if _, err := manager.Publish(initialInput); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	// Without policy: isLiveCallPolicyAdmitted returns true
	if !handler.isLiveCallPolicyAdmitted(call) {
		t.Fatal("expected isLiveCallPolicyAdmitted = true without policy")
	}
	if !handler.liveCallAuthorized(1, call) {
		t.Fatal("expected liveCallAuthorized = true initially")
	}

	// 2. Publish policy that excludes credential 1 for the live model
	ruleJSON := []byte(fmt.Sprintf(`{
		"schema_version": 1,
		"rules": [
			{
				"id": "block-live-cred-1",
				"name": "Block live credential 1",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": %q},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`, liveModel))
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 1, Config: ruleJSON},
	})
	if err != nil {
		t.Fatalf("CompileRuntimeView error = %v", err)
	}

	compileInput := initialInput
	compileInput.Policies = pv
	if _, err := manager.Publish(compileInput); err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	// 3. With policy: isLiveCallPolicyAdmitted returns false (denies connection admission)
	if handler.isLiveCallPolicyAdmitted(call) {
		t.Fatal("expected isLiveCallPolicyAdmitted = false when policy excludes credential")
	}

	// 4. Hangup authorization is NOT blocked by policy! liveCallAuthorized remains true so cleanup/hangup is preserved
	if !handler.liveCallAuthorized(1, call) {
		t.Fatal("expected liveCallAuthorized = true so hangup/cleanup is preserved")
	}
	_ = registry
}

func policyRegressionCompileInput(h *Handler) state.CompileInput {
	return state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ConnectionType: "api_key",
			ID:             1,
			Name:           "openai",
			ChannelID:      channel.OpenAI,
			Params:         json.RawMessage(`{}`),
			Models:         []state.ModelConfig{{ID: "gpt-4o"}},
			Enabled:        true,
		}},
		Credentials: []state.CredentialConfig{{
			ID:                 1,
			GroupID:            1,
			Status:             state.CredentialStatusActive,
			Version:            1,
			IdentityGeneration: 1,
			Fingerprint:        "credential-1",
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID:      1,
			Name:    "client",
			KeyHash: h.encryption.Hash("gl-client"),
			Status:  state.AccessKeyStatusActive,
		}},
	}
}

// 8. Stale affinity success after publication without intervening lookup is rejected
func TestPolicyRouting_StaleSuccessAfterPublicationWithoutLookup(t *testing.T) {
	h, m, r := newHandlerForTest(t, &scriptedForwarder{}, "sk-one")
	old := m.Current()
	ref, _ := r.CredentialRef(1)
	request := h.resolveRequestAffinity(old, 1, protocol.OpenAICompletions, []byte("review conversation"), map[uint]state.CredentialRef{1: ref}, "")
	in := policyRegressionCompileInput(h)
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{{
		Scope:   "group",
		GroupID: 1,
		Config:  []byte(`{"schema_version":1,"rules":[{"id":"p","name":"p","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	in.Policies = pv
	next, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	if next.AffinityRevision != old.AffinityRevision {
		t.Fatal("not policy-only")
	}
	h.recordAffinitySuccess(request, scheduler.Selection{CredentialID: 1, GroupID: 1, Group: old.Groups[1]}, ref)
	current := h.resolveRequestAffinity(next, 1, protocol.OpenAICompletions, []byte("review conversation"), map[uint]state.CredentialRef{1: ref}, "")
	if current.observation.Found() {
		t.Fatalf("stale success after publication was accepted and survives new revision: old=%d current=%d target=%+v", old.Revision, next.Revision, current.observation.Target)
	}
}

type mockLiveSessionTeardownProbe struct {
	hangups int
	closes  int
}

func (s *mockLiveSessionTeardownProbe) DialSideband(context.Context, string, []string) (*websocket.Conn, int, error) {
	panic("denied sideband must not dial")
}
func (s *mockLiveSessionTeardownProbe) ReleaseSideband(*websocket.Conn) {}
func (s *mockLiveSessionTeardownProbe) Hangup(context.Context) error    { s.hangups++; return nil }
func (s *mockLiveSessionTeardownProbe) Close() error                    { s.closes++; return nil }

// 9. Policy-denied connect unclaims sideband without terminating existing live execution
func TestPolicyRouting_DeniedConnectDoesNotTerminateExistingLiveCall(t *testing.T) {
	h, m, _ := newHandlerForTest(t, &scriptedForwarder{}, "sk-one")
	in := policyRegressionCompileInput(h)
	in.Groups[0].ChannelID = channel.Codex
	in.Groups[0].ConnectionType = "subscription"
	in.Groups[0].Models = []state.ModelConfig{{ID: channel.CodexLiveModelID}}
	snap, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	upstream := &mockLiveSessionTeardownProbe{}
	call := &liveCallSession{
		id:          "call-1",
		keyID:       1,
		keyHash:     h.encryption.Hash("gl-client"),
		groupID:     1,
		clientModel: channel.CodexLiveModelID,
		model:       channel.CodexLiveModelID,
		ref:         state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1},
		upstream:    upstream,
	}
	if !h.liveSessions.put(call) {
		t.Fatal("put failed")
	}
	t.Cleanup(h.CloseCodexLive)

	raw := []byte(`{"schema_version":1,"rules":[{"id":"p","name":"p","domain":"scheduling","enabled":true,"when":{"fact":"upstream.model","op":"eq","value":`)
	val, _ := json.Marshal(call.model)
	raw = append(raw, val...)
	raw = append(raw, []byte(`},"then":{"type":"exclude_candidate"}}]}`)...)
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{{Scope: "group", GroupID: 1, Config: raw}})
	if err != nil {
		t.Fatal(err)
	}
	in.Policies = pv
	snap, err = m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	if !h.liveCallAuthorized(1, call) {
		t.Fatal("lifetime auth false")
	}
	if h.isLiveCallPolicyAdmitted(call) {
		t.Fatal("policy should deny")
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/live/call-1", nil)
	c.Params = gin.Params{{Key: "call_id", Value: "call-1"}}
	h.connectCodexLive(c, &dataPlaneRequestContext{snapshot: snap, accessKey: snap.AccessKeysByID[1]})
	if upstream.hangups != 0 || upstream.closes != 0 {
		t.Fatalf("policy denied new sideband but terminated existing live execution: hangups=%d closes=%d", upstream.hangups, upstream.closes)
	}
}

// 10. Multiple incompatible publications without intervening lookups properly invalidate cached affinity
func TestPolicyRouting_MultiplePublicationsNoLookupInvalidateCompatibility(t *testing.T) {
	h, m, r := newHandlerForTest(t, &scriptedForwarder{}, "sk-one")
	old := m.Current()
	ref, _ := r.CredentialRef(1)
	req := h.resolveRequestAffinity(old, 1, protocol.OpenAICompletions, []byte("generation"), map[uint]state.CredentialRef{1: ref}, "")
	h.recordAffinitySuccess(req, scheduler.Selection{CredentialID: 1, GroupID: 1, Group: old.Groups[1]}, ref)
	in := policyRegressionCompileInput(h)
	in.Groups[0].Name = "changed"
	second, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	in = policyRegressionCompileInput(h)
	third, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	fourth, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	if second.AffinityRevision != old.AffinityRevision+1 || third.AffinityRevision != second.AffinityRevision+1 || fourth.AffinityRevision != third.AffinityRevision {
		t.Fatal("bad generation transition")
	}
	found := h.resolveRequestAffinity(fourth, 1, protocol.OpenAICompletions, []byte("generation"), map[uint]state.CredentialRef{1: ref}, "")
	if found.observation.Found() {
		t.Fatal("incompatible intermediate publication not remembered")
	}
}

// 11. Stale request interleaved between Configure and Lookup is rejected on success record
func TestPolicyRouting_PublicationBetweenConfigureAndLookupRejectsOldRequest(t *testing.T) {
	h, m, r := newHandlerForTest(t, &scriptedForwarder{}, "sk-one")
	old := m.Current()
	ref, _ := r.CredentialRef(1)
	key := affinity.Key("configure-lookup-interleaving")
	if !h.affinityCache.Configure(old.Revision, old.AffinityRevision, old.Settings.AffinityCapacity, old.Settings.AffinityTTL) {
		t.Fatal("old Configure failed")
	}
	in := policyRegressionCompileInput(h)
	pv, err := policy.CompileRuntimeView([]policy.BindingConfig{{
		Scope:   "group",
		GroupID: 1,
		Config:  []byte(`{"schema_version":1,"rules":[{"id":"p","name":"p","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	in.Policies = pv
	next, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	request := requestAffinity{key: key, observation: h.affinityCache.Lookup(key), snapshotRevision: old.Revision}
	h.recordAffinitySuccess(request, scheduler.Selection{CredentialID: 1, GroupID: 1, Group: old.Groups[1]}, ref)
	if got := h.affinityCache.Lookup(key); got.Found() {
		t.Fatalf("old request accepted after publication between Configure and Lookup: old=%d current=%d target=%+v", old.Revision, next.Revision, got.Target)
	}
}
