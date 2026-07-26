package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestGetSettingsReturnsSnapshotDefaultsAndNoOverrides(t *testing.T) {
	fixture := newServiceFixture(t)
	got, err := fixture.service.GetSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != fixture.manager.Current().Revision || len(got.Overrides) != 0 {
		t.Fatalf("GetSettings() = %#v", got)
	}
	if got.Values.ConnectTimeout != 15 || got.Values.FirstByteTimeout != 120 ||
		got.Values.RequestTimeout != 600 || got.Values.StreamIdleTimeout != 300 ||
		got.Values.RequestLogRetentionDays != 7 || !got.Values.InjectUsageOptions {
		t.Fatalf("values = %#v", got.Values)
	}
	if got.Values.HeaderRules.Set == nil || len(got.Values.HeaderRules.Set) != 0 {
		t.Fatalf("header_rules.set = %#v, want empty map", got.Values.HeaderRules.Set)
	}
	if got.Values.HeaderRules.Remove == nil || len(got.Values.HeaderRules.Remove) != 0 {
		t.Fatalf("header_rules.remove = %#v, want empty slice", got.Values.HeaderRules.Remove)
	}
	if got.Overrides == nil {
		t.Fatal("overrides = nil, want empty slice")
	}
}

func TestUpdateSettingsInjectUsageOptionsBooleanAndNullReset(t *testing.T) {
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

func TestUpdateSettingsRejectsNonBooleanInjectUsageWithoutMutation(t *testing.T) {
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
	fixture := newServiceFixture(t)
	_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{
		Settings: map[string]json.RawMessage{
			state.SettingRequestTimeout:    json.RawMessage("900"),
			state.SettingConnectTimeout:    json.RawMessage("20"),
			state.SettingStreamIdleTimeout: json.RawMessage("45"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []models.SystemSetting{
		{Key: "unknown_public_setting", Value: `"hidden"`, UpdatedAt: time.Now().UTC()},
		{Key: models.InternalSystemSettingPrefix + "hidden", Value: `"marker"`, UpdatedAt: time.Now().UTC()},
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
		state.SettingConnectTimeout,
		state.SettingRequestTimeout,
		state.SettingStreamIdleTimeout,
	}
	if !reflect.DeepEqual(got.Overrides, wantOverrides) {
		t.Fatalf("overrides = %#v, want %#v", got.Overrides, wantOverrides)
	}
	if got.Values.ConnectTimeout != 20 || got.Values.RequestTimeout != 900 ||
		got.Values.StreamIdleTimeout != 45 {
		t.Fatalf("values = %#v", got.Values)
	}
}

func TestGetSettingsRejectsMissingSnapshot(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.manager = state.NewManager()

	_, err := fixture.service.GetSettings(t.Context())
	if !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("GetSettings() error = %v, want ErrInternalServer", err)
	}
}

func TestGetSettingsWaitsForConfigurationWriteLock(t *testing.T) {
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
	if !row.UpdatedAt.Equal(wantTime) {
		t.Fatalf("updated_at = %s, want %s", row.UpdatedAt, wantTime)
	}
}

func TestUpdateSettingsRejectsInvalidChangesWithoutPublishing(t *testing.T) {
	tests := []struct {
		name    string
		updates map[string]json.RawMessage
		wantErr *app_errors.APIError
	}{
		{name: "unknown", updates: map[string]json.RawMessage{"unknown": json.RawMessage("true")}, wantErr: app_errors.ErrValidation},
		{name: "internal", updates: map[string]json.RawMessage{models.InternalSystemSettingPrefix + "marker": json.RawMessage("true")}, wantErr: app_errors.ErrValidation},
		{name: "fractional timeout", updates: map[string]json.RawMessage{state.SettingRequestTimeout: json.RawMessage("1.5")}, wantErr: app_errors.ErrValidation},
		{name: "zero retention", updates: map[string]json.RawMessage{state.SettingRequestLogRetentionDays: json.RawMessage("0")}, wantErr: app_errors.ErrValidation},
		{name: "excess retention", updates: map[string]json.RawMessage{state.SettingRequestLogRetentionDays: json.RawMessage("366")}, wantErr: app_errors.ErrValidation},
		{name: "unknown header rule", updates: map[string]json.RawMessage{state.SettingHeaderRules: json.RawMessage(`{"append":{}}`)}, wantErr: app_errors.ErrValidation},
		{name: "case folded duplicate header", updates: map[string]json.RawMessage{state.SettingHeaderRules: json.RawMessage(`{"set":{"X-Test":"one","x-test":"two"}}`)}, wantErr: app_errors.ErrValidation},
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
			state.SettingConnectTimeout: json.RawMessage("25"),
			state.SettingRequestTimeout: json.RawMessage("900"),
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
	tests := []struct {
		name string
		body string
	}{
		{name: "duplicate top level", body: `{"settings":{},"settings":{}}`},
		{name: "duplicate runtime setting", body: `{"settings":{"request_timeout":900,"request_timeout":800}}`},
		{name: "duplicate header rules field", body: `{"settings":{"header_rules":{"set":{},"set":{}}}}`},
		{name: "duplicate header set member", body: `{"settings":{"header_rules":{"set":{"X-Test":"one","X-Test":"two"}}}}`},
		{name: "duplicate object nested in array", body: `{"settings":{"header_rules":{"remove":[{"future":1,"future":2}]}}}`},
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
	fixture := newServiceFixture(t)
	before := fixture.manager.Current().Revision
	updates := []SettingsUpdateRequest{
		{Settings: map[string]json.RawMessage{state.SettingConnectTimeout: json.RawMessage("21")}},
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
	if got.Revision != before+2 || got.Values.ConnectTimeout != 21 ||
		got.Values.RequestTimeout != 901 {
		t.Fatalf("settings = %#v", got)
	}
	wantOverrides := []string{state.SettingConnectTimeout, state.SettingRequestTimeout}
	if !reflect.DeepEqual(got.Overrides, wantOverrides) {
		t.Fatalf("overrides = %#v, want %#v", got.Overrides, wantOverrides)
	}
}
