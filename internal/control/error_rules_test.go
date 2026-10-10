package control

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"gpt-load/internal/health"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestCustomErrorSettingsPersistPublishAndRespectEmptyOverride(t *testing.T) {
	assertCustomErrorSettingsPersistPublishAndRespectEmptyOverride(t, newServiceFixture(t))
}

func assertCustomErrorSettingsPersistPublishAndRespectEmptyOverride(t *testing.T, fixture serviceFixture) {
	t.Helper()
	groupID := createGroupWithCredentials(t, fixture, "test-custom-error-key")
	const ruleJSON = `[{"status_codes":[403],"keywords":["欠费"],"retry":"next_candidate","effect":"record_credential_failure"}]`
	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingErrorRules: json.RawMessage(ruleJSON),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Values.ErrorRules) != 1 || len(fixture.manager.Current().Groups[groupID].ErrorRules.Rules()) != 1 {
		t.Fatal("system rules were not returned and inherited by the group")
	}
	read, err := fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Effective.ErrorRules) != 1 {
		t.Fatal("effective group response omitted inherited rules")
	}
	_, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{
			state.SettingErrorRules:         []health.ErrorRule{{Keywords: []string{"余额不足"}, Retry: health.RetryNone, Effect: health.EffectNone}},
			state.SettingBlacklistThreshold: 2,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	read, err = fixture.service.GetGroupSettings(t.Context(), groupID)
	if err != nil || len(read.Effective.ErrorRules) != 1 || read.Effective.ErrorRules[0].Keywords[0] != "余额不足" {
		t.Fatalf("group rules did not round-trip: %v", err)
	}
	group, err := fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{
			state.SettingErrorRules: []any{}, state.SettingBlacklistThreshold: 2,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := group.Overrides[state.SettingErrorRules]; !exists || len(group.Effective.ErrorRules) != 0 || len(group.DefaultErrorRules) != 1 || len(fixture.manager.Current().Groups[groupID].ErrorRules.Rules()) != 0 || group.Effective.BlacklistThreshold != 2 {
		t.Fatal("empty override did not suppress global rules")
	}
	before := fixture.manager.Current()
	_, err = fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingErrorRules: json.RawMessage(`[{"status_codes":[403],"retry":"refresh_credential","effect":"none"}]`),
	}})
	if !errors.Is(err, app_errors.ErrValidation) || fixture.manager.Current() != before {
		t.Fatalf("invalid system rules changed the snapshot: %v", err)
	}
	_, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{state.SettingErrorRules: []any{map[string]any{
			"retry": "none", "effect": "none",
		}}}},
	})
	if !errors.Is(err, app_errors.ErrValidation) || fixture.manager.Current() != before {
		t.Fatalf("invalid group rules changed the snapshot: %v", err)
	}
	group, err = fixture.service.UpdateGroupSettings(t.Context(), groupID, GroupSettingsUpdateRequest{
		Overrides: optionalField[config.Settings]{Set: true, Value: config.Settings{}},
	})
	if err != nil || len(group.Effective.ErrorRules) != 1 {
		t.Fatalf("restoring inheritance failed: %v", err)
	}
	reset, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingErrorRules: json.RawMessage("null"),
	}})
	if err != nil || len(reset.Values.ErrorRules) != 0 {
		t.Fatalf("resetting system rules failed: %v", err)
	}
}

func TestCustomErrorSettingsFitTextStorage(t *testing.T) {
	assertCustomErrorSettingsFitTextStorage(t, newServiceFixture(t))
}

// TestExternalDatabaseCustomErrorRules 覆盖真实 MySQL/PostgreSQL 的设置存储与分组 JSON 合同。
func TestExternalDatabaseCustomErrorRules(t *testing.T) {
	// 共享外部测试库中的全局设置，不并行执行。
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	fixture := newServiceFixtureWithDSN(t, dsn)
	assertCustomErrorSettingsPersistPublishAndRespectEmptyOverride(t, fixture)
	assertCustomErrorSettingsFitTextStorage(t, fixture)
}

func assertCustomErrorSettingsFitTextStorage(t *testing.T, fixture serviceFixture) {
	t.Helper()
	// 既有 MySQL TEXT 列最多存储 65535 字节，包含 JSON 及转义后的内容。
	const limit = 65535
	rules := []health.ErrorRule{{
		StatusCodes: []int{403}, Keywords: []string{"欠费🙂<>&\u2028\t"},
		Retry: health.RetryNone, Effect: health.EffectNone,
	}}
	raw, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	rules[0].Keywords[0] += strings.Repeat("x", limit-len(raw))
	raw, err = json.Marshal(rules)
	if err != nil || len(raw) != limit {
		t.Fatalf("boundary fixture size=%d error=%v", len(raw), err)
	}
	updated, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingErrorRules: raw,
	}})
	if err != nil || len(updated.Values.ErrorRules) != 1 || updated.Values.ErrorRules[0].Keywords[0] != rules[0].Keywords[0] {
		t.Fatalf("boundary rules did not round-trip: %v", err)
	}
	t.Cleanup(func() {
		if err := fixture.db.Where(&models.SystemSetting{Key: state.SettingErrorRules}).Delete(&models.SystemSetting{}).Error; err != nil {
			t.Errorf("clean up error rules: %v", err)
		}
	})
	var stored models.SystemSetting
	if err := fixture.db.Where(&models.SystemSetting{Key: state.SettingErrorRules}).Take(&stored).Error; err != nil || len(stored.Value) != limit {
		t.Fatalf("stored rules size=%d error=%v", len(stored.Value), err)
	}
	before := fixture.manager.Current()
	rules[0].Keywords[0] += "x"
	raw, err = json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		state.SettingErrorRules: raw,
	}})
	if !errors.Is(err, app_errors.ErrValidation) || fixture.manager.Current() != before {
		t.Fatalf("oversized rules must be rejected without publishing: %v", err)
	}
}
