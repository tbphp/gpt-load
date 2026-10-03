package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/affinity"
	"gpt-load/internal/automodel"
	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/policy"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	observation "gpt-load/internal/subscription/providers/observation"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
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
	currentSnap := manager.Current()
	compileInput := policyRegressionCompileInput(handler)
	compileInput.Credentials = append(compileInput.Credentials, state.CredentialConfig{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"})
	compileInput.PolicyBindings = []policy.BindingConfig{{Scope: "group", GroupID: 1, Config: ruleJSON}}

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
	compileInput := policyRegressionCompileInput(handler)
	compileInput.Credentials = append(compileInput.Credentials, state.CredentialConfig{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"})
	compileInput.Groups[0].Name = "openai-modified"

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
		"group_policy": "override",
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
	// Update snapshot with policy
	snap := handler.manager.Current()
	compileInput := policyRegressionCompileInput(handler)
	compileInput.Credentials = append(compileInput.Credentials, state.CredentialConfig{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"})
	compileInput.PolicyBindings = []policy.BindingConfig{{Scope: "credential", GroupID: 1, CredentialID: 1, Config: ruleJSON}}
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
		"group_policy": "override",
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
	compileInput := policyRegressionCompileInput(handler)
	compileInput.Credentials = append(compileInput.Credentials, state.CredentialConfig{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"})
	compileInput.PolicyBindings = []policy.BindingConfig{{Scope: "credential", GroupID: 1, CredentialID: 1, Config: ruleJSON}}
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
	initialInput := policyRegressionCompileInput(handler)
	initialInput.Groups[0].ConnectionType = "subscription"
	initialInput.Groups[0].Name = "codex"
	initialInput.Groups[0].ChannelID = channel.Codex
	initialInput.Groups[0].Models = []state.ModelConfig{{ID: liveModel}}
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
		"group_policy": "override",
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
	compileInput := initialInput
	compileInput.PolicyBindings = []policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 1, Config: ruleJSON},
	}
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
	in.PolicyBindings = []policy.BindingConfig{{
		Scope:   "group",
		GroupID: 1,
		Config:  []byte(`{"schema_version":1,"rules":[{"id":"p","name":"p","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`),
	}}
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
	in.PolicyBindings = []policy.BindingConfig{{Scope: "group", GroupID: 1, Config: raw}}
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
	in.PolicyBindings = []policy.BindingConfig{{
		Scope:   "group",
		GroupID: 1,
		Config:  []byte(`{"schema_version":1,"rules":[{"id":"p","name":"p","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`),
	}}
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

// 12. Dynamic policy pricing freezes per attempt and produces v7 receipt with ordered factors
func TestPolicyPricing_DynamicMultiplierFrozenPerAttemptAndUnchangedByPublication(t *testing.T) {
	forwarder := &scriptedForwarder{
		results: []UpstreamResult{
			completeScriptedUpstreamResult(UpstreamResult{
				StatusCode:     http.StatusOK,
				RequestWritten: true,
				Body:           []byte(`{"id":"chat-1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`),
				Usage:          usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, Output: 10}},
			}),
		},
	}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	handler.priceTables = &mutableGatewayPriceTableProvider{
		table: mustGatewayPriceTable(t, 2_000_000_000, true),
	}
	engine := newAffinityTestEngine(t, handler)

	// Publish policy rules: group x2 (suppressed), credential override x3
	in := policyRegressionCompileInput(handler)
	in.PolicyBindings = []policy.BindingConfig{
		{
			Scope:   "group",
			GroupID: 1,
			Config:  []byte(`{"schema_version":1,"rules":[{"id":"p-grp","name":"Group Double","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`),
		},
		{
			Scope:        "credential",
			GroupID:      1,
			CredentialID: 1,
			Config:       []byte(`{"schema_version":1,"group_policy":"override","rules":[{"id":"p-cred","name":"Cred Triple","domain":"pricing","enabled":true,"when":{"fact":"upstream.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"3"}}]}`),
		},
	}
	if _, err := manager.Publish(in); err != nil {
		t.Fatal(err)
	}

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"test dynamic pricing"}]}`
	serveAffinityRequest(t, engine, body)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	event := events[0]
	if event.Usage.Pricing.ReceiptJSON == "" {
		t.Fatal("expected non-empty ReceiptJSON")
	}
	var receipt pricing.Receipt
	if err := json.Unmarshal([]byte(event.Usage.Pricing.ReceiptJSON), &receipt); err != nil {
		t.Fatalf("unmarshal receipt error: %v", err)
	}
	if receipt.SchemaVersion != 7 {
		t.Fatalf("receipt schema = %d, want 7", receipt.SchemaVersion)
	}
	if len(receipt.PolicyFactors) != 1 {
		t.Fatalf("expected 1 policy factor (credential override), got %d: %+v", len(receipt.PolicyFactors), receipt.PolicyFactors)
	}
	if receipt.PolicyFactors[0].RuleID != "p-cred" || receipt.PolicyFactors[0].BindingScope != "credential" || receipt.PolicyFactors[0].Factor != "3" {
		t.Errorf("factor mismatch: %+v", receipt.PolicyFactors[0])
	}
	if receipt.BaseTotalNanoUSD == nil {
		t.Fatal("expected non-nil BaseTotalNanoUSD")
	}
	// override 仅取凭据 x3；分组动态规则被抑制，静态 base multiplier 不变
	wantTotal := *receipt.BaseTotalNanoUSD * 3
	if receipt.TotalNanoUSD != wantTotal {
		t.Fatalf("receipt total = %d, want %d (base %d * 3)", receipt.TotalNanoUSD, wantTotal, *receipt.BaseTotalNanoUSD)
	}
	if err := pricing.ValidateReceipt(receipt); err != nil {
		t.Fatalf("ValidateReceipt failed on emitted receipt: %v", err)
	}
}

// 13. Retries swap credential and freeze newly selected credential's pricing
func TestPolicyPricing_RetrySwapsCredentialAndFreezesNewPricing(t *testing.T) {
	failure := completeScriptedUpstreamResult(UpstreamResult{
		StatusCode:     http.StatusForbidden,
		RequestWritten: true,
		Body:           []byte(`{"error":{"message":"unclassified upstream rejection"}}`),
	})
	success := completeScriptedUpstreamResult(UpstreamResult{
		StatusCode:     http.StatusOK,
		RequestWritten: true,
		Body:           []byte(`{"id":"chat-2","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"hello retry"}}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`),
		Usage:          usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, Output: 10}},
	})
	forwarder := &scriptedForwarder{results: []UpstreamResult{failure, success}}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one", "sk-two")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	handler.priceTables = &mutableGatewayPriceTableProvider{
		table: mustGatewayPriceTable(t, 2_000_000_000, true),
	}
	engine := newAffinityTestEngine(t, handler)

	// Credential 1 has rule x2; Credential 2 has rule x0.5
	in := policyRegressionCompileInput(handler)
	in.Credentials = []state.CredentialConfig{
		{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-1"},
		{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-2"},
	}
	in.PolicyBindings = []policy.BindingConfig{
		{
			Scope:        "credential",
			GroupID:      1,
			CredentialID: 1,
			Config:       []byte(`{"schema_version":1,"group_policy":"override","rules":[{"id":"p-cred1","name":"Cred 1 Double","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`),
		},
		{
			Scope:        "credential",
			GroupID:      1,
			CredentialID: 2,
			Config:       []byte(`{"schema_version":1,"group_policy":"override","rules":[{"id":"p-cred2","name":"Cred 2 Half","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"0.5"}}]}`),
		},
	}
	if _, err := manager.Publish(in); err != nil {
		t.Fatal(err)
	}
	manager.Current().Settings.RetryCount = 2

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"test retry pricing"}]}`
	serveAffinityRequest(t, engine, body)

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	event := events[0]
	var receipt pricing.Receipt
	if err := json.Unmarshal([]byte(event.Usage.Pricing.ReceiptJSON), &receipt); err != nil {
		t.Fatalf("unmarshal receipt error: %v", err)
	}
	if receipt.SchemaVersion != 7 {
		t.Fatalf("receipt schema = %d, want 7", receipt.SchemaVersion)
	}
	if len(receipt.PolicyFactors) != 1 {
		t.Fatalf("expected 1 policy factor from credential 2, got %d: %+v", len(receipt.PolicyFactors), receipt.PolicyFactors)
	}
	// Attempt 2 succeeded on Credential 2, which has rule p-cred2 (0.5), NOT p-cred1 (2)
	if receipt.PolicyFactors[0].RuleID != "p-cred2" || receipt.PolicyFactors[0].Factor != "0.5" {
		t.Fatalf("expected cred2 factor 0.5, got: %+v", receipt.PolicyFactors[0])
	}
	wantTotal := *receipt.BaseTotalNanoUSD / 2
	if receipt.TotalNanoUSD != wantTotal {
		t.Fatalf("receipt total = %d, want %d (base %d / 2)", receipt.TotalNanoUSD, wantTotal, *receipt.BaseTotalNanoUSD)
	}
}

// 14. Configuration/name changes do not retroactively alter previously frozen attempt receipts
func TestPolicyPricing_FrozenConfigNameChangeDoesNotAffectPastAttempts(t *testing.T) {
	forwarder := &scriptedForwarder{
		results: []UpstreamResult{
			completeScriptedUpstreamResult(UpstreamResult{
				StatusCode:     http.StatusOK,
				RequestWritten: true,
				Body:           []byte(`{"id":"chat-1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"first"}}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`),
				Usage:          usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, Output: 10}},
			}),
			completeScriptedUpstreamResult(UpstreamResult{
				StatusCode:     http.StatusOK,
				RequestWritten: true,
				Body:           []byte(`{"id":"chat-2","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"second"}}],"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`),
				Usage:          usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, Output: 10}},
			}),
		},
	}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-one")
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	handler.priceTables = &mutableGatewayPriceTableProvider{
		table: mustGatewayPriceTable(t, 2_000_000_000, true),
	}
	engine := newAffinityTestEngine(t, handler)

	// Publish v1: rule name "Original Name", factor "2", explicit binding revision 101
	in := policyRegressionCompileInput(handler)
	in.PolicyBindings = []policy.BindingConfig{
		{
			Scope:    "group",
			GroupID:  1,
			Revision: 101,
			Config:   []byte(`{"schema_version":1,"rules":[{"id":"p-dyn","name":"Original Name","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`),
		},
	}
	if _, err := manager.Publish(in); err != nil {
		t.Fatal(err)
	}

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"request 1"}]}`
	serveAffinityRequest(t, engine, body)

	// Publish v2: rename rule to "Renamed Rule" and factor to "5", explicit binding revision 202
	in2 := policyRegressionCompileInput(handler)
	in2.PolicyBindings = []policy.BindingConfig{
		{
			Scope:    "group",
			GroupID:  1,
			Revision: 202,
			Config:   []byte(`{"schema_version":1,"rules":[{"id":"p-dyn","name":"Renamed Rule","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"5"}}]}`),
		},
	}
	if _, err := manager.Publish(in2); err != nil {
		t.Fatal(err)
	}

	body2 := `{"model":"gpt-4o","messages":[{"role":"user","content":"request 2"}]}`
	serveAffinityRequest(t, engine, body2)

	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Verify Event 1 retains binding revision 101, original name snapshot "Original Name", and factor 2
	var r1 pricing.Receipt
	if err := json.Unmarshal([]byte(events[0].Usage.Pricing.ReceiptJSON), &r1); err != nil {
		t.Fatal(err)
	}
	if len(r1.PolicyFactors) != 1 || r1.PolicyFactors[0].NameSnapshot != "Original Name" ||
		r1.PolicyFactors[0].Factor != "2" || r1.PolicyFactors[0].Revision != 101 {
		t.Fatalf("event 1 was corrupted by subsequent publish: %+v", r1.PolicyFactors[0])
	}

	// Verify Event 2 has binding revision 202, renamed name snapshot "Renamed Rule", and factor 5
	var r2 pricing.Receipt
	if err := json.Unmarshal([]byte(events[1].Usage.Pricing.ReceiptJSON), &r2); err != nil {
		t.Fatal(err)
	}
	if len(r2.PolicyFactors) != 1 || r2.PolicyFactors[0].NameSnapshot != "Renamed Rule" ||
		r2.PolicyFactors[0].Factor != "5" || r2.PolicyFactors[0].Revision != 202 {
		t.Fatalf("event 2 mismatch: %+v", r2.PolicyFactors[0])
	}
}

// 15. In-flight unary/stream freeze with price publication: request log estimate matches quota deduction
func TestPolicyPricing_InFlightPricePublicationLogMatchesQuota(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "unary"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode:     http.StatusOK,
				Header:         make(http.Header),
				RequestWritten: true,
				Body:           []byte(`{"ok":true}`),
				Usage:          usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 1, Output: 1}},
			}}}
			if stream {
				forwarder.results[0].Committed = true
				forwarder.results[0].Stream = StreamObservation{EndReason: StreamEndCleanEOF}
				forwarder.streamResults = forwarder.results
			}
			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
				t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "sk-first",
			)
			runtime := accessquota.NewRuntime()
			rules := []accessquota.Rule{{ID: 901, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 10_000_000}}
			if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: rules}); err != nil {
				t.Fatal(err)
			}
			handler.accessQuota = runtime
			table, err := pricing.NewTable([]pricing.Rule{{
				Identity: pricing.Identity{ChannelID: "openai", ModelID: "gpt-4o"},
				Prices: pricing.Prices{
					Input:  pricing.Price{NanoUSDPerMillion: 600_000, Set: true},
					Output: pricing.Price{NanoUSDPerMillion: 600_000, Set: true},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			handler.priceTables = &mutableGatewayPriceTableProvider{table: table}
			input := gatewayAccessQuotaCompileInput(handler, rules)
			setGatewayPriceMultipliers(t, &input, "2", "1")
			input.PolicyBindings = []policy.BindingConfig{{
				Scope:    "group",
				GroupID:  1,
				Revision: 55,
				Config:   []byte(`{"schema_version":1,"rules":[{"id":"dyn","name":"dyn","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"3"}}]}`),
			}}
			if _, err := manager.Publish(input); err != nil {
				t.Fatal(err)
			}
			forwarder.onCall = func(int) {
				setGatewayPriceMultipliers(t, &input, "7", "9")
				input.PolicyBindings[0].Config = []byte(`{"schema_version":1,"rules":[{"id":"dyn","name":"changed","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"9"}}]}`)
				if _, err := manager.Publish(input); err != nil {
					t.Fatal(err)
				}
			}
			forwarder.onStreamCall = func(index int, _ http.ResponseWriter) { forwarder.onCall(index) }

			body := `{"model":"gpt-4o"}`
			if stream {
				body = `{"model":"gpt-4o","stream":true}`
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer gl-client")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			events := sink.snapshot()
			if len(events) != 1 || events[0].Usage.Pricing.EstimatedCostNanoUSD != 12 {
				t.Fatalf("frozen multiplier estimate = %#v, want 12", events)
			}
			view := runtime.Snapshot(1, time.Now())
			if len(view.Rules) != 1 || view.Rules[0].UsedNanoUSD != 12 {
				t.Fatalf("quota = %#v, want matching deduction 12", view)
			}
		})
	}
}

// 16. Replacement identity does not inherit previous identity's quota facts in pricing freeze
func TestPolicyPricing_ReplacementIdentityQuotaBecomesUnknown(t *testing.T) {
	h, m, r := newHandlerForTest(t, &scriptedForwarder{}, "sk-one")
	in := policyRegressionCompileInput(h)
	in.PolicyBindings = []policy.BindingConfig{{
		Scope:    "group",
		GroupID:  1,
		Revision: 10,
		Config:   []byte(`{"schema_version":1,"rules":[{"id":"p","name":"low quota price","domain":"pricing","enabled":true,"when":{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.2},"then":{"type":"multiply_price","factor":"2"}}]}`),
	}}
	snap, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	model := "gpt-4o"
	query := scheduler.Query{ClientProtocol: protocol.OpenAICompletions, ExternalModel: &model}
	sel, err := scheduler.New(snap, r, query).Next()
	if err != nil {
		t.Fatal(err)
	}
	oldRef, _ := r.CredentialRef(1)
	if err = r.ReplaceCredentials([]state.CredentialEntry{{
		ID:                 1,
		GroupID:            1,
		Status:             state.CredentialStatusActive,
		Version:            2,
		IdentityGeneration: 2,
		Fingerprint:        "replacement",
		EncryptedValue:     oldRef.EncryptedValue,
	}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	h.now = func() time.Time { return now }
	used := 0.95
	win := int64(18000)
	reset := now.Add(time.Hour).UnixMilli()
	observed := now.Add(-time.Second).UnixMilli()
	if !r.ApplyQuotaWindows(1, 2, []observation.QuotaWindow{{
		ID: "primary", SourceID: "account", Scope: "account", Unit: "ratio",
		Utilization: &used, WindowSeconds: &win, ResetAtMS: &reset, ObservedAtMS: &observed, State: "available",
	}}) {
		t.Fatal("apply quota failed")
	}
	frozen := h.freezeAttemptPricing(snap, sel, dialect.RequestMetadata{}, true, pricing.DefaultPriceMultiplier, model)
	if len(frozen.policyFactors) != 0 {
		t.Fatalf("old attempt priced from replacement quota facts: %+v", frozen.policyFactors)
	}
}

// 17. WebSocket turn in-flight freeze preserves snapshot despite concurrent publication; sequential turn picks up new snapshot
func TestPolicyPricing_WebSocketTurnsFreezeDynamicPricingPerTurn(t *testing.T) {
	turn1Received := make(chan struct{})
	turn1Proceed := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for n := 1; n <= 2; n++ {
			var req map[string]any
			if conn.ReadJSON(&req) != nil {
				return
			}
			if n == 1 {
				// Signal that Turn 1 is active/in-flight on upstream
				close(turn1Received)
				// Block until concurrent policy publish completes
				<-turn1Proceed
			}
			if conn.WriteMessage(websocket.TextMessage, websocketCompleted(fmt.Sprintf("resp_%d", n), "")) != nil {
				return
			}
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer upstream.Close()

	h, engine, input := websocketTestHandler(t, upstream.URL, channel.OpenAI)
	sink := &recordingRequestLogSink{}
	h.requestLogSink = sink

	input.PolicyBindings = []policy.BindingConfig{{
		Scope: "group", GroupID: 1, Revision: 77,
		Config: []byte(`{"schema_version":1,"rules":[{"id":"p-ws","name":"ws price","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"public"},"then":{"type":"multiply_price","factor":"2"}}]}`),
	}}
	table, err := pricing.NewTable([]pricing.Rule{{
		Identity: pricing.Identity{ChannelID: string(channel.OpenAI), ModelID: "upstream"},
		Prices: pricing.Prices{
			Input:  pricing.Price{NanoUSDPerMillion: 100_000_000, Set: true},
			Output: pricing.Price{NanoUSDPerMillion: 100_000_000, Set: true},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	h.priceTables = &mutableGatewayPriceTableProvider{table: table}
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(engine)
	defer server.Close()

	conn := dialGatewayWebsocket(t, server.URL)
	defer conn.Close()

	// Turn 1: model "public" matches rule (x2, rev 77)
	go func() {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello turn 1"}`))
	}()

	// Wait until Turn 1 is actively in-flight on upstream
	<-turn1Received

	// Genuine in-flight policy publication while Turn 1 is active
	input.PolicyBindings[0].Revision = 88
	input.PolicyBindings[0].Config = []byte(`{"schema_version":1,"rules":[{"id":"p-ws","name":"ws price","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"public"},"then":{"type":"multiply_price","factor":"5"}}]}`)
	if _, err := h.manager.Publish(input); err != nil {
		t.Fatal(err)
	}

	// Release upstream to complete Turn 1
	close(turn1Proceed)

	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	events := waitWebsocketLogs(t, sink, 1)
	if len(events) < 1 || events[0].Usage.Pricing.EstimatedCostNanoUSD == 0 {
		t.Fatalf("turn 1 not priced: %+v", events)
	}
	var r1 pricing.Receipt
	if err := json.Unmarshal([]byte(events[0].Usage.Pricing.ReceiptJSON), &r1); err != nil {
		t.Fatal(err)
	}
	// Turn 1 froze pricing at turn admission, so it preserves factor 2 and rev 77 despite in-flight publish
	if len(r1.PolicyFactors) != 1 || r1.PolicyFactors[0].Factor != "2" || r1.PolicyFactors[0].Revision != 77 {
		t.Fatalf("turn 1 in-flight receipt factors mismatch: %+v", r1.PolicyFactors)
	}

	// Turn 2: on same connection, new sequential turn picks up updated policy (x5, rev 88)
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"public","input":"hello turn 2"}`))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	events2 := waitWebsocketLogs(t, sink, 2)
	var r2 pricing.Receipt
	if err := json.Unmarshal([]byte(events2[1].Usage.Pricing.ReceiptJSON), &r2); err != nil {
		t.Fatal(err)
	}
	if len(r2.PolicyFactors) != 1 || r2.PolicyFactors[0].Factor != "5" || r2.PolicyFactors[0].Revision != 88 {
		t.Fatalf("turn 2 receipt factors mismatch: %+v", r2.PolicyFactors)
	}
}

// 18. Unchanged binding gets same billing revision on unrelated publication
func TestPolicyPricing_BindingRevisionUnchangedByUnrelatedPublication(t *testing.T) {
	h, m, _ := newHandlerForTest(t, &scriptedForwarder{}, "sk-one")
	in := policyRegressionCompileInput(h)
	in.PolicyBindings = []policy.BindingConfig{{
		Scope: "group", GroupID: 1, Revision: 18446744073709551615,
		Config: []byte(`{"schema_version":1,"rules":[{"id":"p","name":"p","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"2"}}]}`),
	}}
	s1, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	model := "gpt-4o"
	sel := scheduler.Selection{CredentialID: 1, GroupID: 1, UpstreamModelID: &model, Group: s1.Groups[1]}
	a := h.freezeAttemptPricing(s1, sel, dialect.RequestMetadata{}, true, pricing.DefaultPriceMultiplier, model)
	in.Groups[0].Name = "unrelated group rename"
	s2, err := m.Publish(in)
	if err != nil {
		t.Fatal(err)
	}
	sel.Group = s2.Groups[1]
	b := h.freezeAttemptPricing(s2, sel, dialect.RequestMetadata{}, true, pricing.DefaultPriceMultiplier, model)
	if len(a.policyFactors) != 1 || len(b.policyFactors) != 1 {
		t.Fatal("missing factors")
	}
	if a.policyFactors[0].Revision != 18446744073709551615 || a.policyFactors[0].Revision != b.policyFactors[0].Revision {
		t.Fatalf("unchanged binding gets different billing revision on unrelated publication: %d -> %d", a.policyFactors[0].Revision, b.policyFactors[0].Revision)
	}
}

// 19. Complete dynamic answer + JEV decision total reaches quota with in-flight price and policy update
func TestPolicyPricing_DynamicJevAuxCostReachesQuota(t *testing.T) {
	groupMultiplier, _ := pricing.ParsePriceMultiplier("2")
	accessMultiplier, _ := pricing.ParsePriceMultiplier("3")
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode:            http.StatusOK,
			Header:                http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"decision-1"}},
			Body:                  []byte(`{"model":"jev-latest","answers":{"preset":{"choice":"strong","confidence":0.9}},"usage":{"input_tokens":7,"output_tokens":1}}`),
			Usage:                 usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 7, Output: 1}},
			UpstreamReportedModel: "decision-key-model",
			ResponseModelObserved: true,
			UpstreamProtocol:      protocol.Decisions,
			UpstreamRequestID:     "decision-key-request",
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       []byte(`{"model":"gpt-4.1","choices":[{"message":{"content":"ok"}}]}`),
			Usage:      usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, Output: 5}},
		},
	}}
	handler, manager, registry := newHandlerForTest(t, forwarder, "answer-key")
	config := automodel.DefaultConfig()
	config.Enabled = true
	config.Model = "jev-router"
	config.Models = []automodel.Entry{{
		ID: "auto-probe", Name: "auto-probe", Enabled: true, Fallback: "balanced",
		Presets: []automodel.Preset{
			{ID: "balanced", Name: "balanced", Description: "Routine", Model: "gpt-4o", ParameterOverrides: json.RawMessage(`[]`)},
			{ID: "strong", Name: "strong", Description: "Complex", Model: "gpt-4.1", ParameterOverrides: json.RawMessage(`[]`)},
		},
	}}
	input := state.CompileInput{
		AutoModel:       &config,
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{ID: 1, Name: "answers", ChannelID: channel.OpenAI, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "gpt-4o"}, {ID: "gpt-4.1"}}, Enabled: true},
			{ID: 2, Name: "decisions", ChannelID: channel.Jev, ConnectionType: "api_key", Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "jev-latest", Alias: "jev-router"}}, PriceMultiplier: &groupMultiplier, Enabled: true},
		},
		Credentials: []state.CredentialConfig{testCredentialConfig(1, 1), testCredentialConfig(2, 2)},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive, PriceMultiplier: &accessMultiplier,
			Filters: state.FilterSet{
				Groups:    map[uint]struct{}{1: {}},
				Protocols: map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}},
				Models:    map[string]struct{}{"auto-probe": {}, "gpt-4o": {}, "gpt-4.1": {}},
			},
		}},
	}
	runtime := accessquota.NewRuntime()
	quotaRules := []accessquota.Rule{{ID: 901, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 10000000}}
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: quotaRules}); err != nil {
		t.Fatal(err)
	}
	handler.accessQuota = runtime
	input.AccessKeys[0].CostLimitRules = quotaRules
	input.PolicyBindings = []policy.BindingConfig{
		{
			Scope:    "group",
			GroupID:  2,
			Revision: 12,
			Config:   []byte(`{"schema_version":1,"rules":[{"id":"aux","name":"aux","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"jev-router"},"then":{"type":"multiply_price","factor":"2"}}]}`),
		},
		{
			Scope:    "group",
			GroupID:  1,
			Revision: 20,
			Config:   []byte(`{"schema_version":1,"rules":[{"id":"ans-dyn","name":"ans dyn","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"auto-probe"},"then":{"type":"multiply_price","factor":"2"}}]}`),
		},
	}

	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{
		testCredentialEntry(t, handler.encryption, 1, 1, "answer-key"),
		testCredentialEntry(t, handler.encryption, 2, 2, "decision-key"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.IncrFailure(2); !ok {
		t.Fatal("cannot seed decision credential failure")
	}
	table, err := pricing.NewTable([]pricing.Rule{
		{
			Identity: pricing.Identity{ChannelID: string(channel.Jev), ModelID: "jev-latest"},
			Prices:   pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 42_000_000, Set: true}},
		},
		{
			Identity: pricing.Identity{ChannelID: string(channel.OpenAI), ModelID: "gpt-4.1"},
			Prices: pricing.Prices{
				Input:  pricing.Price{NanoUSDPerMillion: 100_000_000, Set: true},
				Output: pricing.Price{NanoUSDPerMillion: 200_000_000, Set: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	replacementTable, err := pricing.NewTable([]pricing.Rule{
		{
			Identity: pricing.Identity{ChannelID: string(channel.Jev), ModelID: "jev-latest"},
			Prices:   pricing.Prices{Input: pricing.Price{NanoUSDPerMillion: 420_000_000, Set: true}},
		},
		{
			Identity: pricing.Identity{ChannelID: string(channel.OpenAI), ModelID: "gpt-4.1"},
			Prices: pricing.Prices{
				Input:  pricing.Price{NanoUSDPerMillion: 100_000_000, Set: true},
				Output: pricing.Price{NanoUSDPerMillion: 200_000_000, Set: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	priceTables := &mutableGatewayPriceTableProvider{table: table}
	handler.priceTables = priceTables
	forwarder.onCall = func(index int) {
		if index == 0 {
			priceTables.Publish(replacementTable)
			input.PolicyBindings[0].Config = []byte(`{"schema_version":1,"rules":[{"id":"aux","name":"aux","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"jev-router"},"then":{"type":"multiply_price","factor":"9"}}]}`)
			_, _ = manager.Publish(input)
		}
	}
	sink := &recordingRequestLogSink{}
	handler.requestLogSink = sink
	handler.dialects = dialect.NewSet(dialect.NewOpenAI())
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"auto-probe","messages":[{"role":"user","content":"Design a migration"}]}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].AutoDecision == nil || events[0].AutoDecision.EstimatedCostNanoUSD != 3_528 {
		t.Fatalf("decision observation = %#v", events)
	}
	if events[0].Usage.Pricing.EstimatedCostNanoUSD != 12_000 {
		t.Fatalf("answer cost = %d, want 12000", events[0].Usage.Pricing.EstimatedCostNanoUSD)
	}
	total := telemetry.TotalPricing(events[0].Usage.Pricing, events[0].AutoDecision, events[0].RequestAudit).EstimatedCostNanoUSD
	if total != 15_528 {
		t.Fatalf("total cost = %d, want 15528", total)
	}
	view := runtime.Snapshot(1, time.Now())
	if total != 15_528 || len(view.Rules) != 1 || view.Rules[0].UsedNanoUSD != total {
		t.Fatalf("aux dynamic cost mismatch total=%d quota=%+v", total, view)
	}
}

// 20-22. Same-credential auth refresh replay: the replay attempt must re-evaluate
// policy admission and freeze dynamic pricing. One 401 then one 200, with the
// credential secret rotated by the first attempt so the replay must pick up
// version 2. The three cases differ only in the policy rule and the concurrent
// state mutation performed by attempt 0.
func TestPolicyPricing_AuthRefreshReplay(t *testing.T) {
	const (
		modelRule = `{"schema_version":1,"rules":[{"id":"p-replay","name":"replay dyn","domain":"pricing","enabled":true,"when":{"fact":"request.model","op":"eq","value":"gpt-4o"},"then":{"type":"multiply_price","factor":"3"}}]}`
		timeRule  = `{"schema_version":1,"rules":[{"id":"p-replay","name":"replay dyn","domain":"pricing","enabled":true,"when":{"all":[{"fact":"request.model","op":"eq","value":"gpt-4o"},{"predicate":"time_window","weekdays":[1],"ranges":[["09:00","10:00"]]}]},"then":{"type":"multiply_price","factor":"3"}}]}`
		quotaRule = `{"schema_version":1,"rules":[{"id":"p-replay","name":"replay dyn","domain":"pricing","enabled":true,"when":{"all":[{"fact":"request.model","op":"eq","value":"gpt-4o"},{"fact":"credential.quota.remaining_ratio","select":{"scope":"account","window_seconds":18000},"reduce":"min","op":"lt","value":0.2}]},"then":{"type":"multiply_price","factor":"3"}}]}`
	)
	for _, tc := range []struct {
		name               string
		policyConfig       string
		fixedClock         bool
		hook               func(t *testing.T, env *authRefreshReplayEnv)
		wantCostNano       int64
		wantSchema         int
		wantFactorRevision uint64
	}{
		{
			name:         "SameCredentialAuthRefreshReplay",
			policyConfig: modelRule,
			wantCostNano: 6000, wantFactorRevision: 10,
		},
		{
			name:         "RefreshReplayReevaluatesChangedTime",
			policyConfig: timeRule, fixedClock: true,
			hook:         func(_ *testing.T, env *authRefreshReplayEnv) { *env.now = env.now.Add(2 * time.Second) },
			wantCostNano: 2000, wantSchema: 6,
		},
		{
			name:         "RefreshReplayReevaluatesChangedQuota",
			policyConfig: quotaRule, fixedClock: true,
			hook: func(t *testing.T, env *authRefreshReplayEnv) {
				used := 0.95
				win := int64(18000)
				reset := env.now.Add(time.Hour).UnixMilli()
				observed := env.now.Add(-time.Second).UnixMilli()
				if !env.registry.ApplyQuotaWindows(1, 1, []observation.QuotaWindow{{
					ID: "primary", SourceID: "account", Scope: "account", Unit: "ratio",
					Utilization: &used, WindowSeconds: &win, ResetAtMS: &reset,
					ObservedAtMS: &observed, State: "available",
				}}) {
					t.Fatal("quota publication failed")
				}
			},
			wantCostNano: 6000, wantFactorRevision: 10,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newAuthRefreshReplayEnv(t, tc.policyConfig, tc.fixedClock)
			env.forwarder.onCall = func(index int) {
				if index != 0 {
					return
				}
				if tc.hook != nil {
					tc.hook(t, env)
				}
				if !env.registry.ReplaceCredentialSecretIfMatch(1, 1, 2, "subscription-secret-v2", env.newEncrypted) {
					t.Fatal("publish concurrent credential refresh")
				}
			}
			assertAuthRefreshReplay(t, env, tc.wantCostNano, tc.wantSchema, tc.wantFactorRevision)
		})
	}
}

const (
	authReplayOldCredential = `{"type":"codex","access_token":"old-access","refresh_token":"refresh","account_id":"account-1"}`
	authReplayNewCredential = `{"type":"codex","access_token":"new-access","refresh_token":"new-refresh","account_id":"account-1"}`
)

// authRefreshReplayEnv is the shared fixture for the same-credential auth refresh
// replay cases: a scripted 401-then-200 upstream, dynamic pricing from a group
// policy binding, and a credential secret rotated concurrently by attempt 0.
type authRefreshReplayEnv struct {
	engine       *gin.Engine
	handler      *Handler
	registry     *state.CredentialRegistry
	forwarder    *scriptedForwarder
	sink         *recordingRequestLogSink
	runtime      *accessquota.Runtime
	now          *time.Time
	newEncrypted string
}

func newAuthRefreshReplayEnv(t *testing.T, policyConfig string, fixedClock bool) *authRefreshReplayEnv {
	t.Helper()
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusUnauthorized,
			ExecutionError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindHTTP, StatusCode: http.StatusUnauthorized,
				Hint:         execution.FailureHintRefreshRequired,
				ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
				Summary:      "access token expired",
			},
		},
		{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body:  []byte(`{"id":"ok","model":"gpt-4o"}`),
			Usage: usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 10, Output: 10}},
		},
	}}
	sink := &recordingRequestLogSink{}
	engine, handler, manager, registry := newRequestLogHandlerTestRuntime(
		t, forwarder, &recordingAccessKeyRPMLimiter{}, sink, "placeholder",
	)
	runtime := accessquota.NewRuntime()
	quotaRules := []accessquota.Rule{{ID: 901, Revision: 1, Kind: accessquota.KindTotal, LimitNanoUSD: 10_000_000}}
	if err := runtime.Reconcile(map[uint][]accessquota.Rule{1: quotaRules}); err != nil {
		t.Fatal(err)
	}
	handler.accessQuota = runtime

	env := &authRefreshReplayEnv{
		engine: engine, handler: handler, registry: registry,
		forwarder: forwarder, sink: sink, runtime: runtime,
	}
	if fixedClock {
		now := time.Date(2026, time.June, 1, 9, 59, 59, 0, time.UTC)
		env.now = &now
		handler.now = func() time.Time { return *env.now }
	}

	table, err := pricing.NewTable([]pricing.Rule{{
		Identity: pricing.Identity{ChannelID: string(channel.Codex), ModelID: "gpt-4o"},
		Prices: pricing.Prices{
			Input:  pricing.Price{NanoUSDPerMillion: 100_000_000, Set: true},
			Output: pricing.Price{NanoUSDPerMillion: 100_000_000, Set: true},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler.priceTables = &mutableGatewayPriceTableProvider{table: table}

	input := state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "subscription", ChannelID: channel.Codex,
			ConnectionType: "subscription", Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{{
			ID: 1, GroupID: 1, Status: state.CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "subscription-account",
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
			Status: state.AccessKeyStatusActive, CostLimitRules: quotaRules,
		}},
		PolicyBindings: []policy.BindingConfig{{
			Scope: "group", GroupID: 1, Revision: 10, Config: []byte(policyConfig),
		}},
	}
	if _, err := manager.Publish(input); err != nil {
		t.Fatal(err)
	}

	oldEncrypted, err := handler.encryption.Encrypt(authReplayOldCredential)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1,
		Fingerprint: "subscription-account", Status: state.CredentialStatusActive,
		EncryptedValue: oldEncrypted,
	}}); err != nil {
		t.Fatal(err)
	}

	env.newEncrypted, err = handler.encryption.Encrypt(authReplayNewCredential)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func assertAuthRefreshReplay(t *testing.T, env *authRefreshReplayEnv, wantCostNano int64, wantSchema int, wantFactorRevision uint64) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	env.engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK || len(env.forwarder.inputs) != 2 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(env.forwarder.inputs), response.Body.String())
	}
	if in := env.forwarder.inputs[1]; in.Credential.Version != 2 || string(in.Credential.Data()) != authReplayNewCredential {
		t.Fatalf("replay attempt did not use refreshed credential version: %#v", in)
	}

	events := env.sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 log event, got %d", len(events))
	}
	if len(events[0].Attempts) != 2 {
		t.Fatalf("expected 2 attempts in log, got %d", len(events[0].Attempts))
	}
	if events[0].Attempts[0].StatusCode != http.StatusUnauthorized || events[0].Attempts[1].StatusCode != http.StatusOK {
		t.Fatalf("attempt statuses = %d,%d, want 401,200", events[0].Attempts[0].StatusCode, events[0].Attempts[1].StatusCode)
	}
	if events[0].Usage.Pricing.EstimatedCostNanoUSD != wantCostNano {
		t.Fatalf("cost = %d, want %d", events[0].Usage.Pricing.EstimatedCostNanoUSD, wantCostNano)
	}

	var r pricing.Receipt
	if err := json.Unmarshal([]byte(events[0].Usage.Pricing.ReceiptJSON), &r); err != nil {
		t.Fatal(err)
	}
	if wantFactorRevision == 0 {
		if len(r.PolicyFactors) != 0 || r.SchemaVersion != wantSchema {
			t.Fatalf("receipt = schema %d factors %+v, want schema %d with no factors", r.SchemaVersion, r.PolicyFactors, wantSchema)
		}
	} else if len(r.PolicyFactors) != 1 || r.PolicyFactors[0].Factor != "3" || r.PolicyFactors[0].Revision != wantFactorRevision {
		t.Fatalf("replay receipt factors mismatch: %+v", r.PolicyFactors)
	}

	view := env.runtime.Snapshot(1, time.Now())
	if len(view.Rules) != 1 || view.Rules[0].UsedNanoUSD != wantCostNano {
		t.Fatalf("quota used = %d, want %d", view.Rules[0].UsedNanoUSD, wantCostNano)
	}
}

// 21. Live and Pricing freeze evaluate quota facts independently of scheduling cooldown
func TestPolicyAdmission_LiveAndPricing_QuotaReadIndependentOfCooldown(t *testing.T) {
	liveModel := "gpt-4o-realtime-preview"
	handler, manager, registry := newHandlerForTest(t, &scriptedForwarder{}, "gl-client")

	// 1. Initial snapshot with Codex channel and quota exclusion policy
	policyJSON := []byte(`{
		"schema_version": 1,
		"group_policy": "override",
		"rules": [
			{
				"id": "exclude-low-quota",
				"name": "Exclude candidate when remaining quota < 0.2",
				"domain": "scheduling",
				"enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "lt",
					"value": 0.2
				},
				"then": {"type": "exclude_candidate"}
			},
			{
				"id": "surge-pricing",
				"name": "Surge price when remaining quota < 0.2",
				"domain": "pricing",
				"enabled": true,
				"when": {
					"fact": "credential.quota.remaining_ratio",
					"select": {"scope": "account", "window_seconds": 18000},
					"reduce": "min",
					"op": "lt",
					"value": 0.2
				},
				"then": {"type": "multiply_price", "factor": "2.5"}
			}
		]
	}`)

	input := policyRegressionCompileInput(handler)
	input.Groups[0].ConnectionType = "subscription"
	input.Groups[0].Name = "codex"
	input.Groups[0].ChannelID = channel.Codex
	input.Groups[0].Models = []state.ModelConfig{{ID: liveModel}}
	input.PolicyBindings = []policy.BindingConfig{{Scope: "credential", GroupID: 1, CredentialID: 1, Config: policyJSON}}
	snap, err := manager.Publish(input)
	if err != nil {
		t.Fatalf("Publish error = %v", err)
	}

	call := &liveCallSession{
		id:          "call-cooldown-test",
		keyID:       1,
		keyHash:     handler.encryption.Hash("gl-client"),
		groupID:     1,
		clientModel: liveModel,
		model:       liveModel,
		ref:         state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 1},
	}

	// Case A: Before quota observation is set, quota is unknown -> NOT excluded (isLiveCallPolicyAdmitted = true)
	if !handler.isLiveCallPolicyAdmitted(call) {
		t.Fatal("expected isLiveCallPolicyAdmitted = true when quota facts are unknown")
	}

	// Case B: Set quota window with 0.1 ratio (triggers exclusion rule)
	windowSec := int64(18000)
	utilization := 0.90 // 10% remaining
	resetAt := time.Now().Add(time.Hour).UnixMilli()
	observedAt := time.Now().UnixMilli()
	windows := []observation.QuotaWindow{{
		ID: "primary", Scope: "account", WindowSeconds: &windowSec,
		State: "available", Utilization: &utilization,
		ResetAtMS: &resetAt, ObservedAtMS: &observedAt,
	}}
	if !registry.ApplyQuotaWindows(1, 1, windows) {
		t.Fatal("ApplyQuotaWindows failed")
	}

	// Candidate is now excluded
	if handler.isLiveCallPolicyAdmitted(call) {
		t.Fatal("expected isLiveCallPolicyAdmitted = false after quota facts trigger exclusion")
	}

	// Case C: Place credential into cooldown!
	// Previously, CollectCredentialCandidates filtered out cooldown credentials, causing quota facts
	// to disappear (unknown) and improperly admitting the candidate.
	// With CredentialQuotaWindows, quota facts are read independently of cooldown.
	if !registry.SetCooldown(1, time.Now().Add(time.Hour)) {
		t.Fatal("SetCooldown failed")
	}
	if handler.isLiveCallPolicyAdmitted(call) {
		t.Fatal("expected isLiveCallPolicyAdmitted = false even during cooldown")
	}

	// Case D: Live call session with stale identity generation cannot read quota facts
	staleCall := &liveCallSession{
		id:          "call-stale-gen",
		keyID:       1,
		keyHash:     handler.encryption.Hash("gl-client"),
		groupID:     1,
		clientModel: liveModel,
		model:       liveModel,
		ref:         state.CredentialRef{ID: 1, GroupID: 1, IdentityGeneration: 2}, // stale generation
	}
	if !handler.isLiveCallPolicyAdmitted(staleCall) {
		t.Fatal("stale generation should not match quota facts; unknown fact should skip rule")
	}

	// Case E: Pricing freeze during cooldown reads quota facts and applies multiplier
	sel := scheduler.Selection{
		CredentialID:       1,
		IdentityGeneration: 1,
		GroupID:            1,
		Group:              snap.Groups[1],
	}
	frozen := handler.freezeAttemptPricing(snap, sel, dialect.RequestMetadata{ObserveUsage: true}, true, pricing.DefaultPriceMultiplier, liveModel)
	if len(frozen.policyFactors) != 1 || frozen.policyFactors[0].Factor != "2.5" {
		t.Fatalf("expected 1 surge factor of 2.5 during cooldown, got: %+v", frozen.policyFactors)
	}

	// Case F: HasApplicablePricing short-circuits when no pricing rules exist
	noPricingInput := input
	noPricingJSON := []byte(`{
		"schema_version": 1,
		"group_policy": "override",
		"rules": [
			{
				"id": "scheduling-only",
				"name": "Scheduling only",
				"domain": "scheduling",
				"enabled": true,
				"when": {"fact": "request.model", "op": "eq", "value": "test"},
				"then": {"type": "exclude_candidate"}
			}
		]
	}`)
	noPricingInput.PolicyBindings = []policy.BindingConfig{
		{Scope: "credential", GroupID: 1, CredentialID: 1, Config: noPricingJSON},
	}
	noPricingSnap, err := manager.Publish(noPricingInput)
	if err != nil {
		t.Fatal(err)
	}
	if noPricingSnap.Policies.HasApplicablePricing(1, 1) {
		t.Fatal("HasApplicablePricing returned true for scheduling-only policy")
	}
	frozenShortCircuit := handler.freezeAttemptPricing(noPricingSnap, sel, dialect.RequestMetadata{ObserveUsage: true}, true, pricing.DefaultPriceMultiplier, liveModel)
	if len(frozenShortCircuit.policyFactors) != 0 {
		t.Fatalf("expected 0 policy factors from short-circuited pricing, got: %+v", frozenShortCircuit.policyFactors)
	}
}
