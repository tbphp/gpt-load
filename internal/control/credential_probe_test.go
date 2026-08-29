package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type credentialProbeTestExecutor struct {
	mu      sync.Mutex
	result  execution.AttemptResult
	calls   []execution.AttemptSpec
	execute func(execution.AttemptSpec) execution.AttemptResult
}

func (executor *credentialProbeTestExecutor) Execute(
	_ context.Context,
	spec execution.AttemptSpec,
) execution.AttemptResult {
	executor.mu.Lock()
	executor.calls = append(executor.calls, spec.Clone())
	result := executor.result.Clone()
	execute := executor.execute
	executor.mu.Unlock()
	if execute != nil {
		return execute(spec.Clone())
	}
	return result
}

func (*credentialProbeTestExecutor) ExecuteStream(
	context.Context,
	execution.AttemptSpec,
	execution.StreamSink,
) execution.StreamResult {
	panic("unexpected stream execution")
}

func (executor *credentialProbeTestExecutor) recordedCalls() []execution.AttemptSpec {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	result := make([]execution.AttemptSpec, len(executor.calls))
	for index := range executor.calls {
		result[index] = executor.calls[index].Clone()
	}
	return result
}

func successfulCredentialProbeResult() execution.AttemptResult {
	return execution.AttemptResult{
		DispatchState:   execution.DispatchMaybeSent,
		ResponseStarted: true,
		StatusCode:      http.StatusOK,
		Header:          http.Header{},
	}
}

func TestGroupCredentialProbeHTTPRequiresAuthAndUsesOnlySpecifiedCredential(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "probe-first-secret\nprobe-second-secret")
	var credentials []models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Order("id ASC").Find(&credentials).Error; err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 2 {
		t.Fatalf("credentials = %#v, want two", credentials)
	}
	executor := &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}
	fixture.service.executor = executor
	fixture.service.now = func() time.Time {
		return time.Date(2026, time.August, 29, 12, 30, 0, 0, time.UTC)
	}

	const auth = "credential-probe-auth"
	engine := gin.New()
	NewServer(&config.Config{AuthKey: auth}, fixture.service).RegisterRoutes(engine)
	path := fmt.Sprintf("/api/groups/%d/credentials/%d/test", groupID, credentials[1].ID)
	unauthorized := serveCredentialRequest(t, engine, http.MethodPost, path, "{}", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized response = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	response := serveCredentialRequest(t, engine, http.MethodPost, path, "{}", auth, "")
	if response.Code != http.StatusOK {
		t.Fatalf("probe response = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"reason":null`) {
		t.Fatalf("passed probe response omitted null reason: %s", response.Body.String())
	}
	var envelope struct {
		Code int                     `json:"code"`
		Data CredentialProbeResponse `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 || envelope.Data.Outcome != CredentialProbeOutcomePassed ||
		envelope.Data.Model != "gpt-4o" || envelope.Data.Protocol != protocol.OpenAICompletions ||
		envelope.Data.Reason != nil || envelope.Data.CanRestore ||
		envelope.Data.TestedAtMS != time.Date(2026, time.August, 29, 12, 30, 0, 0, time.UTC).UnixMilli() ||
		envelope.Data.LatencyMS < 0 {
		t.Fatalf("probe envelope = %#v", envelope)
	}
	calls := executor.recordedCalls()
	if len(calls) != 1 {
		t.Fatalf("probe calls = %d, want one", len(calls))
	}
	call := calls[0]
	if call.Credential.ID != credentials[1].ID || call.Operation != execution.OperationProbe ||
		call.ClientModel != "gpt-4o" || call.UpstreamModel != "gpt-4o" ||
		call.ClientProtocol != protocol.OpenAICompletions {
		t.Fatalf("probe attempt = %#v", call)
	}
	var canonical struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(call.Credential.Data(), &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.APIKey != "probe-second-secret" {
		t.Fatalf("probed api key = %q, want specified second credential", canonical.APIKey)
	}
}

func TestGroupCredentialProbeUsesExplicitValidationModel(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "probe-explicit-secret")
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		ValidationModel: optionalField[string]{Set: true, Value: " explicit-probe-model "},
	}); err != nil {
		t.Fatal(err)
	}
	executor := &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}
	fixture.service.executor = executor

	response, err := fixture.service.TestGroupCredential(t.Context(), groupID, credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Model != "explicit-probe-model" || response.Outcome != CredentialProbeOutcomePassed {
		t.Fatalf("probe response = %#v", response)
	}
	calls := executor.recordedCalls()
	if len(calls) != 1 || calls[0].UpstreamModel != "explicit-probe-model" {
		t.Fatalf("probe calls = %#v", calls)
	}
}

func TestGroupCredentialProbeFallsBackToEmbeddingsAndReportsProtocol(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "probe-embeddings-secret")
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	executor := &credentialProbeTestExecutor{execute: func(spec execution.AttemptSpec) execution.AttemptResult {
		if spec.ClientProtocol == protocol.OpenAIEmbeddings {
			return successfulCredentialProbeResult()
		}
		result := failedCredentialProbeResult(
			http.StatusNotFound,
			execution.ErrorKindHTTP,
			execution.FailureHintModelUnavailable,
		)
		result.Error.OriginHint = execution.ErrorOriginUpstream
		return result
	}}
	fixture.service.executor = executor

	response, err := fixture.service.TestGroupCredential(t.Context(), groupID, credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Outcome != CredentialProbeOutcomePassed ||
		response.Protocol != protocol.OpenAIEmbeddings {
		t.Fatalf("probe response = %#v", response)
	}
	calls := executor.recordedCalls()
	if len(calls) != 2 ||
		calls[0].ClientProtocol != protocol.OpenAICompletions ||
		calls[1].ClientProtocol != protocol.OpenAIEmbeddings ||
		calls[0].RequestID != calls[1].RequestID ||
		calls[0].AttemptID == calls[1].AttemptID ||
		calls[0].Sequence != 1 || calls[1].Sequence != 2 {
		t.Fatalf("probe calls = %#v", calls)
	}
}

func TestGroupCredentialProbeHTTPReturnsCompletedUpstreamFailureAsData(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "probe-http-failure-secret")
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	result := failedCredentialProbeResult(
		http.StatusUnauthorized,
		execution.ErrorKindHTTP,
		execution.FailureHintInvalidCredential,
	)
	result.Body = []byte("sensitive-upstream-response")
	result.Error.Summary = "sensitive-upstream-response"
	fixture.service.executor = &credentialProbeTestExecutor{result: result}
	const auth = "credential-probe-failure-auth"
	engine := gin.New()
	NewServer(&config.Config{AuthKey: auth}, fixture.service).RegisterRoutes(engine)

	response := serveCredentialRequest(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/test", groupID, credential.ID),
		"{}",
		auth,
		"",
	)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"code":0`) ||
		!strings.Contains(response.Body.String(), `"outcome":"failed"`) ||
		!strings.Contains(response.Body.String(), `"reason":"invalid_credential"`) ||
		strings.Contains(response.Body.String(), "sensitive-upstream-response") {
		t.Fatalf("probe failure response = %d %s", response.Code, response.Body.String())
	}
}

func TestGroupCredentialProbeDoesNotMutateDisabledCooldownOrBlacklistedState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		prepare        func(t *testing.T, fixture serviceFixture, groupID, credentialID uint)
		wantCanRestore bool
	}{
		{
			name: "disabled",
			prepare: func(t *testing.T, fixture serviceFixture, groupID, credentialID uint) {
				t.Helper()
				_, err := fixture.service.UpdateGroupCredential(t.Context(), groupID, credentialID, CredentialUpdateRequest{
					Status: optionalField[state.CredentialStatus]{Set: true, Value: state.CredentialStatusDisabled},
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "cooldown",
			prepare: func(t *testing.T, fixture serviceFixture, _, credentialID uint) {
				t.Helper()
				if !fixture.registry.SetCooldown(credentialID, time.Now().Add(time.Hour)) {
					t.Fatal("SetCooldown() = false")
				}
			},
		},
		{
			name: "blacklisted",
			prepare: func(t *testing.T, fixture serviceFixture, _, credentialID uint) {
				t.Helper()
				if _, ok := fixture.registry.IncrFailure(credentialID); !ok || !fixture.registry.SetBlacklisted(credentialID) {
					t.Fatal("failed to blacklist credential")
				}
				fixture.stats.RecordFailure(
					credentialID,
					health.FailureCategoryInvalidKey,
					http.StatusUnauthorized,
					time.Now(),
				)
			},
			wantCanRestore: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newServiceFixture(t)
			groupID := createGroupWithCredentials(t, fixture, "probe-state-secret")
			var credential models.Credential
			if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
				t.Fatal(err)
			}
			test.prepare(t, fixture, groupID, credential.ID)
			beforeEntries, err := fixture.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credential.ID})
			if err != nil {
				t.Fatal(err)
			}
			observedAt := time.Now()
			beforeStats := fixture.stats.Snapshot(credential.ID, observedAt)
			fixture.service.executor = &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}

			response, err := fixture.service.TestGroupCredential(t.Context(), groupID, credential.ID)
			if err != nil {
				t.Fatal(err)
			}
			if response.Outcome != CredentialProbeOutcomePassed || response.CanRestore != test.wantCanRestore {
				t.Fatalf("probe response = %#v", response)
			}
			afterEntries, err := fixture.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credential.ID})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(afterEntries, beforeEntries) {
				t.Fatalf("registry mutated by probe:\nbefore=%#v\nafter=%#v", beforeEntries, afterEntries)
			}
			if afterStats := fixture.stats.Snapshot(credential.ID, observedAt); !reflect.DeepEqual(afterStats, beforeStats) {
				t.Fatalf("stats mutated by probe: before=%#v after=%#v", beforeStats, afterStats)
			}
		})
	}
}

func TestGroupCredentialProbeRevokesRestoreEligibilityWhenTargetChangesDuringProbe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(t *testing.T, fixture serviceFixture, groupID uint)
	}{
		{
			name: "validation model",
			mutate: func(t *testing.T, fixture serviceFixture, groupID uint) {
				t.Helper()
				_, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
					ValidationModel: optionalField[string]{Set: true, Value: "changed-probe-model"},
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "header rules",
			mutate: func(t *testing.T, fixture serviceFixture, groupID uint) {
				t.Helper()
				_, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
					Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{
						state.SettingHeaderRules: map[string]any{
							"set": map[string]any{"X-Probe-Changed": "yes"},
						},
					}},
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "group proxy",
			mutate: func(t *testing.T, fixture serviceFixture, groupID uint) {
				t.Helper()
				_, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
					Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{
						Mode: outboundproxy.ModeCustom,
						URL:  "http://changed-probe-proxy.example:8080",
					}},
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "resolved target",
			mutate: func(t *testing.T, fixture serviceFixture, groupID uint) {
				t.Helper()
				_, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
					Params: optionalField[json.RawMessage]{Set: true, Value: json.RawMessage(
						`{"base_url":"https://changed-probe-target.example/v1"}`,
					)},
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newServiceFixture(t)
			created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
				Name:        stringPointer("probe target change " + test.name),
				ChannelID:   channel.OpenAICompatible,
				Params:      json.RawMessage(`{"base_url":"https://initial-probe-target.example/v1"}`),
				Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-4o"}}},
				Credentials: "probe-target-change-secret", ConnectionType: "api_key",
			})
			if err != nil {
				t.Fatal(err)
			}
			var credential models.Credential
			if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&credential).Error; err != nil {
				t.Fatal(err)
			}
			if _, ok := fixture.registry.IncrFailure(credential.ID); !ok ||
				!fixture.registry.SetBlacklisted(credential.ID) {
				t.Fatal("failed to blacklist credential")
			}
			fixture.service.executor = &credentialProbeTestExecutor{
				execute: func(execution.AttemptSpec) execution.AttemptResult {
					test.mutate(t, fixture, created.GroupID)
					return successfulCredentialProbeResult()
				},
			}

			response, err := fixture.service.TestGroupCredential(t.Context(), created.GroupID, credential.ID)
			if err != nil {
				t.Fatal(err)
			}
			if response.Outcome != CredentialProbeOutcomePassed || response.CanRestore || response.RestoreProof != nil {
				t.Fatalf("probe response after target change = %#v", response)
			}
		})
	}
}

func TestRestoreTestedGroupCredentialRequiresMatchingProofAndRestoresAtomically(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	groupID := createGroupWithCredentials(t, fixture, "probe-restore-secret")
	var credential models.Credential
	if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.registry.IncrFailure(credential.ID); !ok ||
		!fixture.registry.SetBlacklisted(credential.ID) {
		t.Fatal("failed to blacklist credential")
	}
	observedAt := time.Now()
	if !fixture.registry.SetCooldown(credential.ID, observedAt.Add(time.Hour)) {
		t.Fatal("failed to set credential cooldown")
	}
	fixture.stats.RecordFailure(
		credential.ID,
		health.FailureCategoryInvalidKey,
		http.StatusUnauthorized,
		observedAt,
	)
	fixture.service.executor = &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}
	probe, err := fixture.service.TestGroupCredential(t.Context(), groupID, credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !probe.CanRestore || probe.RestoreProof == nil || *probe.RestoreProof == "" {
		t.Fatalf("probe response = %#v, want restore proof", probe)
	}

	before, err := fixture.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credential.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.RestoreTestedGroupCredential(
		t.Context(), groupID, credential.ID, "forged-restore-proof",
	); !errors.Is(err, app_errors.ErrCredentialVersionConflict) {
		t.Fatalf("forged proof error = %v, want credential version conflict", err)
	}
	afterForged, err := fixture.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credential.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterForged, before) {
		t.Fatalf("forged proof mutated registry: before=%#v after=%#v", before, afterForged)
	}

	restored, err := fixture.service.RestoreTestedGroupCredential(
		t.Context(), groupID, credential.ID, *probe.RestoreProof,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.EffectiveStatus != "available" {
		t.Fatalf("restored credential = %#v", restored)
	}
	after, err := fixture.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credential.ID})
	if err != nil {
		t.Fatal(err)
	}
	if after[0].Blacklisted || after[0].FailureCount != 0 ||
		fixture.stats.Snapshot(credential.ID, observedAt) != (health.CredentialStats{Failure: 1, Problem: 1}) {
		t.Fatalf("restore state = %#v stats=%#v", after[0], fixture.stats.Snapshot(credential.ID, observedAt))
	}
}

func TestRestoreTestedGroupCredentialRejectsStaleProofWithoutMutation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(t *testing.T, fixture serviceFixture, groupID, credentialID uint)
	}{
		{
			name: "target signature",
			mutate: func(t *testing.T, fixture serviceFixture, groupID, _ uint) {
				t.Helper()
				_, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
					ValidationModel: optionalField[string]{Set: true, Value: "new-restore-model"},
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "credential ref",
			mutate: func(t *testing.T, fixture serviceFixture, groupID, credentialID uint) {
				t.Helper()
				entries, err := fixture.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credentialID})
				if err != nil {
					t.Fatal(err)
				}
				entries[0].Version++
				if err := fixture.db.Model(&models.Credential{}).
					Where("id = ? AND group_id = ?", credentialID, groupID).
					Update("secret_version", entries[0].Version).Error; err != nil {
					t.Fatal(err)
				}
				if err := fixture.registry.RestoreGroupCredentialEntriesExact(groupID, entries); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "failure generation",
			mutate: func(t *testing.T, fixture serviceFixture, _, credentialID uint) {
				t.Helper()
				if _, ok := fixture.registry.IncrFailure(credentialID); !ok {
					t.Fatal("IncrFailure() = false")
				}
			},
		},
		{
			name: "cooldown",
			mutate: func(t *testing.T, fixture serviceFixture, _, credentialID uint) {
				t.Helper()
				if !fixture.registry.SetCooldown(credentialID, time.Now().Add(time.Hour)) {
					t.Fatal("SetCooldown() = false")
				}
			},
		},
		{
			name: "credential proxy",
			mutate: func(t *testing.T, fixture serviceFixture, groupID, credentialID uint) {
				t.Helper()
				_, err := fixture.service.UpdateGroupCredential(t.Context(), groupID, credentialID, CredentialUpdateRequest{
					Proxy: optionalField[outboundproxy.Config]{Set: true, Value: outboundproxy.Config{
						Mode: outboundproxy.ModeCustom,
						URL:  "http://changed-credential-proxy.example:8080",
					}},
				})
				if err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newServiceFixture(t)
			groupID := createGroupWithCredentials(t, fixture, "stale-proof-secret")
			var credential models.Credential
			if err := fixture.db.Where("group_id = ?", groupID).Take(&credential).Error; err != nil {
				t.Fatal(err)
			}
			if _, ok := fixture.registry.IncrFailure(credential.ID); !ok ||
				!fixture.registry.SetBlacklisted(credential.ID) {
				t.Fatal("failed to blacklist credential")
			}
			fixture.service.executor = &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}
			probe, err := fixture.service.TestGroupCredential(t.Context(), groupID, credential.ID)
			if err != nil || probe.RestoreProof == nil {
				t.Fatalf("probe = %#v, error = %v", probe, err)
			}
			test.mutate(t, fixture, groupID, credential.ID)
			before, err := fixture.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credential.ID})
			if err != nil {
				t.Fatal(err)
			}

			_, err = fixture.service.RestoreTestedGroupCredential(
				t.Context(), groupID, credential.ID, *probe.RestoreProof,
			)
			if !errors.Is(err, app_errors.ErrCredentialVersionConflict) {
				t.Fatalf("stale proof error = %v, want credential version conflict", err)
			}
			after, snapshotErr := fixture.registry.SnapshotGroupCredentialEntriesExact(groupID, []uint{credential.ID})
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("stale proof mutated registry: before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestGroupCredentialProbeRejectsSubscriptionGroup(t *testing.T) {
	t.Parallel()
	fixture, groupID, credentialID := newSubscriptionCredentialFixture(t)
	executor := &credentialProbeTestExecutor{result: successfulCredentialProbeResult()}
	fixture.service.executor = executor

	_, err := fixture.service.TestGroupCredential(t.Context(), groupID, credentialID)
	if !errors.Is(err, app_errors.ErrForbidden) {
		t.Fatalf("TestGroupCredential() error = %v, want forbidden", err)
	}
	if calls := executor.recordedCalls(); len(calls) != 0 {
		t.Fatalf("subscription probe calls = %#v, want none", calls)
	}
	initControlI18n(t)
	const auth = "subscription-probe-auth"
	engine := gin.New()
	NewServer(&config.Config{AuthKey: auth}, fixture.service).RegisterRoutes(engine)
	response := serveCredentialRequest(
		t,
		engine,
		http.MethodPost,
		fmt.Sprintf("/api/groups/%d/credentials/%d/test", groupID, credentialID),
		"{}",
		auth,
		"",
	)
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"code":"FORBIDDEN"`) {
		t.Fatalf("subscription probe response = %d %s", response.Code, response.Body.String())
	}
}

func TestClassifyCredentialProbeResultUsesStableSafeOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		result      execution.AttemptResult
		wantOutcome CredentialProbeOutcome
		wantReason  *CredentialProbeReason
	}{
		{name: "passed", result: successfulCredentialProbeResult(), wantOutcome: CredentialProbeOutcomePassed},
		{
			name:        "invalid credential hint",
			result:      failedCredentialProbeResult(http.StatusForbidden, execution.ErrorKindHTTP, execution.FailureHintInvalidCredential),
			wantOutcome: CredentialProbeOutcomeFailed, wantReason: credentialProbeReasonPointer(CredentialProbeReasonInvalidCredential),
		},
		{
			name:        "unauthorized",
			result:      failedCredentialProbeResult(http.StatusUnauthorized, execution.ErrorKindHTTP, ""),
			wantOutcome: CredentialProbeOutcomeFailed, wantReason: credentialProbeReasonPointer(CredentialProbeReasonInvalidCredential),
		},
		{
			name: "unauthorized without required error evidence is unknown",
			result: execution.AttemptResult{
				DispatchState:   execution.DispatchMaybeSent,
				ResponseStarted: true,
				StatusCode:      http.StatusUnauthorized,
			},
			wantOutcome: CredentialProbeOutcomeInconclusive, wantReason: credentialProbeReasonPointer(CredentialProbeReasonUnknown),
		},
		{
			name:        "model unavailable",
			result:      failedCredentialProbeResult(http.StatusNotFound, execution.ErrorKindHTTP, execution.FailureHintModelUnavailable),
			wantOutcome: CredentialProbeOutcomeFailed, wantReason: credentialProbeReasonPointer(CredentialProbeReasonModelUnavailable),
		},
		{
			name:        "model hint wins over local request kind",
			result:      failedCredentialProbeResult(0, execution.ErrorKindInvalidRequest, execution.FailureHintModelUnavailable),
			wantOutcome: CredentialProbeOutcomeFailed, wantReason: credentialProbeReasonPointer(CredentialProbeReasonModelUnavailable),
		},
		{
			name:        "rate limited",
			result:      failedCredentialProbeResult(http.StatusTooManyRequests, execution.ErrorKindHTTP, ""),
			wantOutcome: CredentialProbeOutcomeInconclusive, wantReason: credentialProbeReasonPointer(CredentialProbeReasonRateLimited),
		},
		{
			name:        "timeout",
			result:      failedCredentialProbeResult(0, execution.ErrorKindTimeout, ""),
			wantOutcome: CredentialProbeOutcomeInconclusive, wantReason: credentialProbeReasonPointer(CredentialProbeReasonTimeout),
		},
		{
			name:        "incompatible",
			result:      failedCredentialProbeResult(0, execution.ErrorKindConversionUnsupported, ""),
			wantOutcome: CredentialProbeOutcomeInconclusive, wantReason: credentialProbeReasonPointer(CredentialProbeReasonIncompatible),
		},
		{
			name:        "upstream error",
			result:      failedCredentialProbeResult(http.StatusServiceUnavailable, execution.ErrorKindHTTP, ""),
			wantOutcome: CredentialProbeOutcomeInconclusive, wantReason: credentialProbeReasonPointer(CredentialProbeReasonUpstreamError),
		},
		{
			name:        "unknown",
			result:      execution.AttemptResult{},
			wantOutcome: CredentialProbeOutcomeInconclusive, wantReason: credentialProbeReasonPointer(CredentialProbeReasonUnknown),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outcome, reason := classifyCredentialProbeResult(test.result)
			if outcome != test.wantOutcome || !reflect.DeepEqual(reason, test.wantReason) {
				t.Fatalf("classifyCredentialProbeResult() = %q/%v, want %q/%v", outcome, reason, test.wantOutcome, test.wantReason)
			}
		})
	}

	secret := "credential-probe-sensitive-value"
	result := failedCredentialProbeResult(http.StatusUnauthorized, execution.ErrorKindHTTP, execution.FailureHintInvalidCredential)
	result.Body = []byte(secret)
	result.Error.Summary = secret
	outcome, reason := classifyCredentialProbeResult(result)
	encoded, err := json.Marshal(CredentialProbeResponse{Outcome: outcome, Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("probe response leaked sensitive upstream content: %s", encoded)
	}
}

func failedCredentialProbeResult(
	statusCode int,
	kind execution.ErrorKind,
	hint execution.FailureHint,
) execution.AttemptResult {
	return execution.AttemptResult{
		DispatchState:   execution.DispatchMaybeSent,
		ResponseStarted: statusCode != 0,
		StatusCode:      statusCode,
		Header:          http.Header{},
		Error: &execution.ErrorEvidence{
			Kind: kind, Hint: hint, StatusCode: statusCode,
			Summary: "sanitized test failure",
		},
	}
}

func credentialProbeReasonPointer(reason CredentialProbeReason) *CredentialProbeReason {
	return &reason
}

func TestGroupCredentialProbeRouteContract(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	module := NewServer(&config.Config{AuthKey: "credential-probe-route-auth"}, fixture.service).HTTPModule()
	for _, route := range module.Routes {
		if route.Name != "control.group-credentials.test" {
			continue
		}
		if !reflect.DeepEqual(route.Methods, []string{http.MethodPost}) ||
			route.Path != "/groups/:group_id/credentials/:credential_id/test" {
			t.Fatalf("credential probe route = %#v", route)
		}
		return
	}
	t.Fatal("credential probe route is missing")
}

var _ execution.Executor = (*credentialProbeTestExecutor)(nil)
