package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/catalog"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestSettingsProxyConfigIsEncryptedMaskedAndResettable(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	const endpoint = "http://proxy-user:proxy-password@proxy.example.com:8080"

	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			outboundproxy.SystemSettingKey: json.RawMessage(`{"mode":"custom","url":"` + endpoint + `"}`),
		},
	})
	if err != nil {
		t.Fatalf("UpdateSettings(proxy) error = %v", err)
	}
	view := updated.Values.ProxyConfig
	if view.ConfiguredMode != outboundproxy.ModeCustom || view.EffectiveSource != outboundproxy.SourceGlobal ||
		view.DisplayURL != "http://proxy-user:******@proxy.example.com:8080" || !view.HasAuth {
		t.Fatalf("proxy view = %#v", view)
	}

	var row models.SystemSetting
	if err := fixture.db.Where("key = ?", outboundproxy.SystemSettingKey).Take(&row).Error; err != nil {
		t.Fatalf("read proxy setting: %v", err)
	}
	if strings.Contains(row.Value, endpoint) || strings.Contains(row.Value, "proxy-password") {
		t.Fatalf("proxy setting stored plaintext: %q", row.Value)
	}
	plaintext, err := fixture.encryption.Decrypt(row.Value)
	if err != nil {
		t.Fatalf("decrypt proxy setting: %v", err)
	}
	config, err := outboundproxy.Decode(plaintext)
	if err != nil || config.URL != endpoint {
		t.Fatalf("stored proxy config = %#v, %v", config, err)
	}

	reset, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{outboundproxy.SystemSettingKey: json.RawMessage("null")},
	})
	if err != nil {
		t.Fatalf("UpdateSettings(proxy null) error = %v", err)
	}
	if reset.Values.ProxyConfig.ConfiguredMode != outboundproxy.ModeInherit {
		t.Fatalf("reset proxy view = %#v", reset.Values.ProxyConfig)
	}
	var count int64
	if err := fixture.db.Model(&models.SystemSetting{}).Where("key = ?", outboundproxy.SystemSettingKey).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("proxy setting rows after reset = %d", count)
	}
}

func TestUpdateSettingsRouteStrategyPersistsPublishesReloadsAndResets(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	for _, test := range []struct {
		name     string
		raw      json.RawMessage
		strategy state.RouteStrategy
	}{
		{name: "weighted mix", raw: json.RawMessage(`"weighted_mix"`), strategy: state.RouteStrategyWeightedMix},
		{name: "explicit native first", raw: json.RawMessage(`"native_first"`), strategy: state.RouteStrategyNativeFirst},
		{name: "weighted mix again", raw: json.RawMessage(`"weighted_mix"`), strategy: state.RouteStrategyWeightedMix},
		{name: "reset", raw: json.RawMessage(`null`), strategy: state.RouteStrategyNativeFirst},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := fixture.manager.Current()
			previousStrategy := before.Settings.RouteStrategy
			got, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
				Settings: map[string]json.RawMessage{state.SettingRouteStrategy: test.raw},
			})
			if err != nil {
				t.Fatal(err)
			}
			after := fixture.manager.Current()
			if got.Values.RouteStrategy != test.strategy || after.Settings.RouteStrategy != test.strategy ||
				after.Revision != before.Revision+1 || before.Settings.RouteStrategy != previousStrategy {
				t.Fatalf("response/snapshots = %#v / %#v / %#v", got, before.Settings, after.Settings)
			}
			var rows []models.SystemSetting
			if err := fixture.db.Where("key = ?", state.SettingRouteStrategy).Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if string(test.raw) == "null" {
				if len(got.Overrides) != 0 || len(rows) != 0 {
					t.Fatalf("reset overrides/rows = %#v / %#v", got.Overrides, rows)
				}
			} else if !reflect.DeepEqual(got.Overrides, []string{state.SettingRouteStrategy}) ||
				len(rows) != 1 || rows[0].Value != string(test.raw) {
				t.Fatalf("persisted overrides/rows = %#v / %#v", got.Overrides, rows)
			}
			reloaded := state.NewManager()
			if err := stateloader.New(fixture.db, reloaded, state.NewCredentialRegistry()).Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := reloaded.Current().Settings.RouteStrategy; got != test.strategy {
				t.Fatalf("reloaded route strategy = %q, want %q", got, test.strategy)
			}
		})
	}
}

func TestSettingsProxyRejectsInvalidConfigWithoutMutation(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{outboundproxy.SystemSettingKey: json.RawMessage(
			`{"mode":"custom","url":"ftp://user:password@proxy.example.com"}`,
		)},
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateSettings(invalid proxy) error = %v, want validation", err)
	}
	var count int64
	if err := fixture.db.Model(&models.SystemSetting{}).Where("key = ?", outboundproxy.SystemSettingKey).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid proxy persisted %d rows", count)
	}
}

func TestGetSettingsReturnsSnapshotDefaultsAndNoOverrides(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	got, err := fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != fixture.manager.Current().Revision || len(got.Overrides) != 0 {
		t.Fatalf("GetSettings() = %#v", got)
	}
	if got.Values.FirstByteTimeout != 120 ||
		got.Values.RequestTimeout != 600 || got.Values.StreamIdleTimeout != 300 ||
		got.Values.ValidationInterval != 600 ||
		got.Values.RouteStrategy != state.RouteStrategyNativeFirst ||
		got.Values.RequestLogRetentionDays != 7 || !got.Values.InjectUsageOptions {
		t.Fatalf("values = %#v", got.Values)
	}
	if !got.Values.AffinityEnabled || got.Values.AffinityTTL != 3600 ||
		got.Values.AffinityCapacity != 10_000 {
		t.Fatalf("affinity values = %#v", got.Values)
	}
	if got.Values.HeaderRules.Set == nil || len(got.Values.HeaderRules.Set) != 0 {
		t.Fatalf("header_rules.set = %#v, want empty map", got.Values.HeaderRules.Set)
	}
	if got.Values.HeaderRules.Remove == nil || len(got.Values.HeaderRules.Remove) != 0 {
		t.Fatalf("header_rules.remove = %#v, want empty slice", got.Values.HeaderRules.Remove)
	}
	if got.Values.CORS.Enabled ||
		!reflect.DeepEqual(got.Values.CORS.AllowedMethods, []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}) ||
		!reflect.DeepEqual(got.Values.CORS.AllowedHeaders, []string{"*"}) ||
		got.Values.CORS.MaxAge != 600 {
		t.Fatalf("cors = %#v, want disabled browser-access defaults", got.Values.CORS)
	}
	if got.Values.CORS.AllowedOrigins == nil || got.Values.CORS.ExposedHeaders == nil {
		t.Fatalf("cors collections must be non-nil: %#v", got.Values.CORS)
	}
	if got.Values.ResponseHeaderRules.Set == nil || got.Values.ResponseHeaderRules.Remove == nil {
		t.Fatalf("response_header_rules collections must be non-nil: %#v", got.Values.ResponseHeaderRules)
	}
	if got.Overrides == nil {
		t.Fatal("overrides = nil, want empty slice")
	}
	if !got.Values.ModelsDevAutoSyncEnabled || got.ReadOnly == nil || len(got.ReadOnly) != 0 {
		t.Fatalf("Models.dev settings = %#v/%#v, want true and no read-only keys", got.Values, got.ReadOnly)
	}
}

func TestUpdateSettingsPublishesCORSAndResponseHeaderRules(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingCORS: json.RawMessage(`{
				"enabled": true,
				"allowed_origins": ["app://obsidian.md"],
				"allowed_methods": ["post"],
				"allowed_headers": ["authorization", "content-type"],
				"exposed_headers": ["x-request-id"],
				"allow_credentials": true,
				"max_age": 900
			}`),
			state.SettingResponseHeaderRules: json.RawMessage(`{
				"set": {"x-browser-client": "enabled"},
				"remove": ["x-upstream-marker"]
			}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantCORS := CORSConfigResponse{
		Enabled:          true,
		AllowedOrigins:   []string{"app://obsidian.md"},
		AllowedMethods:   []string{"POST"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: true,
		MaxAge:           900,
	}
	if !reflect.DeepEqual(updated.Values.CORS, wantCORS) {
		t.Fatalf("cors = %#v, want %#v", updated.Values.CORS, wantCORS)
	}
	wantRules := HeaderRulesResponse{
		Set:    map[string]string{"X-Browser-Client": "enabled"},
		Remove: []string{"X-Upstream-Marker"},
	}
	if !reflect.DeepEqual(updated.Values.ResponseHeaderRules, wantRules) {
		t.Fatalf("response header rules = %#v, want %#v", updated.Values.ResponseHeaderRules, wantRules)
	}
	wantOverrides := []string{state.SettingCORS, state.SettingResponseHeaderRules}
	if !reflect.DeepEqual(updated.Overrides, wantOverrides) {
		t.Fatalf("overrides = %#v, want %#v", updated.Overrides, wantOverrides)
	}

	var rows []models.SystemSetting
	if err := fixture.db.Order("key").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Key != state.SettingCORS || rows[1].Key != state.SettingResponseHeaderRules {
		t.Fatalf("persisted rows = %#v", rows)
	}
}

func TestUpdateSettingsResolvesRetryAndBlacklistPolicies(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)

	defaults, err := fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	assertSettingsPolicyJSON(t, defaults.Values, 2, 3)

	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingRetryCount:         json.RawMessage("0"),
			state.SettingBlacklistThreshold: json.RawMessage("0"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSettingsPolicyJSON(t, updated.Values, 0, 0)
	wantOverrides := []string{
		state.SettingBlacklistThreshold,
		state.SettingRetryCount,
	}
	if !reflect.DeepEqual(updated.Overrides, wantOverrides) {
		t.Fatalf("overrides = %#v, want %#v", updated.Overrides, wantOverrides)
	}
}

func assertSettingsPolicyJSON(
	t *testing.T,
	values SettingsValuesResponse,
	retryCount int,
	blacklistThreshold int,
) {
	t.Helper()
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		state.SettingRetryCount:         float64(retryCount),
		state.SettingBlacklistThreshold: float64(blacklistThreshold),
	}
	for key, expected := range want {
		if got[key] != expected {
			t.Errorf("%s = %#v, want %#v; values=%s", key, got[key], expected, encoded)
		}
	}
}

func TestUpdateSettingsChangesAndResetsValidationInterval(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingValidationInterval: json.RawMessage("900"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Values.ValidationInterval != 900 ||
		fixture.manager.Current().Settings.ValidationInterval != 15*time.Minute ||
		!reflect.DeepEqual(updated.Overrides, []string{state.SettingValidationInterval}) {
		t.Fatalf("updated validation interval = %#v", updated)
	}

	reset, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingValidationInterval: json.RawMessage("null"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Values.ValidationInterval != 600 || len(reset.Overrides) != 0 {
		t.Fatalf("reset validation interval = %#v", reset)
	}
}

func TestUpdateSettingsChangesAndResetsAffinityDefaults(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingAffinityEnabled:  json.RawMessage("false"),
			state.SettingAffinityTTL:      json.RawMessage("7200"),
			state.SettingAffinityCapacity: json.RawMessage("20000"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Values.AffinityEnabled || updated.Values.AffinityTTL != 7200 ||
		updated.Values.AffinityCapacity != 20_000 {
		t.Fatalf("updated affinity settings = %#v", updated)
	}
	snapshot := fixture.manager.Current()
	if snapshot.Settings.AffinityEnabled || snapshot.Settings.AffinityTTL != 2*time.Hour ||
		snapshot.Settings.AffinityCapacity != 20_000 {
		t.Fatalf("published affinity settings = %#v", snapshot.Settings)
	}

	reset, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingAffinityEnabled:  json.RawMessage("null"),
			state.SettingAffinityTTL:      json.RawMessage("null"),
			state.SettingAffinityCapacity: json.RawMessage("null"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reset.Values.AffinityEnabled || reset.Values.AffinityTTL != 3600 ||
		reset.Values.AffinityCapacity != 10_000 {
		t.Fatalf("reset affinity settings = %#v", reset)
	}
}

func TestModelsDevEnvironmentOverrideWinsAndIsReadOnlyWithoutPersistence(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	disabled := false
	fixture.service.modelsDevAutoSyncOverride = &disabled

	got, err := fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Values.ModelsDevAutoSyncEnabled || !reflect.DeepEqual(got.ReadOnly, []string{state.SettingModelsDevAutoSyncEnabled}) {
		t.Fatalf("GetSettings() = %#v, want effective false environment lock", got)
	}

	_, err = fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingModelsDevAutoSyncEnabled: json.RawMessage("true"),
		},
	})
	if !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateSettings(environment locked) error = %v, want validation", err)
	}
	var count int64
	if err := fixture.db.Model(&models.SystemSetting{}).
		Where("key = ?", state.SettingModelsDevAutoSyncEnabled).
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("environment override persisted %d rows", count)
	}
}

func TestUpdateSettingsInjectUsageOptionsBooleanAndNullReset(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{state.SettingInjectUsageOptions: json.RawMessage("false")},
	})
	if err != nil || updated.Values.InjectUsageOptions {
		t.Fatalf("UpdateSettings(false) = %#v, %v", updated, err)
	}
	if !reflect.DeepEqual(updated.Overrides, []string{state.SettingInjectUsageOptions}) {
		t.Fatalf("overrides = %#v", updated.Overrides)
	}
	reset, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{state.SettingInjectUsageOptions: json.RawMessage("null")},
	})
	if err != nil || !reset.Values.InjectUsageOptions || len(reset.Overrides) != 0 {
		t.Fatalf("UpdateSettings(null) = %#v, %v", reset, err)
	}
}

func TestUpdateSettingsEnablingModelsDevRequestsImmediateSyncOnce(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingModelsDevAutoSyncEnabled: json.RawMessage("false"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	coordinator := newCatalogSyncCoordinator(
		fixture.service,
		nil,
		"unused",
		catalog.Metadata{},
		false,
	)

	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingModelsDevAutoSyncEnabled: json.RawMessage("true"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-coordinator.immediateWake:
	default:
		t.Fatal("false -> true did not request immediate catalog sync")
	}

	if _, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingModelsDevAutoSyncEnabled: json.RawMessage("true"),
		},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-coordinator.immediateWake:
		t.Fatal("true -> true requested another immediate catalog sync")
	default:
	}
}

func TestUpdateSettingsRejectsNonBooleanInjectUsageWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, raw := range []json.RawMessage{json.RawMessage("0"), json.RawMessage("1"), json.RawMessage(`"true"`), json.RawMessage("[]"), json.RawMessage("{}")} {
		fixture := newServiceFixture(t)
		before := fixture.manager.Current().Revision
		_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
			Settings: map[string]json.RawMessage{state.SettingInjectUsageOptions: raw},
		})
		if !errors.Is(err, app_errors.ErrValidation) || fixture.manager.Current().Revision != before {
			t.Fatalf("UpdateSettings(%s) error/revision = %v/%d, want validation/%d", raw, err, fixture.manager.Current().Revision, before)
		}
		var count int64
		if err := fixture.db.Model(&models.SystemSetting{}).Where("key = ?", state.SettingInjectUsageOptions).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("persisted rows = %d, %v", count, err)
		}
	}
}

func TestGetSettingsFiltersAndSortsPublicRuntimeOverrides(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingRequestTimeout:    json.RawMessage("900"),
			state.SettingFirstByteTimeout:  json.RawMessage("20"),
			state.SettingStreamIdleTimeout: json.RawMessage("45"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []models.SystemSetting{
		{Key: "unknown_public_setting", Value: `"hidden"`, UpdatedAtMS: time.Now().UnixMilli()},
		{Key: models.InternalSystemSettingPrefix + "hidden", Value: `"marker"`, UpdatedAtMS: time.Now().UnixMilli()},
	} {
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	got, err := fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantOverrides := []string{
		state.SettingFirstByteTimeout,
		state.SettingRequestTimeout,
		state.SettingStreamIdleTimeout,
	}
	if !reflect.DeepEqual(got.Overrides, wantOverrides) {
		t.Fatalf("overrides = %#v, want %#v", got.Overrides, wantOverrides)
	}
	if got.Values.FirstByteTimeout != 20 || got.Values.RequestTimeout != 900 ||
		got.Values.StreamIdleTimeout != 45 {
		t.Fatalf("values = %#v", got.Values)
	}
}

func TestGetSettingsRejectsMissingSnapshot(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.manager = state.NewManager()

	_, err := fixture.service.GetSettings(t.Context())
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("GetSettings() error = %v, want ErrInternalServer", err)
	}
}

func TestGetSettingsWaitsForConfigurationWriteLock(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.writeMu.Lock()
	locked := true
	defer func() {
		if locked {
			fixture.service.writeMu.Unlock()
		}
	}()

	type result struct {
		settings SettingsResponse
		err      error
	}
	done := make(chan result, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		settings, err := fixture.service.GetSettings(t.Context())
		done <- result{settings: settings, err: err}
	}()
	<-started
	select {
	case got := <-done:
		t.Fatalf("GetSettings completed while writeMu held: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}

	fixture.service.writeMu.Unlock()
	locked = false
	select {
	case got := <-done:
		if got.err != nil || got.settings.Revision != 1 {
			t.Fatalf("GetSettings after unlock = %#v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GetSettings did not complete after writeMu release")
	}
}

func TestUpdateSettingsPersistsPublishesAndResetsOverrides(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	got, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingRequestTimeout:          json.RawMessage("900"),
			state.SettingRequestLogRetentionDays: json.RawMessage("30"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != before+1 || got.Values.RequestTimeout != 900 ||
		got.Values.RequestLogRetentionDays != 30 {
		t.Fatalf("result = %#v", got)
	}
	wantOverrides := []string{state.SettingRequestLogRetentionDays, state.SettingRequestTimeout}
	if !reflect.DeepEqual(got.Overrides, wantOverrides) {
		t.Fatalf("overrides = %#v, want %#v", got.Overrides, wantOverrides)
	}

	reset, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingRequestTimeout: json.RawMessage("null"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Revision != before+2 || reset.Values.RequestTimeout != 600 {
		t.Fatalf("reset = %#v", reset)
	}
	if !reflect.DeepEqual(reset.Overrides, []string{state.SettingRequestLogRetentionDays}) {
		t.Fatalf("reset overrides = %#v", reset.Overrides)
	}
	var count int64
	if err := fixture.db.Model(&models.SystemSetting{}).
		Where("key = ?", state.SettingRequestTimeout).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("request_timeout rows = %d", count)
	}
}

func TestUpdateSettingsCanonicalizesValuesAndReturnsEffectiveHeaderRules(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	fixture.service.now = func() time.Time {
		return time.Date(2026, time.July, 24, 12, 30, 0, 0, time.FixedZone("offset", 8*60*60))
	}
	got, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingHeaderRules: json.RawMessage(` {
				"remove": ["x-old"],
				"set": {"x-zed": "distinctive-value", "A-Test": "first"}
			} `),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRules := HeaderRulesResponse{
		Set: map[string]string{
			"A-Test": "first",
			"X-Zed":  "distinctive-value",
		},
		Remove: []string{"X-Old"},
	}
	if !reflect.DeepEqual(got.Values.HeaderRules, wantRules) {
		t.Fatalf("header rules = %#v, want %#v", got.Values.HeaderRules, wantRules)
	}
	var row models.SystemSetting
	if err := fixture.db.First(&row, "key = ?", state.SettingHeaderRules).Error; err != nil {
		t.Fatal(err)
	}
	const wantValue = `{"remove":["x-old"],"set":{"A-Test":"first","x-zed":"distinctive-value"}}`
	if row.Value != wantValue {
		t.Fatalf("persisted value = %q, want %q", row.Value, wantValue)
	}
	wantTime := time.Date(2026, time.July, 24, 4, 30, 0, 0, time.UTC)
	if row.UpdatedAtMS != wantTime.UnixMilli() {
		t.Fatalf("updated_at_ms = %d, want %d", row.UpdatedAtMS, wantTime.UnixMilli())
	}
}

func TestUpdateSettingsRejectsSDKOwnedCredentialHeaders(t *testing.T) {
	t.Parallel()
	initControlI18n(t)
	const (
		authKey       = "settings-template-test-auth"
		providerToken = "fake-provider-token-canary"
	)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authKey}, fixture.service).RegisterRoutes(engine)

	beforeSnapshot := fixture.manager.Current()
	for _, value := range []string{"Bearer ${API_KEY}", "Bearer " + providerToken} {
		rejected := serveSettingsRequest(
			t,
			engine,
			http.MethodPut,
			authKey,
			`{"settings":{"header_rules":{"set":{"Authorization":`+strconv.Quote(value)+`}}}}`,
		)
		if rejected.Code != http.StatusBadRequest ||
			!strings.Contains(rejected.Body.String(), `"code":"VALIDATION_FAILED"`) {
			t.Fatalf("credential header response = %d %s, want 400 validation", rejected.Code, rejected.Body.String())
		}
	}
	if removed := serveSettingsRequest(
		t,
		engine,
		http.MethodPut,
		authKey,
		`{"settings":{"header_rules":{"remove":["Authorization"]}}}`,
	); removed.Code != http.StatusBadRequest {
		t.Fatalf("credential removal response = %d %s, want 400", removed.Code, removed.Body.String())
	}
	if fixture.manager.Current() != beforeSnapshot {
		t.Fatal("rejected credential HeaderRules published a new Snapshot revision")
	}

	var rows []models.SystemSetting
	if err := fixture.db.Where("key = ?", state.SettingHeaderRules).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rejected credential HeaderRules changed persisted rows: %#v", rows)
	}
}

func TestUpdateSettingsRejectsInvalidChangesWithoutPublishing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		updates map[string]json.RawMessage
		wantErr *app_errors.APIError
	}{
		{name: "unknown", updates: map[string]json.RawMessage{"unknown": json.RawMessage("true")}, wantErr: app_errors.ErrValidation},
		{name: "internal", updates: map[string]json.RawMessage{models.InternalSystemSettingPrefix + "marker": json.RawMessage("true")}, wantErr: app_errors.ErrValidation},
		{name: "fractional timeout", updates: map[string]json.RawMessage{state.SettingRequestTimeout: json.RawMessage("1.5")}, wantErr: app_errors.ErrValidation},
		{name: "negative retry count", updates: map[string]json.RawMessage{state.SettingRetryCount: json.RawMessage("-1")}, wantErr: app_errors.ErrValidation},
		{name: "fractional blacklist threshold", updates: map[string]json.RawMessage{state.SettingBlacklistThreshold: json.RawMessage("1.5")}, wantErr: app_errors.ErrValidation},
		{name: "zero retention", updates: map[string]json.RawMessage{state.SettingRequestLogRetentionDays: json.RawMessage("0")}, wantErr: app_errors.ErrValidation},
		{name: "excess retention", updates: map[string]json.RawMessage{state.SettingRequestLogRetentionDays: json.RawMessage("366")}, wantErr: app_errors.ErrValidation},
		{name: "unknown header rule", updates: map[string]json.RawMessage{state.SettingHeaderRules: json.RawMessage(`{"append":{}}`)}, wantErr: app_errors.ErrValidation},
		{name: "case folded duplicate header", updates: map[string]json.RawMessage{state.SettingHeaderRules: json.RawMessage(`{"set":{"X-Test":"one","x-test":"two"}}`)}, wantErr: app_errors.ErrValidation},
		{name: "enabled CORS without origins", updates: map[string]json.RawMessage{state.SettingCORS: json.RawMessage(`{"enabled":true,"allowed_origins":[]}`)}, wantErr: app_errors.ErrValidation},
		{name: "reserved response header", updates: map[string]json.RawMessage{state.SettingResponseHeaderRules: json.RawMessage(`{"set":{"Access-Control-Allow-Origin":"*"}}`)}, wantErr: app_errors.ErrValidation},
		{name: "malformed raw value", updates: map[string]json.RawMessage{state.SettingRequestTimeout: json.RawMessage("900 800")}, wantErr: app_errors.ErrValidation},
		{name: "empty", updates: map[string]json.RawMessage{}, wantErr: app_errors.ErrBadRequest},
		{name: "nil", updates: nil, wantErr: app_errors.ErrBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			before := fixture.manager.Current()
			_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: test.updates})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %s", err, test.wantErr.Code)
			}
			if fixture.manager.Current() != before {
				t.Fatal("revision or Snapshot pointer changed")
			}
			var count int64
			if err := fixture.db.Model(&models.SystemSetting{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("persisted %d rows", count)
			}
		})
	}
}

func TestUpdateSettingsRollsBackAllRowsWithoutPublishing(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	if err := fixture.db.Exec(`
		CREATE TRIGGER reject_request_timeout
		BEFORE INSERT ON system_settings
		WHEN NEW.key = 'request_timeout'
		BEGIN
			SELECT RAISE(ABORT, 'forced settings rollback');
		END
	`).Error; err != nil {
		t.Fatal(err)
	}
	fixture.service.db = fixture.db.Session(&gorm.Session{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	before := fixture.manager.Current()

	_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingStreamIdleTimeout: json.RawMessage("25"),
			state.SettingRequestTimeout:    json.RawMessage("900"),
		},
	})
	if !errors.Is(err, app_errors.ErrDatabase) {
		t.Fatalf("UpdateSettings() error = %v, want ErrDatabase", err)
	}
	if fixture.manager.Current() != before {
		t.Fatal("failed transaction published a Snapshot")
	}
	var count int64
	if err := fixture.db.Model(&models.SystemSetting{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("system setting rows = %d, want rollback to zero", count)
	}
}

func TestSettingsUpdateRequestRejectsDuplicateUnknownAndWrongShapeJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{name: "duplicate top level", body: `{"settings":{},"settings":{}}`},
		{name: "duplicate runtime setting", body: `{"settings":{"request_timeout":900,"request_timeout":800}}`},
		{name: "duplicate header rules field", body: `{"settings":{"header_rules":{"set":{},"set":{}}}}`},
		{name: "duplicate header set member", body: `{"settings":{"header_rules":{"set":{"X-Test":"one","X-Test":"two"}}}}`},
		{name: "duplicate object nested in array", body: `{"settings":{"header_rules":{"remove":[{"future":1,"future":2}]}}}`},
		{name: "duplicate CORS field", body: `{"settings":{"cors":{"enabled":true,"enabled":false}}}`},
		{name: "unknown top level", body: `{"settings":{},"other":{}}`},
		{name: "missing settings", body: `{}`},
		{name: "null settings", body: `{"settings":null}`},
		{name: "array settings", body: `{"settings":[]}`},
		{name: "top level array", body: `[]`},
		{name: "top level scalar", body: `true`},
		{name: "malformed", body: `{"settings":{"request_timeout":900}`},
		{name: "trailing value", body: `{"settings":{}} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var request SettingsUpdateRequest
			if err := json.Unmarshal([]byte(test.body), &request); err == nil {
				t.Fatalf("json.Unmarshal(%s) accepted", test.body)
			}
		})
	}
}

func TestSettingsUpdateRequestPreservesRawNullAndNumbers(t *testing.T) {
	t.Parallel()
	var request SettingsUpdateRequest
	if err := json.Unmarshal([]byte(
		`{"settings":{"request_timeout":9e2,"header_rules":null}}`,
	), &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Settings) != 2 ||
		!bytes.Equal(request.Settings[state.SettingRequestTimeout], []byte("9e2")) ||
		!bytes.Equal(request.Settings[state.SettingHeaderRules], []byte("null")) {
		t.Fatalf("settings = %#v", request.Settings)
	}
}

func TestRejectDuplicateJSONFieldsWalksArraysScalarsAndTrailingValues(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{
		`1`,
		`[true,null,"value",{"one":[{"two":2}]}]`,
		`{"array":[1,2,3],"scalar":false}`,
	} {
		if err := rejectDuplicateJSONFields([]byte(valid)); err != nil {
			t.Errorf("rejectDuplicateJSONFields(%s) error = %v", valid, err)
		}
	}
	for _, invalid := range []string{
		`[{"duplicate":1,"duplicate":2}]`,
		`{} []`,
		`{"array":[1,2}`,
	} {
		if err := rejectDuplicateJSONFields([]byte(invalid)); err == nil {
			t.Errorf("rejectDuplicateJSONFields(%s) accepted", invalid)
		}
	}
}

func TestConcurrentSettingsUpdatesPublishDatabaseTruth(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	updates := []SettingsUpdateRequest{
		{Settings: map[string]json.RawMessage{state.SettingFirstByteTimeout: json.RawMessage("21")}},
		{Settings: map[string]json.RawMessage{state.SettingRequestTimeout: json.RawMessage("901")}},
	}
	start := make(chan struct{})
	errs := make(chan error, len(updates))
	var ready sync.WaitGroup
	ready.Add(len(updates))
	for _, update := range updates {
		update := update
		go func() {
			ready.Done()
			<-start
			_, err := fixture.service.UpdateSettings(t.Context(), update)
			errs <- err
		}()
	}
	ready.Wait()
	close(start)
	for range updates {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	got, err := fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != before+2 || got.Values.FirstByteTimeout != 21 ||
		got.Values.RequestTimeout != 901 {
		t.Fatalf("settings = %#v", got)
	}
	wantOverrides := []string{state.SettingFirstByteTimeout, state.SettingRequestTimeout}
	if !reflect.DeepEqual(got.Overrides, wantOverrides) {
		t.Fatalf("overrides = %#v, want %#v", got.Overrides, wantOverrides)
	}
}
