package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/state"
)

// 在真正取得凭据锁之前完成一次配置变更，验证目标校验位于锁内。
type targetHealthMutationCoordinator struct {
	delegate credentialMutationCoordinator
	before   func()
}

func (coordinator *targetHealthMutationCoordinator) Do(id uint, mutate func()) {
	if before := coordinator.before; before != nil {
		coordinator.before = nil
		before()
	}
	coordinator.delegate.Do(id, mutate)
}

func TestHandlerHealthEffectsRespectSelectedTarget(t *testing.T) {
	for _, upstream := range []struct {
		channelID      channel.ID
		connectionType string
		credential     string
	}{
		{channel.OpenAI, "api_key", `{"api_key":"test-key"}`},
		{channel.Codex, "subscription", `{"type":"codex","access_token":"access-test","refresh_token":"refresh-test","account_id":"account-test"}`},
	} {
		for _, stream := range []bool{false, true} {
			for _, statusCode := range []int{http.StatusOK, http.StatusUnauthorized, http.StatusTooManyRequests} {
				for _, change := range []string{"target before response", "target before mutation", "secret only"} {
					t.Run(fmt.Sprintf("%s/stream=%t/status=%d/%s", upstream.channelID, stream, statusCode, change), func(t *testing.T) {
						now := time.Date(2026, time.September, 8, 12, 0, 0, 0, time.UTC)
						result := UpstreamResult{
							StatusCode: statusCode, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
						}
						if statusCode != http.StatusOK {
							hint := execution.FailureHintInvalidCredential
							if statusCode == http.StatusTooManyRequests {
								hint = execution.FailureHintRateLimited
							}
							result.ExecutionError = &execution.ErrorEvidence{
								Kind: execution.ErrorKindHTTP, Hint: hint, StatusCode: statusCode,
								ScopeHint:    execution.ErrorScopeCredential,
								ReplaySafety: execution.ReplaySafetyRejectedBeforeProcessing,
							}
						}
						if stream {
							result.Committed = true
							result.Stream.EndReason = StreamEndCleanEOF
							if statusCode != http.StatusOK {
								result.Stream.EndReason = StreamEndSSEError
							}
						}
						forwarder := &scriptedForwarder{results: []UpstreamResult{result}, streamResults: []UpstreamResult{result}}
						sink := &recordingRequestLogSink{}
						engine, handler, manager, registry := newRequestLogHandlerTestRuntime(t, forwarder, &recordingAccessKeyRPMLimiter{}, sink)
						handler.now = func() time.Time { return now }
						mutations := handler.mutations
						encrypted, err := handler.encryption.Encrypt(upstream.credential)
						if err != nil {
							t.Fatal(err)
						}
						publish := func(generation, version uint64, root string) {
							t.Helper()
							params, err := json.Marshal(map[string]string{"base_url": root})
							if err != nil {
								t.Fatal(err)
							}
							mutations.Do(1, func() {
								handler.stats.Reset(1)
								if _, err := registry.ReconcileGroup(1, []state.CredentialEntry{{
									ID: 1, GroupID: 1, Version: version, IdentityGeneration: generation,
									Fingerprint: "target-health-test", Status: state.CredentialStatusActive, EncryptedValue: encrypted,
								}}); err != nil {
									t.Fatal(err)
								}
								if _, err := manager.Publish(state.CompileInput{
									ChannelRegistry: channel.NewRegistry(),
									Groups: []state.GroupConfig{{
										ID: 1, Name: "target-health", ConnectionType: upstream.connectionType, ChannelID: upstream.channelID,
										Params: params, Models: []state.ModelConfig{{ID: "test-model"}}, Enabled: true,
									}},
									Credentials: []state.CredentialConfig{{
										ID: 1, GroupID: 1, Version: version, IdentityGeneration: generation,
										Fingerprint: "target-health-test", Status: state.CredentialStatusActive,
									}},
									AccessKeys: []state.AccessKeyConfig{{ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"), Status: state.AccessKeyStatusActive}},
								}); err != nil {
									t.Fatal(err)
								}
							})
						}
						publish(1, 1, "https://relay.example/old")
						var before state.CredentialRuntimeView
						var beforeStats health.CredentialStats
						changeTarget := func() {
							if change == "secret only" {
								publish(1, 2, "https://relay.example/old")
							} else {
								publish(2, 1, "https://relay.example/new")
							}
							for range 2 {
								registry.IncrFailure(1)
								handler.stats.RecordFailure(1, health.FailureCategoryInvalidKey, http.StatusUnauthorized, now)
							}
							before = registry.Snapshot()[0]
							beforeStats = handler.stats.Snapshot(1, now)
						}
						if change == "target before mutation" {
							handler.mutations = &targetHealthMutationCoordinator{delegate: mutations, before: changeTarget}
						} else {
							forwarder.onCall = func(int) { changeTarget() }
							forwarder.onStreamCall = func(int, http.ResponseWriter) { changeTarget() }
						}
						request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":"test-model","messages":[],"stream":%t}`, stream)))
						request.Header.Set("Authorization", "Bearer gl-client")
						engine.ServeHTTP(httptest.NewRecorder(), request)
						inputs := append(forwarder.inputs, forwarder.streamInputs...)
						if len(inputs) != 1 || inputs[0].Credential.IdentityGeneration != 1 || before.ID == 0 {
							t.Fatalf("expected one original-target attempt and a configuration change, calls=%d", len(inputs))
						}
						after := registry.Snapshot()[0]
						afterStats := handler.stats.Snapshot(1, now)
						if change != "secret only" {
							if !reflect.DeepEqual(before, after) || beforeStats != afterStats {
								t.Errorf("old target changed new health: failures %d->%d, blacklisted=%t, cooldown=%t, stats=%#v", before.FailureCount, after.FailureCount, after.Blacklisted, !after.CooldownUntil.IsZero(), afterStats)
							}
						} else {
							switch statusCode {
							case http.StatusOK:
								if after.FailureCount != 0 || afterStats.Success != 1 {
									t.Error("same-target token refresh suppressed a valid success")
								}
							case http.StatusUnauthorized:
								if after.FailureCount != 3 || !after.Blacklisted || afterStats.Failure != 3 {
									t.Error("same-target token refresh suppressed a valid failure")
								}
							case http.StatusTooManyRequests:
								if after.CooldownUntil.IsZero() || afterStats.Problem != 3 {
									t.Error("same-target token refresh suppressed a valid cooldown")
								}
							}
						}
						if events := sink.snapshot(); len(events) != 1 || len(events[0].Attempts) != 1 {
							t.Fatal("target isolation dropped the original request log")
						}
					})
				}
			}
		}
	}
}
