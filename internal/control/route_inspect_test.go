package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
)

func performRouteInspectRequest(
	engine *gin.Engine,
	authKey string,
	body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/route/inspect",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeRouteInspectSuccess(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) routeInspectResponse {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                  `json:"code"`
		Data routeInspectResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 0 {
		t.Fatalf("code = %d, want 0: %s", envelope.Code, recorder.Body.String())
	}
	return envelope.Data
}

func assertRouteReason(
	t *testing.T,
	got *scheduler.ReasonCode,
	want scheduler.ReasonCode,
) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("reason = %v, want %q", got, want)
	}
}

func routeModelValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestRouteInspectEndpointRejectsMalformedAndInvalidRequests(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{
			name:     "unknown field",
			body:     `{"protocol":"openai-completions","external_model":"model","access_key_id":1,"extra":true}`,
			wantCode: app_errors.ErrInvalidJSON.Code,
		},
		{
			name:     "plaintext access key rejected",
			body:     `{"protocol":"openai-completions","external_model":"model","access_key_id":1,"access_key":"secret"}`,
			wantCode: app_errors.ErrInvalidJSON.Code,
		},
		{
			name:     "multiple values",
			body:     `{"protocol":"openai-completions","external_model":"model","access_key_id":1}{}`,
			wantCode: app_errors.ErrInvalidJSON.Code,
		},
		{
			name:     "trailing malformed value",
			body:     `{"protocol":"openai-completions","external_model":"model","access_key_id":1}x`,
			wantCode: app_errors.ErrInvalidJSON.Code,
		},
		{
			name:     "malformed JSON",
			body:     `{"protocol":"openai-completions"`,
			wantCode: app_errors.ErrInvalidJSON.Code,
		},
		{
			name:     "invalid protocol",
			body:     `{"protocol":"invalid","operation":"chat_completion","required_features":[],"external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "reserved protocol",
			body:     `{"protocol":"openai-response","external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "replaced completions protocol",
			body:     `{"protocol":"openai-chat-completions","external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "missing operation",
			body:     `{"protocol":"openai-completions","required_features":[],"external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "missing required features",
			body:     `{"protocol":"openai-completions","operation":"chat_completion","external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "invalid operation",
			body:     `{"protocol":"openai-completions","operation":"unknown","required_features":[],"external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "invalid feature",
			body:     `{"protocol":"openai-completions","operation":"chat_completion","required_features":["unknown"],"external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "duplicate feature",
			body:     `{"protocol":"openai-completions","operation":"chat_completion","required_features":["tools","tools"],"external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "operation protocol mismatch",
			body:     `{"protocol":"anthropic","operation":"responses_create","required_features":[],"external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "model required by operation",
			body:     `{"protocol":"openai-responses","operation":"responses_create","required_features":[],"access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "model forbidden by lifecycle operation",
			body:     `{"protocol":"openai-responses","operation":"responses_retrieve","required_features":[],"external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "empty model",
			body:     `{"protocol":"openai-completions","operation":"chat_completion","required_features":[],"external_model":"","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "trimmed model required",
			body:     `{"protocol":"openai-completions","external_model":" model ","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "control character in model",
			body:     `{"protocol":"openai-completions","external_model":"model\u0085id","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name: "model exceeds UTF-8 byte limit",
			body: `{"protocol":"openai-completions","external_model":"` +
				strings.Repeat("a", 253) + `猫","access_key_id":1}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "zero access key id",
			body:     `{"protocol":"openai-completions","external_model":"model","access_key_id":0}`,
			wantCode: app_errors.ErrValidation.Code,
		},
		{
			name:     "protocol has wrong JSON type",
			body:     `{"protocol":1,"external_model":"model","access_key_id":1}`,
			wantCode: app_errors.ErrInvalidJSON.Code,
		},
		{
			name:     "external model has wrong JSON type",
			body:     `{"protocol":"openai-completions","external_model":false,"access_key_id":1}`,
			wantCode: app_errors.ErrInvalidJSON.Code,
		},
		{
			name:     "access key id has wrong JSON type",
			body:     `{"protocol":"openai-completions","external_model":"model","access_key_id":"1"}`,
			wantCode: app_errors.ErrInvalidJSON.Code,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := performRouteInspectRequest(engine, "test-auth-key", test.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("response = %d %s, want 400", recorder.Code, recorder.Body.String())
			}
			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if envelope.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", envelope.Code, test.wantCode)
			}
		})
	}
}

func TestRouteInspectSupportsResponsesWithoutModel(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "responses", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID:      10,
			Name:    "client",
			KeyHash: "hash",
			Status:  state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 21, GroupID: 1, Version: 1, IdentityGeneration: 21,
		Fingerprint:    "test-21",
		Status:         state.CredentialStatusActive,
		EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	engine := gin.New()
	NewServer(
		&config.Config{AuthKey: "test-auth-key"},
		fixture.service,
	).RegisterRoutes(engine)

	for _, body := range []string{
		`{"protocol":"openai-responses","operation":"responses_retrieve","required_features":["native_resource_semantics"],"access_key_id":10}`,
		`{"protocol":"openai-responses","operation":"responses_retrieve","required_features":["native_resource_semantics"],"external_model":null,"access_key_id":10}`,
	} {
		recorder := performRouteInspectRequest(
			engine,
			"test-auth-key",
			body,
		)
		got := decodeRouteInspectSuccess(t, recorder)
		if got.Protocol != protocol.OpenAIResponses ||
			got.ExternalModel != nil ||
			!got.Routable ||
			len(got.Groups) != 1 ||
			got.Groups[0].UpstreamModel != nil ||
			!got.Groups[0].Routable {
			t.Fatalf("model-less response = %#v", got)
		}
		if !strings.Contains(recorder.Body.String(), `"external_model":null`) ||
			!strings.Contains(recorder.Body.String(), `"upstream_model":null`) {
			t.Fatalf("nullable model JSON = %s", recorder.Body.String())
		}
	}
}

func TestRouteInspectWithoutModelReturnsSharedFilterReason(t *testing.T) {
	fixture := newServiceFixture(t)
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "responses", ChannelID: channel.OpenAI,
			Params: json.RawMessage(`{}`), Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID:      10,
			Name:    "filtered",
			KeyHash: "hash",
			Status:  state.AccessKeyStatusActive,
			Filters: state.FilterSet{
				Models: map[string]struct{}{"gpt-5": {}},
			},
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	result, err := fixture.service.InspectRoute(routeInspectRequest{
		Protocol:         protocol.OpenAIResponses,
		Operation:        execution.OperationResponsesRetrieve,
		RequiredFeatures: []execution.Feature{execution.FeatureNativeResourceSemantics},
		AccessKeyID:      10,
	})
	if err != nil {
		t.Fatalf("InspectRoute() error = %v", err)
	}
	if result.Routable ||
		result.ReasonCode == nil ||
		*result.ReasonCode != scheduler.ReasonModelRequiredByFilter ||
		len(result.Groups) != 0 {
		t.Fatalf("model-filter response = %#v", result)
	}
}

func TestRouteInspectEndpointReturnsCurrentSafeExplanation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	groupWeight := 20
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{
			{
				ID: 2, Name: "backup", ChannelID: channel.OpenAI,
				Params:       json.RawMessage(`{}`),
				Models:       []state.ModelConfig{{ID: "provider-backup", Alias: "public-model"}},
				WeightManual: &groupWeight, Enabled: true,
			},
			{
				ID: 1, Name: "primary", ChannelID: channel.OpenAI,
				Params:  json.RawMessage(`{}`),
				Models:  []state.ModelConfig{{ID: "provider-model", Alias: "public-model"}},
				Enabled: true,
			},
		},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 10, Name: "production", KeyHash: "active-hash",
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	keyWeight := 25
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
		{
			ID: 31, GroupID: 2, Version: 1, IdentityGeneration: 31, Fingerprint: "test-31", Status: state.CredentialStatusActive,
			WeightAuto: 30, EncryptedValue: "cipher-three",
		},
		{
			ID: 22, GroupID: 1, Version: 1, IdentityGeneration: 22, Fingerprint: "test-22", Status: state.CredentialStatusActive,
			CooldownUntil: now.Add(time.Minute), WeightAuto: 40,
			EncryptedValue: "cipher-two",
		},
		{
			ID: 21, GroupID: 1, Version: 1, IdentityGeneration: 21, Fingerprint: "test-21", Status: state.CredentialStatusActive,
			WeightManual: &keyWeight, WeightAuto: 90,
			EncryptedValue: "cipher-one",
		},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	recorder := performRouteInspectRequest(
		engine,
		"test-auth-key",
		`{"protocol":"openai-completions","operation":"chat_completion","required_features":["tools"],"external_model":"public-model","access_key_id":10}`,
	)
	got := decodeRouteInspectSuccess(t, recorder)
	if got.ObservedAtMS != now.UnixMilli() ||
		got.SnapshotRevision != fixture.manager.Current().Revision ||
		got.Protocol != protocol.OpenAICompletions ||
		got.Operation != execution.OperationChatCompletion ||
		!reflect.DeepEqual(got.RequiredFeatures, []execution.Feature{execution.FeatureTools}) ||
		routeModelValue(got.ExternalModel) != "public-model" ||
		got.AccessKey != (routeInspectAccessKeyResponse{
			ID: 10, Name: "production", Status: state.AccessKeyStatusActive,
		}) ||
		!got.Routable || got.ReasonCode != nil {
		t.Fatalf("route response = %#v", got)
	}
	if len(got.Groups) != 2 || got.Groups[0].GroupID != 1 ||
		got.Groups[1].GroupID != 2 {
		t.Fatalf("group order = %#v", got.Groups)
	}
	primary := got.Groups[0]
	if primary.GroupName != "primary" ||
		primary.ChannelID != channel.OpenAI ||
		primary.RouteMode != execution.RouteNative ||
		!primary.CapabilitySupported ||
		routeModelValue(primary.UpstreamModel) != "provider-model" ||
		primary.WeightManual != nil || !primary.Included ||
		!primary.Routable || primary.ReasonCode != nil ||
		len(primary.Credentials) != 2 ||
		primary.Credentials[0].CredentialID != 21 || primary.Credentials[1].CredentialID != 22 {
		t.Fatalf("primary group = %#v", primary)
	}
	available := primary.Credentials[0]
	if !available.Available || available.ReasonCode != nil ||
		available.WeightManual == nil || *available.WeightManual != 25 ||
		available.WeightAuto != 90 || available.EffectiveWeight != 50*25 ||
		available.CooldownUntilMS != nil {
		t.Fatalf("available key = %#v", available)
	}
	cooldown := primary.Credentials[1]
	if cooldown.Available || cooldown.WeightManual != nil ||
		cooldown.WeightAuto != 40 || cooldown.EffectiveWeight != 0 ||
		cooldown.CooldownUntilMS == nil ||
		*cooldown.CooldownUntilMS != now.Add(time.Minute).UnixMilli() {
		t.Fatalf("cooldown key = %#v", cooldown)
	}
	assertRouteReason(t, cooldown.ReasonCode, scheduler.ReasonCredentialCooldown)
	backup := got.Groups[1]
	if backup.GroupName != "backup" ||
		routeModelValue(backup.UpstreamModel) != "provider-backup" ||
		backup.WeightManual == nil || *backup.WeightManual != 20 ||
		!backup.Included || !backup.Routable || backup.ReasonCode != nil ||
		len(backup.Credentials) != 1 || backup.Credentials[0].CredentialID != 31 ||
		!backup.Credentials[0].Available || backup.Credentials[0].ReasonCode != nil ||
		backup.Credentials[0].WeightManual != nil ||
		backup.Credentials[0].WeightAuto != 30 ||
		backup.Credentials[0].EffectiveWeight != 20*30 ||
		backup.Credentials[0].CooldownUntilMS != nil {
		t.Fatalf("backup group = %#v", backup)
	}
	body := recorder.Body.String()
	if strings.Count(body, `"reason_code":null`) != 5 ||
		strings.Count(body, `"cooldown_until_ms":null`) != 2 {
		t.Fatalf("success response must preserve explicit nulls: %s", body)
	}
	lower := strings.ToLower(body)
	for _, forbidden := range []string{
		"cipher-one", "cipher-two", "cipher-three", "active-hash", "upstream_url",
		"header_rules", "filters",
	} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("response exposes %q: %s", forbidden, body)
		}
	}
}

func TestRouteInspectEndpointReturnsFilterExplanations(t *testing.T) {
	initControlI18n(t)
	tests := []struct {
		name        string
		filters     state.FilterSet
		wantReason  scheduler.ReasonCode
		assertGroup bool
	}{
		{
			name: "protocol filter",
			filters: state.FilterSet{
				Protocols: map[protocol.Protocol]struct{}{protocol.Anthropic: {}},
			},
			wantReason: scheduler.ReasonProtocolFiltered,
		},
		{
			name: "model filter",
			filters: state.FilterSet{
				Models: map[string]struct{}{"other-model": {}},
			},
			wantReason: scheduler.ReasonModelFiltered,
		},
		{
			name: "group filter",
			filters: state.FilterSet{
				Groups: map[uint]struct{}{99: {}},
			},
			wantReason: scheduler.ReasonGroupFiltered, assertGroup: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			now := healthNow()
			fixture.service.now = func() time.Time { return now }
			manual := 15
			if _, err := fixture.manager.Publish(state.CompileInput{
				ChannelRegistry: fixture.channelRegistry,
				Groups: []state.GroupConfig{
					{
						ID: 2, Name: "second", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
						Models:       []state.ModelConfig{{ID: "provider-two", Alias: "public-model"}},
						WeightManual: &manual, Enabled: true,
					},
					{
						ID: 1, Name: "first", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
						Models:  []state.ModelConfig{{ID: "provider-one", Alias: "public-model"}},
						Enabled: true,
					},
				},
				AccessKeys: []state.AccessKeyConfig{{
					ID: 10, Name: "filtered", KeyHash: "filtered-hash",
					Status: state.AccessKeyStatusActive, Filters: test.filters,
				}},
			}); err != nil {
				t.Fatalf("Publish() error = %v", err)
			}
			if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
				{ID: 22, GroupID: 2, Version: 1, IdentityGeneration: 22, Fingerprint: "test-22", Status: state.CredentialStatusActive, EncryptedValue: "two"},
				{ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "test-11", Status: state.CredentialStatusActive, EncryptedValue: "one"},
			}); err != nil {
				t.Fatalf("Replace() error = %v", err)
			}
			engine := gin.New()
			NewServer(
				&config.Config{AuthKey: "test-auth-key"},
				fixture.service,
			).RegisterRoutes(engine)
			recorder := performRouteInspectRequest(
				engine,
				"test-auth-key",
				`{"protocol":"openai-completions","operation":"chat_completion","required_features":[],"external_model":"public-model","access_key_id":10}`,
			)
			got := decodeRouteInspectSuccess(t, recorder)
			if got.ObservedAtMS != now.UnixMilli() ||
				got.SnapshotRevision != fixture.manager.Current().Revision ||
				got.Routable {
				t.Fatalf("filter response = %#v", got)
			}
			assertRouteReason(t, got.ReasonCode, test.wantReason)
			if !test.assertGroup {
				if got.Groups == nil || len(got.Groups) != 0 ||
					!strings.Contains(recorder.Body.String(), `"groups":[]`) {
					t.Fatalf("top-level filter groups = %#v", got.Groups)
				}
				return
			}
			if len(got.Groups) != 2 ||
				got.Groups[0].GroupID != 1 || got.Groups[1].GroupID != 2 {
				t.Fatalf("group order = %#v", got.Groups)
			}
			for index, group := range got.Groups {
				if group.Included || group.Routable ||
					group.Credentials == nil || len(group.Credentials) != 0 {
					t.Fatalf("filtered group %d = %#v", index, group)
				}
				assertRouteReason(t, group.ReasonCode, scheduler.ReasonGroupFiltered)
			}
			if got.Groups[0].WeightManual != nil ||
				got.Groups[1].WeightManual == nil ||
				*got.Groups[1].WeightManual != 15 ||
				routeModelValue(got.Groups[0].UpstreamModel) != "provider-one" ||
				routeModelValue(got.Groups[1].UpstreamModel) != "provider-two" {
				t.Fatalf("filtered group mapping = %#v", got.Groups)
			}
		})
	}
}

func TestRouteInspectEndpointReturnsNoRouteTargetExplanation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "primary", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "configured-model"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 10, Name: "production", KeyHash: "active-hash",
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	recorder := performRouteInspectRequest(
		engine,
		"test-auth-key",
		`{"protocol":"openai-completions","operation":"chat_completion","required_features":[],"external_model":"missing-model","access_key_id":10}`,
	)
	got := decodeRouteInspectSuccess(t, recorder)
	if got.ObservedAtMS != now.UnixMilli() ||
		got.SnapshotRevision != fixture.manager.Current().Revision ||
		got.Routable || got.Groups == nil || len(got.Groups) != 0 {
		t.Fatalf("no-target response = %#v", got)
	}
	assertRouteReason(t, got.ReasonCode, scheduler.ReasonNoRouteTarget)
	if !strings.Contains(recorder.Body.String(), `"groups":[]`) {
		t.Fatalf("no-target groups must be []: %s", recorder.Body.String())
	}
}

func TestRouteInspectEndpointReturnsNoAvailableKeyExplanation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	now := healthNow()
	fixture.service.now = func() time.Time { return now }
	groupWeight := 25
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "primary", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
			Models:       []state.ModelConfig{{ID: "provider-model", Alias: "public-model"}},
			WeightManual: &groupWeight, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 10, Name: "production", KeyHash: "active-hash",
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	zero := 0
	disabledManual := 7
	sourceZone := time.FixedZone("source-offset", 8*60*60)
	cooldownAt := now.In(sourceZone).Add(90 * time.Second)
	if cooldownAt.Location() == time.UTC {
		t.Fatal("cooldown fixture must use a non-UTC location")
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{
		{
			ID: 14, GroupID: 1, Version: 1, IdentityGeneration: 14, Fingerprint: "test-14", Status: state.CredentialStatusActive,
			WeightAuto: 70, CooldownUntil: cooldownAt, EncryptedValue: "cooldown",
		},
		{
			ID: 12, GroupID: 1, Version: 1, IdentityGeneration: 12, Fingerprint: "test-12", Status: state.CredentialStatusActive,
			WeightManual: &zero, WeightAuto: 45, EncryptedValue: "zero",
		},
		{
			ID: 13, GroupID: 1, Version: 1, IdentityGeneration: 13, Fingerprint: "test-13", Status: state.CredentialStatusActive,
			WeightAuto: 60, Blacklisted: true, EncryptedValue: "blacklisted",
		},
		{
			ID: 11, GroupID: 1, Version: 1, IdentityGeneration: 11, Fingerprint: "test-11", Status: state.CredentialStatusDisabled,
			WeightManual: &disabledManual, WeightAuto: 30, EncryptedValue: "disabled",
		},
	}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	recorder := performRouteInspectRequest(
		engine,
		"test-auth-key",
		`{"protocol":"openai-completions","operation":"chat_completion","required_features":[],"external_model":"public-model","access_key_id":10}`,
	)
	got := decodeRouteInspectSuccess(t, recorder)
	if got.ObservedAtMS != now.UnixMilli() ||
		got.SnapshotRevision != fixture.manager.Current().Revision ||
		got.Routable {
		t.Fatalf("unavailable response = %#v", got)
	}
	assertRouteReason(t, got.ReasonCode, scheduler.ReasonNoAvailableCredential)
	if len(got.Groups) != 1 {
		t.Fatalf("groups = %#v", got.Groups)
	}
	group := got.Groups[0]
	if group.GroupID != 1 || group.GroupName != "primary" ||
		routeModelValue(group.UpstreamModel) != "provider-model" ||
		group.WeightManual == nil || *group.WeightManual != 25 ||
		!group.Included || group.Routable || len(group.Credentials) != 4 {
		t.Fatalf("unavailable group = %#v", group)
	}
	assertRouteReason(t, group.ReasonCode, scheduler.ReasonNoAvailableCredential)
	wantReasons := []scheduler.ReasonCode{
		scheduler.ReasonCredentialDisabled,
		scheduler.ReasonCredentialWeightZero,
		scheduler.ReasonCredentialBlacklisted,
		scheduler.ReasonCredentialCooldown,
	}
	wantManual := []*int{&disabledManual, &zero, nil, nil}
	wantAuto := []int{30, 45, 60, 70}
	for index, credential := range group.Credentials {
		if credential.CredentialID != uint(11+index) || credential.Available ||
			credential.EffectiveWeight != 0 ||
			credential.WeightAuto != wantAuto[index] {
			t.Fatalf("unavailable credential %d = %#v", index, credential)
		}
		assertRouteReason(t, credential.ReasonCode, wantReasons[index])
		if wantManual[index] == nil {
			if credential.WeightManual != nil {
				t.Fatalf("key %d manual weight = %v, want nil", index, credential.WeightManual)
			}
		} else if credential.WeightManual == nil ||
			*credential.WeightManual != *wantManual[index] {
			t.Fatalf("key %d manual weight = %v, want %d", index, credential.WeightManual, *wantManual[index])
		}
		if index == 3 {
			if credential.CooldownUntilMS == nil ||
				*credential.CooldownUntilMS != cooldownAt.UnixMilli() {
				t.Fatalf("cooldown = %v, want %v", credential.CooldownUntilMS, cooldownAt.UnixMilli())
			}
		} else if credential.CooldownUntilMS != nil {
			t.Fatalf("key %d cooldown = %v, want nil", index, credential.CooldownUntilMS)
		}
	}
	if strings.Count(recorder.Body.String(), `"cooldown_until_ms":null`) != 3 {
		t.Fatalf("non-cooldown keys must encode null cooldown: %s", recorder.Body.String())
	}
	wantCooldownJSON := `"cooldown_until_ms":` +
		strconv.FormatInt(cooldownAt.UnixMilli(), 10)
	if !strings.Contains(recorder.Body.String(), wantCooldownJSON) {
		t.Fatalf(
			"cooldown must encode as milliseconds: got %s, want %s",
			recorder.Body.String(),
			wantCooldownJSON,
		)
	}
}

func TestRouteInspectReturnsDisabledAccessKeyAsExplanation(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	if _, err := fixture.manager.Publish(state.CompileInput{
		AccessKeys: []state.AccessKeyConfig{{
			ID: 12, Name: "disabled", KeyHash: "disabled-hash",
			Status: state.AccessKeyStatusDisabled,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	result, err := fixture.service.InspectRoute(routeInspectRequest{
		Protocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
		RequiredFeatures: []execution.Feature{}, ExternalModel: stringPointer("model"), AccessKeyID: 12,
	})
	if err != nil {
		t.Fatalf("InspectRoute() error = %v", err)
	}
	if result.Routable || result.ReasonCode == nil ||
		*result.ReasonCode != scheduler.ReasonAccessKeyDisabled ||
		result.Groups == nil || len(result.Groups) != 0 {
		t.Fatalf("disabled result = %#v", result)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	recorder := performRouteInspectRequest(
		engine,
		"test-auth-key",
		`{"protocol":"openai-completions","operation":"chat_completion","required_features":[],"external_model":"model","access_key_id":12}`,
	)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"reason_code":"access_key_disabled"`) ||
		!strings.Contains(recorder.Body.String(), `"groups":[]`) {
		t.Fatalf("disabled HTTP response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRouteInspectMissingAccessKeyReturnsNotFound(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	_, err := fixture.service.InspectRoute(routeInspectRequest{
		Protocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
		RequiredFeatures: []execution.Feature{}, ExternalModel: stringPointer("model"), AccessKeyID: 404,
	})
	if !errors.Is(err, app_errors.ErrResourceNotFound) {
		t.Fatalf("InspectRoute() error = %v, want NOT_FOUND", err)
	}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	recorder := performRouteInspectRequest(
		engine,
		"test-auth-key",
		`{"protocol":"openai-completions","operation":"chat_completion","required_features":[],"external_model":"model","access_key_id":404}`,
	)
	var envelope struct {
		Code string `json:"code"`
	}
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &envelope); decodeErr != nil {
		t.Fatalf("decode missing-key response: %v", decodeErr)
	}
	if recorder.Code != http.StatusNotFound ||
		envelope.Code != app_errors.ErrResourceNotFound.Code {
		t.Fatalf("missing-key response = %d %#v", recorder.Code, envelope)
	}
}

type routeInspectEncryptionSpy struct {
	calls atomic.Int64
}

func (spy *routeInspectEncryptionSpy) Encrypt(string) (string, error) {
	spy.calls.Add(1)
	return "", nil
}

func (spy *routeInspectEncryptionSpy) Decrypt(string) (string, error) {
	spy.calls.Add(1)
	return "", nil
}

func (spy *routeInspectEncryptionSpy) Hash(string) string {
	spy.calls.Add(1)
	return ""
}

func TestRouteInspectNeverCallsUpstreamOrMutatesRuntime(t *testing.T) {
	var upstreamCalls atomic.Int64
	fixture := newServiceFixture(t)
	encryptionSpy := &routeInspectEncryptionSpy{}
	fixture.service.encryption = encryptionSpy
	dialectCalls := 0
	fixture.service.executor = newRecordingDiscoveryExecutor(&recordingDiscoveryExecutorTarget{
		value: protocol.OpenAICompletions,
		listFn: func(
			context.Context,
			string,
			string,
			state.HeaderRules,
		) ([]string, error) {
			dialectCalls++
			return nil, nil
		},
	})
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "upstream", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: "hash",
			Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := fixture.registry.ReplaceCredentials([]state.CredentialEntry{{
		ID: 1, GroupID: 1, Version: 1, IdentityGeneration: 1, Fingerprint: "test-1", Status: state.CredentialStatusActive,
		EncryptedValue: "cipher",
	}}); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	beforeSnapshot := fixture.manager.Current()
	beforeKeys := fixture.registry.Snapshot()
	beforeStats := fixture.stats.Snapshot(1, healthNow())
	requestLogStatsCalls := 0
	fixture.requestLogStats.fn = func() requestlog.Stats {
		requestLogStatsCalls++
		return requestlog.Stats{}
	}

	if _, err := fixture.service.InspectRoute(routeInspectRequest{
		Protocol: protocol.OpenAICompletions, Operation: execution.OperationChatCompletion,
		RequiredFeatures: []execution.Feature{}, ExternalModel: stringPointer("model"), AccessKeyID: 1,
	}); err != nil {
		t.Fatalf("InspectRoute() error = %v", err)
	}
	if upstreamCalls.Load() != 0 || dialectCalls != 0 || encryptionSpy.calls.Load() != 0 {
		t.Fatalf(
			"upstream calls = %d, Dialect calls = %d, encryption calls = %d",
			upstreamCalls.Load(),
			dialectCalls,
			encryptionSpy.calls.Load(),
		)
	}
	if requestLogStatsCalls != 0 {
		t.Fatalf("RequestLog stats calls = %d, want 0", requestLogStatsCalls)
	}
	if fixture.manager.Current() != beforeSnapshot ||
		!reflect.DeepEqual(fixture.registry.Snapshot(), beforeKeys) ||
		fixture.stats.Snapshot(1, healthNow()) != beforeStats {
		t.Fatal("InspectRoute() mutated runtime state")
	}
}

func TestRouteInspectEndpointRequiresManagementAuthentication(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	beforeSnapshot := fixture.manager.Current()
	beforeKeys := fixture.registry.Snapshot()
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	recorder := performRouteInspectRequest(
		engine,
		"",
		`{"protocol":"openai-completions","operation":"chat_completion","required_features":[],"external_model":"model","access_key_id":1}`,
	)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
	}
	if fixture.manager.Current() != beforeSnapshot ||
		!reflect.DeepEqual(fixture.registry.Snapshot(), beforeKeys) {
		t.Fatal("unauthenticated Inspector request reached runtime state")
	}
}

func TestRouteInspectCatalogMismatchReturnsInternalServerError(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	if _, err := fixture.manager.Publish(state.CompileInput{
		ChannelRegistry: fixture.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "primary", ChannelID: channel.OpenAI, Params: json.RawMessage(`{}`),
			Models: []state.ModelConfig{{ID: "model"}}, Enabled: true,
		}},
		AccessKeys: []state.AccessKeyConfig{{
			ID: 1, Name: "client", KeyHash: "hash", Status: state.AccessKeyStatusActive,
		}},
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	fixture.manager.Current().ExecutionRouteCatalog[protocol.OpenAICompletions][execution.OperationChatCompletion]["model"] = []state.RouteTarget{{
		GroupID: 999, UpstreamModelID: "model",
	}}
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	recorder := performRouteInspectRequest(
		engine,
		"test-auth-key",
		`{"protocol":"openai-completions","operation":"chat_completion","required_features":[],"external_model":"model","access_key_id":1}`,
	)
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusInternalServerError ||
		envelope.Code != app_errors.ErrInternalServer.Code {
		t.Fatalf("catalog mismatch response = %d %#v", recorder.Code, envelope)
	}
}
