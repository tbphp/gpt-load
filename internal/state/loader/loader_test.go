package loader_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/state/loader"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestLoadSystemSettingsDecodesPersistedValues(t *testing.T) {
	db := openMigratedDatabase(t)
	for _, row := range []models.SystemSetting{
		{Key: "plain", Value: "not-json"},
		{Key: "number", Value: "12.50"},
		{Key: "object", Value: `{"set":{"X-Test":"original"},"remove":["X-Old"]}`},
	} {
		row := row
		mustCreate(t, db, &row)
	}
	mustCreate(t, db, &models.Group{
		Name: "unrelated", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models:    models.JSON(`[]`),
		Overrides: models.JSON(`{}`), Enabled: true,
	})

	var orderColumns []clause.OrderByColumn
	seenTables := make(map[string]int)
	const callbackName = "test:load_system_settings_order"
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		seenTables[tx.Statement.Table]++
		if orderBy, ok := tx.Statement.Clauses["ORDER BY"].Expression.(clause.OrderBy); ok {
			orderColumns = append([]clause.OrderByColumn(nil), orderBy.Columns...)
		}
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	settings, err := loader.LoadSystemSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSystemSettings() error = %v", err)
	}
	want := map[string]any{
		"number": json.Number("12.50"),
		"object": map[string]any{
			"set":    map[string]any{"X-Test": "original"},
			"remove": []any{"X-Old"},
		},
		"plain": "not-json",
	}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("settings = %#v, want %#v", settings, want)
	}
	if seenTables["system_settings"] != 1 || len(seenTables) != 1 {
		t.Fatalf("queried tables = %#v, want only system_settings once", seenTables)
	}
	if len(orderColumns) != 1 || orderColumns[0].Column.Name != "key" ||
		orderColumns[0].Column.Raw || orderColumns[0].Desc {
		t.Fatalf("ORDER BY = %#v, want quoted key ASC", orderColumns)
	}

	settings["plain"] = "mutated"
	settings["object"].(map[string]any)["set"].(map[string]any)["X-Test"] = "mutated"
	reloaded, err := loader.LoadSystemSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("second LoadSystemSettings() error = %v", err)
	}
	if !reflect.DeepEqual(reloaded, want) {
		t.Fatalf("reloaded settings = %#v, want DB-independent %#v", reloaded, want)
	}
}

func TestMapSystemSettingsMapsOwnedRowsWithoutDatabase(t *testing.T) {
	rows := []models.SystemSetting{
		{Key: "plain", Value: "not-json"},
		{Key: "number", Value: "12.50"},
		{Key: "object", Value: `{"set":{"X-Test":"original"},"remove":["X-Old"]}`},
		{
			Key:   models.InternalSystemSettingPrefix + "bootstrap.default_access_key.v1",
			Value: "true",
		},
	}
	settings, err := loader.MapSystemSettings(rows)
	if err != nil {
		t.Fatalf("MapSystemSettings() error = %v", err)
	}
	want := config.Settings{
		"number": json.Number("12.50"),
		"object": map[string]any{
			"set":    map[string]any{"X-Test": "original"},
			"remove": []any{"X-Old"},
		},
		"plain": "not-json",
	}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("settings = %#v, want %#v", settings, want)
	}

	rows[0].Value = "mutated-after-map"
	settings["object"].(map[string]any)["set"].(map[string]any)["X-Test"] = "mutated"
	remapped, err := loader.MapSystemSettings([]models.SystemSetting{
		{Key: "plain", Value: "not-json"},
		{Key: "object", Value: `{"set":{"X-Test":"original"},"remove":["X-Old"]}`},
	})
	if err != nil {
		t.Fatalf("second MapSystemSettings() error = %v", err)
	}
	if remapped["plain"] != "not-json" ||
		remapped["object"].(map[string]any)["set"].(map[string]any)["X-Test"] != "original" {
		t.Fatalf("MapSystemSettings retained caller aliases: %#v", remapped)
	}
}

func TestBuildCompileInputExcludesInternalSystemSettings(t *testing.T) {
	db := openMigratedDatabase(t)
	for _, row := range []models.SystemSetting{
		{Key: "request_timeout", Value: "60"},
		{
			Key:   models.InternalSystemSettingPrefix + "bootstrap.default_access_key.v1",
			Value: "true",
		},
	} {
		row := row
		mustCreate(t, db, &row)
	}

	input, err := loader.BuildCompileInput(context.Background(), db)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if len(input.SystemSettings) != 1 {
		t.Fatalf("SystemSettings = %#v, want only request_timeout", input.SystemSettings)
	}
	if got := fmt.Sprint(input.SystemSettings["request_timeout"]); got != "60" {
		t.Fatalf("request_timeout = %q, want 60", got)
	}
	if _, ok := input.SystemSettings[models.InternalSystemSettingPrefix+"bootstrap.default_access_key.v1"]; ok {
		t.Fatal("internal bootstrap marker leaked into compile input")
	}
	if _, err := state.Compile(input); err != nil {
		t.Fatalf("Compile() rejected filtered input: %v", err)
	}
}

func TestLoadSystemSettingsExcludesInternalSystemSettings(t *testing.T) {
	db := openMigratedDatabase(t)
	for _, row := range []models.SystemSetting{
		{Key: "request_timeout", Value: "60"},
		{
			Key:   models.InternalSystemSettingPrefix + "bootstrap.default_access_key.v1",
			Value: "true",
		},
	} {
		row := row
		mustCreate(t, db, &row)
	}

	settings, err := loader.LoadSystemSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("LoadSystemSettings() error = %v", err)
	}
	want := config.Settings{"request_timeout": json.Number("60")}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("settings = %#v, want %#v", settings, want)
	}
}

func TestLoaderPublishesDefaultsFromEmptyDatabase(t *testing.T) {
	db := openMigratedDatabase(t)
	manager := state.NewManager()
	if err := loader.New(db, manager, state.NewCredentialRegistry()).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := manager.Current().Settings
	if got.FirstByteTimeout != 120*time.Second ||
		got.RequestTimeout != 600*time.Second ||
		got.StreamIdleTimeout != 300*time.Second ||
		got.RequestLogRetentionDays != 7 {
		t.Fatalf("Settings = %#v", got)
	}
}

func TestLoaderRejectsUnknownPublicSystemSetting(t *testing.T) {
	db := openMigratedDatabase(t)
	mustCreate(t, db, &models.SystemSetting{Key: "unknown_public", Value: "true"})
	manager := state.NewManager()
	err := loader.New(db, manager, state.NewCredentialRegistry()).Load(context.Background())
	if err == nil || manager.Current() != nil {
		t.Fatalf("Load() error/current = %v/%#v", err, manager.Current())
	}
}

func TestLoaderRejectsInvalidKnownPublicSystemSettingsWithoutPublishing(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "null header rules", key: state.SettingHeaderRules, value: "null"},
		{name: "fractional request timeout", key: state.SettingRequestTimeout, value: "1.5"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedDatabase(t)
			mustCreate(t, db, &models.SystemSetting{Key: test.key, Value: test.value})
			manager := state.NewManager()

			err := loader.New(db, manager, state.NewCredentialRegistry()).Load(context.Background())
			if err == nil {
				t.Fatalf("Load() accepted %s=%s", test.key, test.value)
			}
			if manager.Current() != nil {
				t.Fatalf("Load() published invalid %s=%s: %#v", test.key, test.value, manager.Current())
			}
		})
	}
}

func TestLoaderLoadsEmptyMigratedDatabase(t *testing.T) {
	db := openMigratedDatabase(t)
	manager := state.NewManager()
	registry := state.NewCredentialRegistry()

	if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	snapshot := manager.Current()
	if snapshot == nil {
		t.Fatal("Current() = nil, want an initialized snapshot")
	}
	if snapshot.Revision != 1 {
		t.Errorf("snapshot revision = %d, want 1", snapshot.Revision)
	}
	if snapshot.ExecutionCandidates == nil || len(snapshot.ExecutionCandidates) != 0 {
		t.Errorf("snapshot candidates = %#v, want initialized empty map", snapshot.ExecutionCandidates)
	}
	if snapshot.Groups == nil || len(snapshot.Groups) != 0 {
		t.Errorf("snapshot groups = %#v, want initialized empty map", snapshot.Groups)
	}
	if snapshot.AccessKeysByHash == nil || len(snapshot.AccessKeysByHash) != 0 {
		t.Errorf("snapshot access keys = %#v, want initialized empty map", snapshot.AccessKeysByHash)
	}
	if got := registry.CollectCredentialCandidates([]uint{1}, nil, time.Time{}); len(got) != 0 {
		t.Errorf("registry candidates = %#v, want empty", got)
	}
}

func TestBuildCompileInputReadsUncommittedTransactionState(t *testing.T) {
	db := openMigratedDatabase(t)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("Begin() error = %v", tx.Error)
	}
	t.Cleanup(func() {
		_ = tx.Rollback().Error
	})

	group := models.Group{
		Name: "pending", ChannelID: string(channel.OpenAI), Params: models.JSON(`{}`),
		Models: models.JSON(`[{"id":"gpt-pending"}]`), Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, tx, &group)
	mustCreate(t, tx, &models.AccessKey{
		Name: "pending", KeyValue: "cipher", KeyHash: "pending-hash",
		KeySuffix: "0000", Status: "active",
		Filters: models.JSON(fmt.Sprintf(`{"groups":[%d]}`, group.ID)),
	})

	input, err := loader.BuildCompileInput(context.Background(), tx)
	if err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if len(input.Groups) != 1 || len(input.AccessKeys) != 1 {
		t.Fatalf("input = %#v, want one pending group and access key", input)
	}
}

func TestBuildCompileInputReturnsIndependentData(t *testing.T) {
	db := openMigratedDatabase(t)
	mustCreate(t, db, &models.SystemSetting{Key: "first_byte_timeout", Value: "20"})
	group := createRuntimeGroup(t, db, "owned", protocol.OpenAICompletions, "gpt-owned")
	if err := db.Model(&group).Update("overrides", models.JSON(`{"request_timeout":30}`)).Error; err != nil {
		t.Fatalf("update group overrides: %v", err)
	}
	mustCreate(t, db, &models.AccessKey{
		Name: "owned", KeyValue: "cipher", KeyHash: "owned-hash", Status: "active",
		KeySuffix: "0001",
		Filters:   models.JSON(fmt.Sprintf(`{"groups":[%d],"protocols":["openai-completions"],"models":["gpt-owned"]}`, group.ID)),
	})

	first, err := loader.BuildCompileInput(context.Background(), db)
	if err != nil {
		t.Fatalf("first BuildCompileInput() error = %v", err)
	}
	first.SystemSettings["first_byte_timeout"] = 99
	first.Groups[0].Params[0] = '['
	first.Groups[0].Settings["request_timeout"] = 99
	first.AccessKeys[0].Filters.Groups[999] = struct{}{}
	first.AccessKeys[0].Filters.Models["mutated"] = struct{}{}

	second, err := loader.BuildCompileInput(context.Background(), db)
	if err != nil {
		t.Fatalf("second BuildCompileInput() error = %v", err)
	}
	if got := fmt.Sprint(second.SystemSettings["first_byte_timeout"]); got != "20" {
		t.Fatalf("first_byte_timeout = %q, want 20", got)
	}
	if second.Groups[0].ChannelID != channel.OpenAI || string(second.Groups[0].Params) != `{}` {
		t.Fatalf("channel group = %#v, want OpenAI with canonical params", second.Groups[0])
	}
	if got := fmt.Sprint(second.Groups[0].Settings["request_timeout"]); got != "30" {
		t.Fatalf("request_timeout = %q, want 30", got)
	}
	if _, ok := second.AccessKeys[0].Filters.Groups[999]; ok {
		t.Fatal("second input retained mutated group filter")
	}
	if _, ok := second.AccessKeys[0].Filters.Models["mutated"]; ok {
		t.Fatal("second input retained mutated model filter")
	}
}

func TestLoaderMapsSystemAndGroupRows(t *testing.T) {
	db := openMigratedDatabase(t)
	mustCreate(t, db, &models.SystemSetting{Key: "first_byte_timeout", Value: "20"})
	mustCreate(t, db, &models.SystemSetting{
		Key:   "header_rules",
		Value: `{"set":{"X-System":"system"},"remove":["X-System-Remove"]}`,
	})

	enabled := models.Group{
		Name:      "enabled",
		ChannelID: string(channel.OpenAI),
		Params:    models.JSON(`{}`),
		Models: models.JSON(`[
			{"id":"gpt-4o","alias":"Primary"},
			{"id":"gpt-4o","alias":"Secondary"},
			{"id":"gpt-4.1","alias":"Other"}
		]`),
		Overrides: models.JSON(`{
			"request_timeout":30,
			"header_rules":{"set":{"X-Group":"group"},"remove":["X-Group-Remove"]}
		}`),
		Enabled: true,
	}
	mustCreate(t, db, &enabled)
	disabled := models.Group{
		Name:      "disabled",
		ChannelID: string(channel.OpenAI),
		Params:    models.JSON(`{}`),
		Models:    models.JSON(`[{"id":"hidden","alias":"Hidden"}]`),
		Overrides: models.JSON(`{}`),
		Enabled:   true,
	}
	mustCreate(t, db, &disabled)
	if err := db.Model(&disabled).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable group: %v", err)
	}

	manager := state.NewManager()
	registry := state.NewCredentialRegistry()
	if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	snapshot := manager.Current()
	if snapshot == nil {
		t.Fatal("Current() = nil, want snapshot")
	}
	wantSettings := state.RuntimeSettings{
		FirstByteTimeout:  20 * time.Second,
		RequestTimeout:    600 * time.Second,
		StreamIdleTimeout: 300 * time.Second,
		HeaderRules: state.HeaderRules{
			Set:    map[string]string{"X-System": "system"},
			Remove: []string{"X-System-Remove"},
		},
		InjectUsageOptions:       true,
		AffinityEnabled:          true,
		AffinityTTL:              time.Hour,
		AffinityCapacity:         10_000,
		ValidationInterval:       10 * time.Minute,
		RequestLogRetentionDays:  7,
		ModelsDevAutoSyncEnabled: true,
	}
	if !reflect.DeepEqual(snapshot.Settings, wantSettings) {
		t.Fatalf("snapshot Settings = %#v, want %#v", snapshot.Settings, wantSettings)
	}
	if len(snapshot.Groups) != 1 {
		t.Fatalf("snapshot groups = %#v, want enabled group only", snapshot.Groups)
	}
	view, ok := snapshot.Groups[enabled.ID]
	if !ok {
		t.Fatalf("snapshot groups = %#v, want group %d", snapshot.Groups, enabled.ID)
	}
	if _, ok := snapshot.Groups[disabled.ID]; ok {
		t.Fatalf("disabled group %d is present in snapshot", disabled.ID)
	}
	if got := snapshot.GroupCatalog[disabled.ID]; got.ID != disabled.ID ||
		got.Name != disabled.Name || got.Enabled {
		t.Fatalf("disabled GroupCatalog entry = %#v", got)
	}
	if len(view.Models) != 3 || view.Models[0].Alias != "Primary" || view.Models[1].Alias != "Secondary" {
		t.Errorf("group models = %#v, want all aliases retained", view.Models)
	}
	if view.Timeouts.FirstByte != 20*time.Second || view.Timeouts.Request != 30*time.Second {
		t.Errorf("group timeouts = %#v, want first byte 20s and request 30s", view.Timeouts)
	}
	if len(view.HeaderRules.Set) != 1 || view.HeaderRules.Set["X-Group"] != "group" {
		t.Errorf("group header set rules = %#v, want whole group override", view.HeaderRules.Set)
	}
	if len(view.HeaderRules.Remove) != 1 || view.HeaderRules.Remove[0] != "X-Group-Remove" {
		t.Errorf("group header remove rules = %#v, want group override", view.HeaderRules.Remove)
	}

	openAICandidates := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]
	if len(openAICandidates) != 3 {
		t.Fatalf("OpenAI candidates = %#v, want three external model names", openAICandidates)
	}
	for external, upstream := range map[string]string{
		"Primary":   "gpt-4o",
		"Secondary": "gpt-4o",
		"Other":     "gpt-4.1",
	} {
		if got := openAICandidates[external]; len(got) != 1 || got[0].GroupID != enabled.ID || got[0].UpstreamModelID != upstream {
			t.Errorf("%s candidates = %#v, want one route to %q for group %d", external, got, upstream, enabled.ID)
		}
	}
	if _, ok := openAICandidates["hidden"]; ok {
		t.Fatal("disabled group model hidden is present in candidates")
	}
	if got := snapshot.ExecutionRouteCatalog[protocol.OpenAICompletions][execution.OperationChatCompletion]["Hidden"]; len(got) != 1 ||
		got[0].GroupID != disabled.ID || got[0].UpstreamModelID != "hidden" {
		t.Fatalf("disabled RouteCatalog entry = %#v", got)
	}
}

func TestLoaderMapsValidationModelIntoRuntimeSnapshot(t *testing.T) {
	tests := []struct {
		name            string
		validationModel *string
		want            string
	}{
		{name: "trimmed", validationModel: stringPtr("  probe-model  "), want: "probe-model"},
		{name: "nil", want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedDatabase(t)
			group := models.Group{
				Name:            "validation-" + test.name,
				ChannelID:       string(channel.OpenAI),
				Params:          models.JSON(`{}`),
				Models:          models.JSON(`[{"id":"real-model","alias":"public-model"}]`),
				ValidationModel: test.validationModel,
				Overrides:       models.JSON(`{}`),
				Enabled:         true,
			}
			mustCreate(t, db, &group)

			manager := state.NewManager()
			registry := state.NewCredentialRegistry()
			if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			snapshot := manager.Current()
			if got := snapshot.Groups[group.ID].ValidationModel; got != test.want {
				t.Fatalf("ValidationModel = %q, want %q", got, test.want)
			}
			if got := snapshot.ExecutionCandidates[protocol.OpenAICompletions][execution.OperationChatCompletion]["public-model"][0].UpstreamModelID; got != "real-model" {
				t.Fatalf("candidate upstream model = %q, want real-model", got)
			}
		})
	}
}

func TestLoaderRejectsInvalidGroupRowsWithoutPublishing(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		params    models.JSON
		models    models.JSON
		overrides models.JSON
		wantError string
	}{
		{name: "unknown channel", channelID: "unknown", params: models.JSON(`{}`), models: models.JSON(`[]`), overrides: models.JSON(`{}`), wantError: "unknown channel"},
		{name: "invalid channel params", channelID: string(channel.OpenAICompatible), params: models.JSON(`{}`), models: models.JSON(`[]`), overrides: models.JSON(`{}`), wantError: "params.base_url"},
		{name: "models object", channelID: string(channel.OpenAI), params: models.JSON(`{}`), models: models.JSON(`{}`), overrides: models.JSON(`{}`), wantError: "models"},
		{name: "overrides array", channelID: string(channel.OpenAI), params: models.JSON(`{}`), models: models.JSON(`[]`), overrides: models.JSON(`[]`), wantError: "overrides"},
		{name: "unknown group setting", channelID: string(channel.OpenAI), params: models.JSON(`{}`), models: models.JSON(`[{"id":"gpt-4o"}]`), overrides: models.JSON(`{"unknown":true}`), wantError: "unknown group setting"},
		{name: "blank model id", channelID: string(channel.OpenAI), params: models.JSON(`{}`), models: models.JSON(`[{"id":"  "}]`), overrides: models.JSON(`{}`), wantError: "model id is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedDatabase(t)
			group := models.Group{
				Name: "invalid", ChannelID: test.channelID, Params: test.params,
				Models: test.models, Overrides: test.overrides, Enabled: true,
			}
			mustCreate(t, db, &group)
			manager := state.NewManager()
			registry := state.NewCredentialRegistry()

			err := loader.New(db, manager, registry).Load(context.Background())
			if err == nil {
				t.Fatal("Load() error = nil, want invalid group rejection")
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Errorf("Load() error = %q, want context containing %q", err, test.wantError)
			}
			if manager.Current() != nil {
				t.Fatalf("Current() = %#v after failed load, want nil", manager.Current())
			}
			if got := registry.CollectCredentialCandidates([]uint{group.ID}, nil, time.Time{}); len(got) != 0 {
				t.Fatalf("registry candidates after failed load = %#v, want empty", got)
			}
		})
	}
}

func TestLoaderMapsAccessAndCredentials(t *testing.T) {
	db := openMigratedDatabase(t)
	firstGroup := createRuntimeGroup(t, db, "first", protocol.OpenAICompletions, "gpt-4o")
	if err := db.Model(&firstGroup).Update("models", models.JSON(`[{"id":"gpt-4o","alias":"Primary"}]`)).Error; err != nil {
		t.Fatalf("set group model alias: %v", err)
	}
	secondGroup := createRuntimeGroup(t, db, "second", protocol.Anthropic, "claude-3-5-sonnet")

	activeAccess := models.AccessKey{
		Name: "active access", KeyValue: "access-cipher-active", KeyHash: "active-hash",
		KeySuffix: "0003", Status: "active",
		Filters: models.JSON(fmt.Sprintf(
			`{"groups":[%d,9999],"protocols":["openai-completions"],"models":["Primary"]}`,
			firstGroup.ID,
		)),
	}
	disabledAccess := models.AccessKey{
		Name: "disabled access", KeyValue: "access-cipher-disabled", KeyHash: "disabled-hash",
		KeySuffix: "0004", Status: "disabled", Filters: models.JSON(`{}`),
	}
	mustCreate(t, db, &activeAccess)
	mustCreate(t, db, &disabledAccess)

	firstWeight := 7
	credentials := []models.Credential{
		{GroupID: firstGroup.ID, Data: "credential-cipher-one", Fingerprint: "credential-fingerprint-one", Status: models.CredentialStatusActive, WeightManual: &firstWeight},
		{GroupID: firstGroup.ID, Data: "credential-cipher-two", Fingerprint: "credential-fingerprint-two", Status: models.CredentialStatusDisabled},
		{GroupID: secondGroup.ID, Data: "credential-cipher-three", Fingerprint: "credential-fingerprint-three", Status: models.CredentialStatusActive},
		{GroupID: secondGroup.ID, Data: "credential-cipher-four", Fingerprint: "credential-fingerprint-four", Status: models.CredentialStatusDisabled},
	}
	for index := range credentials {
		mustCreate(t, db, &credentials[index])
	}

	manager := state.NewManager()
	registry := state.NewCredentialRegistry()
	if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	snapshot := manager.Current()
	if snapshot == nil {
		t.Fatal("Current() = nil, want snapshot")
	}
	if len(snapshot.AccessKeysByHash) != 1 {
		t.Fatalf("snapshot access keys = %#v, want active key only", snapshot.AccessKeysByHash)
	}
	access, ok := snapshot.AccessKeysByHash[activeAccess.KeyHash]
	if !ok {
		t.Fatalf("snapshot access keys = %#v, want hash %q", snapshot.AccessKeysByHash, activeAccess.KeyHash)
	}
	if _, ok := snapshot.AccessKeysByHash[disabledAccess.KeyHash]; ok {
		t.Fatalf("disabled access hash %q is present in snapshot", disabledAccess.KeyHash)
	}
	if got := snapshot.AccessKeysByID[disabledAccess.ID]; got.ID != disabledAccess.ID ||
		got.Status != state.AccessKeyStatusDisabled || got.Name != disabledAccess.Name {
		t.Fatalf("disabled AccessKeysByID entry = %#v", got)
	}
	if got := snapshot.GroupCatalog[secondGroup.ID]; got.ID != secondGroup.ID ||
		got.Name != secondGroup.Name || !got.Enabled {
		t.Fatalf("second GroupCatalog entry = %#v", got)
	}
	if len(snapshot.ExecutionRouteCatalog) == 0 {
		t.Fatal("RouteCatalog was not compiled from persisted groups")
	}
	if _, ok := access.Filters.Groups[firstGroup.ID]; !ok {
		t.Errorf("access filters groups = %#v, want group %d", access.Filters.Groups, firstGroup.ID)
	}
	if _, ok := access.Filters.Groups[9999]; !ok {
		t.Errorf("access filters groups = %#v, want dangling group 9999 retained", access.Filters.Groups)
	}
	if _, ok := access.Filters.Protocols[protocol.OpenAICompletions]; !ok {
		t.Errorf("access filters protocols = %#v, want OpenAI", access.Filters.Protocols)
	}
	if _, ok := access.Filters.Models["Primary"]; !ok {
		t.Errorf("access filters models = %#v, want Primary", access.Filters.Models)
	}
	if _, ok := access.Filters.Models["gpt-4o"]; ok {
		t.Errorf("access filters models = %#v, must not expose hidden upstream id", access.Filters.Models)
	}

	candidates := registry.CollectCredentialCandidates([]uint{firstGroup.ID, secondGroup.ID}, nil, time.Time{})
	if len(candidates) != 2 {
		t.Fatalf("registry candidates = %#v, want two active credentials", candidates)
	}
	if candidates[0].ID != credentials[0].ID || candidates[0].GroupID != firstGroup.ID || candidates[0].WeightManual == nil || *candidates[0].WeightManual != firstWeight {
		t.Errorf("first candidate = %#v, want active weighted credential %d", candidates[0], credentials[0].ID)
	}
	if candidates[1].ID != credentials[2].ID || candidates[1].GroupID != secondGroup.ID {
		t.Errorf("second candidate = %#v, want active credential %d", candidates[1], credentials[2].ID)
	}
	for _, credential := range credentials {
		got, ok := registry.EncryptedCredentialData(credential.ID)
		if !ok || got != credential.Data {
			t.Errorf("EncryptedValue(%d) = %q, %t, want %q, true", credential.ID, got, ok, credential.Data)
		}
	}

	snapshotText := fmt.Sprintf("%#v", snapshot)
	for _, secret := range []string{
		activeAccess.KeyValue, disabledAccess.KeyValue,
		credentials[0].Data, credentials[1].Data, credentials[2].Data, credentials[3].Data,
	} {
		if strings.Contains(snapshotText, secret) {
			t.Errorf("snapshot exposes credential material %q", secret)
		}
	}
}

func TestBuildGroupCredentialEntriesReadsOnlyRequestedGroupInStableOrder(t *testing.T) {
	db := openMigratedDatabase(t)
	firstGroup := createRuntimeGroup(t, db, "first-entries", protocol.OpenAICompletions, "gpt-4o")
	secondGroup := createRuntimeGroup(t, db, "second-entries", protocol.Anthropic, "claude")
	weight := 9
	credentials := []models.Credential{
		{
			GroupID: secondGroup.ID, Data: "other-cipher",
			Fingerprint: "other-fingerprint", Status: models.CredentialStatusActive,
		},
		{
			GroupID: firstGroup.ID, Data: "first-cipher",
			Fingerprint: "first-fingerprint", Status: models.CredentialStatusActive,
		},
		{
			GroupID: firstGroup.ID, Data: "second-cipher",
			Fingerprint: "second-fingerprint", Status: models.CredentialStatusDisabled,
			WeightManual: &weight,
		},
	}
	for index := range credentials {
		mustCreate(t, db, &credentials[index])
	}

	got, err := loader.BuildGroupCredentialEntries(t.Context(), db, firstGroup.ID)
	if err != nil {
		t.Fatalf("BuildGroupCredentialEntries() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != credentials[1].ID || got[1].ID != credentials[2].ID {
		t.Fatalf("BuildGroupCredentialEntries() = %#v", got)
	}
	if got[1].WeightManual == nil || *got[1].WeightManual != weight ||
		got[1].Status != state.CredentialStatusDisabled ||
		got[1].EncryptedValue != "second-cipher" {
		t.Fatalf("second entry = %#v", got[1])
	}
}

func TestBuildGroupCredentialEntriesRejectsMissingGroup(t *testing.T) {
	db := openMigratedDatabase(t)
	if _, err := loader.BuildGroupCredentialEntries(t.Context(), db, 999); err == nil {
		t.Fatal("BuildGroupCredentialEntries(missing group) error = nil")
	}
}

func TestLoaderMapsAccessKeyRPMLimit(t *testing.T) {
	db := openMigratedDatabase(t)
	accessKey := models.AccessKey{
		Name: "rate-limited", KeyValue: "access-cipher", KeyHash: "rate-limited-hash",
		KeySuffix: "0005", Status: "active", Filters: models.JSON(`{}`), RPMLimit: 27,
	}
	mustCreate(t, db, &accessKey)

	manager := state.NewManager()
	registry := state.NewCredentialRegistry()
	if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := manager.Current().AccessKeysByHash[accessKey.KeyHash].RPMLimit; got != 27 {
		t.Fatalf("RPMLimit = %d, want 27", got)
	}
}

func TestLoaderRejectsInvalidCredentialRowsWithoutPublishing(t *testing.T) {
	tests := []struct {
		name      string
		insert    func(*testing.T, *gorm.DB, models.Group)
		wantError string
	}{
		{
			name: "unknown access status",
			insert: func(t *testing.T, db *gorm.DB, _ models.Group) {
				if err := db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
					t.Fatalf("disable SQLite check constraints: %v", err)
				}
				defer func() {
					if err := db.Exec("PRAGMA ignore_check_constraints = OFF").Error; err != nil {
						t.Errorf("restore SQLite check constraints: %v", err)
					}
				}()
				mustCreate(t, db, &models.AccessKey{
					Name: "invalid", KeyValue: "access-cipher", KeyHash: "invalid-status-hash",
					KeySuffix: "0006", Status: "revoked", Filters: models.JSON(`{}`),
				})
			},
			wantError: "invalid status",
		},
		{
			name: "invalid filter protocol",
			insert: func(t *testing.T, db *gorm.DB, _ models.Group) {
				mustCreate(t, db, &models.AccessKey{
					Name: "invalid", KeyValue: "access-cipher", KeyHash: "invalid-protocol-hash",
					KeySuffix: "0007", Status: "active",
					Filters: models.JSON(`{"protocols":["unknown"]}`),
				})
			},
			wantError: "invalid protocol",
		},
		{
			name: "blank filter model",
			insert: func(t *testing.T, db *gorm.DB, _ models.Group) {
				mustCreate(t, db, &models.AccessKey{
					Name: "invalid", KeyValue: "access-cipher", KeyHash: "blank-model-hash",
					KeySuffix: "0008", Status: "active",
					Filters: models.JSON(`{"models":["  "]}`),
				})
			},
			wantError: "filter model is required",
		},
		{
			name: "unknown filter field",
			insert: func(t *testing.T, db *gorm.DB, _ models.Group) {
				mustCreate(t, db, &models.AccessKey{
					Name: "invalid", KeyValue: "access-cipher", KeyHash: "unknown-filter-field-hash",
					KeySuffix: "0009", Status: "active",
					Filters: models.JSON(`{"protcols":["openai-completions"]}`),
				})
			},
			wantError: "unknown field",
		},
		{
			name: "empty credential ciphertext",
			insert: func(t *testing.T, db *gorm.DB, group models.Group) {
				mustCreate(t, db, &models.Credential{
					GroupID: group.ID, Data: "", Fingerprint: "empty-cipher-fingerprint",
					Status: models.CredentialStatusActive,
				})
			},
			wantError: "encrypted value is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedDatabase(t)
			group := createRuntimeGroup(t, db, "valid", protocol.OpenAICompletions, "gpt-4o")
			test.insert(t, db, group)
			manager := state.NewManager()
			registry := state.NewCredentialRegistry()

			err := loader.New(db, manager, registry).Load(context.Background())
			if err == nil {
				t.Fatal("Load() error = nil, want invalid credential rejection")
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Errorf("Load() error = %q, want context containing %q", err, test.wantError)
			}
			if manager.Current() != nil {
				t.Fatalf("Current() = %#v after failed load, want nil", manager.Current())
			}
			if got := registry.CollectCredentialCandidates([]uint{group.ID}, nil, time.Time{}); len(got) != 0 {
				t.Fatalf("registry candidates after failed load = %#v, want empty", got)
			}
		})
	}
}

func openMigratedDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("storage.Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("storage.AutoMigrate() error = %v", err)
	}
	return db
}

func mustCreate(t *testing.T, db *gorm.DB, value any) {
	t.Helper()
	if err := db.Create(value).Error; err != nil {
		t.Fatalf("create %T: %v", value, err)
	}
}

func createRuntimeGroup(t *testing.T, db *gorm.DB, name string, p protocol.Protocol, model string) models.Group {
	t.Helper()
	channelID := channel.OpenAI
	switch p {
	case protocol.Anthropic:
		channelID = channel.Anthropic
	case protocol.Gemini:
		channelID = channel.Gemini
	}
	group := models.Group{
		Name: name, ChannelID: string(channelID), Params: models.JSON(`{}`),
		Models: models.JSON(fmt.Sprintf(`[{"id":%q}]`, model)), Overrides: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	return group
}

func stringPtr(value string) *string {
	return &value
}
