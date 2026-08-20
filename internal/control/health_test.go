package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
)

func healthNow() time.Time {
	return time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
}

func encryptHealthKey(t *testing.T, fixture serviceFixture, plaintext string) string {
	t.Helper()
	credential := plaintext
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(plaintext), &object); err != nil || object == nil {
		encoded, marshalErr := json.Marshal(map[string]string{"api_key": plaintext})
		if marshalErr != nil {
			t.Fatalf("Marshal credential error = %v", marshalErr)
		}
		credential = string(encoded)
	}
	ciphertext, err := fixture.encryption.Encrypt(credential)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	return ciphertext
}

func TestRuntimeHealthReturnsMutuallyExclusiveCurrentState(t *testing.T) {
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	cooldownPlaintext := "rate-limit-secret-safe"
	blacklistedPlaintext := "invalid-key-secret-lock"
	zero := 0
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{
			{ConnectionType: "api_key", ID: 1, Name: "active", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
			{ConnectionType: "api_key", ID: 2, Name: "disabled", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: false,
			},
			{ConnectionType: "api_key", ID: 3, Name: "zero-weight", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models:       []state.ModelConfig{{ID: "model"}},
				WeightManual: &zero, Enabled: true,
			},
			{ConnectionType: "api_key", ID: 4, Name: "empty", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	keyWeightZero := 0
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
		{ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "test-11", Status: state.CredentialStatusActive, EncryptedValue: "available"},
		{
			ID: 12, GroupID: 1, Version: 1, IdentityGeneration: 12, Fingerprint: "test-12", Status: state.CredentialStatusActive,
			CooldownUntil: now.Add(time.Minute), FailureCount: 1,
			EncryptedValue: encryptHealthKey(t, fixture, cooldownPlaintext),
		},
		{
			ID: 13, GroupID: 1, Version: 1, IdentityGeneration: 13, Fingerprint: "test-13", Status: state.CredentialStatusActive,
			Blacklisted: true, CooldownUntil: now.Add(time.Hour),
			FailureCount:   3,
			EncryptedValue: encryptHealthKey(t, fixture, blacklistedPlaintext),
		},
		{
			ID: 14, GroupID: 1, Version: 1, IdentityGeneration: 14, Fingerprint: "test-14", Status: state.CredentialStatusDisabled,
			Blacklisted: true, EncryptedValue: "disabled",
		},
		{
			ID: 15, GroupID: 1, Version: 1, IdentityGeneration: 15, Fingerprint: "test-15", Status: state.CredentialStatusActive,
			WeightManual: &keyWeightZero, Blacklisted: true,
			EncryptedValue: "weight-zero",
		},
		{ID: 21, GroupID: 2, Version: 1, IdentityGeneration: 21, Fingerprint: "test-21", Status: state.CredentialStatusActive, EncryptedValue: "disabled-group"},
		{ID: 31, GroupID: 3, Version: 1, IdentityGeneration: 31, Fingerprint: "test-31", Status: state.CredentialStatusActive, EncryptedValue: "zero-group"},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	fixture.stats.RecordSuccess(12, now)
	fixture.stats.RecordProblem(12, health.FailureCategoryRateLimited, 429, now)
	fixture.stats.RecordFailure(13, health.FailureCategoryInvalidKey, 401, now)
	fixture.requestLogStats.value = requestlog.Stats{
		EnqueuedTotal: 100, PersistedTotal: 98,
		DroppedQueueFullTotal: 1, DroppedPersistFailedTotal: 1,
		DroppedTotal: 2, WriteFailureTotal: 1,
		AccessQuotaCheckpointWriteFailureTotal: 2,
		QueueDepth:                             2, QueueCapacity: 4096,
		LastWriteFailureAt:                      now.Add(-time.Minute),
		LastAccessQuotaCheckpointWriteFailureAt: now.Add(-2 * time.Minute),
	}

	got, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}
	if got.ObservedAtMS != now.UnixMilli() || got.SnapshotRevision != fixture.manager.Current().Revision ||
		got.StatsWindowSeconds != 300 {
		t.Fatalf("observation metadata = %#v", got)
	}
	if got.RequestLog.AccessQuotaCheckpointWriteFailureTotal != 2 ||
		got.RequestLog.LastAccessQuotaCheckpointWriteFailureAtMS == nil ||
		*got.RequestLog.LastAccessQuotaCheckpointWriteFailureAtMS != now.Add(-2*time.Minute).UnixMilli() {
		t.Fatalf("access quota checkpoint health = %#v", got.RequestLog)
	}
	wantCounts := healthCountsResponse{
		Credentials: 3, Available: 1, Cooldown: 1, Blacklisted: 1,
	}
	if got.Counts != wantCounts {
		t.Fatalf("global counts = %#v, want %#v", got.Counts, wantCounts)
	}
	if len(got.Groups) != 4 || got.Groups[0].ID != 1 ||
		got.Groups[1].ID != 2 || got.Groups[2].ID != 3 || got.Groups[3].ID != 4 {
		t.Fatalf("group order = %#v", got.Groups)
	}
	if got.Groups[0].Counts != (healthCountsResponse{
		Credentials: 3, Available: 1, Cooldown: 1, Blacklisted: 1,
	}) {
		t.Fatalf("active group counts = %#v", got.Groups[0].Counts)
	}
	if got.Groups[1].Counts.Credentials != 0 || got.Groups[2].Counts.Credentials != 0 ||
		got.Groups[3].Counts.Credentials != 0 {
		t.Fatalf("disabled/zero/empty group counts = %#v", got.Groups)
	}
	if len(got.CooldownCredentials) != 1 || got.CooldownCredentials[0].CredentialID != 12 ||
		got.CooldownCredentials[0].Mask != "rate****safe" ||
		got.CooldownCredentials[0].LastFailureCategory != "rate_limited" ||
		got.CooldownCredentials[0].LastStatusCode == nil ||
		*got.CooldownCredentials[0].LastStatusCode != 429 ||
		got.CooldownCredentials[0].RecentSuccessCount != 1 ||
		got.CooldownCredentials[0].RecentProblemCount != 1 ||
		got.CooldownCredentials[0].ConsecutiveProblemCount != 1 ||
		got.CooldownCredentials[0].Recovery.Mode != "cooldown_expiry" {
		t.Fatalf("cooldown details = %#v", got.CooldownCredentials)
	}
	if len(got.BlacklistedCredentials) != 1 || got.BlacklistedCredentials[0].CredentialID != 13 ||
		got.BlacklistedCredentials[0].Mask != "inva****lock" ||
		got.BlacklistedCredentials[0].LastFailureCategory != "invalid_key" ||
		got.BlacklistedCredentials[0].LastStatusCode == nil ||
		*got.BlacklistedCredentials[0].LastStatusCode != 401 ||
		got.BlacklistedCredentials[0].ConsecutiveProblemCount != 1 ||
		got.BlacklistedCredentials[0].Recovery.Mode != "validation_probe" ||
		got.BlacklistedCredentials[0].Recovery.AtMS != nil {
		t.Fatalf("blacklisted details = %#v", got.BlacklistedCredentials)
	}
	if got.RequestLog.DroppedTotal != 2 ||
		got.RequestLog.LastWriteFailureAtMS == nil ||
		*got.RequestLog.LastWriteFailureAtMS != now.Add(-time.Minute).UnixMilli() ||
		got.RequestLog.LastRetentionFailureAtMS != nil {
		t.Fatalf("request log stats = %#v", got.RequestLog)
	}
}

func TestRuntimeHealthAdvertisesExecutorValidationForChannelCredential(t *testing.T) {
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "channel", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "test-11", Status: state.CredentialStatusActive, Blacklisted: true,
		EncryptedValue: encryptHealthKey(t, fixture, `{"api_key":"blacklisted-channel-credential"}`),
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	fixture.stats.RecordFailure(11, health.FailureCategoryInvalidKey, 401, now)

	got, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}
	if len(got.BlacklistedCredentials) != 1 {
		t.Fatalf("blacklisted keys = %#v", got.BlacklistedCredentials)
	}
	recovery := got.BlacklistedCredentials[0].Recovery
	if !recovery.Automatic || recovery.Mode != "validation_probe" || recovery.AtMS != nil {
		t.Fatalf("recovery = %#v, want automatic validation probe", recovery)
	}
}

func TestRuntimeHealthExposesProblemCountsInsteadOfFailureAliases(t *testing.T) {
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	if _, err := fixture.manager.Publish(state.CompileInput{ChannelRegistry: fixture.channelRegistry, Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "active", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
		Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
	}}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "test-11", Status: state.CredentialStatusActive,
		CooldownUntil:  now.Add(time.Minute),
		EncryptedValue: encryptHealthKey(t, fixture, "rate-limit-secret-safe"),
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	fixture.stats.RecordSuccess(11, now.Add(-2*time.Minute))
	fixture.stats.RecordProblem(11, health.FailureCategoryRateLimited, 429, now.Add(-time.Minute))
	fixture.stats.RecordFailure(11, health.FailureCategoryInvalidKey, 401, now)

	result, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var document struct {
		CooldownCredentials []map[string]json.RawMessage `json:"cooldown_credentials"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(document.CooldownCredentials) != 1 {
		t.Fatalf("cooldown key count = %d, want 1", len(document.CooldownCredentials))
	}
	key := document.CooldownCredentials[0]
	for field, want := range map[string]uint64{
		"recent_success_count":      1,
		"recent_problem_count":      2,
		"consecutive_problem_count": 2,
	} {
		var got uint64
		if err := json.Unmarshal(key[field], &got); err != nil {
			t.Fatalf("decode %s: %v", field, err)
		}
		if got != want {
			t.Fatalf("%s = %d, want %d", field, got, want)
		}
	}
	for _, obsolete := range []string{"recent_failure_count", "consecutive_failure_count"} {
		if _, exists := key[obsolete]; exists {
			t.Fatalf("health response still exposes obsolete field %q", obsolete)
		}
	}
}

func TestRuntimeHealthSortsProblemKeysByGroupAndKey(t *testing.T) {
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{
			{ConnectionType: "api_key", ID: 2, Name: "two", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
			{ConnectionType: "api_key", ID: 1, Name: "one", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
		{
			ID: 22, GroupID: 2, Version: 1, IdentityGeneration: 22, Fingerprint: "test-22", Status: state.CredentialStatusActive,
			CooldownUntil:  now.Add(time.Minute),
			EncryptedValue: encryptHealthKey(t, fixture, "cooldown-secret-0022"),
		},
		{
			ID: 13, GroupID: 1, Version: 1, IdentityGeneration: 13, Fingerprint: "test-13", Status: state.CredentialStatusActive,
			Blacklisted:    true,
			EncryptedValue: encryptHealthKey(t, fixture, "blacklisted-secret-0013"),
		},
		{
			ID: 12, GroupID: 1, Version: 1, IdentityGeneration: 12, Fingerprint: "test-12", Status: state.CredentialStatusActive,
			CooldownUntil:  now.Add(time.Minute),
			EncryptedValue: encryptHealthKey(t, fixture, "cooldown-secret-0012"),
		},
		{
			ID: 21, GroupID: 2, Version: 1, IdentityGeneration: 21, Fingerprint: "test-21", Status: state.CredentialStatusActive,
			Blacklisted:    true,
			EncryptedValue: encryptHealthKey(t, fixture, "blacklisted-secret-0021"),
		},
		{
			ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "test-11", Status: state.CredentialStatusActive,
			CooldownUntil:  now.Add(time.Minute),
			EncryptedValue: encryptHealthKey(t, fixture, "cooldown-secret-0011"),
		},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	got, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}
	pairs := func(items []healthProblemCredentialResponse) [][2]uint {
		result := make([][2]uint, 0, len(items))
		for _, item := range items {
			result = append(result, [2]uint{item.GroupID, item.CredentialID})
		}
		return result
	}
	if gotPairs, want := pairs(got.CooldownCredentials), [][2]uint{
		{1, 11}, {1, 12}, {2, 22},
	}; !reflect.DeepEqual(gotPairs, want) {
		t.Fatalf("cooldown order = %v, want %v", gotPairs, want)
	}
	if gotPairs, want := pairs(got.BlacklistedCredentials), [][2]uint{
		{1, 13}, {2, 21},
	}; !reflect.DeepEqual(gotPairs, want) {
		t.Fatalf("blacklisted order = %v, want %v", gotPairs, want)
	}
}

func TestRuntimeHealthJSONOmitsScoresCredentialsAndZeroTimes(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.now = healthNow
	plaintext := "provider-secret-credential-tail"
	ciphertext := encryptHealthKey(t, fixture, plaintext)
	if _, err := fixture.manager.Publish(state.CompileInput{ChannelRegistry: fixture.channelRegistry, Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "safe", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
		Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
	}}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
		Blacklisted: true, EncryptedValue: ciphertext,
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	beforeSnapshot := fixture.manager.Current()
	beforeKeys := fixture.registry.Snapshot()
	beforeStats := fixture.stats.Snapshot(1, healthNow())

	result, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}
	if len(result.BlacklistedCredentials) != 1 ||
		result.BlacklistedCredentials[0].Mask != "prov****tail" ||
		result.BlacklistedCredentials[0].LastFailureCategory != "ambiguous" ||
		result.BlacklistedCredentials[0].LastStatusCode != nil {
		t.Fatalf("blacklisted details = %#v", result.BlacklistedCredentials)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var document struct {
		RequestLog map[string]json.RawMessage `json:"request_log"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	requestLogFields := make(map[string]struct{}, len(document.RequestLog))
	for name := range document.RequestLog {
		requestLogFields[name] = struct{}{}
	}
	wantRequestLogFields := map[string]struct{}{
		"enqueued_total":                                   {},
		"persisted_total":                                  {},
		"dropped_not_running_total":                        {},
		"dropped_queue_full_total":                         {},
		"dropped_stopping_total":                           {},
		"dropped_persist_failed_total":                     {},
		"dropped_shutdown_total":                           {},
		"dropped_total":                                    {},
		"write_failure_total":                              {},
		"access_quota_checkpoint_write_failure_total":      {},
		"retention_delete_failure_total":                   {},
		"queue_depth":                                      {},
		"queue_capacity":                                   {},
		"last_write_failure_at_ms":                         {},
		"last_access_quota_checkpoint_write_failure_at_ms": {},
		"last_retention_failure_at_ms":                     {},
	}
	if !reflect.DeepEqual(requestLogFields, wantRequestLogFields) {
		t.Fatalf("request_log fields = %#v, want %#v", requestLogFields, wantRequestLogFields)
	}

	lower := strings.ToLower(string(body))
	for _, forbidden := range []string{
		strings.ToLower(plaintext), strings.ToLower(ciphertext),
		"encrypted", "hash", "header_rules",
		"percentage", "success_rate", "score", "average_latency",
		"0001-01-01",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("health JSON exposes %q: %s", forbidden, body)
		}
	}
	if fixture.manager.Current() != beforeSnapshot ||
		!reflect.DeepEqual(fixture.registry.Snapshot(), beforeKeys) ||
		fixture.stats.Snapshot(1, healthNow()) != beforeStats {
		t.Fatal("RuntimeHealth() mutated runtime state")
	}
}

func TestRuntimeHealthFailsClosedWhenProblemKeyCannotBeDecrypted(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.now = healthNow
	if _, err := fixture.manager.Publish(state.CompileInput{ChannelRegistry: fixture.channelRegistry, Groups: []state.GroupConfig{{ConnectionType: "api_key", ID: 1, Name: "safe", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
		Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
	}}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
		Blacklisted: true, EncryptedValue: "not-a-valid-ciphertext",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}

	if _, err := fixture.service.RuntimeHealth(); !errors.Is(
		err,
		app_errors.ErrInternalServer,
	) {
		t.Fatalf("RuntimeHealth() error = %v, want INTERNAL_SERVER_ERROR", err)
	}
}

func TestHealthProblemMaskFailsClosedWhenCiphertextIsMissing(t *testing.T) {
	fixture := newServiceFixture(t)
	if _, err := fixture.service.healthProblemMask(nil, 1, "", "api_key"); !errors.Is(
		err,
		app_errors.ErrInternalServer,
	) {
		t.Fatalf("healthProblemMask() error = %v, want INTERNAL_SERVER_ERROR", err)
	}
}

func TestHealthProblemMaskExtractsTypedCredentialSecret(t *testing.T) {
	fixture := newServiceFixture(t)
	ciphertext := encryptHealthKey(
		t,
		fixture,
		`{"api_key":"provider-secret-credential-tail"}`,
	)
	mask, err := fixture.service.healthProblemMask(
		map[uint]string{1: ciphertext},
		1,
		channel.OpenAI,
		"api_key",
	)
	if err != nil {
		t.Fatalf("healthProblemMask() error = %v", err)
	}
	if mask != "prov****tail" || strings.Contains(mask, "api_key") || strings.Contains(mask, "{") {
		t.Fatalf("healthProblemMask() = %q, want api_key-only mask", mask)
	}
}

func TestHealthProblemMaskUsesSafeSubscriptionAccountIdentity(t *testing.T) {
	fixture := newServiceFixture(t)
	ciphertext := encryptHealthKey(t, fixture,
		`{"type":"codex","access_token":"access-secret","refresh_token":"refresh-secret","account_id":"account-one","email":"owner@example.com"}`)
	mask, err := fixture.service.healthProblemMask(map[uint]string{1: ciphertext}, 1, channel.Codex, "subscription")
	if err != nil {
		t.Fatal(err)
	}
	if mask != "o***r@example.com" || strings.Contains(mask, "secret") {
		t.Fatalf("mask = %q", mask)
	}
}

func TestRuntimeHealthDTOHasNoCredentialOrScoreFields(t *testing.T) {
	forbidden := map[string]struct{}{
		"EncryptedValue": {}, "KeyHash": {}, "AccessKey": {},
		"HeaderRules": {}, "Percentage": {}, "SuccessRate": {}, "Score": {},
	}
	types := []reflect.Type{
		reflect.TypeOf(runtimeHealthResponse{}),
		reflect.TypeOf(healthCountsResponse{}),
		reflect.TypeOf(healthGroupResponse{}),
		reflect.TypeOf(healthProblemCredentialResponse{}),
		reflect.TypeOf(healthRecoveryResponse{}),
		reflect.TypeOf(requestLogHealthResponse{}),
	}
	for _, typ := range types {
		for index := 0; index < typ.NumField(); index++ {
			name := typ.Field(index).Name
			if _, denied := forbidden[name]; denied {
				t.Fatalf("%s exposes forbidden field %s", typ.Name(), name)
			}
		}
	}
}

func TestRuntimeHealthFailsLoudForRegistryCatalogMismatch(t *testing.T) {
	fixture := newServiceFixture(t)
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 999, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
		EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if _, err := fixture.service.RuntimeHealth(); !errors.Is(
		err,
		app_errors.ErrInternalServer,
	) {
		t.Fatalf("RuntimeHealth() error = %v, want INTERNAL_SERVER_ERROR", err)
	}
}

func TestRuntimeHealthFailsWhenSnapshotIsUninitialized(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.manager = state.NewManager()
	if _, err := fixture.service.RuntimeHealth(); !errors.Is(
		err,
		app_errors.ErrInternalServer,
	) {
		t.Fatalf("RuntimeHealth() error = %v, want INTERNAL_SERVER_ERROR", err)
	}
}

func TestRuntimeHealthEndpointRequiresManagementAuthentication(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.now = healthNow
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
	}

	authenticated := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	authenticated.Header.Set("Authorization", "Bearer test-auth-key")
	success := httptest.NewRecorder()
	engine.ServeHTTP(success, authenticated)
	if success.Code != http.StatusOK {
		t.Fatalf("authenticated response = %d %s, want 200", success.Code, success.Body.String())
	}
	var envelope struct {
		Code int                   `json:"code"`
		Data runtimeHealthResponse `json:"data"`
	}
	if err := json.Unmarshal(success.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode authenticated response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.ObservedAtMS != healthNow().UnixMilli() ||
		envelope.Data.StatsWindowSeconds != 300 {
		t.Fatalf("authenticated envelope = %#v", envelope)
	}
	body := success.Body.String()
	for _, emptyArray := range []string{
		`"groups":[]`, `"cooldown_credentials":[]`, `"blacklisted_credentials":[]`,
	} {
		if !strings.Contains(body, emptyArray) {
			t.Fatalf("response must contain %s: %s", emptyArray, body)
		}
	}
}
