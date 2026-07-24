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
		Name: "unrelated", UpstreamURL: "https://unrelated.example.com",
		Protocols: models.JSON(`["openai"]`), Models: models.JSON(`[]`),
		Config: models.JSON(`{}`), Enabled: true,
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
	if len(orderColumns) != 1 || orderColumns[0].Column.Name != "key ASC" ||
		!orderColumns[0].Column.Raw || orderColumns[0].Desc {
		t.Fatalf("ORDER BY = %#v, want key ASC", orderColumns)
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

func TestLoaderLoadsEmptyMigratedDatabase(t *testing.T) {
	db := openMigratedDatabase(t)
	manager := state.NewManager()
	registry := state.NewKeyRegistry()

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
	if snapshot.Candidates == nil || len(snapshot.Candidates) != 0 {
		t.Errorf("snapshot candidates = %#v, want initialized empty map", snapshot.Candidates)
	}
	if snapshot.Groups == nil || len(snapshot.Groups) != 0 {
		t.Errorf("snapshot groups = %#v, want initialized empty map", snapshot.Groups)
	}
	if snapshot.AccessKeysByHash == nil || len(snapshot.AccessKeysByHash) != 0 {
		t.Errorf("snapshot access keys = %#v, want initialized empty map", snapshot.AccessKeysByHash)
	}
	if got := registry.CollectCandidates([]uint{1}, nil, time.Time{}); len(got) != 0 {
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
		Name: "pending", UpstreamURL: "https://pending.example",
		Protocols: models.JSON(`["openai"]`),
		Models:    models.JSON(`[{"id":"gpt-pending"}]`), Config: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, tx, &group)
	mustCreate(t, tx, &models.AccessKey{
		Name: "pending", KeyValue: "cipher", KeyHash: "pending-hash",
		Status: "active", Filters: models.JSON(fmt.Sprintf(`{"groups":[%d]}`, group.ID)),
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
	mustCreate(t, db, &models.SystemSetting{Key: "connect_timeout", Value: "20"})
	group := createRuntimeGroup(t, db, "owned", protocol.OpenAI, "gpt-owned")
	if err := db.Model(&group).Update("config", models.JSON(`{"request_timeout":30}`)).Error; err != nil {
		t.Fatalf("update group config: %v", err)
	}
	mustCreate(t, db, &models.AccessKey{
		Name: "owned", KeyValue: "cipher", KeyHash: "owned-hash", Status: "active",
		Filters: models.JSON(fmt.Sprintf(`{"groups":[%d],"protocols":["openai"],"models":["gpt-owned"]}`, group.ID)),
	})

	first, err := loader.BuildCompileInput(context.Background(), db)
	if err != nil {
		t.Fatalf("first BuildCompileInput() error = %v", err)
	}
	first.SystemSettings["connect_timeout"] = 99
	first.Groups[0].Protocols[0] = protocol.Anthropic
	first.Groups[0].Settings["request_timeout"] = 99
	first.AccessKeys[0].Filters.Groups[999] = struct{}{}
	first.AccessKeys[0].Filters.Models["mutated"] = struct{}{}

	second, err := loader.BuildCompileInput(context.Background(), db)
	if err != nil {
		t.Fatalf("second BuildCompileInput() error = %v", err)
	}
	if got := fmt.Sprint(second.SystemSettings["connect_timeout"]); got != "20" {
		t.Fatalf("connect_timeout = %q, want 20", got)
	}
	if second.Groups[0].Protocols[0] != protocol.OpenAI {
		t.Fatalf("protocol = %q, want openai", second.Groups[0].Protocols[0])
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

func TestBuildCompileInputDoesNotQueryUpstreamKeys(t *testing.T) {
	db := openMigratedDatabase(t)
	group := createRuntimeGroup(t, db, "query-boundary", protocol.OpenAI, "gpt-query")
	mustCreate(t, db, &models.AccessKey{
		Name: "query-boundary", KeyValue: "cipher", KeyHash: "query-boundary-hash",
		Status: "active", Filters: models.JSON(`{}`),
	})
	mustCreate(t, db, &models.UpstreamKey{
		GroupID: group.ID, KeyValue: "upstream-cipher", KeyHash: "upstream-query-hash",
		Status: models.UpstreamKeyStatusActive,
	})

	const callbackName = "test:build_compile_input_tables"
	seen := make(map[string]int)
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		seen[tx.Statement.Table]++
	}); err != nil {
		t.Fatalf("register query callback: %v", err)
	}

	if _, err := loader.BuildCompileInput(context.Background(), db); err != nil {
		t.Fatalf("BuildCompileInput() error = %v", err)
	}
	if seen["upstream_keys"] != 0 {
		t.Fatalf("upstream_keys query count = %d, want 0", seen["upstream_keys"])
	}
	for _, table := range []string{"system_settings", "groups", "access_keys"} {
		if seen[table] != 1 {
			t.Errorf("%s query count = %d, want 1; all=%#v", table, seen[table], seen)
		}
	}
}

func TestLoaderMapsSystemAndGroupRows(t *testing.T) {
	db := openMigratedDatabase(t)
	mustCreate(t, db, &models.SystemSetting{Key: "connect_timeout", Value: "20"})
	mustCreate(t, db, &models.SystemSetting{
		Key:   "header_rules",
		Value: `{"set":{"X-System":"system"},"remove":["X-System-Remove"]}`,
	})

	enabled := models.Group{
		Name:        "enabled",
		UpstreamURL: "https://enabled.example.com/v1",
		Protocols:   models.JSON(`["openai"]`),
		Models: models.JSON(`[
			{"id":"gpt-4o","alias":"Primary"},
			{"id":"gpt-4o","alias":"Secondary"},
			{"id":"gpt-4.1","alias":"Other"}
		]`),
		Config: models.JSON(`{
			"request_timeout":30,
			"header_rules":{"set":{"X-Group":"group"},"remove":["X-Group-Remove"]}
		}`),
		Enabled: true,
	}
	mustCreate(t, db, &enabled)
	disabled := models.Group{
		Name:        "disabled",
		UpstreamURL: "https://disabled.example.com/v1",
		Protocols:   models.JSON(`["openai"]`),
		Models:      models.JSON(`[{"id":"hidden","alias":"Hidden"}]`),
		Config:      models.JSON(`{}`),
		Enabled:     true,
	}
	mustCreate(t, db, &disabled)
	if err := db.Model(&disabled).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable group: %v", err)
	}

	manager := state.NewManager()
	registry := state.NewKeyRegistry()
	if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	snapshot := manager.Current()
	if snapshot == nil {
		t.Fatal("Current() = nil, want snapshot")
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
	if view.Timeouts.Connect != 20*time.Second || view.Timeouts.Request != 30*time.Second {
		t.Errorf("group timeouts = %#v, want connect 20s and request 30s", view.Timeouts)
	}
	if len(view.HeaderRules.Set) != 1 || view.HeaderRules.Set["X-Group"] != "group" {
		t.Errorf("group header set rules = %#v, want whole group override", view.HeaderRules.Set)
	}
	if len(view.HeaderRules.Remove) != 1 || view.HeaderRules.Remove[0] != "X-Group-Remove" {
		t.Errorf("group header remove rules = %#v, want group override", view.HeaderRules.Remove)
	}

	openAICandidates := snapshot.Candidates[protocol.OpenAI]
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
	if got := snapshot.RouteCatalog[protocol.OpenAI]["Hidden"]; len(got) != 1 ||
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
				UpstreamURL:     "https://validation.example.com/v1",
				Protocols:       models.JSON(`["openai"]`),
				Models:          models.JSON(`[{"id":"real-model","alias":"public-model"}]`),
				ValidationModel: test.validationModel,
				Config:          models.JSON(`{}`),
				Enabled:         true,
			}
			mustCreate(t, db, &group)

			manager := state.NewManager()
			registry := state.NewKeyRegistry()
			if err := loader.New(db, manager, registry).Load(context.Background()); err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			snapshot := manager.Current()
			if got := snapshot.Groups[group.ID].ValidationModel; got != test.want {
				t.Fatalf("ValidationModel = %q, want %q", got, test.want)
			}
			if got := snapshot.Candidates[protocol.OpenAI]["public-model"][0].UpstreamModelID; got != "real-model" {
				t.Fatalf("candidate upstream model = %q, want real-model", got)
			}
		})
	}
}

func TestLoaderRejectsInvalidGroupRowsWithoutPublishing(t *testing.T) {
	tests := []struct {
		name      string
		protocols models.JSON
		models    models.JSON
		config    models.JSON
		wantError string
	}{
		{name: "protocols object", protocols: models.JSON(`{}`), models: models.JSON(`[]`), config: models.JSON(`{}`), wantError: "protocols"},
		{name: "models object", protocols: models.JSON(`["openai"]`), models: models.JSON(`{}`), config: models.JSON(`{}`), wantError: "models"},
		{name: "config array", protocols: models.JSON(`["openai"]`), models: models.JSON(`[]`), config: models.JSON(`[]`), wantError: "config"},
		{name: "unknown group setting", protocols: models.JSON(`["openai"]`), models: models.JSON(`[{"id":"gpt-4o"}]`), config: models.JSON(`{"unknown":true}`), wantError: "unknown group setting"},
		{name: "duplicate protocol", protocols: models.JSON(`["openai","openai"]`), models: models.JSON(`[{"id":"gpt-4o"}]`), config: models.JSON(`{}`), wantError: "duplicate protocol"},
		{name: "blank model id", protocols: models.JSON(`["openai"]`), models: models.JSON(`[{"id":"  "}]`), config: models.JSON(`{}`), wantError: "model id is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedDatabase(t)
			group := models.Group{
				Name: "invalid", UpstreamURL: "https://invalid.example.com/v1",
				Protocols: test.protocols,
				Models:    test.models, Config: test.config, Enabled: true,
			}
			mustCreate(t, db, &group)
			manager := state.NewManager()
			registry := state.NewKeyRegistry()

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
			if got := registry.CollectCandidates([]uint{group.ID}, nil, time.Time{}); len(got) != 0 {
				t.Fatalf("registry candidates after failed load = %#v, want empty", got)
			}
		})
	}
}

func TestLoaderMapsAccessAndUpstreamKeys(t *testing.T) {
	db := openMigratedDatabase(t)
	firstGroup := createRuntimeGroup(t, db, "first", protocol.OpenAI, "gpt-4o")
	if err := db.Model(&firstGroup).Update("models", models.JSON(`[{"id":"gpt-4o","alias":"Primary"}]`)).Error; err != nil {
		t.Fatalf("set group model alias: %v", err)
	}
	secondGroup := createRuntimeGroup(t, db, "second", protocol.Anthropic, "claude-3-5-sonnet")

	activeAccess := models.AccessKey{
		Name: "active access", KeyValue: "access-cipher-active", KeyHash: "active-hash",
		Status: "active",
		Filters: models.JSON(fmt.Sprintf(
			`{"groups":[%d,9999],"protocols":["openai"],"models":["Primary"]}`,
			firstGroup.ID,
		)),
	}
	disabledAccess := models.AccessKey{
		Name: "disabled access", KeyValue: "access-cipher-disabled", KeyHash: "disabled-hash",
		Status: "disabled", Filters: models.JSON(`{}`),
	}
	mustCreate(t, db, &activeAccess)
	mustCreate(t, db, &disabledAccess)

	firstWeight := 7
	keys := []models.UpstreamKey{
		{GroupID: firstGroup.ID, KeyValue: "upstream-cipher-one", KeyHash: "upstream-hash-one", Status: models.UpstreamKeyStatusActive, WeightManual: &firstWeight},
		{GroupID: firstGroup.ID, KeyValue: "upstream-cipher-two", KeyHash: "upstream-hash-two", Status: models.UpstreamKeyStatusDisabled},
		{GroupID: secondGroup.ID, KeyValue: "upstream-cipher-three", KeyHash: "upstream-hash-three", Status: models.UpstreamKeyStatusActive},
		{GroupID: secondGroup.ID, KeyValue: "upstream-cipher-four", KeyHash: "upstream-hash-four", Status: models.UpstreamKeyStatusDisabled},
	}
	for index := range keys {
		mustCreate(t, db, &keys[index])
	}

	manager := state.NewManager()
	registry := state.NewKeyRegistry()
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
	if len(snapshot.RouteCatalog) == 0 {
		t.Fatal("RouteCatalog was not compiled from persisted groups")
	}
	if _, ok := access.Filters.Groups[firstGroup.ID]; !ok {
		t.Errorf("access filters groups = %#v, want group %d", access.Filters.Groups, firstGroup.ID)
	}
	if _, ok := access.Filters.Groups[9999]; !ok {
		t.Errorf("access filters groups = %#v, want dangling group 9999 retained", access.Filters.Groups)
	}
	if _, ok := access.Filters.Protocols[protocol.OpenAI]; !ok {
		t.Errorf("access filters protocols = %#v, want OpenAI", access.Filters.Protocols)
	}
	if _, ok := access.Filters.Models["Primary"]; !ok {
		t.Errorf("access filters models = %#v, want Primary", access.Filters.Models)
	}
	if _, ok := access.Filters.Models["gpt-4o"]; ok {
		t.Errorf("access filters models = %#v, must not expose hidden upstream id", access.Filters.Models)
	}

	candidates := registry.CollectCandidates([]uint{firstGroup.ID, secondGroup.ID}, nil, time.Time{})
	if len(candidates) != 2 {
		t.Fatalf("registry candidates = %#v, want two active keys", candidates)
	}
	if candidates[0].ID != keys[0].ID || candidates[0].GroupID != firstGroup.ID || candidates[0].WeightManual == nil || *candidates[0].WeightManual != firstWeight {
		t.Errorf("first candidate = %#v, want active weighted key %d", candidates[0], keys[0].ID)
	}
	if candidates[1].ID != keys[2].ID || candidates[1].GroupID != secondGroup.ID {
		t.Errorf("second candidate = %#v, want active key %d", candidates[1], keys[2].ID)
	}
	for _, key := range keys {
		got, ok := registry.EncryptedValue(key.ID)
		if !ok || got != key.KeyValue {
			t.Errorf("EncryptedValue(%d) = %q, %t, want %q, true", key.ID, got, ok, key.KeyValue)
		}
	}

	snapshotText := fmt.Sprintf("%#v", snapshot)
	for _, secret := range []string{
		activeAccess.KeyValue, disabledAccess.KeyValue,
		keys[0].KeyValue, keys[1].KeyValue, keys[2].KeyValue, keys[3].KeyValue,
	} {
		if strings.Contains(snapshotText, secret) {
			t.Errorf("snapshot exposes credential material %q", secret)
		}
	}
}

func TestLoaderMapsAccessKeyRPMLimit(t *testing.T) {
	db := openMigratedDatabase(t)
	accessKey := models.AccessKey{
		Name: "rate-limited", KeyValue: "access-cipher", KeyHash: "rate-limited-hash",
		Status: "active", Filters: models.JSON(`{}`), RPMLimit: 27,
	}
	mustCreate(t, db, &accessKey)

	manager := state.NewManager()
	registry := state.NewKeyRegistry()
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
					Status: "revoked", Filters: models.JSON(`{}`),
				})
			},
			wantError: "invalid status",
		},
		{
			name: "invalid filter protocol",
			insert: func(t *testing.T, db *gorm.DB, _ models.Group) {
				mustCreate(t, db, &models.AccessKey{
					Name: "invalid", KeyValue: "access-cipher", KeyHash: "invalid-protocol-hash",
					Status: "active", Filters: models.JSON(`{"protocols":["unknown"]}`),
				})
			},
			wantError: "invalid protocol",
		},
		{
			name: "blank filter model",
			insert: func(t *testing.T, db *gorm.DB, _ models.Group) {
				mustCreate(t, db, &models.AccessKey{
					Name: "invalid", KeyValue: "access-cipher", KeyHash: "blank-model-hash",
					Status: "active", Filters: models.JSON(`{"models":["  "]}`),
				})
			},
			wantError: "filter model is required",
		},
		{
			name: "unknown filter field",
			insert: func(t *testing.T, db *gorm.DB, _ models.Group) {
				mustCreate(t, db, &models.AccessKey{
					Name: "invalid", KeyValue: "access-cipher", KeyHash: "unknown-filter-field-hash",
					Status: "active", Filters: models.JSON(`{"protcols":["openai"]}`),
				})
			},
			wantError: "unknown field",
		},
		{
			name: "empty upstream ciphertext",
			insert: func(t *testing.T, db *gorm.DB, group models.Group) {
				mustCreate(t, db, &models.UpstreamKey{
					GroupID: group.ID, KeyValue: "", KeyHash: "empty-cipher-hash",
					Status: models.UpstreamKeyStatusActive,
				})
			},
			wantError: "encrypted value is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMigratedDatabase(t)
			group := createRuntimeGroup(t, db, "valid", protocol.OpenAI, "gpt-4o")
			test.insert(t, db, group)
			manager := state.NewManager()
			registry := state.NewKeyRegistry()

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
			if got := registry.CollectCandidates([]uint{group.ID}, nil, time.Time{}); len(got) != 0 {
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
	group := models.Group{
		Name: name, UpstreamURL: "https://" + name + ".example.com/v1",
		Protocols: models.JSON(fmt.Sprintf(`[%q]`, p)),
		Models:    models.JSON(fmt.Sprintf(`[{"id":%q}]`, model)), Config: models.JSON(`{}`), Enabled: true,
	}
	mustCreate(t, db, &group)
	return group
}

func stringPtr(value string) *string {
	return &value
}
