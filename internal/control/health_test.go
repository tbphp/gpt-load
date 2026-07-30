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

	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/state"
)

func healthNow() time.Time {
	return time.Date(2026, time.July, 24, 10, 0, 0, 0, time.UTC)
}

func encryptHealthKey(t *testing.T, fixture serviceFixture, plaintext string) string {
	t.Helper()
	ciphertext, err := fixture.encryption.Encrypt(plaintext)
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
		Groups: []state.GroupConfig{
			{
				ID: 1, Name: "active", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
			{
				ID: 2, Name: "disabled", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: false,
			},
			{
				ID: 3, Name: "zero-weight", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models:       []state.ModelConfig{{ID: "model"}},
				WeightManual: &zero, Enabled: true,
			},
			{
				ID: 4, Name: "empty", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	keyWeightZero := 0
	if err := fixture.registry.Replace([]state.KeyEntry{
		{ID: 11, GroupID: 1, Status: state.KeyStatusActive, EncryptedValue: "available"},
		{
			ID: 12, GroupID: 1, Status: state.KeyStatusActive,
			CooldownUntil: now.Add(time.Minute), FailureCount: 1,
			EncryptedValue: encryptHealthKey(t, fixture, cooldownPlaintext),
		},
		{
			ID: 13, GroupID: 1, Status: state.KeyStatusActive,
			Blacklisted: true, CooldownUntil: now.Add(time.Hour),
			FailureCount:   3,
			EncryptedValue: encryptHealthKey(t, fixture, blacklistedPlaintext),
		},
		{
			ID: 14, GroupID: 1, Status: state.KeyStatusDisabled,
			Blacklisted: true, EncryptedValue: "disabled",
		},
		{
			ID: 15, GroupID: 1, Status: state.KeyStatusActive,
			WeightManual: &keyWeightZero, Blacklisted: true,
			EncryptedValue: "weight-zero",
		},
		{ID: 21, GroupID: 2, Status: state.KeyStatusActive, EncryptedValue: "disabled-group"},
		{ID: 31, GroupID: 3, Status: state.KeyStatusActive, EncryptedValue: "zero-group"},
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
		QueueDepth: 2, QueueCapacity: 4096,
		LastWriteFailureAt: now.Add(-time.Minute),
	}

	got, err := fixture.service.RuntimeHealth()
	if err != nil {
		t.Fatalf("RuntimeHealth() error = %v", err)
	}
	if !got.ObservedAt.Equal(now) || got.SnapshotRevision != fixture.manager.Current().Revision ||
		got.StatsWindowSeconds != 300 {
		t.Fatalf("observation metadata = %#v", got)
	}
	wantCounts := healthCountsResponse{
		Total: 7, Available: 1, Cooldown: 1, Blacklisted: 1, Disabled: 4,
	}
	if got.Counts != wantCounts {
		t.Fatalf("global counts = %#v, want %#v", got.Counts, wantCounts)
	}
	if len(got.Groups) != 4 || got.Groups[0].ID != 1 ||
		got.Groups[1].ID != 2 || got.Groups[2].ID != 3 || got.Groups[3].ID != 4 {
		t.Fatalf("group order = %#v", got.Groups)
	}
	if got.Groups[0].Counts != (healthCountsResponse{
		Total: 5, Available: 1, Cooldown: 1, Blacklisted: 1, Disabled: 2,
	}) {
		t.Fatalf("active group counts = %#v", got.Groups[0].Counts)
	}
	if got.Groups[1].Counts.Disabled != 1 || got.Groups[2].Counts.Disabled != 1 ||
		got.Groups[3].Counts.Total != 0 {
		t.Fatalf("disabled/zero/empty group counts = %#v", got.Groups)
	}
	if len(got.CooldownKeys) != 1 || got.CooldownKeys[0].KeyID != 12 ||
		got.CooldownKeys[0].Mask != "rate****safe" ||
		got.CooldownKeys[0].LastFailureCategory != "rate_limited" ||
		got.CooldownKeys[0].LastStatusCode == nil ||
		*got.CooldownKeys[0].LastStatusCode != 429 ||
		got.CooldownKeys[0].RecentSuccessCount != 1 ||
		got.CooldownKeys[0].RecentFailureCount != 0 ||
		got.CooldownKeys[0].ConsecutiveFailureCount != 1 ||
		got.CooldownKeys[0].Recovery.Mode != "cooldown_expiry" {
		t.Fatalf("cooldown details = %#v", got.CooldownKeys)
	}
	if len(got.BlacklistedKeys) != 1 || got.BlacklistedKeys[0].KeyID != 13 ||
		got.BlacklistedKeys[0].Mask != "inva****lock" ||
		got.BlacklistedKeys[0].LastFailureCategory != "invalid_key" ||
		got.BlacklistedKeys[0].LastStatusCode == nil ||
		*got.BlacklistedKeys[0].LastStatusCode != 401 ||
		got.BlacklistedKeys[0].ConsecutiveFailureCount != 1 ||
		got.BlacklistedKeys[0].Recovery.Mode != "validation_probe" ||
		got.BlacklistedKeys[0].Recovery.At != nil {
		t.Fatalf("blacklisted details = %#v", got.BlacklistedKeys)
	}
	if got.RequestLog.DroppedTotal != 2 ||
		got.RequestLog.LastWriteFailureAt == nil ||
		got.RequestLog.LastRetentionFailureAt != nil {
		t.Fatalf("request log stats = %#v", got.RequestLog)
	}
}

func TestRuntimeHealthSortsProblemKeysByGroupAndKey(t *testing.T) {
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	if _, err := fixture.manager.Publish(state.CompileInput{
		Groups: []state.GroupConfig{
			{
				ID: 2, Name: "two", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
			{
				ID: 1, Name: "one", Protocols: []protocol.Protocol{protocol.OpenAI},
				Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
			},
		},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.Replace([]state.KeyEntry{
		{
			ID: 22, GroupID: 2, Status: state.KeyStatusActive,
			CooldownUntil:  now.Add(time.Minute),
			EncryptedValue: encryptHealthKey(t, fixture, "cooldown-secret-0022"),
		},
		{
			ID: 13, GroupID: 1, Status: state.KeyStatusActive,
			Blacklisted:    true,
			EncryptedValue: encryptHealthKey(t, fixture, "blacklisted-secret-0013"),
		},
		{
			ID: 12, GroupID: 1, Status: state.KeyStatusActive,
			CooldownUntil:  now.Add(time.Minute),
			EncryptedValue: encryptHealthKey(t, fixture, "cooldown-secret-0012"),
		},
		{
			ID: 21, GroupID: 2, Status: state.KeyStatusActive,
			Blacklisted:    true,
			EncryptedValue: encryptHealthKey(t, fixture, "blacklisted-secret-0021"),
		},
		{
			ID: 11, GroupID: 1, Status: state.KeyStatusActive,
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
	pairs := func(items []healthProblemKeyResponse) [][2]uint {
		result := make([][2]uint, 0, len(items))
		for _, item := range items {
			result = append(result, [2]uint{item.GroupID, item.KeyID})
		}
		return result
	}
	if gotPairs, want := pairs(got.CooldownKeys), [][2]uint{
		{1, 11}, {1, 12}, {2, 22},
	}; !reflect.DeepEqual(gotPairs, want) {
		t.Fatalf("cooldown order = %v, want %v", gotPairs, want)
	}
	if gotPairs, want := pairs(got.BlacklistedKeys), [][2]uint{
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
	if _, err := fixture.manager.Publish(state.CompileInput{Groups: []state.GroupConfig{{
		ID: 1, Name: "safe", Protocols: []protocol.Protocol{protocol.OpenAI},
		Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
	}}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.Replace([]state.KeyEntry{{
		ID: 1, GroupID: 1, Status: state.KeyStatusActive,
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
	if len(result.BlacklistedKeys) != 1 ||
		result.BlacklistedKeys[0].Mask != "prov****tail" ||
		result.BlacklistedKeys[0].LastFailureCategory != "ambiguous" ||
		result.BlacklistedKeys[0].LastStatusCode != nil {
		t.Fatalf("blacklisted details = %#v", result.BlacklistedKeys)
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
		"enqueued_total":                 {},
		"persisted_total":                {},
		"dropped_not_running_total":      {},
		"dropped_queue_full_total":       {},
		"dropped_stopping_total":         {},
		"dropped_persist_failed_total":   {},
		"dropped_shutdown_total":         {},
		"dropped_total":                  {},
		"write_failure_total":            {},
		"retention_delete_failure_total": {},
		"queue_depth":                    {},
		"queue_capacity":                 {},
		"last_write_failure_at":          {},
		"last_retention_failure_at":      {},
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
	if _, err := fixture.manager.Publish(state.CompileInput{Groups: []state.GroupConfig{{
		ID: 1, Name: "safe", Protocols: []protocol.Protocol{protocol.OpenAI},
		Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
	}}}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.Replace([]state.KeyEntry{{
		ID: 1, GroupID: 1, Status: state.KeyStatusActive,
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
	if _, err := fixture.service.healthProblemMask(nil, 1); !errors.Is(
		err,
		app_errors.ErrInternalServer,
	) {
		t.Fatalf("healthProblemMask() error = %v, want INTERNAL_SERVER_ERROR", err)
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
		reflect.TypeOf(healthProblemKeyResponse{}),
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
	if err := fixture.registry.Replace([]state.KeyEntry{{
		ID: 1, GroupID: 999, Status: state.KeyStatusActive,
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
	if envelope.Code != 0 || !envelope.Data.ObservedAt.Equal(healthNow()) ||
		envelope.Data.StatsWindowSeconds != 300 {
		t.Fatalf("authenticated envelope = %#v", envelope)
	}
	body := success.Body.String()
	for _, emptyArray := range []string{
		`"groups":[]`, `"cooldown_keys":[]`, `"blacklisted_keys":[]`,
	} {
		if !strings.Contains(body, emptyArray) {
			t.Fatalf("response must contain %s: %s", emptyArray, body)
		}
	}
}
