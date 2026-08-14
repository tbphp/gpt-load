package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/contentcoding"
	"gpt-load/internal/platform/encryption"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/ratelimit"
	"gpt-load/internal/state"
	"gpt-load/internal/testutil/fakeupstream"
	"gpt-load/internal/usage"
)

func withProviderErrorBeforeCommit(result UpstreamResult) UpstreamResult {
	result.ProviderErrorBeforeCommit = true
	return result
}

func TestHandlerForwardsStructuredCloudCredentialWithoutAPIKeyAssumption(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"ok","model":"anthropic.claude-test"}`),
	}}}
	handler, manager, registry := newHandlerForTest(t, forwarder)
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "bedrock", ChannelID: channel.AWSBedrock,
			Params: json.RawMessage(`{"region":"us-east-1"}`),
			Models: []state.ModelConfig{{ID: "anthropic.claude-test"}}, Enabled: true,
		}},
		Credentials: []state.CredentialConfig{{
			ID: 1, GroupID: 1, Status: state.CredentialStatusActive,
			Version: 1, IdentityGeneration: 1, Fingerprint: "bedrock-credential",
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	const canonical = `{"access_key":"AKIA_TEST","secret_key":"bedrock-secret","session_token":"bedrock-session"}`
	encrypted, err := handler.encryption.Encrypt(canonical)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Status: state.CredentialStatusActive,
		Version: 1, IdentityGeneration: 1, Fingerprint: "bedrock-credential",
		EncryptedValue: encrypted,
	}}); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"anthropic.claude-test","messages":[{"role":"user","content":"ping"}]}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()

	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK || len(forwarder.inputs) != 1 {
		t.Fatalf("status/inputs = %d/%d; body=%s", response.Code, len(forwarder.inputs), response.Body.String())
	}
	input := forwarder.inputs[0]
	if input.ChannelID != string(channel.AWSBedrock) || input.APIKey != "" ||
		string(input.Credential.Data()) != canonical {
		t.Fatalf("forward input = %#v; credential=%s", input, input.Credential.Data())
	}
	if want := []string{"AKIA_TEST", "bedrock-secret", "bedrock-session"}; !reflect.DeepEqual(input.CredentialSecrets, want) {
		t.Fatalf("credential secrets = %#v, want %#v", input.CredentialSecrets, want)
	}
}

type scriptedForwarder struct {
	results           []UpstreamResult
	streamResults     []UpstreamResult
	inputs            []ForwardInput
	streamInputs      []ForwardInput
	onCall            func(int)
	onStreamCall      func(int, http.ResponseWriter)
	invokeStreamReady bool
}

type streamReadyBlockingForwarder struct {
	result      UpstreamResult
	ready       chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

type recordingDecryptEncryption struct {
	encryption.Service
	ciphertexts []string
}

func (service *recordingDecryptEncryption) Decrypt(ciphertext string) (string, error) {
	service.ciphertexts = append(service.ciphertexts, ciphertext)
	return service.Service.Decrypt(ciphertext)
}

type blockingRequestBody struct {
	payload      []byte
	started      chan struct{}
	release      chan struct{}
	onFirstRead  func() error
	firstReadErr error
	once         sync.Once
	offset       int
}

func newBlockingRequestBody(payload string, onFirstRead func() error) *blockingRequestBody {
	return &blockingRequestBody{
		payload:     []byte(payload),
		started:     make(chan struct{}),
		release:     make(chan struct{}),
		onFirstRead: onFirstRead,
	}
}

func (body *blockingRequestBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() {
		if body.onFirstRead != nil {
			body.firstReadErr = body.onFirstRead()
		}
		close(body.started)
	})
	<-body.release
	if body.firstReadErr != nil {
		return 0, body.firstReadErr
	}
	if body.offset >= len(body.payload) {
		return 0, io.EOF
	}
	read := copy(buffer, body.payload[body.offset:])
	body.offset += read
	return read, nil
}

func (*blockingRequestBody) Close() error {
	return nil
}

type mutatingRuntimeRegistry struct {
	*state.CredentialRegistry
	mutate  func()
	mutated bool
}

type recordingRuntimeRegistry struct {
	*state.CredentialRegistry
	cooldownCredentialID uint
	cooldownUntil        time.Time
	cooldownCalls        int
	incrFailureCalls     int
	lastFailureCount     int
	blacklistCalls       int
	clearCalls           int
}

type captureCountingRuntimeRegistry struct {
	runtimeCredentialRegistry
	captureCalls int
}

type cancelingSuccessfulInspectDialect struct {
	dialect.Dialect
	cancel context.CancelFunc
}

func (value *cancelingSuccessfulInspectDialect) InspectRequest(
	request *dialect.ParsedRequest,
) (dialect.RequestMetadata, error) {
	metadata, err := value.Dialect.InspectRequest(request)
	if err == nil {
		value.cancel()
	}
	return metadata, err
}

func (registry *captureCountingRuntimeRegistry) CaptureActiveCredentialRefs(
	groupIDs []uint,
) []state.CredentialRef {
	registry.captureCalls++
	return registry.runtimeCredentialRegistry.CaptureActiveCredentialRefs(groupIDs)
}

type gatewayMutationObservation struct {
	cooldownCalls    int
	clearCalls       int
	incrFailureCalls int
	lastFailureCount int
	blacklistCalls   int
	stats            health.CredentialStats
}

func TestHandlerCoordinatesCooldownMutation(t *testing.T) {
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	registry := &recordingRuntimeRegistry{CredentialRegistry: state.NewCredentialRegistry()}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatal(err)
	}
	stats := health.NewStatsStore()
	coordinator := newBarrierGatewayMutationCoordinator(func() gatewayMutationObservation {
		return gatewayMutationObservation{
			cooldownCalls: registry.cooldownCalls,
			stats:         stats.Snapshot(1, now),
		}
	})
	handler := &Handler{registry: registry, stats: stats, mutations: coordinator}
	done := make(chan struct{})
	go func() {
		handler.applyCredentialAction(1, health.Result{
			Category: health.FailureCategoryRateLimited,
			Action:   health.ActionCooldownCredential,
		}, http.StatusTooManyRequests, now)
		close(done)
	}()

	receiveTestSignal(t, coordinator.entered, "cooldown coordinator entry")
	if registry.cooldownCalls != 0 || stats.Snapshot(1, now) != (health.CredentialStats{}) {
		t.Fatal("cooldown bundle changed state before coordinator callback")
	}
	close(coordinator.releaseEntry)
	observed := receiveTestSignal(t, coordinator.observed, "cooldown mutation observation")
	if observed.cooldownCalls != 1 || observed.stats != (health.CredentialStats{
		Problem:             1,
		ConsecutiveProblem:  1,
		LastFailureCategory: health.FailureCategoryRateLimited,
		LastStatusCode:      http.StatusTooManyRequests,
	}) {
		t.Fatalf("coordinator callback observation = %#v", observed)
	}
	close(coordinator.releaseExit)
	receiveTestSignal(t, done, "cooldown mutation completion")
}

type barrierGatewayMutationCoordinator struct {
	entered      chan struct{}
	releaseEntry chan struct{}
	observe      func() gatewayMutationObservation
	observed     chan gatewayMutationObservation
	releaseExit  chan struct{}
}

type gatewayFailureOrderRegistry struct {
	*state.CredentialRegistry
	failureEntered chan struct{}
	releaseFailure chan struct{}
	enterOnce      sync.Once
}

func (registry *gatewayFailureOrderRegistry) IncrFailure(credentialID uint) (int, bool) {
	registry.enterOnce.Do(func() {
		close(registry.failureEntered)
		<-registry.releaseFailure
	})
	return registry.CredentialRegistry.IncrFailure(credentialID)
}

func newBarrierGatewayMutationCoordinator(
	observe func() gatewayMutationObservation,
) *barrierGatewayMutationCoordinator {
	return &barrierGatewayMutationCoordinator{
		entered: make(chan struct{}), releaseEntry: make(chan struct{}),
		observe: observe, observed: make(chan gatewayMutationObservation, 1),
		releaseExit: make(chan struct{}),
	}
}

func (coordinator *barrierGatewayMutationCoordinator) Do(_ uint, fn func()) {
	close(coordinator.entered)
	<-coordinator.releaseEntry
	fn()
	coordinator.observed <- coordinator.observe()
	<-coordinator.releaseExit
}

func receiveTestSignal[T any](t *testing.T, signal <-chan T, name string) T {
	t.Helper()
	select {
	case value := <-signal:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out after 1s waiting for %s", name)
		var zero T
		return zero
	}
}

func (registry *recordingRuntimeRegistry) SetCooldown(credentialID uint, until time.Time) bool {
	registry.cooldownCredentialID = credentialID
	registry.cooldownUntil = until
	registry.cooldownCalls++
	return registry.CredentialRegistry.SetCooldown(credentialID, until)
}

func (registry *recordingRuntimeRegistry) SetCooldownWithChange(
	credentialID uint,
	until time.Time,
) (bool, bool) {
	registry.cooldownCredentialID = credentialID
	registry.cooldownUntil = until
	registry.cooldownCalls++
	return registry.CredentialRegistry.SetCooldownWithChange(credentialID, until)
}

func (registry *recordingRuntimeRegistry) IncrFailure(credentialID uint) (int, bool) {
	registry.incrFailureCalls++
	count, ok := registry.CredentialRegistry.IncrFailure(credentialID)
	registry.lastFailureCount = count
	return count, ok
}

func (registry *recordingRuntimeRegistry) SetBlacklisted(credentialID uint) bool {
	registry.blacklistCalls++
	return registry.CredentialRegistry.SetBlacklisted(credentialID)
}

func (registry *recordingRuntimeRegistry) SetBlacklistedWithChange(credentialID uint) (bool, bool) {
	registry.blacklistCalls++
	return registry.CredentialRegistry.SetBlacklistedWithChange(credentialID)
}

func (registry *recordingRuntimeRegistry) ClearFailure(credentialID uint) bool {
	registry.clearCalls++
	return registry.CredentialRegistry.ClearFailure(credentialID)
}

func TestHandlerCoordinatesSuccessMutation(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	registry := &recordingRuntimeRegistry{CredentialRegistry: state.NewCredentialRegistry()}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, FailureCount: 2, EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	stats := health.NewStatsStore()
	coordinator := newBarrierGatewayMutationCoordinator(func() gatewayMutationObservation {
		return gatewayMutationObservation{
			clearCalls: registry.clearCalls,
			stats:      stats.Snapshot(1, now),
		}
	})
	handler := &Handler{registry: registry, stats: stats, mutations: coordinator}

	done := make(chan struct{})
	go func() {
		handler.recordCredentialSuccess(1, now)
		close(done)
	}()
	receiveTestSignal(t, coordinator.entered, "success coordinator entry")
	if registry.clearCalls != 0 || stats.Snapshot(1, now) != (health.CredentialStats{}) {
		t.Fatalf("success bundle changed state before coordinator callback")
	}
	close(coordinator.releaseEntry)
	observed := receiveTestSignal(t, coordinator.observed, "success mutation observation")
	if observed.clearCalls != 1 || observed.stats != (health.CredentialStats{Success: 1}) {
		t.Fatalf("coordinator callback observation = %#v", observed)
	}
	select {
	case <-done:
		t.Fatal("success mutation returned before coordinator interval was released")
	default:
	}
	close(coordinator.releaseExit)
	receiveTestSignal(t, done, "success mutation completion")
}

func TestHandlerLogsCredentialStateChanges(t *testing.T) {
	now := time.Date(2026, time.August, 11, 18, 0, 0, 0, time.UTC)
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1,
		Fingerprint: "test-1", Status: state.CredentialStatusActive,
		EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler := &Handler{
		registry:  registry,
		stats:     health.NewStatsStore(),
		mutations: health.NewMutationCoordinator(),
		logger:    newGatewayJSONLogger(&logs),
	}
	cooldown := health.Result{
		Category:      health.FailureCategoryRateLimited,
		Action:        health.ActionCooldownCredential,
		CooldownUntil: now.Add(time.Minute),
	}

	handler.applyCredentialAction(1, cooldown, http.StatusTooManyRequests, now)
	handler.applyCredentialAction(1, cooldown, http.StatusTooManyRequests, now)
	for range blacklistFailureThreshold + 1 {
		handler.applyCredentialAction(1, health.Result{
			Category: health.FailureCategoryInvalidKey,
			Action:   health.ActionFailCredential,
		}, http.StatusUnauthorized, now)
	}

	events := decodeGatewayJSONLogs(t, logs.Bytes())
	if got := gatewayEventsNamed(events, "credential_cooldown"); len(got) != 1 {
		t.Fatalf("credential_cooldown events = %#v, want one", got)
	} else if event := got[0]; event["credential_id"] != float64(1) ||
		event["category"] != "rate_limited" ||
		event["status_code"] != float64(http.StatusTooManyRequests) ||
		event["level"] != "warning" ||
		event["msg"] != "[DATA] Credential entered cooldown" {
		t.Fatalf("credential_cooldown event = %#v", event)
	}
	if got := gatewayEventsNamed(events, "credential_blacklisted"); len(got) != 1 {
		t.Fatalf("credential_blacklisted events = %#v, want one", got)
	} else if event := got[0]; event["credential_id"] != float64(1) ||
		event["failures"] != float64(blacklistFailureThreshold) ||
		event["category"] != "invalid_key" ||
		event["status_code"] != float64(http.StatusUnauthorized) ||
		event["level"] != "warning" ||
		event["msg"] != "[DATA] Credential blacklisted" {
		t.Fatalf("credential_blacklisted event = %#v", event)
	}
	if len(events) != 2 {
		t.Fatalf("credential state events = %#v, want only two", events)
	}
}

func TestHandlerRecordsCooldownFailureContext(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	registry := &recordingRuntimeRegistry{CredentialRegistry: state.NewCredentialRegistry()}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	stats := health.NewStatsStore()
	handler := &Handler{registry: registry, stats: stats}
	until := now.Add(30 * time.Second)

	handler.applyCredentialAction(1, health.Result{
		Category:      health.FailureCategoryRateLimited,
		Action:        health.ActionCooldownCredential,
		CooldownUntil: until,
	}, http.StatusTooManyRequests, now)

	if registry.cooldownCalls != 1 || registry.cooldownCredentialID != 1 ||
		!registry.cooldownUntil.Equal(until) {
		t.Fatalf(
			"cooldown mutation = calls:%d key:%d until:%s",
			registry.cooldownCalls,
			registry.cooldownCredentialID,
			registry.cooldownUntil,
		)
	}
	if got, want := stats.Snapshot(1, now), (health.CredentialStats{
		Problem:             1,
		ConsecutiveProblem:  1,
		LastFailureCategory: health.FailureCategoryRateLimited,
		LastStatusCode:      http.StatusTooManyRequests,
	}); got != want {
		t.Fatalf("cooldown stats = %#v, want %#v", got, want)
	}
}

func TestHandlerSkipsStatsWhenRegistryKeyWasDeletedBeforeCompletion(t *testing.T) {
	now := time.Date(2026, time.August, 1, 14, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*Handler)
	}{
		{
			name: "cooldown",
			mutate: func(handler *Handler) {
				handler.applyCredentialAction(1, health.Result{
					Category: health.FailureCategoryRateLimited,
					Action:   health.ActionCooldownCredential,
				}, http.StatusTooManyRequests, now)
			},
		},
		{
			name: "attributable failure",
			mutate: func(handler *Handler) {
				handler.applyCredentialAction(1, health.Result{
					Category: health.FailureCategoryInvalidKey,
					Action:   health.ActionFailCredential,
				}, http.StatusUnauthorized, now)
			},
		},
		{
			name: "success",
			mutate: func(handler *Handler) {
				handler.recordCredentialSuccess(1, now)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			stats := health.NewStatsStore()
			handler := &Handler{
				registry:  state.NewCredentialRegistry(),
				stats:     stats,
				mutations: health.NewMutationCoordinator(),
			}
			test.mutate(handler)
			if got := stats.Snapshot(1, now); got != (health.CredentialStats{}) {
				t.Fatalf("stats after deleted-key completion = %#v, want zero", got)
			}
		})
	}
}

func TestHandlerCoordinatesAttributableFailureMutation(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	registry := &recordingRuntimeRegistry{CredentialRegistry: state.NewCredentialRegistry()}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, FailureCount: 2, EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	stats := health.NewStatsStore()
	coordinator := newBarrierGatewayMutationCoordinator(func() gatewayMutationObservation {
		return gatewayMutationObservation{
			incrFailureCalls: registry.incrFailureCalls,
			lastFailureCount: registry.lastFailureCount,
			blacklistCalls:   registry.blacklistCalls,
			stats:            stats.Snapshot(1, now),
		}
	})
	handler := &Handler{registry: registry, stats: stats, mutations: coordinator}

	done := make(chan struct{})
	go func() {
		handler.applyCredentialAction(1, health.Result{
			Category: health.FailureCategoryInvalidKey,
			Action:   health.ActionFailCredential,
		}, http.StatusUnauthorized, now)
		close(done)
	}()
	receiveTestSignal(t, coordinator.entered, "failure coordinator entry")
	if registry.incrFailureCalls != 0 || registry.blacklistCalls != 0 ||
		stats.Snapshot(1, now) != (health.CredentialStats{}) {
		t.Fatalf("failure bundle changed state before coordinator callback")
	}
	close(coordinator.releaseEntry)
	observed := receiveTestSignal(t, coordinator.observed, "failure mutation observation")
	if observed.incrFailureCalls != 1 || observed.lastFailureCount != 3 ||
		observed.blacklistCalls != 1 ||
		observed.stats != (health.CredentialStats{
			Failure:             1,
			Problem:             1,
			ConsecutiveFailure:  1,
			ConsecutiveProblem:  1,
			LastFailureCategory: health.FailureCategoryInvalidKey,
			LastStatusCode:      http.StatusUnauthorized,
		}) {
		t.Fatalf("coordinator callback observation = %#v", observed)
	}
	select {
	case <-done:
		t.Fatal("failure mutation returned before coordinator interval was released")
	default:
	}
	close(coordinator.releaseExit)
	receiveTestSignal(t, done, "failure mutation completion")
}

func TestGatewayFailureAndValidationRecoveryFailureFirstKeepsRegistryAndStatsFailed(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	baseRegistry := state.NewCredentialRegistry()
	if err := baseRegistry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
		Blacklisted: true, FailureCount: 3, EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	ref := baseRegistry.BlacklistedCredentials()[0]
	registry := &gatewayFailureOrderRegistry{
		CredentialRegistry: baseRegistry,
		failureEntered:     make(chan struct{}),
		releaseFailure:     make(chan struct{}),
	}
	stats := health.NewStatsStore()
	stats.RecordFailure(1, health.FailureCategoryAmbiguous, 0, now)
	mutations := health.NewMutationCoordinator()
	handler := &Handler{registry: registry, stats: stats, mutations: mutations}

	failureDone := make(chan struct{})
	go func() {
		handler.applyCredentialAction(1, health.Result{Action: health.ActionFailCredential}, 0, now)
		close(failureDone)
	}()
	receiveTestSignal(t, registry.failureEntered, "gateway failure mutation")

	recoveryAttempted := make(chan struct{})
	recoveryResult := make(chan bool, 1)
	go func() {
		close(recoveryAttempted)
		var recovered bool
		mutations.Do(1, func() {
			recovered = baseRegistry.RecoverIfMatch(ref, state.DefaultWeight)
			if recovered {
				stats.Reset(1)
			}
		})
		recoveryResult <- recovered
	}()
	receiveTestSignal(t, recoveryAttempted, "validation recovery attempt")
	close(registry.releaseFailure)
	receiveTestSignal(t, failureDone, "gateway failure completion")
	if recovered := receiveTestSignal(t, recoveryResult, "validation recovery completion"); recovered {
		t.Fatal("stale validation recovery = true after gateway failure, want false")
	}

	if got, want := baseRegistry.BlacklistedCredentials(), []state.CredentialRef{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1,
		Fingerprint: "test-1", EncryptedValue: "cipher", FailureGeneration: 1,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blacklisted keys = %#v, want %#v", got, want)
	}
	if got, want := stats.Snapshot(1, now), (health.CredentialStats{
		Failure: 2, Problem: 2, ConsecutiveFailure: 2, ConsecutiveProblem: 2,
	}); got != want {
		t.Fatalf("stats = %#v, want %#v", got, want)
	}
}

func TestGatewayFailureAndValidationRecoveryRecoveryFirstLeavesNewFailure(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	registry := state.NewCredentialRegistry()
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
		Blacklisted: true, FailureCount: 3, EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	ref := registry.BlacklistedCredentials()[0]
	stats := health.NewStatsStore()
	stats.RecordFailure(1, health.FailureCategoryAmbiguous, 0, now)
	mutations := health.NewMutationCoordinator()
	handler := &Handler{registry: registry, stats: stats, mutations: mutations}

	recoveryEntered := make(chan struct{})
	releaseRecovery := make(chan struct{})
	recoveryResult := make(chan bool, 1)
	go func() {
		var recovered bool
		mutations.Do(1, func() {
			close(recoveryEntered)
			<-releaseRecovery
			recovered = registry.RecoverIfMatch(ref, state.DefaultWeight)
			if recovered {
				stats.Reset(1)
			}
		})
		recoveryResult <- recovered
	}()
	receiveTestSignal(t, recoveryEntered, "validation recovery mutation")

	failureAttempted := make(chan struct{})
	failureDone := make(chan struct{})
	go func() {
		close(failureAttempted)
		handler.applyCredentialAction(1, health.Result{Action: health.ActionFailCredential}, 0, now)
		close(failureDone)
	}()
	receiveTestSignal(t, failureAttempted, "gateway failure attempt")
	close(releaseRecovery)
	if recovered := receiveTestSignal(t, recoveryResult, "validation recovery completion"); !recovered {
		t.Fatal("fresh validation recovery = false, want true")
	}
	receiveTestSignal(t, failureDone, "gateway failure completion")

	if got := registry.BlacklistedCredentials(); len(got) != 0 {
		t.Fatalf("blacklisted keys = %#v, want recovered key with new sub-threshold failure", got)
	}
	if refs := registry.CaptureActiveCredentialRefs([]uint{1}); len(refs) != 1 || refs[0].FailureGeneration != 2 {
		t.Fatalf("active refs = %#v, want generation 2 after recovery then failure", refs)
	}
	if got, want := stats.Snapshot(1, now), (health.CredentialStats{
		Failure: 1, Problem: 1, ConsecutiveFailure: 1, ConsecutiveProblem: 1,
	}); got != want {
		t.Fatalf("stats = %#v, want %#v", got, want)
	}
}

func (registry *mutatingRuntimeRegistry) CollectCredentialCandidates(
	groupIDs []uint,
	excluded func(uint) bool,
	now time.Time,
) []state.CredentialMeta {
	candidates := registry.CredentialRegistry.CollectCredentialCandidates(groupIDs, excluded, now)
	if !registry.mutated && len(candidates) > 0 {
		registry.mutated = true
		registry.mutate()
	}
	return candidates
}

func (forwarder *scriptedForwarder) Forward(_ context.Context, input ForwardInput) UpstreamResult {
	index := len(forwarder.inputs)
	forwarder.inputs = append(forwarder.inputs, input)
	if forwarder.onCall != nil {
		forwarder.onCall(index)
	}
	if index >= len(forwarder.results) {
		return UpstreamResult{Err: errors.New("script exhausted")}
	}
	return forwarder.results[index]
}

func (forwarder *scriptedForwarder) ForwardStream(
	_ context.Context,
	input ForwardInput,
	writer http.ResponseWriter,
) UpstreamResult {
	index := len(forwarder.streamInputs)
	forwarder.streamInputs = append(forwarder.streamInputs, input)
	if forwarder.onStreamCall != nil {
		forwarder.onStreamCall(index, writer)
	}
	if index >= len(forwarder.streamResults) {
		return UpstreamResult{Err: errors.New("stream script exhausted")}
	}
	result := forwarder.streamResults[index]
	if forwarder.invokeStreamReady && result.Committed && input.OnStreamReady != nil {
		input.OnStreamReady()
	}
	return result
}

func (forwarder *streamReadyBlockingForwarder) Forward(
	context.Context,
	ForwardInput,
) UpstreamResult {
	return UpstreamResult{Err: errors.New("unexpected non-streaming forward")}
}

func (forwarder *streamReadyBlockingForwarder) ForwardStream(
	_ context.Context,
	input ForwardInput,
	_ http.ResponseWriter,
) UpstreamResult {
	if input.OnStreamReady != nil {
		input.OnStreamReady()
	}
	close(forwarder.ready)
	<-forwarder.release
	return forwarder.result
}

func (forwarder *streamReadyBlockingForwarder) Release() {
	forwarder.releaseOnce.Do(func() { close(forwarder.release) })
}

func TestHandlerRecordsNonStreamingResultStatsByAction(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		result UpstreamResult
		want   health.CredentialStats
	}{
		{
			name: "2xx success",
			result: UpstreamResult{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
			},
			want: health.CredentialStats{Success: 1},
		},
		{
			name: "invalid key",
			result: UpstreamResult{
				StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid key"}`),
				ClassificationBody: []byte(`{"error":"invalid key"}`), RequestWritten: true,
			},
			want: health.CredentialStats{
				Failure:             1,
				Problem:             1,
				ConsecutiveFailure:  1,
				ConsecutiveProblem:  1,
				LastFailureCategory: health.FailureCategoryInvalidKey,
				LastStatusCode:      http.StatusUnauthorized,
			},
		},
		{
			name: "client error",
			result: UpstreamResult{
				StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: []byte(`{"error":"invalid input"}`),
				ClassificationBody: []byte(`{"error":"invalid input"}`), RequestWritten: true,
			},
			want: health.CredentialStats{},
		},
		{
			name: "rate limited",
			result: UpstreamResult{
				StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: []byte(`{"error":"rate limit"}`),
				ClassificationBody: []byte(`{"error":"rate limit"}`), RequestWritten: true,
			},
			want: health.CredentialStats{
				Problem:             1,
				ConsecutiveProblem:  1,
				LastFailureCategory: health.FailureCategoryRateLimited,
				LastStatusCode:      http.StatusTooManyRequests,
			},
		},
		{
			name: "model unavailable",
			result: UpstreamResult{
				StatusCode: http.StatusNotFound, Header: make(http.Header), Body: []byte(`{"error":"model not found"}`),
				ClassificationBody: []byte(`{"error":"model not found"}`), RequestWritten: true,
			},
			want: health.CredentialStats{
				Problem:             1,
				ConsecutiveProblem:  1,
				LastFailureCategory: health.FailureCategoryModelUnavailable,
				LastStatusCode:      http.StatusNotFound,
			},
		},
		{
			name: "host error",
			result: UpstreamResult{
				StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: []byte(`{"error":"overloaded"}`),
				ClassificationBody: []byte(`{"error":"overloaded"}`), RequestWritten: true,
			},
			want: health.CredentialStats{},
		},
		{
			name: "pre-write transport",
			result: UpstreamResult{
				Err: errors.New("dial upstream"),
			},
			want: health.CredentialStats{},
		},
		{
			name: "context canceled",
			result: UpstreamResult{
				Err: context.Canceled,
			},
			want: health.CredentialStats{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{test.result}}
			engine, handler, _, stats := newStatsHandlerTestRuntime(t, forwarder, "sk-one")
			handler.now = func() time.Time { return now }

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewBufferString(`{"model":"gpt-4o"}`),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			engine.ServeHTTP(httptest.NewRecorder(), request)

			if got := stats.Snapshot(1, now); got != test.want {
				t.Fatalf("stats = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestHandlerRecordsInvalidKeyPerAttempt(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid key"}`),
			ClassificationBody: []byte(`{"error":"invalid key"}`), RequestWritten: true,
		},
		{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
		},
	}}
	engine, handler, _, stats := newStatsHandlerTestRuntime(t, forwarder, "sk-first", "sk-second")
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	handler.now = func() time.Time { return now }

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if got := stats.Snapshot(1, now); got != (health.CredentialStats{
		Failure:             1,
		Problem:             1,
		ConsecutiveFailure:  1,
		ConsecutiveProblem:  1,
		LastFailureCategory: health.FailureCategoryInvalidKey,
		LastStatusCode:      http.StatusUnauthorized,
	}) {
		t.Fatalf("first key stats = %#v, want one failure", got)
	}
	if got := stats.Snapshot(2, now); got != (health.CredentialStats{Success: 1}) {
		t.Fatalf("second key stats = %#v, want one success", got)
	}
}

func TestHandlerRecordsStreamSuccessOnlyAfterCleanTerminal(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		result      UpstreamResult
		wantSuccess bool
	}{
		{
			name: "clean terminal records after forward returns",
			result: UpstreamResult{
				StatusCode: http.StatusOK, Committed: true, RequestWritten: true,
				Stream: StreamObservation{EndReason: StreamEndCleanEOF},
			},
			wantSuccess: true,
		},
		{
			name: "pump failure after ready does not record success",
			result: UpstreamResult{
				Err: errors.New("stream pump failed"), Committed: true, RequestWritten: true,
				Stream: StreamObservation{EndReason: StreamEndUpstreamTerminated},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &streamReadyBlockingForwarder{
				result: test.result, ready: make(chan struct{}), release: make(chan struct{}),
			}
			t.Cleanup(forwarder.Release)
			engine, handler, _, stats := newStatsHandlerTestRuntime(t, forwarder, "sk-one")
			handler.now = func() time.Time { return now }

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			done := make(chan struct{})
			go func() {
				engine.ServeHTTP(httptest.NewRecorder(), request)
				close(done)
			}()

			receiveTestSignal(t, forwarder.ready, "stream-ready callback")
			if got := stats.Snapshot(1, now); got != (health.CredentialStats{}) {
				t.Fatalf("stats before forward returns = %#v, want zero", got)
			}
			forwarder.Release()
			receiveTestSignal(t, done, "stream request completion")
			want := health.CredentialStats{}
			if test.wantSuccess {
				want.Success = 1
			}
			if got := stats.Snapshot(1, now); got != want {
				t.Fatalf("stats after forward returns = %#v, want %#v", got, want)
			}
		})
	}

	preCommitForwarder := &scriptedForwarder{streamResults: []UpstreamResult{{
		Err: errors.New("first stream event failed"), RequestWritten: true,
	}}}
	engine, handler, _, stats := newStatsHandlerTestRuntime(t, preCommitForwarder, "sk-one")
	handler.now = func() time.Time { return now }
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)
	if got := stats.Snapshot(1, now); got != (health.CredentialStats{}) {
		t.Fatalf("pre-commit stream stats = %#v, want zero", got)
	}
}

func TestHandlerDoesNotRecordCanceledAttempt(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	forwarder := &scriptedForwarder{
		results: []UpstreamResult{{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
		}},
		onCall: func(int) { cancel() },
	}
	engine, handler, _, stats := newStatsHandlerTestRuntime(t, forwarder, "sk-one")
	handler.now = func() time.Time {
		t.Fatal("canceled attempt read the clock")
		return now
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`),
	).WithContext(requestContext)
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if got := stats.Snapshot(1, now); got != (health.CredentialStats{}) {
		t.Fatalf("canceled attempt stats = %#v, want zero", got)
	}
}

func TestHandlerDoesNotRecordCommittedNonStreamingAttempt(t *testing.T) {
	now := time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`),
		Committed: true, RequestWritten: true,
	}}}
	engine, handler, _, stats := newStatsHandlerTestRuntime(t, forwarder, "sk-one")
	handler.now = func() time.Time {
		t.Fatal("committed attempt read the clock")
		return now
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	if got := stats.Snapshot(1, now); got != (health.CredentialStats{}) {
		t.Fatalf("committed attempt stats = %#v, want zero", got)
	}
}

func TestHandlerInitializesDebugHeadersBeforeValidation(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		accessKey string
		body      string
	}{
		{name: "invalid auth", path: "/v1/chat/completions", accessKey: "wrong", body: `{"model":"gpt-4o"}`},
		{name: "invalid model", path: "/v1/chat/completions", accessKey: "gl-client", body: `{}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
			request := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			request.Header.Set("Authorization", "Bearer "+tt.accessKey)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			assertDebugHeaders(t, recorder.Header(), "", "0")
			if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
				t.Fatal("validation failure reached upstream forwarder")
			}
		})
	}
}

func TestHandlerRegistersEveryEnabledDataPlaneRoute(t *testing.T) {
	engine, _, _ := newHandlerTestRuntime(t, &scriptedForwarder{}, "sk-one")

	for _, testCase := range []struct {
		name   string
		method string
		target string
	}{
		{name: "OpenAI chat", method: http.MethodPost, target: "/v1/chat/completions"},
		{name: "Anthropic messages", method: http.MethodPost, target: "/v1/messages"},
		{
			name:   "Gemini generate",
			method: http.MethodPost,
			target: "/v1beta/models/gemini-2.5-pro:generateContent",
		},
		{
			name:   "Gemini stream",
			method: http.MethodPost,
			target: "/v1beta/models/gemini-2.5-pro:streamGenerateContent",
		},
		{name: "Gemini models", method: http.MethodGet, target: "/v1beta/models"},
		{name: "OpenAI or Anthropic models", method: http.MethodGet, target: "/v1/models"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(testCase.method, testCase.target, nil)
			request.Header.Set("Authorization", "Bearer wrong")

			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf(
					"%s %s = %d %s, want 401",
					testCase.method,
					testCase.target,
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestHandlerRejectsCaseCollidingModelBeforeAttempt(t *testing.T) {
	forwarder := &scriptedForwarder{}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"forbidden","Model":"allowed"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest ||
		!strings.Contains(recorder.Body.String(), `"code":"invalid_protocol_request"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
		t.Fatal("case-colliding request reached upstream")
	}
}

func TestHandlerEnforcesModelUTF8ByteLimitBeforeAttempt(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		wantStatus int
		wantCode   string
		wantCalls  int
	}{
		{
			name:       "ASCII 255 bytes accepted",
			model:      strings.Repeat("a", 255),
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
		{
			name:       "ASCII 256 bytes rejected",
			model:      strings.Repeat("a", 256),
			wantStatus: http.StatusBadRequest,
			wantCode:   reasonInvalidProtocolRequest.Code,
		},
		{
			name:       "multibyte UTF-8 255 bytes accepted",
			model:      strings.Repeat("界", 85),
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
		{
			name:       "multibyte UTF-8 256 bytes rejected",
			model:      strings.Repeat("界", 84) + strings.Repeat("é", 2),
			wantStatus: http.StatusBadRequest,
			wantCode:   reasonInvalidProtocolRequest.Code,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode:     http.StatusOK,
				Header:         make(http.Header),
				Body:           []byte(`{"ok":true}`),
				RequestWritten: true,
			}}}
			sink := &recordingRequestLogSink{}
			engine, handler, manager, _ := newRequestLogHandlerTestRuntime(
				t,
				forwarder,
				&recordingAccessKeyRPMLimiter{},
				sink,
				"sk-one",
			)
			if _, err := manager.Publish(state.CompileInput{
				ChannelRegistry: channel.NewRegistry(),
				Groups: []state.GroupConfig{{
					ID: 1, Name: "openai", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
					Models: []state.ModelConfig{{
						ID:    "gpt-4o",
						Alias: test.model,
					}},
					Enabled: true,
				}},
				Credentials: []state.CredentialConfig{testCredentialConfig(1, 1)},
				AccessKeys: []state.AccessKeyConfig{{
					ID:      1,
					Name:    "client",
					KeyHash: handler.encryption.Hash("gl-client"),
					Status:  state.AccessKeyStatusActive,
				}},
			}); err != nil {
				t.Fatalf("Publish() error = %v", err)
			}

			payload, err := json.Marshal(map[string]string{"model": test.model})
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewReader(payload),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf(
					"status = %d, want %d; body=%s",
					recorder.Code,
					test.wantStatus,
					recorder.Body.String(),
				)
			}
			if len(forwarder.inputs) != test.wantCalls {
				t.Fatalf("upstream attempts = %d, want %d", len(forwarder.inputs), test.wantCalls)
			}

			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("RequestLog events = %d, want 1", len(events))
			}
			event := events[0]
			if test.wantCode == "" {
				if event.ClientModel != test.model {
					t.Fatalf("client model = %q, want accepted model", event.ClientModel)
				}
				return
			}

			var response struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Code != test.wantCode {
				t.Fatalf("response code = %q, want %q", response.Code, test.wantCode)
			}
			assertDebugHeaders(t, recorder.Header(), "", "0")
			if event.ClientModel != "" || len(event.Attempts) != 0 ||
				strings.Contains(event.ErrorSummary, test.model) {
				t.Fatalf("rejected model leaked into RequestLog event: %#v", event)
			}
			if strings.Contains(recorder.Body.String(), test.model) ||
				strings.Contains(fmt.Sprint(recorder.Header()), test.model) {
				t.Fatalf(
					"rejected model leaked into response: headers=%v body=%s",
					recorder.Header(),
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestHandlerServesLocalModelEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		target   string
		headers  http.Header
		expected string
	}{
		{
			name: "OpenAI with Bearer", method: http.MethodGet, target: "/v1/models",
			headers:  http.Header{"Authorization": {"Bearer gl-client"}},
			expected: `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"beta","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"zeta","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`,
		},
		{
			name: "Anthropic with Bearer", method: http.MethodGet, target: "/v1/models",
			headers:  http.Header{"Authorization": {"Bearer gl-client"}, "Anthropic-Version": {"2023-06-01"}},
			expected: `{"data":[{"type":"model","id":"alpha","display_name":"alpha","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"beta","display_name":"beta","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"zeta","display_name":"zeta","created_at":"2025-01-01T00:00:00Z"}],"has_more":false,"first_id":"alpha","last_id":"zeta"}`,
		},
		{
			name: "x-api-key alone stays OpenAI", method: http.MethodGet, target: "/v1/models",
			headers:  http.Header{"X-Api-Key": {"gl-client"}},
			expected: `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"beta","object":"model","created":1735689600,"owned_by":"gpt-load"},{"id":"zeta","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`,
		},
		{
			name: "Anthropic with x-api-key", method: http.MethodGet, target: "/v1/models",
			headers:  http.Header{"X-Api-Key": {"gl-client"}, "Anthropic-Version": {"2023-06-01"}},
			expected: `{"data":[{"type":"model","id":"alpha","display_name":"alpha","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"beta","display_name":"beta","created_at":"2025-01-01T00:00:00Z"},{"type":"model","id":"zeta","display_name":"zeta","created_at":"2025-01-01T00:00:00Z"}],"has_more":false,"first_id":"alpha","last_id":"zeta"}`,
		},
		{
			name: "Gemini with query key", method: http.MethodGet, target: "/v1beta/models?key=gl-client",
			expected: `{"models":[{"name":"models/alpha"},{"name":"models/beta"},{"name":"models/zeta"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := newModelListHandlerEngine(t, state.FilterSet{})
			request := httptest.NewRequest(test.method, test.target, nil)
			request.Header = test.headers.Clone()
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			assertJSONEqual(t, recorder.Body.String(), test.expected)
			assertDebugHeaders(t, recorder.Header(), "", "0")
		})
	}
}

func TestHandlerModelEndpointsApplyFiltersAndKeepEmptyShape(t *testing.T) {
	t.Run("joint filters", func(t *testing.T) {
		engine := newModelListHandlerEngine(t, state.FilterSet{
			Protocols: map[protocol.Protocol]struct{}{protocol.OpenAICompletions: {}},
			Models:    map[string]struct{}{"alpha": {}},
			Groups:    map[uint]struct{}{1: {}},
		})
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertJSONEqual(t, recorder.Body.String(), `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`)
		assertDebugHeaders(t, recorder.Header(), "", "0")
	})

	t.Run("protocol denied keeps official empty envelope", func(t *testing.T) {
		engine := newModelListHandlerEngine(t, state.FilterSet{
			Protocols: map[protocol.Protocol]struct{}{protocol.Gemini: {}},
		})
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("X-Api-Key", "gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertJSONEqual(t, recorder.Body.String(), `{"object":"list","data":[]}`)
		assertDebugHeaders(t, recorder.Header(), "", "0")
	})
}

func TestHandlerModelEndpointsRequireValidAccessKey(t *testing.T) {
	engine := newModelListHandlerEngine(t, state.FilterSet{})
	for _, header := range []string{"", "Bearer wrong"} {
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), reasonInvalidAccessKey.Code) {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
		assertDebugHeaders(t, recorder.Header(), "", "0")
	}
}

func TestHandlerModelEndpointHasNoDataPlaneSideEffects(t *testing.T) {
	keyService, err := encryption.NewService("model-handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "openai", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "alpha"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	spyEncryption := &decryptPanicEncryption{Service: keyService}
	handler := NewHandler(
		manager, state.NewCredentialRegistry(), spyEncryption, panicForwarder{}, dialect.NewSet(), health.NewStatsStore(),
		health.NewMutationCoordinator(),
		nil, nil, nil,
	)
	handler.registry = panicRuntimeRegistry{}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Body = panicReadCloser{}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	assertJSONEqual(t, recorder.Body.String(), `{"object":"list","data":[{"id":"alpha","object":"model","created":1735689600,"owned_by":"gpt-load"}]}`)
}

func TestHandlerModelListOverflowReturnsSmallStableErrorWithoutPartialJSON(t *testing.T) {
	engine := newModelListHandlerEngineWithLimit(t, state.FilterSet{}, 64)
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Anthropic-Version", "2023-06-01")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || recorder.Body.Len() > 256 ||
		!strings.Contains(recorder.Body.String(), `"code":"model_list_too_large"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, fragment := range []string{`"type":"model"`, `"id":"alpha"`, `"id":"beta"`, `"id":"zeta"`} {
		if strings.Contains(recorder.Body.String(), fragment) {
			t.Fatalf("overflow response contains partial model fragment %q: %s", fragment, recorder.Body.String())
		}
	}
}

func newModelListHandlerEngine(t *testing.T, filters state.FilterSet) *gin.Engine {
	return newModelListHandlerEngineWithLimit(t, filters, maxNonStreamingResponseBodyBytes)
}

func newModelListHandlerEngineWithLimit(
	t *testing.T,
	filters state.FilterSet,
	limit int64,
) *gin.Engine {
	t.Helper()
	keyService, err := encryption.NewService("model-handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{
			{
				ID: 1, Name: "multi", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "zeta"}, {ID: "alpha"}}, Enabled: true,
			},
			{
				ID: 2, Name: "openai", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "beta"}}, Enabled: true,
			},
		},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive, Filters: filters,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	handler := NewHandler(
		manager, state.NewCredentialRegistry(), keyService, &scriptedForwarder{}, dialect.NewSet(), health.NewStatsStore(),
		health.NewMutationCoordinator(),
		nil, nil, nil,
	)
	handler.modelListLimit = limit
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine
}

type panicRuntimeRegistry struct{}

func (panicRuntimeRegistry) CollectCredentialCandidates([]uint, func(uint) bool, time.Time) []state.CredentialMeta {
	panic("model endpoint collected upstream candidates")
}

func (panicRuntimeRegistry) ActiveEncryptedCredentialData(uint, uint) (string, bool) {
	panic("model endpoint read an upstream key")
}

func (panicRuntimeRegistry) CaptureActiveCredentialRefs([]uint) []state.CredentialRef {
	panic("model endpoint captured upstream keys")
}

func (panicRuntimeRegistry) CredentialRef(uint) (state.CredentialRef, bool) {
	panic("model endpoint read a credential reference")
}

func (panicRuntimeRegistry) ActiveEncryptedCredentialDataIfMatch(state.CredentialRef) (string, bool) {
	panic("model endpoint matched an upstream key")
}

func (panicRuntimeRegistry) SetCooldown(uint, time.Time) bool {
	panic("model endpoint set cooldown")
}

func (panicRuntimeRegistry) SetCooldownWithChange(uint, time.Time) (bool, bool) {
	panic("model endpoint set cooldown")
}

func (panicRuntimeRegistry) IncrFailure(uint) (int, bool) {
	panic("model endpoint incremented failure")
}

func (panicRuntimeRegistry) SetBlacklisted(uint) bool {
	panic("model endpoint set blacklist")
}

func (panicRuntimeRegistry) SetBlacklistedWithChange(uint) (bool, bool) {
	panic("model endpoint set blacklist")
}

func (panicRuntimeRegistry) ClearFailure(uint) bool {
	panic("model endpoint cleared failure")
}

type panicForwarder struct{}

func (panicForwarder) Forward(context.Context, ForwardInput) UpstreamResult {
	panic("model endpoint called Forward")
}

func (panicForwarder) ForwardStream(context.Context, ForwardInput, http.ResponseWriter) UpstreamResult {
	panic("model endpoint called ForwardStream")
}

type decryptPanicEncryption struct {
	encryption.Service
}

func (*decryptPanicEncryption) Decrypt(string) (string, error) {
	panic("model endpoint decrypted an upstream key")
}

type panicReadCloser struct{}

func (panicReadCloser) Read([]byte) (int, error) {
	panic("model endpoint read request body")
}

func (panicReadCloser) Close() error {
	return nil
}

func TestHandlerNeverExposesProviderKeyFragmentsInResponseHeaders(t *testing.T) {
	const providerKey = "prov-fragment-test-abcdefghwxyz"
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       []byte(`{"ok":true}`),
	}}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, providerKey)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if values := recorder.Header().Values(debugHeaderKey); len(values) != 0 {
		t.Fatalf("%s = %#v, want absent", debugHeaderKey, values)
	}
	if recorder.Header().Get(debugHeaderGroup) != "openai" ||
		recorder.Header().Get(debugHeaderAttempts) != "1" {
		t.Fatalf("debug headers = %#v", recorder.Header())
	}
	serialized := fmt.Sprint(recorder.Header())
	for _, forbidden := range []string{
		providerKey,
		providerKey[:4],
		providerKey[len(providerKey)-4:],
		utils.MaskAPIKey(providerKey),
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("response headers expose provider key fragment %q: %s", forbidden, serialized)
		}
	}
}

func TestHandlerReportsFinalAttemptInDebugHeaders(t *testing.T) {
	tests := []struct {
		name         string
		results      []UpstreamResult
		upstreamKeys []string
		wantAttempts string
	}{
		{
			name: "first attempt success",
			results: []UpstreamResult{{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`),
			}},
			upstreamKeys: []string{"sk-first-success"}, wantAttempts: "1",
		},
		{
			name: "retry success",
			results: []UpstreamResult{
				{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`)},
				{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`)},
			},
			upstreamKeys: []string{"sk-retry-one", "sk-retry-two"}, wantAttempts: "2",
		},
		{
			name:         "transport skips only group",
			results:      []UpstreamResult{{Err: errors.New("dial failed")}},
			upstreamKeys: []string{"sk-dial-one", "sk-dial-two"},
			wantAttempts: "1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: tt.results}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, tt.upstreamKeys...)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			assertDebugHeaders(t, recorder.Header(), "openai", tt.wantAttempts)
		})
	}
}

func TestHandlerWriteUpstreamResponseChecksWriteResultAndClearsDeadline(t *testing.T) {
	writeFailure := errors.New("write failed")
	flushFailure := errors.New("flush failed")
	tests := []struct {
		name     string
		write    func([]byte) (int, error)
		flushErr error
		wantErr  error
	}{
		{
			name: "short write",
			write: func(body []byte) (int, error) {
				return len(body) - 1, nil
			},
			wantErr: io.ErrShortWrite,
		},
		{
			name: "write error",
			write: func([]byte) (int, error) {
				return 0, writeFailure
			},
			wantErr: writeFailure,
		},
		{
			name: "flush error",
			write: func(body []byte) (int, error) {
				return len(body), nil
			},
			flushErr: flushFailure,
			wantErr:  flushFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			writer := &deadlineGinWriter{
				ResponseWriter: ginContext.Writer,
				write:          test.write,
				flushErr:       test.flushErr,
			}
			ginContext.Writer = writer
			handler := &Handler{writeTimeout: time.Second}

			err := handler.writeUpstreamResponse(ginContext, UpstreamResult{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       []byte(`{"ok":true}`),
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("writeUpstreamResponse() error = %v, want %v", err, test.wantErr)
			}
			if len(writer.deadlines) < 2 || writer.deadlines[0].IsZero() ||
				!writer.deadlines[len(writer.deadlines)-1].IsZero() {
				t.Fatalf("deadlines = %#v, want armed operations followed by clear", writer.deadlines)
			}
		})
	}
}

func TestHandlerWriteEmptyResponseCommitsStatusBeforeFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	writer := &deadlineGinWriter{
		ResponseWriter: ginContext.Writer,
		write: func(body []byte) (int, error) {
			return len(body), nil
		},
	}
	ginContext.Writer = writer
	handler := &Handler{writeTimeout: time.Second}

	err := handler.writeUpstreamResponse(ginContext, UpstreamResult{
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
	})
	if err != nil || recorder.Code != http.StatusNoContent || writer.flushes != 1 {
		t.Fatalf(
			"writeUpstreamResponse() error/status/flushes = %v/%d/%d",
			err, recorder.Code, writer.flushes,
		)
	}
}

type deadlineGinWriter struct {
	gin.ResponseWriter
	write     func([]byte) (int, error)
	flushErr  error
	flushes   int
	deadlines []time.Time
}

func (writer *deadlineGinWriter) Write(body []byte) (int, error) {
	return writer.write(body)
}

func (writer *deadlineGinWriter) SetWriteDeadline(deadline time.Time) error {
	writer.deadlines = append(writer.deadlines, deadline)
	return nil
}

func (writer *deadlineGinWriter) FlushError() error {
	writer.flushes++
	return writer.flushErr
}

func TestHandlerRejectsSpoofedDebugHeaders(t *testing.T) {
	spoofed := http.Header{
		"X-GPTLoad-Group":    {"spoofed-group"},
		"X-GPTLoad-Key":      {"spoofed-key"},
		"X-GPTLoad-Attempts": {"999"},
	}
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: spoofed, Body: []byte(`{"ok":true}`),
	}}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-real-key")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	assertDebugHeaders(t, recorder.Header(), "openai", "1")
}

func assertDebugHeaders(t *testing.T, headers http.Header, group, attempts string) {
	t.Helper()
	want := map[string]string{
		debugHeaderGroup: group, debugHeaderAttempts: attempts,
	}
	for name, value := range want {
		values, exists := headers[http.CanonicalHeaderKey(name)]
		if !exists || len(values) != 1 || values[0] != value {
			t.Errorf("%s = %#v (exists=%t), want exactly [%q]", name, values, exists, value)
		}
	}
	if values := headers.Values(debugHeaderKey); len(values) != 0 {
		t.Errorf("%s = %#v, want absent", debugHeaderKey, values)
	}
}

func TestReadDecodedRequestBodyHonorsIndependentLimits(t *testing.T) {
	t.Run("exact limit is accepted", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("1234"))
		body, err := readDecodedRequestBody(request, contentcoding.Identity, 4, 4)
		if err != nil || string(body) != "1234" {
			t.Fatalf("readDecodedRequestBody() = %q, %v", body, err)
		}
	})

	t.Run("limit plus one is rejected without draining", func(t *testing.T) {
		reader := &boundedCountingReader{remaining: 100}
		request := httptest.NewRequest(http.MethodPost, "/", reader)
		request.ContentLength = -1
		body, err := readDecodedRequestBody(request, contentcoding.Identity, 4, 4)
		if !errors.Is(err, errRequestTooLarge) || body != nil {
			t.Fatalf("readDecodedRequestBody() = %q, %v, want request too large", body, err)
		}
		if reader.read != 5 {
			t.Fatalf("reader consumed %d bytes, want limit+1 (5)", reader.read)
		}
	})

	t.Run("positive ContentLength rejects before reading", func(t *testing.T) {
		reader := &boundedCountingReader{remaining: 100}
		request := httptest.NewRequest(http.MethodPost, "/", reader)
		request.ContentLength = 5
		body, err := readDecodedRequestBody(request, contentcoding.Identity, 4, 4)
		if !errors.Is(err, errRequestTooLarge) || body != nil || reader.read != 0 {
			t.Fatalf("readDecodedRequestBody() = %q, %v; read=%d, want early rejection", body, err, reader.read)
		}
	})

	t.Run("decoded overflow maps to request too large", func(t *testing.T) {
		encoded := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, []byte("12345"))
		request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(encoded))
		body, err := readDecodedRequestBody(request, contentcoding.Gzip, int64(len(encoded)), 4)
		if !errors.Is(err, errRequestTooLarge) || body != nil {
			t.Fatalf("readDecodedRequestBody() = %q, %v, want decoded overflow", body, err)
		}
	})

	t.Run("MaxInt64 encoded limit does not overflow limit plus one", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("1234"))
		body, err := readDecodedRequestBody(
			request,
			contentcoding.Identity,
			math.MaxInt64,
			4,
		)
		if err != nil || string(body) != "1234" {
			t.Fatalf("readDecodedRequestBody() = %q, %v, want 1234", body, err)
		}
	})

	t.Run("negative decoded limit is explicitly rejected", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("1234"))
		body, err := readDecodedRequestBody(
			request,
			contentcoding.Identity,
			4,
			-1,
		)
		if body != nil || err == nil || !strings.Contains(err.Error(), "decoded request body limit") {
			t.Fatalf("readDecodedRequestBody() = %q, %v, want explicit decoded-limit error", body, err)
		}
	})
}

func TestHandlerCapturesKeyIdentityOnlyAfterDecodedInspection(t *testing.T) {
	tests := []struct {
		name          string
		body          []byte
		encoding      string
		contentLength int64
		wantStatus    int
		wantCapture   int
	}{
		{
			name: "malformed coding", body: []byte("not-gzip"), encoding: "gzip",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "encoded limit", body: []byte(`{"model":"gpt-4o"}`),
			contentLength: maxRequestBodyBytes + 1, wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "invalid JSON", body: []byte(`{"model":`),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "valid decoded request", body: []byte(`{"model":"gpt-4o"}`),
			wantStatus: http.StatusOK, wantCapture: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: []byte(`{"ok":true}`), RequestWritten: true,
			}}}
			handler, _, registry := newHandlerForTest(t, forwarder, "sk-upstream")
			counting := &captureCountingRuntimeRegistry{runtimeCredentialRegistry: registry}
			handler.registry = counting
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			if test.encoding != "" {
				request.Header.Set("Content-Encoding", test.encoding)
			}
			if test.contentLength > 0 {
				request.ContentLength = test.contentLength
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus || counting.captureCalls != test.wantCapture {
				t.Fatalf(
					"response/capture calls = %d/%d, want %d/%d; body=%s",
					recorder.Code,
					counting.captureCalls,
					test.wantStatus,
					test.wantCapture,
					recorder.Body.String(),
				)
			}
		})
	}

	t.Run("canceled body read", func(t *testing.T) {
		forwarder := &scriptedForwarder{}
		handler, _, registry := newHandlerForTest(t, forwarder, "sk-upstream")
		counting := &captureCountingRuntimeRegistry{runtimeCredentialRegistry: registry}
		handler.registry = counting
		engine := gin.New()
		bindGatewayRoutesForTest(t, engine, handler)
		requestContext, cancel := context.WithCancel(context.Background())
		body := newBlockingRequestBody(`{"model":"gpt-4o"}`, func() error {
			cancel()
			return context.Canceled
		})
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			body,
		).WithContext(requestContext)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			engine.ServeHTTP(recorder, request)
			close(done)
		}()
		receiveTestSignal(t, body.started, "post-inspection capture canceled read")
		close(body.release)
		receiveTestSignal(t, done, "post-inspection capture canceled completion")
		if counting.captureCalls != 0 || len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
			t.Fatalf(
				"capture/forward calls = %d/%d, want 0/0",
				counting.captureCalls,
				len(forwarder.inputs)+len(forwarder.streamInputs),
			)
		}
	})

	t.Run("canceled after successful inspection", func(t *testing.T) {
		forwarder := &scriptedForwarder{}
		handler, _, registry := newHandlerForTest(t, forwarder, "sk-upstream")
		counting := &captureCountingRuntimeRegistry{runtimeCredentialRegistry: registry}
		handler.registry = counting
		requestContext, cancel := context.WithCancel(context.Background())
		baseDialect := handler.dialects[protocol.OpenAICompletions]
		handler.dialects = dialect.NewSet(&cancelingSuccessfulInspectDialect{
			Dialect: baseDialect,
			cancel:  cancel,
		})
		engine := gin.New()
		bindGatewayRoutesForTest(t, engine, handler)
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4o"}`),
		).WithContext(requestContext)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		if counting.captureCalls != 0 || len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
			t.Fatalf(
				"capture/forward calls = %d/%d, want 0/0 after inspection cancellation",
				counting.captureCalls,
				len(forwarder.inputs)+len(forwarder.streamInputs),
			)
		}
		if recorder.Body.Len() != 0 {
			t.Fatalf("canceled response body = %q, want empty", recorder.Body.String())
		}
	})
}

func TestHandlerContentCodingErrorContract(t *testing.T) {
	tests := []struct {
		name               string
		contentEncodings   []string
		collidingEncoding  string
		body               []byte
		contentLength      int64
		wantStatus         int
		wantCode           string
		wantAcceptEncoding string
	}{
		{
			name: "unknown coding", contentEncodings: []string{"snappy"},
			body:       []byte(`{"model":"gpt-4o"}`),
			wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_content_encoding",
			wantAcceptEncoding: contentcoding.SupportedRequestEncodings,
		},
		{
			name: "multiple case-colliding fields", contentEncodings: []string{"identity"},
			collidingEncoding: "gzip", body: []byte(`{"model":"gpt-4o"}`),
			wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_content_encoding",
			wantAcceptEncoding: contentcoding.SupportedRequestEncodings,
		},
		{
			name: "stacked coding", contentEncodings: []string{"gzip, br"},
			body:       []byte(`{"model":"gpt-4o"}`),
			wantStatus: http.StatusUnsupportedMediaType, wantCode: "unsupported_content_encoding",
			wantAcceptEncoding: contentcoding.SupportedRequestEncodings,
		},
		{
			name: "malformed known coding", contentEncodings: []string{"gzip"},
			body:       []byte("not-gzip"),
			wantStatus: http.StatusBadRequest, wantCode: "invalid_content_encoding",
		},
		{
			name: "encoded length fast rejection", contentEncodings: []string{"identity"},
			body: []byte(`{"model":"gpt-4o"}`), contentLength: maxRequestBodyBytes + 1,
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: "request_too_large",
		},
		{
			name: "decoded length rejection", contentEncodings: []string{"gzip"},
			body: encodeContentCodingForGatewayTest(
				t,
				contentcoding.Gzip,
				bytes.Repeat([]byte("x"), int(maxRequestBodyBytes+1)),
			),
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: "request_too_large",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-upstream")
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewReader(test.body),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			request.Header.Del("Content-Encoding")
			for _, value := range test.contentEncodings {
				request.Header.Add("Content-Encoding", value)
			}
			if test.collidingEncoding != "" {
				request.Header["content-encoding"] = []string{test.collidingEncoding}
			}
			if test.contentLength > 0 {
				request.ContentLength = test.contentLength
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			assertGatewayReasonTest(t, recorder, test.wantStatus, test.wantCode)
			if got := recorder.Header().Get("Accept-Encoding"); got != test.wantAcceptEncoding {
				t.Fatalf("Accept-Encoding = %q, want %q", got, test.wantAcceptEncoding)
			}
			if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
				t.Fatal("invalid content coding reached upstream")
			}
		})
	}
}

func TestHandlerContentCodingInspectionUsesDecodedBody(t *testing.T) {
	plaintext := []byte(`{"model":"gpt-4o","stream":true}`)
	for _, encoding := range []contentcoding.Encoding{
		contentcoding.Identity,
		contentcoding.Gzip,
		contentcoding.Brotli,
		contentcoding.Deflate,
		contentcoding.Zstd,
	} {
		t.Run(string(encoding), func(t *testing.T) {
			forwarder := &scriptedForwarder{streamResults: []UpstreamResult{{
				StatusCode:     http.StatusOK,
				Header:         http.Header{"Content-Type": {"text/event-stream"}},
				Committed:      true,
				RequestWritten: true,
				Stream:         StreamObservation{EndReason: StreamEndCleanEOF},
			}}}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-upstream")
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewReader(encodeContentCodingForGatewayTest(t, encoding, plaintext)),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			if encoding != contentcoding.Identity {
				request.Header.Set("Content-Encoding", string(encoding))
			}
			request.Header["content-length"] = []string{"stale-client-length"}
			for name, value := range map[string]string{
				"ETag":            `"stale"`,
				"Digest":          "sha-256=stale",
				"Content-MD5":     "stale-md5",
				"Content-Range":   "bytes 0-1/2",
				"Content-Digest":  "sha-256=:c3RhbGU=:",
				"Repr-Digest":     "sha-256=:c3RhbGU=:",
				"Signature":       "stale-signature",
				"Signature-Input": "stale-signature-input",
			} {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK || len(forwarder.streamInputs) != 1 {
				t.Fatalf(
					"response/stream calls = %d/%d body=%s, want decoded stream request",
					recorder.Code,
					len(forwarder.streamInputs),
					recorder.Body.String(),
				)
			}
			parsed := forwarder.streamInputs[0].Request
			if !bytes.Equal(parsed.Body, plaintext) || forwarder.streamInputs[0].ExternalModel != "gpt-4o" {
				t.Fatalf("parsed body/model = %s / %q", parsed.Body, forwarder.streamInputs[0].ExternalModel)
			}
			for _, name := range []string{
				"Content-Encoding",
				"Content-Length",
				"ETag",
				"Digest",
				"Content-MD5",
				"Content-Range",
				"Content-Digest",
				"Repr-Digest",
				"Signature",
				"Signature-Input",
			} {
				if values := headerFieldValues(parsed.Header, name); len(values) != 0 {
					t.Fatalf("parsed %s = %#v, want absent", name, values)
				}
			}
		})
	}
}

func TestHandlerContentCodingErrorPrecedence(t *testing.T) {
	t.Run("invalid access key wins", func(t *testing.T) {
		engine, _, _ := newHandlerTestRuntime(t, &scriptedForwarder{}, "sk-upstream")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("invalid"))
		request.Header.Set("Authorization", "Bearer wrong")
		request.Header.Set("Content-Encoding", "unknown")
		request.Header.Set("Accept-Encoding", "identity;q=0")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertGatewayReasonTest(t, recorder, http.StatusUnauthorized, reasonInvalidAccessKey.Code)
	})

	for _, test := range []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantCode   string
	}{
		{name: "route wins", method: http.MethodPost, target: "/v1/unknown", wantStatus: http.StatusNotFound, wantCode: reasonEndpointNotFound.Code},
		{name: "method wins", method: http.MethodGet, target: "/v1/chat/completions", wantStatus: http.StatusMethodNotAllowed, wantCode: reasonMethodNotAllowed.Code},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, _, _ := newHandlerTestRuntime(t, &scriptedForwarder{}, "sk-upstream")
			request := httptest.NewRequest(test.method, test.target, strings.NewReader("invalid"))
			request.Header.Set("Authorization", "Bearer gl-client")
			request.Header.Set("Content-Encoding", "unknown")
			request.Header.Set("Accept-Encoding", "identity;q=0")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assertGatewayReasonTest(t, recorder, test.wantStatus, test.wantCode)
		})
	}

	t.Run("RPM wins", func(t *testing.T) {
		limiter := &recordingAccessKeyRPMLimiter{decisions: []ratelimit.LimitDecision{{
			Allowed: false, RetryAfter: time.Second,
		}}}
		engine, _, _, _ := newRequestLogHandlerTestRuntime(
			t,
			&scriptedForwarder{},
			limiter,
			&recordingRequestLogSink{},
			"sk-upstream",
		)
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("invalid"))
		request.Header.Set("Authorization", "Bearer gl-client")
		request.Header.Set("Content-Encoding", "unknown")
		request.Header.Set("Accept-Encoding", "identity;q=0")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertGatewayReasonTest(t, recorder, http.StatusTooManyRequests, reasonAccessKeyRateLimited.Code)
	})

	t.Run("415 wins over 406", func(t *testing.T) {
		engine, _, _ := newHandlerTestRuntime(t, &scriptedForwarder{}, "sk-upstream")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("invalid"))
		request.Header.Set("Authorization", "Bearer gl-client")
		request.Header.Set("Content-Encoding", "unknown")
		request.Header.Set("Accept-Encoding", "identity;q=0")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertGatewayReasonTest(t, recorder, http.StatusUnsupportedMediaType, "unsupported_content_encoding")
	})

	t.Run("406 wins over body and JSON", func(t *testing.T) {
		engine, _, _ := newHandlerTestRuntime(t, &scriptedForwarder{}, "sk-upstream")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("not-gzip"))
		request.Header.Set("Authorization", "Bearer gl-client")
		request.Header.Set("Content-Encoding", "gzip")
		request.Header.Set("Accept-Encoding", "identity;q=0")
		request.ContentLength = maxRequestBodyBytes + 1
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertGatewayReasonTest(t, recorder, http.StatusNotAcceptable, "not_acceptable")
	})
}

func TestHandlerIdentityNegotiation(t *testing.T) {
	t.Run("case-colliding explicit identity exclusion wins over wildcard", func(t *testing.T) {
		engine, _, _ := newHandlerTestRuntime(t, &scriptedForwarder{}, "sk-upstream")
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4o"}`),
		)
		request.Header.Set("Authorization", "Bearer gl-client")
		request.Header["Accept-Encoding"] = []string{"*;q=1"}
		request.Header["accept-encoding"] = []string{"identity;q=0"}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertGatewayReasonTest(t, recorder, http.StatusNotAcceptable, "not_acceptable")
	})

	t.Run("model list negotiates without reading body", func(t *testing.T) {
		engine := newModelListHandlerEngine(t, state.FilterSet{})
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer gl-client")
		request.Header.Set("Accept-Encoding", "identity;q=0")
		request.Header.Set("Content-Encoding", "unknown")
		request.Body = panicReadCloser{}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		assertGatewayReasonTest(t, recorder, http.StatusNotAcceptable, "not_acceptable")
	})

	t.Run("model list accepts implicit identity without reading body", func(t *testing.T) {
		engine := newModelListHandlerEngine(t, state.FilterSet{})
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer gl-client")
		request.Header.Set("Accept-Encoding", "gzip")
		request.Header.Set("Content-Encoding", "unknown")
		request.Body = panicReadCloser{}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("response = %d %s, want 200", recorder.Code, recorder.Body.String())
		}
	})
}

func TestHandlerCancellationWinsOverContentCoding(t *testing.T) {
	forwarder := &scriptedForwarder{}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-upstream")
	requestContext, cancel := context.WithCancel(context.Background())
	body := newBlockingRequestBody("not-gzip", func() error {
		cancel()
		return context.Canceled
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body).WithContext(requestContext)
	request.Header.Set("Authorization", "Bearer gl-client")
	request.Header.Set("Content-Encoding", "gzip")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		engine.ServeHTTP(recorder, request)
		close(done)
	}()
	receiveTestSignal(t, body.started, "canceled body read")
	close(body.release)
	receiveTestSignal(t, done, "canceled request completion")

	if recorder.Body.Len() != 0 || recorder.Code == http.StatusBadRequest || recorder.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("canceled response = %d %q, want no coding reason", recorder.Code, recorder.Body.String())
	}
	if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
		t.Fatal("canceled request reached upstream")
	}
}

func assertGatewayReasonTest(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, wantStatus, recorder.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode reason body: %v; body=%q", err, recorder.Body.String())
	}
	if body.Code != wantCode {
		t.Fatalf("code = %q, want %q; body=%s", body.Code, wantCode, recorder.Body.String())
	}
}

func TestHandlerRejectsOversizedRequestBody(t *testing.T) {
	if maxRequestBodyBytes != 32<<20 {
		t.Fatalf("maxRequestBodyBytes = %d, want %d", maxRequestBodyBytes, 32<<20)
	}
	forwarder := &scriptedForwarder{}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-unused")
	reader := &boundedCountingReader{remaining: maxRequestBodyBytes + 100}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", reader)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusRequestEntityTooLarge || body.Code != reasonRequestTooLarge.Code {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if reader.read != maxRequestBodyBytes+1 {
		t.Fatalf("reader consumed %d bytes, want %d", reader.read, maxRequestBodyBytes+1)
	}
	if len(forwarder.inputs)+len(forwarder.streamInputs) != 0 {
		t.Fatal("oversized request reached upstream forwarder")
	}
}

type boundedCountingReader struct {
	remaining int64
	read      int64
}

func (reader *boundedCountingReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	read := int64(len(destination))
	if read > reader.remaining {
		read = reader.remaining
	}
	for index := int64(0); index < read; index++ {
		destination[index] = 'x'
	}
	reader.remaining -= read
	reader.read += read
	return int(read), nil
}

func TestHandlerUsesStreamingForwarder(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantNormal  int
		wantStreams int
	}{
		{name: "stream absent", body: `{"model":"gpt-4o"}`, wantNormal: 1},
		{name: "stream false", body: `{"model":"gpt-4o","stream":false}`, wantNormal: 1},
		{name: "stream true", body: `{"model":"gpt-4o","stream":true}`, wantStreams: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{
				results: []UpstreamResult{{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: []byte(`{"ok":true}`), RequestWritten: true,
				}},
				streamResults: []UpstreamResult{{
					StatusCode: http.StatusOK, Committed: true, RequestWritten: true,
				}},
			}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(tt.body))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if len(forwarder.inputs) != tt.wantNormal || len(forwarder.streamInputs) != tt.wantStreams {
				t.Fatalf("normal/stream calls = %d/%d, want %d/%d", len(forwarder.inputs), len(forwarder.streamInputs), tt.wantNormal, tt.wantStreams)
			}
		})
	}
}

func TestHandlerSuccessfulStreamClearsFailureAfterCommittedReturn(t *testing.T) {
	forwarder := &scriptedForwarder{
		invokeStreamReady: true,
		streamResults: []UpstreamResult{{
			StatusCode: http.StatusOK, RequestWritten: true, Committed: true,
			Stream: StreamObservation{EndReason: StreamEndCleanEOF},
		}},
	}
	engine, _, registry := newHandlerTestRuntime(t, forwarder, "sk-one")
	_, _ = registry.IncrFailure(1)
	_, _ = registry.IncrFailure(1)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	count, ok := registry.IncrFailure(1)
	if !ok || count != 1 {
		t.Fatalf("failure count = %d, %t, want 1, true", count, ok)
	}
}

func TestHandlerDoesNotRetryOversizedResponse(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{Err: fmt.Errorf("%w: response too large", ErrUpstreamProtocol), RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
	}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway || len(forwarder.inputs) != 1 {
		t.Fatalf("response/attempts = %d/%d, body=%s", recorder.Code, len(forwarder.inputs), recorder.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != reasonUpstreamProtocol.Code {
		t.Fatalf("response = %s, error=%v", recorder.Body.String(), err)
	}
}

func TestHandlerTerminatesRequestWrittenStreamFailuresBeforeCommit(t *testing.T) {
	protocolFailure := fmt.Errorf("%w: gzip", ErrUpstreamProtocol)
	tests := []struct {
		name         string
		results      []UpstreamResult
		wantStatus   int
		wantCode     string
		wantAttempts int
	}{
		{
			name: "protocol error does not reach committed success",
			results: []UpstreamResult{
				{Err: protocolFailure, RequestWritten: true},
				{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
			},
			wantStatus: http.StatusBadGateway, wantCode: reasonUpstreamProtocol.Code, wantAttempts: 1,
		},
		{
			name: "first-event timeout does not reach committed success",
			results: []UpstreamResult{
				{Err: context.DeadlineExceeded, RequestWritten: true},
				{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
			},
			wantStatus: http.StatusGatewayTimeout, wantCode: reasonUpstreamTimeout.Code, wantAttempts: 1,
		},
		{
			name: "protocol errors exhausted",
			results: []UpstreamResult{
				{Err: protocolFailure, RequestWritten: true},
				{Err: protocolFailure, RequestWritten: true},
			},
			wantStatus: http.StatusBadGateway, wantCode: reasonUpstreamProtocol.Code, wantAttempts: 1,
		},
		{
			name: "first-event timeouts exhausted",
			results: []UpstreamResult{
				{Err: context.DeadlineExceeded, RequestWritten: true},
				{Err: context.DeadlineExceeded, RequestWritten: true},
			},
			wantStatus: http.StatusGatewayTimeout, wantCode: reasonUpstreamTimeout.Code, wantAttempts: 1,
		},
		{
			name: "transport failures exhausted",
			results: []UpstreamResult{
				{Err: errors.New("stream disconnected"), RequestWritten: true},
				{Err: errors.New("stream disconnected"), RequestWritten: true},
			},
			wantStatus: http.StatusBadGateway, wantCode: reasonUpstreamConnect.Code, wantAttempts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{streamResults: tt.results}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus || len(forwarder.streamInputs) != tt.wantAttempts {
				t.Fatalf("status/attempts = %d/%d, want %d/%d; body=%s", recorder.Code, len(forwarder.streamInputs), tt.wantStatus, tt.wantAttempts, recorder.Body.String())
			}
			if tt.wantCode != "" {
				var body struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != tt.wantCode {
					t.Fatalf("response = %s, error=%v, want code %q", recorder.Body.String(), err, tt.wantCode)
				}
			}
		})
	}
}

func TestHandlerRetriesClassifiedFirstProviderErrorAndReturns502OnExhaustion(t *testing.T) {
	const marker = "rate_limit_error"
	providerError := UpstreamResult{
		StatusCode:                http.StatusOK,
		Header:                    http.Header{"Retry-After": []string{"1"}},
		ClassificationBody:        []byte(`{"error":{"type":"` + marker + `"}}`),
		ProviderErrorBeforeCommit: true,
		RequestWritten:            true,
	}

	t.Run("retryable category changes key", func(t *testing.T) {
		forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
			providerError,
			{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-first", "sk-second")
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
		)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK || len(forwarder.streamInputs) != 2 {
			t.Fatalf("status/attempts = %d/%d; body=%s",
				recorder.Code, len(forwarder.streamInputs), recorder.Body.String())
		}
		if forwarder.streamInputs[0].APIKey == forwarder.streamInputs[1].APIKey {
			t.Fatalf("retry reused key %q", forwarder.streamInputs[0].APIKey)
		}
	})

	t.Run("candidate exhaustion returns protocol 502", func(t *testing.T) {
		forwarder := &scriptedForwarder{streamResults: []UpstreamResult{providerError}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-only")
		request := httptest.NewRequest(
			http.MethodPost,
			"/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
		)
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		var body struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v; body=%s", err, recorder.Body.String())
		}
		if recorder.Code != http.StatusBadGateway ||
			body.Code != reasonUpstreamProtocol.Code ||
			strings.Contains(recorder.Body.String(), "rate_limit_error") {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestHandlerFirstProviderErrorDoesNotCommitOrRecordSuccess(t *testing.T) {
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	forwarder := &scriptedForwarder{
		invokeStreamReady: true,
		streamResults: []UpstreamResult{withProviderErrorBeforeCommit(UpstreamResult{
			StatusCode:         http.StatusOK,
			ClassificationBody: []byte(`{"error":{"type":"unexpected_provider_error"}}`),
			ErrorSummary:       fixedErrorSummary("upstream_sse_error"),
			RequestWritten:     true,
			Usage:              usage.Result{State: usage.StateMissing},
		})},
	}
	engine, handler, registry, stats := newStatsHandlerTestRuntime(t, forwarder, "sk-only")
	handler.now = func() time.Time { return now }
	_, _ = registry.IncrFailure(1)

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway || len(forwarder.streamInputs) != 1 {
		t.Fatalf("status/attempts = %d/%d", recorder.Code, len(forwarder.streamInputs))
	}
	if got := stats.Snapshot(1, now); got != (health.CredentialStats{}) {
		t.Fatalf("stats = %#v, want no success/failure record", got)
	}
	count, ok := registry.IncrFailure(1)
	if !ok || count != 2 {
		t.Fatalf("failure count after provider error = %d/%t, want unchanged then increment to 2", count, ok)
	}
}

func TestHandlerDoesNotRetryRequestWrittenAmbiguousPreCommitFailures(t *testing.T) {
	tests := []struct {
		name   string
		result UpstreamResult
	}{
		{name: "timeout", result: UpstreamResult{Err: context.DeadlineExceeded, RequestWritten: true}},
		{name: "read error", result: UpstreamResult{Err: errors.New("read failed"), RequestWritten: true}},
		{name: "clean EOF", result: UpstreamResult{Err: errIncompleteSSEEvent, RequestWritten: true}},
		{name: "framing", result: UpstreamResult{Err: fmt.Errorf("%w: framing", ErrUpstreamProtocol), RequestWritten: true}},
		{name: "encoding", result: UpstreamResult{Err: fmt.Errorf("%w: encoding", ErrUpstreamProtocol), RequestWritten: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
				test.result,
				{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
			}}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-first", "sk-second")
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			engine.ServeHTTP(httptest.NewRecorder(), request)

			if len(forwarder.streamInputs) != 1 {
				t.Fatalf("stream attempts = %d, want 1", len(forwarder.streamInputs))
			}
		})
	}
}

func TestHandlerSkipsGroupAfterRequestNotWrittenTransportFailure(t *testing.T) {
	forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
		{Err: errors.New("dial failed"), RequestWritten: false},
		{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
	}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-first", "sk-second")
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway || len(forwarder.streamInputs) != 1 {
		t.Fatalf("status/attempts = %d/%d, body=%s",
			recorder.Code, len(forwarder.streamInputs), recorder.Body.String())
	}
}

func TestHandlerStopsAtStreamingTerminalBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		result    UpstreamResult
		writeBody bool
	}{
		{
			name: "committed disconnect",
			result: UpstreamResult{
				Err: errors.New("upstream disconnected"), RequestWritten: true, Committed: true,
			},
			writeBody: true,
		},
		{
			name: "downstream cancellation",
			result: UpstreamResult{
				Err: context.Canceled, RequestWritten: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
				tt.result,
				{StatusCode: http.StatusOK, Committed: true},
			}}
			if tt.writeBody {
				forwarder.onStreamCall = func(index int, writer http.ResponseWriter) {
					if index == 0 {
						writer.WriteHeader(http.StatusOK)
						_, _ = writer.Write([]byte("data: first\n\n"))
					}
				}
			}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if len(forwarder.streamInputs) != 1 {
				t.Fatalf("stream attempts = %d, want 1", len(forwarder.streamInputs))
			}
			if tt.writeBody && recorder.Body.String() != "data: first\n\n" {
				t.Fatalf("committed body = %q, want only first event", recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), `"code"`) {
				t.Fatalf("terminal stream appended reason: %s", recorder.Body.String())
			}
		})
	}
}

func TestHandlerDoesNotRetryDownstreamWriteDeadline(t *testing.T) {
	deadlineErr := errors.New("downstream stream write deadline exceeded")
	forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
		{
			Err: deadlineErr, RequestWritten: true,
			Committed: true,
		},
		{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
	}}
	forwarder.onStreamCall = func(index int, writer http.ResponseWriter) {
		if index == 0 {
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("data: first\n\n"))
		}
	}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if len(forwarder.streamInputs) != 1 {
		t.Fatalf("stream attempts = %d, want 1 after downstream write deadline", len(forwarder.streamInputs))
	}
	if recorder.Body.String() != "data: first\n\n" {
		t.Fatalf("committed body = %q, want only first event", recorder.Body.String())
	}
}

func TestHandlerDoesNotAdvanceCandidatesAfterRequestDeadline(t *testing.T) {
	forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
		{Err: context.DeadlineExceeded, RequestWritten: true},
		{StatusCode: http.StatusOK, RequestWritten: true, Committed: true},
	}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Millisecond)
	defer cancel()
	request = request.WithContext(ctx)
	forwarder.onStreamCall = func(_ int, _ http.ResponseWriter) {
		<-ctx.Done()
	}
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if len(forwarder.streamInputs) != 1 {
		t.Fatalf("stream attempts = %d, want 1 after downstream deadline", len(forwarder.streamInputs))
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("deadline path appended a response: %s", recorder.Body.String())
	}
}

func TestHandlerUsesClassifierForStreamingNonSuccess(t *testing.T) {
	t.Run("retry then committed success", func(t *testing.T) {
		forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
			{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true},
			{StatusCode: http.StatusOK, Committed: true, RequestWritten: true},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK || len(forwarder.streamInputs) != 2 {
			t.Fatalf("status/attempts = %d/%d, want 200/2", recorder.Code, len(forwarder.streamInputs))
		}
	})

	t.Run("last key-level retryable response is passed through", func(t *testing.T) {
		forwarder := &scriptedForwarder{streamResults: []UpstreamResult{
			{StatusCode: http.StatusUnauthorized, Header: make(http.Header),
				Body:               []byte(`{"error":"first"}`),
				ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true},
			{StatusCode: http.StatusUnauthorized, Header: make(http.Header),
				Body:               []byte(`{"error":"last"}`),
				ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o","stream":true}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized || recorder.Body.String() != `{"error":"last"}` {
			t.Fatalf("final response = %d %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestHandlerUsesClassifierForNonStreamingNonSuccess(t *testing.T) {
	t.Run("client error terminates after one attempt", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{
			{StatusCode: http.StatusBadRequest, Header: make(http.Header),
				Body:               []byte(`{"error":"invalid input"}`),
				ClassificationBody: []byte(`{"error":"invalid input"}`), RequestWritten: true},
			{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`)},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || len(forwarder.inputs) != 1 {
			t.Fatalf("status/attempts = %d/%d, want 400/1", recorder.Code, len(forwarder.inputs))
		}
	})

	t.Run("rate limit advances to a second key", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{
			{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": {"30"}},
				Body:               []byte(`{"error":"rate_limit"}`),
				ClassificationBody: []byte(`{"error":"rate_limit"}`), RequestWritten: true},
			{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
		}}
		engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
			t.Fatalf("status/attempts = %d/%d, want 200/2", recorder.Code, len(forwarder.inputs))
		}
	})
}

func TestHandlerRetriesAnotherGroupAfterLocalConversionFailure(t *testing.T) {
	t.Parallel()

	conversionFailure := UpstreamResult{
		Err:           fmt.Errorf("%w: target conversion failed", ErrUpstreamProtocol),
		DispatchState: execution.DispatchNotSent,
		ExecutionError: &execution.ErrorEvidence{
			Kind:    execution.ErrorKind("conversion_unsupported"),
			Code:    "target_conversion_not_supported",
			Summary: "target conversion is not supported",
		},
		ErrorSummary: "target conversion is not supported",
	}

	t.Run("falls back to another group without mutating credential health", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{
			conversionFailure,
			{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
		}}
		engine, registry := newConvertedFallbackHandlerTestRuntime(t, forwarder)
		before := registry.Snapshot()
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(
			`{"model":"claude-client","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
		))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK || recorder.Body.String() != `{"ok":true}` || len(forwarder.inputs) != 2 {
			t.Fatalf("response/attempts = %d %s / %d, want 200 and two groups", recorder.Code, recorder.Body.String(), len(forwarder.inputs))
		}
		if forwarder.inputs[0].Group.ID == forwarder.inputs[1].Group.ID {
			t.Fatalf("attempts stayed in group %d", forwarder.inputs[0].Group.ID)
		}
		if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
			t.Fatalf("credential health changed: before=%#v after=%#v", before, after)
		}
	})

	t.Run("returns stable 422 after every target rejects conversion", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{conversionFailure, conversionFailure}}
		engine, registry := newConvertedFallbackHandlerTestRuntime(t, forwarder)
		before := registry.Snapshot()
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(
			`{"model":"claude-client","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
		))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)

		var body struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if recorder.Code != http.StatusUnprocessableEntity || body.Code != "protocol_conversion_unsupported" || len(forwarder.inputs) != 2 {
			t.Fatalf("response/attempts = %d %s / %d", recorder.Code, recorder.Body.String(), len(forwarder.inputs))
		}
		if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
			t.Fatalf("credential health changed: before=%#v after=%#v", before, after)
		}
	})

	t.Run("malformed client request remains 400 and is not retried", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{{
			Err:           fmt.Errorf("%w: invalid request", ErrUpstreamProtocol),
			DispatchState: execution.DispatchNotSent,
			ExecutionError: &execution.ErrorEvidence{
				Kind:    execution.ErrorKindInvalidRequest,
				Summary: "invalid Anthropic request body",
			},
		}}}
		engine, _ := newConvertedFallbackHandlerTestRuntime(t, forwarder)
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(
			`{"model":"claude-client","messages":[{"role":"user","content":"hello"}]}`,
		))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest ||
			!strings.Contains(recorder.Body.String(), `"code":"invalid_protocol_request"`) ||
			len(forwarder.inputs) != 1 {
			t.Fatalf("response/attempts = %d %s / %d", recorder.Code, recorder.Body.String(), len(forwarder.inputs))
		}
	})

	t.Run("upstream unsupported model 400 is passed through once", func(t *testing.T) {
		forwarder := &scriptedForwarder{results: []UpstreamResult{{
			StatusCode:      http.StatusBadRequest,
			Header:          http.Header{"Content-Type": {"application/json"}},
			Body:            []byte(`{"error":{"code":"unsupported_model"}}`),
			RequestWritten:  true,
			DispatchState:   execution.DispatchMaybeSent,
			ResponseStarted: true,
			ExecutionError: &execution.ErrorEvidence{
				Kind:       execution.ErrorKindHTTP,
				Hint:       execution.FailureHintModelUnavailable,
				StatusCode: http.StatusBadRequest,
				Code:       "unsupported_model",
				Summary:    "request capability is unavailable",
			},
		}}}
		engine, registry := newConvertedFallbackHandlerTestRuntime(t, forwarder)
		before := registry.Snapshot()
		request := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(
			`{"model":"claude-client","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`,
		))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || recorder.Body.String() != `{"error":{"code":"unsupported_model"}}` || len(forwarder.inputs) != 1 {
			t.Fatalf("response/attempts = %d %s / %d", recorder.Code, recorder.Body.String(), len(forwarder.inputs))
		}
		if after := registry.Snapshot(); !reflect.DeepEqual(after, before) {
			t.Fatalf("credential health changed: before=%#v after=%#v", before, after)
		}
	})
}

func TestHandlerAppliesExactCooldownDeadline(t *testing.T) {
	attemptNow := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		result UpstreamResult
		want   time.Time
	}{
		{
			name: "rate limit reset",
			result: UpstreamResult{
				StatusCode:         http.StatusTooManyRequests,
				Header:             http.Header{"Retry-After": {"30"}},
				Body:               []byte(`{"error":"rate_limit"}`),
				ClassificationBody: []byte(`{"error":"rate_limit"}`),
				RequestWritten:     true,
			},
			want: attemptNow.Add(30 * time.Second),
		},
		{
			name: "fixed fallback",
			result: UpstreamResult{
				StatusCode: http.StatusTooManyRequests, Header: make(http.Header),
				Body:               []byte(`{"error":"rate_limit"}`),
				ClassificationBody: []byte(`{"error":"rate_limit"}`),
				RequestWritten:     true,
			},
			want: attemptNow.Add(time.Minute),
		},
		{
			name: "model unavailable",
			result: UpstreamResult{
				StatusCode: http.StatusNotFound, Header: make(http.Header),
				Body:               []byte(`{"error":"model_not_found"}`),
				ClassificationBody: []byte(`{"error":"model_not_found"}`),
				RequestWritten:     true,
			},
			want: attemptNow.Add(time.Hour),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{test.result}}
			handler, _, registry := newHandlerForTest(t, forwarder, "sk-one")
			recording := &recordingRuntimeRegistry{CredentialRegistry: registry}
			handler.registry = recording
			handler.now = func() time.Time { return attemptNow }
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)

			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				bytes.NewBufferString(`{"model":"gpt-4o"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recording.cooldownCalls != 1 || recording.cooldownCredentialID != 1 ||
				!recording.cooldownUntil.Equal(test.want) {
				t.Fatalf("cooldown = calls:%d key:%d until:%v, want 1/1/%v",
					recording.cooldownCalls, recording.cooldownCredentialID,
					recording.cooldownUntil, test.want)
			}
		})
	}
}

func TestHandlerCooldownExcludesKeyAcrossRequests(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			StatusCode:         http.StatusTooManyRequests,
			Header:             http.Header{"Retry-After": {"3600"}},
			Body:               []byte(`{"error":"rate_limit"}`),
			ClassificationBody: []byte(`{"error":"rate_limit"}`),
			RequestWritten:     true,
		},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
	}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")

	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
		}
	}
	if len(forwarder.inputs) != 3 ||
		forwarder.inputs[0].APIKey == forwarder.inputs[1].APIKey ||
		forwarder.inputs[1].APIKey != forwarder.inputs[2].APIKey {
		t.Fatalf("attempt keys = %#v, want cooled key then stable backup", forwarder.inputs)
	}
}

func TestHandlerBlacklistsKeyOnThirdInvalidFailure(t *testing.T) {
	invalid := UpstreamResult{
		StatusCode: http.StatusUnauthorized, Header: make(http.Header),
		Body:               []byte(`{"error":"invalid_api_key"}`),
		ClassificationBody: []byte(`{"error":"invalid_api_key"}`),
		RequestWritten:     true,
	}
	forwarder := &scriptedForwarder{results: []UpstreamResult{invalid, invalid, invalid}}
	engine, _, registry := newHandlerTestRuntime(t, forwarder, "sk-one")
	for attempt := 1; attempt <= 3; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			bytes.NewBufferString(`{"model":"gpt-4o"}`))
		request.Header.Set("Authorization", "Bearer gl-client")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d response = %d %s", attempt, recorder.Code, recorder.Body.String())
		}
		blacklisted := registry.BlacklistedCredentials()
		if (attempt < 3 && len(blacklisted) != 0) ||
			(attempt == 3 && (len(blacklisted) != 1 || blacklisted[0].ID != 1)) {
			t.Fatalf("attempt %d blacklisted = %#v", attempt, blacklisted)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable || len(forwarder.inputs) != 3 {
		t.Fatalf("post-blacklist response/attempts = %d/%d, want 503/3",
			recorder.Code, len(forwarder.inputs))
	}
}

func TestHandlerClearsFailureOnlyForNonStreamingSuccess(t *testing.T) {
	tests := []struct {
		name      string
		result    UpstreamResult
		wantCount int
	}{
		{
			name: "success clears",
			result: UpstreamResult{StatusCode: http.StatusOK, Header: make(http.Header),
				Body: []byte(`{"ok":true}`), RequestWritten: true},
			wantCount: 1,
		},
		{
			name: "client error does not clear",
			result: UpstreamResult{StatusCode: http.StatusBadRequest, Header: make(http.Header),
				Body:               []byte(`{"error":"invalid input"}`),
				ClassificationBody: []byte(`{"error":"invalid input"}`), RequestWritten: true},
			wantCount: 3,
		},
		{
			name: "two hundred with error does not clear",
			result: UpstreamResult{StatusCode: http.StatusOK,
				Err: errors.New("response failed"), RequestWritten: true},
			wantCount: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{test.result}}
			engine, _, registry := newHandlerTestRuntime(t, forwarder, "sk-one")
			_, _ = registry.IncrFailure(1)
			_, _ = registry.IncrFailure(1)

			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				bytes.NewBufferString(`{"model":"gpt-4o"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			count, ok := registry.IncrFailure(1)
			if !ok || count != test.wantCount {
				t.Fatalf("failure count = %d, %t, want %d, true", count, ok, test.wantCount)
			}
		})
	}
}

func TestHandlerDoesNotClearFailureAfterDownstreamCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	forwarder := &scriptedForwarder{
		results: []UpstreamResult{{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: []byte(`{"ok":true}`), RequestWritten: true,
		}},
		onCall: func(int) { cancel() },
	}
	engine, _, registry := newHandlerTestRuntime(t, forwarder, "sk-one")
	_, _ = registry.IncrFailure(1)
	_, _ = registry.IncrFailure(1)

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		bytes.NewBufferString(`{"model":"gpt-4o"}`)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer gl-client")
	engine.ServeHTTP(httptest.NewRecorder(), request)

	count, ok := registry.IncrFailure(1)
	if !ok || count != 3 {
		t.Fatalf("failure count = %d, %t, want 3, true", count, ok)
	}
}

func TestCrossCandidateRetryRespectsReplaySafety(t *testing.T) {
	retry := health.Result{Category: health.FailureCategoryUpstreamHostError, Action: health.ActionSkipGroup}
	authRetry := health.Result{Category: health.FailureCategoryInvalidKey, Action: health.ActionFailCredential}
	tests := []struct {
		name      string
		operation execution.Operation
		method    string
		result    UpstreamResult
		decision  health.Result
		want      bool
	}{
		{
			name: "chat 500 may have executed", operation: execution.OperationChatCompletion,
			method:   http.MethodPost,
			result:   UpstreamResult{DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusInternalServerError},
			decision: retry,
		},
		{
			name: "chat DNS failure was not sent", operation: execution.OperationChatCompletion,
			method:   http.MethodPost,
			result:   UpstreamResult{DispatchState: execution.DispatchNotSent},
			decision: retry, want: true,
		},
		{
			name: "subscription refresh rejected before model dispatch", operation: execution.OperationChatCompletion,
			method: http.MethodPost,
			result: UpstreamResult{
				DispatchState: execution.DispatchNotSent,
				ExecutionError: &execution.ErrorEvidence{
					Kind: execution.ErrorKindProvider,
					Hint: execution.FailureHintReauthorizationRequired,
				},
			},
			decision: health.Result{Category: health.FailureCategoryAuthenticationRequired, Action: health.ActionRetry},
			want:     true,
		},
		{
			name: "local conversion failure was not sent", operation: execution.OperationChatCompletion,
			method: http.MethodPost,
			result: UpstreamResult{
				DispatchState: execution.DispatchNotSent,
				ExecutionError: &execution.ErrorEvidence{
					Kind:    execution.ErrorKind("conversion_unsupported"),
					Summary: "target conversion is not supported",
				},
			},
			decision: health.Result{Category: health.FailureCategoryConversionUnsupported, Action: health.ActionSkipGroup},
			want:     true,
		},
		{
			name: "explicit credential rejection", operation: execution.OperationResponsesCreate,
			method:   http.MethodPost,
			result:   UpstreamResult{DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusUnauthorized},
			decision: authRetry, want: true,
		},
		{
			name: "subscription authorization failure with unknown replay safety", operation: execution.OperationResponsesCreate,
			method: http.MethodPost,
			result: UpstreamResult{
				DispatchState: execution.DispatchMaybeSent,
				StatusCode:    http.StatusUnauthorized,
				ExecutionError: &execution.ErrorEvidence{
					Kind:         execution.ErrorKindHTTP,
					StatusCode:   http.StatusUnauthorized,
					ReplaySafety: execution.ReplaySafetyUnknown,
					Summary:      "authorization failed",
				},
			},
			decision: authRetry,
		},
		{
			name: "retrieve is read only", operation: execution.OperationResponsesRetrieve,
			method:   http.MethodGet,
			result:   UpstreamResult{DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusInternalServerError},
			decision: retry, want: true,
		},
		{
			name: "delete is a mutation", operation: execution.OperationResponsesDelete,
			method:   http.MethodDelete,
			result:   UpstreamResult{DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusInternalServerError},
			decision: retry,
		},
		{
			name: "GET passthrough is read only", operation: execution.OperationResponsesPassthrough,
			method:   http.MethodGet,
			result:   UpstreamResult{DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusInternalServerError},
			decision: retry, want: true,
		},
		{
			name: "POST passthrough may mutate", operation: execution.OperationResponsesPassthrough,
			method:   http.MethodPost,
			result:   UpstreamResult{DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusInternalServerError},
			decision: retry,
		},
		{
			name: "downstream commit is terminal", operation: execution.OperationResponsesRetrieve,
			method:   http.MethodGet,
			result:   UpstreamResult{DispatchState: execution.DispatchMaybeSent, StatusCode: http.StatusInternalServerError, Committed: true},
			decision: retry,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldRetryAcrossCandidates(test.operation, test.method, test.result, test.decision); got != test.want {
				t.Fatalf("shouldRetryAcrossCandidates() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestSubscriptionExplicit401RetriesSameCredentialWithForcedRefresh(t *testing.T) {
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
			Body: []byte(`{"id":"ok","model":"gpt-4o"}`),
		},
	}}
	handler, manager, registry := newHandlerForTest(t, forwarder, "placeholder")
	if _, err := manager.Publish(state.CompileInput{
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
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	canonical := `{"type":"codex","access_token":"access","refresh_token":"refresh","account_id":"account-1"}`
	encrypted, err := handler.encryption.Encrypt(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1,
		Fingerprint: "subscription-account", Status: state.CredentialStatusActive,
		EncryptedValue: encrypted,
	}}); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body.String())
	}
	if forwarder.inputs[0].Credential.ID != forwarder.inputs[1].Credential.ID ||
		forwarder.inputs[0].ForceCredentialRefresh || !forwarder.inputs[1].ForceCredentialRefresh {
		t.Fatalf("inputs = %#v", forwarder.inputs)
	}
}

func TestSubscriptionExplicit401UsesNewerCredentialVersionFromConcurrentRefresh(t *testing.T) {
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
			Body: []byte(`{"id":"ok","model":"gpt-4o"}`),
		},
	}}
	handler, manager, registry := newHandlerForTest(t, forwarder, "placeholder")
	if _, err := manager.Publish(state.CompileInput{
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
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	oldCredential := `{"type":"codex","access_token":"old-access","refresh_token":"refresh","account_id":"account-1"}`
	oldEncrypted, err := handler.encryption.Encrypt(oldCredential)
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
	newCredential := `{"type":"codex","access_token":"new-access","refresh_token":"new-refresh","account_id":"account-1"}`
	newEncrypted, err := handler.encryption.Encrypt(newCredential)
	if err != nil {
		t.Fatal(err)
	}
	forwarder.onCall = func(index int) {
		if index == 0 && !registry.ReplaceCredentialSecretIfMatch(1, 1, 2, "subscription-secret-v2", newEncrypted) {
			t.Fatal("publish concurrent credential refresh")
		}
	}

	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body.String())
	}
	if forwarder.inputs[1].Credential.Version != 2 || forwarder.inputs[1].ForceCredentialRefresh ||
		string(forwarder.inputs[1].Credential.Data()) != newCredential {
		t.Fatalf("retry input = %#v credential=%s", forwarder.inputs[1], forwarder.inputs[1].Credential.Data())
	}
}

func TestSubscriptionRefreshFailureBeforeDispatchRetriesAnotherCredential(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{
			DispatchState: execution.DispatchNotSent,
			ExecutionError: &execution.ErrorEvidence{
				Kind: execution.ErrorKindProvider, Code: "refresh_rejected",
				Hint: execution.FailureHintReauthorizationRequired,
			},
		},
		{
			DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
			Body: []byte(`{"id":"ok","model":"gpt-4o"}`),
		},
	}}
	handler, manager, registry := newHandlerForTest(t, forwarder, "placeholder-1", "placeholder-2")
	credentials := []state.CredentialConfig{
		{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "subscription-account-1"},
		{ID: 2, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "subscription-account-2"},
	}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "subscription", ChannelID: channel.Codex,
			ConnectionType: "subscription", Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: credentials,
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: handler.encryption.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	entries := make([]state.CredentialEntry, 0, 2)
	for index := 1; index <= 2; index++ {
		canonical := fmt.Sprintf(`{"type":"codex","access_token":"access-%d","refresh_token":"refresh-%d","account_id":"account-%d"}`, index, index, index)
		encrypted, err := handler.encryption.Encrypt(canonical)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, state.CredentialEntry{
			ID: uint(index), GroupID: 1, Version: 1, IdentityGeneration: uint64(index),
			Fingerprint: fmt.Sprintf("subscription-account-%d", index),
			Status:      state.CredentialStatusActive, EncryptedValue: encrypted,
		})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatal(err)
	}

	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK || len(forwarder.inputs) != 2 {
		t.Fatalf("status=%d inputs=%d body=%s", response.Code, len(forwarder.inputs), response.Body.String())
	}
	if forwarder.inputs[0].Credential.ID == forwarder.inputs[1].Credential.ID {
		t.Fatalf("credential ids = %d, %d", forwarder.inputs[0].Credential.ID, forwarder.inputs[1].Credential.ID)
	}
}

func TestHandlerLeavesKeyRegistryUnchangedForNonKeyActions(t *testing.T) {
	for _, action := range []health.Action{
		health.ActionRetry,
		health.ActionSkipGroup,
		health.ActionTerminate,
		health.Action(255),
	} {
		t.Run(fmt.Sprintf("action_%d", action), func(t *testing.T) {
			recording := &recordingRuntimeRegistry{CredentialRegistry: state.NewCredentialRegistry()}
			handler := &Handler{registry: recording}
			handler.applyCredentialAction(1, health.Result{Action: action}, 0, time.Time{})
			if recording.cooldownCalls != 0 || recording.incrFailureCalls != 0 ||
				recording.blacklistCalls != 0 || recording.clearCalls != 0 {
				t.Fatalf("mutation calls = cooldown:%d failure:%d blacklist:%d clear:%d",
					recording.cooldownCalls, recording.incrFailureCalls,
					recording.blacklistCalls, recording.clearCalls)
			}
		})
	}
}

func TestHandlerReturnsStableTerminalReasons(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		accessKey    string
		body         string
		upstreamKeys []string
		results      []UpstreamResult
		wantStatus   int
		wantCode     string
		wantAttempts int
	}{
		{name: "invalid access key", path: "/v1/chat/completions", accessKey: "wrong", body: `{"model":"gpt-4o"}`, wantStatus: http.StatusUnauthorized, wantCode: "invalid_access_key"},
		{name: "malformed registered endpoint", path: "/v1beta/models/missing-action", accessKey: "gl-client", body: `{}`, wantStatus: http.StatusNotFound, wantCode: "protocol_endpoint_not_found"},
		{name: "invalid protocol request", path: "/v1/chat/completions", accessKey: "gl-client", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_protocol_request"},
		{name: "no candidate", path: "/v1/chat/completions", accessKey: "gl-client", body: `{"model":"gpt-4o"}`, wantStatus: http.StatusServiceUnavailable, wantCode: "no_available_candidate"},
		{
			name: "post-write timeout",
			path: "/v1/chat/completions", accessKey: "gl-client", body: `{"model":"gpt-4o"}`,
			upstreamKeys: []string{"sk-one"},
			results:      []UpstreamResult{{Err: context.DeadlineExceeded, RequestWritten: true}},
			wantStatus:   http.StatusGatewayTimeout, wantCode: "upstream_timeout",
		},
		{
			name: "connection failure skips only group",
			path: "/v1/chat/completions", accessKey: "gl-client", body: `{"model":"gpt-4o"}`,
			upstreamKeys: []string{"sk-one", "sk-two"},
			results:      []UpstreamResult{{Err: errors.New("dial failed")}},
			wantStatus:   http.StatusBadGateway, wantCode: "upstream_connect_failed", wantAttempts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: tt.results}
			engine, _, _ := newHandlerTestRuntime(t, forwarder, tt.upstreamKeys...)
			request := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			request.Header.Set("Authorization", "Bearer "+tt.accessKey)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.wantAttempts > 0 && len(forwarder.inputs) != tt.wantAttempts {
				t.Fatalf("attempts = %d, want %d", len(forwarder.inputs), tt.wantAttempts)
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode reason body: %v", err)
			}
			if body.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", body.Code, tt.wantCode)
			}
		})
	}
}

func TestHandlerReturnsModelRequiredByFilterForProtocolOnlyRequest(t *testing.T) {
	forwarder := &scriptedForwarder{}
	handler, manager, _ := newHandlerForTest(t, forwarder, "sk-responses")
	keyService, err := encryption.NewService("handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID:        1,
			Name:      "responses",
			ChannelID: channel.OpenAI,
			Params:    json.RawMessage(`{}`),
			Enabled:   true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID:      1,
			Name:    "client",
			KeyHash: keyService.Hash("gl-client"),
			Status:  state.AccessKeyStatusActive,
			Filters: state.FilterSet{
				Models: map[string]struct{}{"public-model": {}},
			},
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	handler.dialects = dialect.NewSet(
		dialect.NewOpenAIResponses(),
	)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)

	request := httptest.NewRequest(
		http.MethodGet,
		"/v1/responses/resp_123",
		nil,
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest ||
		!strings.Contains(
			recorder.Body.String(),
			`"code":"model_required_by_filter"`,
		) ||
		len(forwarder.inputs) != 0 {
		t.Fatalf(
			"response/attempts = %d %s / %d, want 400 reason and no forward",
			recorder.Code,
			recorder.Body.String(),
			len(forwarder.inputs),
		)
	}
}

func TestHandlerStripsDownstreamQueryCredentialBeforeForwarding(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
	}}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?key=gl-client&trace=true", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 1 {
		t.Fatalf("response/input count = %d/%d", recorder.Code, len(forwarder.inputs))
	}
	if got := forwarder.inputs[0].Request.RawQuery; got != "trace=true" {
		t.Fatalf("forward RawQuery = %q, want trace=true", got)
	}
}

func TestHandlerPreservesRawQueryBytesAfterStrippingCredential(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{{
		StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
	}}}
	engine, _, _ := newHandlerTestRuntime(t, forwarder, "sk-one")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.URL.RawQuery = "trace=first&key=gl-client&filter=%ZZ&sig=a%2Fb&z=last"
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 1 {
		t.Fatalf("response/input count = %d/%d", recorder.Code, len(forwarder.inputs))
	}
	const want = "trace=first&filter=%ZZ&sig=a%2Fb&z=last"
	if got := forwarder.inputs[0].Request.RawQuery; got != want {
		t.Fatalf("forward RawQuery = %q, want %q", got, want)
	}
}

func TestHandlerRetries401WithAnotherKeyThenReturnsSuccess(t *testing.T) {
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusUnauthorized, Fixture: "openai/401.json"},
		fakeupstream.Step{Status: http.StatusOK, Fixture: "openai/success.json"},
	)
	defer upstream.Close()

	engine := newRealGatewayEngine(t, upstream.URL, "sk-first", "sk-second")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte("chatcmpl-test")) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	requests := upstream.Requests()
	if len(requests) != 2 {
		t.Fatalf("upstream requests = %d, want 2", len(requests))
	}
	first := requests[0].Headers.Get("Authorization")
	second := requests[1].Headers.Get("Authorization")
	if first == second || first == "" || second == "" {
		t.Fatalf("upstream credentials = %q then %q, want two distinct keys", first, second)
	}
	for _, credential := range []string{first, second} {
		if strings.Contains(credential, "gl-client") {
			t.Fatalf("downstream access key reached upstream: %q", credential)
		}
	}
}

func TestHandlerReturnsLastUpstreamResponseWhenBudgetIsExhausted(t *testing.T) {
	upstream := fakeupstream.New(
		fakeupstream.Step{Status: http.StatusUnauthorized, Fixture: "openai/401.json"},
		fakeupstream.Step{Status: http.StatusTooManyRequests, Fixture: "openai/429.json"},
		fakeupstream.Step{Status: http.StatusInternalServerError, Fixture: "openai/500.json"},
	)
	defer upstream.Close()

	engine := newRealGatewayEngine(t, upstream.URL, "sk-one", "sk-two", "sk-three", "sk-unused")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("X-Api-Key", "gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || len(upstream.Requests()) != maxAttempts {
		t.Fatalf("response/attempts = %d/%d, want 500/%d", recorder.Code, len(upstream.Requests()), maxAttempts)
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("internal_error")) {
		t.Fatalf("body = %s, want final upstream fixture", recorder.Body.String())
	}
}

func TestHandlerDoesNotExposeAliasedUpstreamModelWhenRetryBudgetIsExhausted(t *testing.T) {
	const (
		externalModel = "public-model"
		upstreamModel = "provider-model"
	)
	var attempts atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Upstream-Model", upstreamModel)
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":"provider-model internal error"}`))
	}))
	defer upstream.Close()

	engine, _ := newDialectGatewayEngine(t, protocol.OpenAICompletions, externalModel,
		dialect.NewSet(dialect.NewOpenAI()),
		dialectGatewayGroup{id: 1, name: "openai-1", upstreamURL: upstream.URL,
			apiKeys: []string{"sk-one"},
			models:  []state.ModelConfig{{ID: upstreamModel, Alias: externalModel}}},
		dialectGatewayGroup{id: 2, name: "openai-2", upstreamURL: upstream.URL,
			apiKeys: []string{"sk-two"},
			models:  []state.ModelConfig{{ID: upstreamModel, Alias: externalModel}}},
		dialectGatewayGroup{id: 3, name: "openai-3", upstreamURL: upstream.URL,
			apiKeys: []string{"sk-three"},
			models:  []state.ModelConfig{{ID: upstreamModel, Alias: externalModel}}},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewBufferString(`{"model":"public-model"}`),
	)
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || attempts.Load() != 1 {
		t.Fatalf("response/attempts = %d/%d, want 500/1", recorder.Code, attempts.Load())
	}
	if strings.Contains(recorder.Body.String(), upstreamModel) ||
		!strings.Contains(recorder.Body.String(), externalModel) {
		t.Fatalf("final response body = %s", recorder.Body.String())
	}
	assertHeadersDoNotContain(t, recorder.Header(), upstreamModel)
}

func TestHandlerKeepsFrozenSnapshotAndInjectUsageSettingAcrossRetry(t *testing.T) {
	forwarder := &scriptedForwarder{results: []UpstreamResult{
		{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: []byte(`{"error":"invalid_api_key"}`), ClassificationBody: []byte(`{"error":"invalid_api_key"}`), RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
		{StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true},
	}}
	engine, manager, _ := newHandlerTestRuntime(t, forwarder, "sk-one", "sk-two")
	keyService, err := encryption.NewService("handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	forwarder.onCall = func(index int) {
		if index != 0 {
			return
		}
		if _, err := manager.Publish(state.CompileInput{
			ChannelRegistry: channel.NewRegistry(),
			Groups: []state.GroupConfig{{
				ID: 1, Name: "openai", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`), Enabled: true,
				Models:   []state.ModelConfig{{ID: "gpt-4o"}},
				Settings: config.Settings{state.SettingInjectUsageOptions: false},
			}},
			Credentials: []state.CredentialConfig{
				testCredentialConfig(1, 1),
				testCredentialConfig(2, 1),
			},
			AccessKeys: []state.AccessKeyConfig{{
				ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive,
			}},
		}); err != nil {
			t.Fatalf("Publish() during request error = %v", err)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	request.Header.Set("Authorization", "Bearer gl-client")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 ||
		!forwarder.inputs[0].Group.InjectUsageOptions || !forwarder.inputs[1].Group.InjectUsageOptions {
		t.Fatalf("response/attempts/inject settings = %d/%d/%t/%t, want 200/2/true/true", recorder.Code, len(forwarder.inputs), forwarder.inputs[0].Group.InjectUsageOptions, forwarder.inputs[1].Group.InjectUsageOptions)
	}
	if current := manager.Current(); current == nil || current.Groups[1].InjectUsageOptions {
		t.Fatalf("current snapshot = %#v, want newly published inject_usage_options=false", current)
	}

	secondRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
	secondRequest.Header.Set("Authorization", "Bearer gl-client")
	secondRecorder := httptest.NewRecorder()
	engine.ServeHTTP(secondRecorder, secondRequest)
	if secondRecorder.Code != http.StatusOK || len(forwarder.inputs) != 3 || forwarder.inputs[2].Group.InjectUsageOptions {
		t.Fatalf("new request/attempts/inject setting = %d/%d/%t, want 200/3/false", secondRecorder.Code, len(forwarder.inputs), forwarder.inputs[2].Group.InjectUsageOptions)
	}
}

func TestHandlerSkipsCandidateChangedAfterCollection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *state.CredentialRegistry, encryption.Service)
	}{
		{
			name: "key moved to another group",
			mutate: func(t *testing.T, registry *state.CredentialRegistry, keyService encryption.Service) {
				t.Helper()
				encrypted, err := keyService.Encrypt("sk-group-two")
				if err != nil {
					t.Fatalf("Encrypt(group two key) error = %v", err)
				}
				if err := registry.ReplaceCredentials([]state.CredentialEntry{{
					ID: 1, GroupID: 2, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive, EncryptedValue: encrypted,
				}}); err != nil {
					t.Fatalf("Replace(moved key) error = %v", err)
				}
			},
		},
		{
			name: "key disabled",
			mutate: func(t *testing.T, registry *state.CredentialRegistry, _ encryption.Service) {
				t.Helper()
				if err := registry.SetCredentialStatus(1, state.CredentialStatusDisabled); err != nil {
					t.Fatalf("SetCredentialStatus(disabled) error = %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{{
				StatusCode: http.StatusOK, Header: make(http.Header), Body: []byte(`{"ok":true}`), RequestWritten: true,
			}}}
			_, manager, registry := newHandlerTestRuntime(t, forwarder, "sk-group-one")
			keyService, err := encryption.NewService("handler-test-master-key")
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			runtimeRegistry := &mutatingRuntimeRegistry{
				CredentialRegistry: registry,
				mutate:             func() { tt.mutate(t, registry, keyService) },
			}
			openAI := dialect.NewOpenAI()
			handler := NewHandler(
				manager, registry, keyService, forwarder, dialect.NewSet(openAI), health.NewStatsStore(),
				health.NewMutationCoordinator(),
				nil, nil, nil,
			)
			handler.registry = runtimeRegistry
			handler.newRandom = func() *rand.Rand { return rand.New(rand.NewSource(1)) }
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)

			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"gpt-4o"}`))
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusServiceUnavailable || len(forwarder.inputs) != 0 {
				t.Fatalf("response/attempts = %d/%d, want 503/0; body=%s", recorder.Code, len(forwarder.inputs), recorder.Body.String())
			}
			var body struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != reasonNoCandidate.Code {
				t.Fatalf("response code = %q, want %q", body.Code, reasonNoCandidate.Code)
			}
		})
	}
}

func TestHandlerFreezesKeyIdentityAfterInspectingBody(t *testing.T) {
	tests := []struct {
		name         string
		seedPlain    string
		seedStatus   state.CredentialStatus
		currentPlain string
		mutate       func(*state.CredentialRegistry, string) error
	}{
		{
			name: "new import", currentPlain: "sk-imported",
			mutate: func(registry *state.CredentialRegistry, encrypted string) error {
				return registry.ApplyCredentialImport(1, []state.CredentialEntry{{
					ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
					EncryptedValue: encrypted,
				}})
			},
		},
		{
			name: "disabled becomes active", seedPlain: "sk-enabled",
			seedStatus: state.CredentialStatusDisabled, currentPlain: "sk-enabled",
			mutate: func(registry *state.CredentialRegistry, _ string) error {
				return registry.SetCredentialStatus(1, state.CredentialStatusActive)
			},
		},
		{
			name: "same ID gets new ciphertext", seedPlain: "sk-old",
			seedStatus: state.CredentialStatusActive, currentPlain: "sk-replaced",
			mutate: func(registry *state.CredentialRegistry, encrypted string) error {
				return registry.ApplyCredentialImport(1, []state.CredentialEntry{{
					ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
					EncryptedValue: encrypted,
				}})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forwarder := &scriptedForwarder{results: []UpstreamResult{
				{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: []byte(`{"ok":true}`), RequestWritten: true,
				},
				{
					StatusCode: http.StatusOK, Header: make(http.Header),
					Body: []byte(`{"ok":true}`), RequestWritten: true,
				},
			}}
			handler, _, registry := newHandlerForTest(t, forwarder)
			keyService, err := encryption.NewService("handler-test-master-key")
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			recordingEncryption := &recordingDecryptEncryption{Service: keyService}
			handler.encryption = recordingEncryption
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			currentCiphertext := encryptTestCredentialValue(t, keyService, test.currentPlain)
			if test.seedStatus != "" {
				seedCiphertext := currentCiphertext
				if test.seedPlain != test.currentPlain {
					seedCiphertext = encryptTestCredentialValue(t, keyService, test.seedPlain)
				}
				if replaceErr := registry.ReplaceCredentials([]state.CredentialEntry{{
					ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: test.seedStatus,
					EncryptedValue: seedCiphertext,
				}}); replaceErr != nil {
					t.Fatalf("Replace(seed key) error = %v", replaceErr)
				}
			}

			body := newBlockingRequestBody(
				`{"model":"gpt-4o"}`,
				func() error { return test.mutate(registry, currentCiphertext) },
			)
			request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
			request.Header.Set("Authorization", "Bearer gl-client")
			oldRecorder := httptest.NewRecorder()
			oldDone := make(chan struct{})
			go func() {
				engine.ServeHTTP(oldRecorder, request)
				close(oldDone)
			}()

			receiveTestSignal(t, body.started, "blocked request body read")
			close(body.release)
			receiveTestSignal(t, oldDone, "blocked request completion")
			if body.firstReadErr != nil {
				t.Fatalf("first Read identity mutation error = %v", body.firstReadErr)
			}

			if oldRecorder.Code != http.StatusOK || len(forwarder.inputs) != 1 {
				t.Fatalf(
					"decoded request response/attempts = %d/%d, want 200/1; body=%s",
					oldRecorder.Code,
					len(forwarder.inputs),
					oldRecorder.Body.String(),
				)
			}
			if got := forwarder.inputs[0].APIKey; got != test.currentPlain {
				t.Fatalf("decoded request API key = %q, want %q", got, test.currentPlain)
			}
			if len(recordingEncryption.ciphertexts) != 1 {
				t.Fatalf("decoded request decrypt calls = %d, want 1", len(recordingEncryption.ciphertexts))
			}

			newRequest := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewBufferString(`{"model":"gpt-4o"}`),
			)
			newRequest.Header.Set("Authorization", "Bearer gl-client")
			newRecorder := httptest.NewRecorder()
			engine.ServeHTTP(newRecorder, newRequest)

			if newRecorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
				t.Fatalf(
					"new request response/attempts = %d/%d, want 200/2; body=%s",
					newRecorder.Code,
					len(forwarder.inputs),
					newRecorder.Body.String(),
				)
			}
			if got := forwarder.inputs[1].APIKey; got != test.currentPlain {
				t.Fatalf("new request API key = %q, want %q", got, test.currentPlain)
			}
			if len(recordingEncryption.ciphertexts) != 2 {
				t.Fatalf(
					"new request decrypt calls = %d, want 2",
					len(recordingEncryption.ciphertexts),
				)
			}
		})
	}
}

func TestHandlerAllowsCapturedUnavailableIdentityAfterRecovery(t *testing.T) {
	tests := []struct {
		name            string
		makeUnavailable func(*testing.T, *state.CredentialRegistry)
		recover         func(*testing.T, *state.CredentialRegistry, string)
	}{
		{
			name: "blacklisted",
			makeUnavailable: func(t *testing.T, registry *state.CredentialRegistry) {
				t.Helper()
				if ok := registry.SetBlacklisted(3); !ok {
					t.Fatal("SetBlacklisted(3) = false")
				}
			},
			recover: func(t *testing.T, registry *state.CredentialRegistry, _ string) {
				t.Helper()
				if ok := registry.Recover(3); !ok {
					t.Fatal("Recover(3) = false")
				}
			},
		},
		{
			name: "cooldown",
			makeUnavailable: func(t *testing.T, registry *state.CredentialRegistry) {
				t.Helper()
				if ok := registry.SetCooldown(3, time.Now().Add(time.Hour)); !ok {
					t.Fatal("SetCooldown(3) = false")
				}
			},
			recover: func(t *testing.T, registry *state.CredentialRegistry, encrypted string) {
				t.Helper()
				if err := registry.ApplyCredentialImport(1, []state.CredentialEntry{{
					ID: 3, GroupID: 1, Version: 1, IdentityGeneration: 3, Fingerprint: "test-3", Status: state.CredentialStatusActive,
					EncryptedValue: encrypted,
				}}); err != nil {
					t.Fatalf("ApplyImport(recovered cooldown key) error = %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			firstForward := make(chan struct{})
			releaseForward := make(chan struct{})
			forwarder := &scriptedForwarder{
				results: []UpstreamResult{
					{
						StatusCode: http.StatusTooManyRequests, Header: make(http.Header),
						Body:               []byte(`{"error":"rate limit"}`),
						ClassificationBody: []byte(`{"error":"rate limit"}`),
						RequestWritten:     true,
					},
					{
						StatusCode: http.StatusOK, Header: make(http.Header),
						Body: []byte(`{"ok":true}`), RequestWritten: true,
					},
				},
				onCall: func(index int) {
					if index != 0 {
						return
					}
					close(firstForward)
					<-releaseForward
				},
			}
			handler, _, registry := newHandlerForTest(t, forwarder)
			handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
			engine := gin.New()
			bindGatewayRoutesForTest(t, engine, handler)
			keyService, err := encryption.NewService("handler-test-master-key")
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			encrypt := func(plaintext string) string {
				t.Helper()
				return encryptTestCredentialValue(t, keyService, plaintext)
			}
			recoverableCiphertext := encrypt("sk-recoverable")
			if err := registry.ReplaceCredentials([]state.CredentialEntry{
				{
					ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
					EncryptedValue: encrypt("sk-first"),
				},
				{
					ID: 2, GroupID: 1, Version: 1, IdentityGeneration: 2, Fingerprint: "test-2", Status: state.CredentialStatusDisabled,
					EncryptedValue: encrypt("sk-newly-enabled"),
				},
				{
					ID: 3, GroupID: 1, Version: 1, IdentityGeneration: 3, Fingerprint: "test-3", Status: state.CredentialStatusActive,
					EncryptedValue: recoverableCiphertext,
				},
			}); err != nil {
				t.Fatalf("Replace(keys) error = %v", err)
			}
			test.makeUnavailable(t, registry)

			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				bytes.NewBufferString(`{"model":"gpt-4o"}`),
			)
			request.Header.Set("Authorization", "Bearer gl-client")
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				engine.ServeHTTP(recorder, request)
				close(done)
			}()

			receiveTestSignal(t, firstForward, "first forward")
			if err := registry.SetCredentialStatus(2, state.CredentialStatusActive); err != nil {
				t.Fatalf("SetCredentialStatus(2, active) error = %v", err)
			}
			test.recover(t, registry, recoverableCiphertext)
			close(releaseForward)
			receiveTestSignal(t, done, "recovery request completion")

			if recorder.Code != http.StatusOK || len(forwarder.inputs) != 2 {
				t.Fatalf(
					"response/attempts = %d/%d, want 200/2; body=%s",
					recorder.Code,
					len(forwarder.inputs),
					recorder.Body.String(),
				)
			}
			if got := forwarder.inputs[1].APIKey; got != "sk-recoverable" {
				t.Fatalf(
					"second attempt API key = %q, want captured recovered identity",
					got,
				)
			}
		})
	}
}

func newRealGatewayEngine(t *testing.T, upstreamURL string, upstreamKeys ...string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService, err := encryption.NewService("handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	baseURL := testUpstreamBaseURL(upstreamURL, protocol.OpenAICompletions)
	channelID, params := testChannelConfig(t, protocol.OpenAICompletions, baseURL)
	credentialConfigs := make([]state.CredentialConfig, 0, len(upstreamKeys))
	for index := range upstreamKeys {
		credentialConfigs = append(credentialConfigs, testCredentialConfig(uint(index+1), 1))
	}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "openai", ChannelID: channelID, Params: params,
			Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: credentialConfigs,
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	registry := state.NewCredentialRegistry()
	entries := make([]state.CredentialEntry, 0, len(upstreamKeys))
	for index, plaintext := range upstreamKeys {
		entries = append(entries, testCredentialEntry(t, keyService, uint(index+1), 1, plaintext))
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}

	openAI := dialect.NewOpenAI()
	handler := NewHandler(
		manager,
		registry,
		keyService,
		newTestExecutionForwarder(t),
		dialect.NewSet(openAI),
		health.NewStatsStore(),
		health.NewMutationCoordinator(),
		nil,
		nil,
		nil,
	)
	handler.newRandom = func() *rand.Rand { return rand.New(rand.NewSource(1)) }
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine
}

func testDialectClientConfig() *platformhttp.Config {
	return &platformhttp.Config{
		ConnectTimeout: time.Second, ResponseHeaderTimeout: time.Second,
		IdleConnTimeout: time.Second, MaxIdleConns: 10, MaxIdleConnsPerHost: 10,
		DisableCompression: true, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: time.Second, ExpectContinueTimeout: time.Second,
	}
}

func newHandlerTestRuntime(
	t *testing.T,
	forwarder AttemptForwarder,
	upstreamKeys ...string,
) (*gin.Engine, *state.Manager, *state.CredentialRegistry) {
	t.Helper()
	handler, manager, registry := newHandlerForTest(t, forwarder, upstreamKeys...)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine, manager, registry
}

func newStatsHandlerTestRuntime(
	t *testing.T,
	forwarder AttemptForwarder,
	upstreamKeys ...string,
) (*gin.Engine, *Handler, *state.CredentialRegistry, *health.StatsStore) {
	t.Helper()
	stats := health.NewStatsStore()
	handler, _, registry := newHandlerForTestWithStats(t, forwarder, stats, upstreamKeys...)
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine, handler, registry, stats
}

func newHandlerForTest(
	t *testing.T,
	forwarder AttemptForwarder,
	upstreamKeys ...string,
) (*Handler, *state.Manager, *state.CredentialRegistry) {
	return newHandlerForTestWithStats(t, forwarder, health.NewStatsStore(), upstreamKeys...)
}

func newHandlerForTestWithStats(
	t *testing.T,
	forwarder AttemptForwarder,
	stats *health.StatsStore,
	upstreamKeys ...string,
) (*Handler, *state.Manager, *state.CredentialRegistry) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService, err := encryption.NewService("handler-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager := state.NewManager()
	credentialConfigs := make([]state.CredentialConfig, 0, len(upstreamKeys))
	for index := range upstreamKeys {
		credentialConfigs = append(credentialConfigs, state.CredentialConfig{
			ID:                 uint(index + 1),
			GroupID:            1,
			Status:             state.CredentialStatusActive,
			Version:            1,
			IdentityGeneration: uint64(index + 1),
			Fingerprint:        fmt.Sprintf("credential-%d", index+1),
		})
	}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channel.NewRegistry(),
		Groups: []state.GroupConfig{{
			ID: 1, Name: "openai", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "gpt-4o"}}, Enabled: true,
		}},
		Credentials: credentialConfigs,
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"),
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	registry := state.NewCredentialRegistry()
	entries := make([]state.CredentialEntry, 0, len(upstreamKeys))
	for index, plaintext := range upstreamKeys {
		credential, err := json.Marshal(map[string]string{"api_key": plaintext})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		encrypted, err := keyService.Encrypt(string(credential))
		if err != nil {
			t.Fatalf("Encrypt() error = %v", err)
		}
		entries = append(entries, state.CredentialEntry{
			ID: uint(index + 1), GroupID: 1,
			Version: 1, IdentityGeneration: uint64(index + 1),
			Fingerprint: fmt.Sprintf("credential-%d", index+1),
			Status:      state.CredentialStatusActive, EncryptedValue: encrypted,
		})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}

	openAI := dialect.NewOpenAI()
	handler := NewHandler(
		manager, registry, keyService, forwarder, dialect.NewSet(openAI), stats,
		health.NewMutationCoordinator(),
		nil, nil, nil,
	)
	handler.newRandom = func() *rand.Rand { return rand.New(rand.NewSource(1)) }
	return handler, manager, registry
}

func newConvertedFallbackHandlerTestRuntime(
	t *testing.T,
	forwarder AttemptForwarder,
) (*gin.Engine, *state.CredentialRegistry) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	keyService, err := encryption.NewService("handler-conversion-fallback-test-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	channelRegistry := channel.NewRegistry()
	manager := state.NewManager()
	groups := []state.GroupConfig{
		{
			ID: 1, Name: "compatible-one", ChannelID: channel.OpenAICompatible,
			Params: json.RawMessage(`{"base_url":"https://one.example/v1"}`), Enabled: true,
			Models: []state.ModelConfig{{ID: "upstream-one", Alias: "claude-client"}},
		},
		{
			ID: 2, Name: "compatible-two", ChannelID: channel.OpenAICompatible,
			Params: json.RawMessage(`{"base_url":"https://two.example/v1"}`), Enabled: true,
			Models: []state.ModelConfig{{ID: "upstream-two", Alias: "claude-client"}},
		},
	}
	credentials := []state.CredentialConfig{
		{ID: 1, GroupID: 1, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 1, Fingerprint: "credential-one"},
		{ID: 2, GroupID: 2, Status: state.CredentialStatusActive, Version: 1, IdentityGeneration: 2, Fingerprint: "credential-two"},
	}
	if _, err := manager.Publish(state.CompileInput{
		ChannelRegistry: channelRegistry,
		Groups:          groups,
		Credentials:     credentials,
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: keyService.Hash("gl-client"), Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	registry := state.NewCredentialRegistry()
	entries := make([]state.CredentialEntry, 0, len(credentials))
	for _, credential := range credentials {
		plaintext := fmt.Sprintf(`{"api_key":"sk-%d"}`, credential.ID)
		encrypted, err := keyService.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("Encrypt() error = %v", err)
		}
		entries = append(entries, state.CredentialEntry{
			ID: credential.ID, GroupID: credential.GroupID, Status: credential.Status,
			Version: credential.Version, IdentityGeneration: credential.IdentityGeneration,
			Fingerprint: credential.Fingerprint, EncryptedValue: encrypted,
		})
	}
	if err := registry.ReplaceCredentials(entries); err != nil {
		t.Fatalf("ReplaceCredentials() error = %v", err)
	}
	handler := NewHandler(
		manager, registry, keyService, forwarder, dialect.NewSet(dialect.NewAnthropic()),
		health.NewStatsStore(), health.NewMutationCoordinator(),
		nil, nil, nil,
	)
	handler.channels = channelRegistry
	handler.newRandom = func() *rand.Rand { return rand.New(zeroSource{}) }
	engine := gin.New()
	bindGatewayRoutesForTest(t, engine, handler)
	return engine, registry
}
