package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/catalog"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

var errPriceTestMutation = errors.New("forced price mutation failure")

func TestReconcileReferencedPricesMaterializesProviderAndCustomIdentities(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider", ProviderID: &providerID, UpstreamURL: "https://proxy.example/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models:    models.JSON(`[{"id":"gpt-4o","alias":"public-gpt"}]`),
		Config:    models.JSON(`{}`), Enabled: true,
	})
	custom := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "custom", UpstreamURL: "https://custom.example/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models:    models.JSON(`[{"id":"private-model","alias":"public-private"}]`),
		Config:    models.JSON(`{}`), Enabled: true,
	})
	zero := pricing.NanoUSD(0)
	snapshot := &catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai",
			Models: map[string]catalog.Model{
				"gpt-4o": {
					ID: "gpt-4o",
					Cost: &catalog.ModelCost{
						Prices: pricing.Prices{
							Input:      priceTestValue(2_500_000_000),
							Output:     priceTestValue(10_000_000_000),
							CacheRead:  pricing.Price{NanoUSDPerMillion: zero, Set: true},
							CacheWrite: priceTestValue(3_000_000_000),
						},
						ContextTiers: []pricing.ContextTier{{
							InputThresholdTokens: 200_000,
							Prices: pricing.Prices{
								Input:  priceTestValue(5_000_000_000),
								Output: priceTestValue(15_000_000_000),
							},
						}},
					},
				},
			},
		},
	}}

	if err := fixture.service.withControlTransaction(t.Context(), func(tx *gorm.DB) error {
		return reconcileReferencedPrices(tx, snapshot)
	}); err != nil {
		t.Fatalf("reconcileReferencedPrices() error = %v", err)
	}

	var rows []models.ModelPrice
	if err := fixture.db.Order("price_scope_key, model_id").Find(&rows).Error; err != nil {
		t.Fatalf("query model prices: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("model price rows = %d, want 2: %#v", len(rows), rows)
	}
	customScope, _ := pricing.GroupScopeKey(custom.ID)
	if rows[0].PriceScopeKey != customScope || rows[0].ModelID != "private-model" ||
		rows[0].IsManual || priceTestRowHasValue(rows[0]) {
		t.Fatalf("custom row = %#v, want automatic all-null group placeholder", rows[0])
	}
	if rows[1].PriceScopeKey != "provider:openai" || rows[1].ModelID != "gpt-4o" ||
		rows[1].InputPriceNanoUSDPerMillionTokens == nil ||
		*rows[1].InputPriceNanoUSDPerMillionTokens != 2_500_000_000 ||
		rows[1].CacheReadPriceNanoUSDPerMillionTokens == nil ||
		*rows[1].CacheReadPriceNanoUSDPerMillionTokens != 0 || rows[1].IsManual {
		t.Fatalf("provider row = %#v, want exact catalog prices including explicit zero", rows[1])
	}
	var tiers []models.ContextPriceTier
	if err := json.Unmarshal(rows[1].ContextPriceTiers, &tiers); err != nil {
		t.Fatalf("decode persisted tiers: %v", err)
	}
	if len(tiers) != 1 || tiers[0].ThresholdTokens != 200_000 ||
		tiers[0].InputPriceNanoUSDPerMillionTokens == nil ||
		*tiers[0].InputPriceNanoUSDPerMillionTokens != 5_000_000_000 {
		t.Fatalf("persisted tiers = %#v", tiers)
	}
	if _, ok := mustLoadPriceTable(t, fixture.db).Lookup(pricing.Identity{
		ScopeKey: "provider:openai", ModelID: "gpt-4o",
	}); !ok {
		t.Fatal("compiled exact table missed provider identity")
	}
}

func TestReconcileReferencedPricesPreservesManualRowsAndAliasOnlyIdentity(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "provider", ProviderID: &providerID, UpstreamURL: "https://api.openai.com/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models:    models.JSON(`[{"id":"gpt-4o","alias":"first"}]`),
		Config:    models.JSON(`{}`), Enabled: true,
	})
	manualValue := int64(7_000_000_000)
	manual := models.ModelPrice{
		PriceScopeKey: "provider:openai", ModelID: "gpt-4o",
		InputPriceNanoUSDPerMillionTokens: &manualValue,
		IsManual:                          true, CreatedAtMS: 11, UpdatedAtMS: 12,
	}
	manualNull := models.ModelPrice{
		PriceScopeKey: "provider:openai", ModelID: "manual-null",
		IsManual: true, CreatedAtMS: 21, UpdatedAtMS: 22,
	}
	if err := fixture.db.Create(&manual).Error; err != nil {
		t.Fatalf("create manual price: %v", err)
	}
	if err := fixture.db.Create(&manualNull).Error; err != nil {
		t.Fatalf("create manual null price: %v", err)
	}
	before := []models.ModelPrice{}
	if err := fixture.db.Order("id").Find(&before).Error; err != nil {
		t.Fatal(err)
	}

	if err := fixture.service.withControlTransaction(t.Context(), func(tx *gorm.DB) error {
		if err := tx.Model(&models.Group{}).Where("id = ?", group.ID).
			Update("models", models.JSON(`[{"id":"gpt-4o","alias":"second"}]`)).Error; err != nil {
			return err
		}
		return reconcileReferencedPrices(tx, &catalog.Snapshot{})
	}); err != nil {
		t.Fatalf("alias-only reconcile error = %v", err)
	}

	after := []models.ModelPrice{}
	if err := fixture.db.Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("manual rows changed across alias-only reconcile\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestLoadPriceTableFailsClosedOnMalformedPersistedRows(t *testing.T) {
	tests := []struct {
		name string
		row  models.ModelPrice
		raw  string
		args []any
	}{
		{name: "provider", row: models.ModelPrice{PriceScopeKey: "provider:OpenAI", ModelID: "gpt-4o"}},
		{name: "model", row: models.ModelPrice{PriceScopeKey: "provider:openai", ModelID: " bad"}},
		{
			name: "tier",
			raw: `INSERT INTO model_prices
				(price_scope_key, model_id, context_price_tiers, is_manual, created_at_ms, updated_at_ms)
				VALUES (?, ?, ?, false, 1, 1)`,
			args: []any{"provider:openai", "gpt-4o", `[{"threshold_tokens":1}]`},
		},
		{
			name: "automatic custom price",
			row: models.ModelPrice{
				PriceScopeKey: "group:1", ModelID: "private-model",
				InputPriceNanoUSDPerMillionTokens: priceTestInt64Pointer(1),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			if test.raw != "" {
				if err := fixture.db.Exec(test.raw, test.args...).Error; err != nil {
					t.Fatalf("insert malformed raw row: %v", err)
				}
			} else if err := fixture.db.Create(&test.row).Error; err != nil {
				t.Fatalf("create malformed row fixture: %v", err)
			}
			if _, err := loadPriceTable(t.Context(), fixture.db); !errors.Is(err, app_errors.ErrInternalServer) {
				t.Fatalf("loadPriceTable() error = %v, want internal corruption", err)
			}
		})
	}
}

func TestLoadPriceTableFailsClosedOnDuplicatePersistedIdentity(t *testing.T) {
	fixture := newServiceFixture(t)
	if err := fixture.db.Exec("DROP INDEX idx_model_prices_scope_model").Error; err != nil {
		t.Fatalf("drop uniqueness fixture index: %v", err)
	}
	for range 2 {
		if err := fixture.db.Exec(`INSERT INTO model_prices
			(price_scope_key, model_id, is_manual, created_at_ms, updated_at_ms)
			VALUES (?, ?, false, 1, 1)`, "provider:openai", "gpt-4o").Error; err != nil {
			t.Fatalf("insert duplicate fixture row: %v", err)
		}
	}
	if _, err := loadPriceTable(t.Context(), fixture.db); !errors.Is(err, app_errors.ErrInternalServer) {
		t.Fatalf("loadPriceTable() error = %v, want internal corruption", err)
	}
}

func TestPriceIdentityForGroupNeverUsesAliasOrUpstreamURL(t *testing.T) {
	providerID := "openai"
	providerIdentity, err := PriceIdentityForGroup(models.Group{
		ID: 99, ProviderID: &providerID, UpstreamURL: "https://not-openai.invalid",
	}, "actual-upstream-model")
	if err != nil {
		t.Fatalf("provider PriceIdentityForGroup() error = %v", err)
	}
	if providerIdentity != (pricing.Identity{ScopeKey: "provider:openai", ModelID: "actual-upstream-model"}) {
		t.Fatalf("provider identity = %#v", providerIdentity)
	}
	customIdentity, err := PriceIdentityForGroup(models.Group{
		ID: 99, UpstreamURL: "https://api.openai.com/v1",
	}, "actual-upstream-model")
	if err != nil {
		t.Fatalf("custom PriceIdentityForGroup() error = %v", err)
	}
	if customIdentity != (pricing.Identity{ScopeKey: "group:99", ModelID: "actual-upstream-model"}) {
		t.Fatalf("custom identity = %#v", customIdentity)
	}
	invalid := "OpenAI"
	if _, err := PriceIdentityForGroup(models.Group{ID: 99, ProviderID: &invalid}, "model"); err == nil {
		t.Fatal("PriceIdentityForGroup() accepted non-canonical provider")
	}
	if _, err := PriceIdentityForGroup(models.Group{ID: 99}, " bad"); err == nil {
		t.Fatal("PriceIdentityForGroup() accepted invalid model ID")
	}
}

func TestCreateGroupPublishesCatalogPriceAndProviderRuntimeIdentity(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai",
			Models: map[string]catalog.Model{
				"gpt-4o": {
					ID: "gpt-4o",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{
						Input: priceTestValue(2_500_000_000),
					}},
				},
			},
		},
	}})

	result, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ProviderID:  &providerID,
		UpstreamURL: "https://proxy.example/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{Set: true, Values: []GroupModel{{
			ID: "gpt-4o",
		}}},
		Keys: "sk-provider-create",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	identity := pricing.Identity{ScopeKey: "provider:openai", ModelID: "gpt-4o"}
	rule, exists := fixture.priceRuntime.Load().Lookup(identity)
	if !exists || !rule.Prices.Input.Set || rule.Prices.Input.NanoUSDPerMillion != 2_500_000_000 {
		t.Fatalf("published provider rule = %#v, %t", rule, exists)
	}
	view := fixture.manager.Current().Groups[result.GroupID]
	if view.ProviderID == nil || *view.ProviderID != "openai" {
		t.Fatalf("published GroupView.ProviderID = %v", view.ProviderID)
	}
	var persisted models.Group
	if err := fixture.db.First(&persisted, result.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ProviderID == nil || *persisted.ProviderID != "openai" {
		t.Fatalf("persisted provider_id = %v", persisted.ProviderID)
	}
}

func TestCreateGroupIdempotentPublishesPricesBeforeRegistryAndConfig(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai",
			Models: map[string]catalog.Model{
				"gpt-4o": {
					ID: "gpt-4o",
					Cost: &catalog.ModelCost{Prices: pricing.Prices{
						Input: priceTestValue(2_500_000_000),
					}},
				},
			},
		},
	}})
	reconcileCalls := 0
	fixture.service.reconcileRegistryGroup = func(groupID uint, entries []state.KeyEntry) (bool, error) {
		reconcileCalls++
		identity := pricing.Identity{ScopeKey: "provider:openai", ModelID: "gpt-4o"}
		if _, exists := fixture.priceRuntime.Load().Lookup(identity); !exists {
			return false, errors.New("price table was not published before registry recovery")
		}
		if _, published := fixture.manager.Current().GroupCatalog[groupID]; published {
			return false, errors.New("config snapshot was published before registry recovery")
		}
		return fixture.registry.ReconcileGroup(groupID, entries)
	}

	result, err := fixture.service.CreateGroupIdempotent(
		t.Context(),
		"418f47a2-9c35-4d6e-8b1a-1234567890ab",
		GroupCreateRequest{
			ProviderID:  &providerID,
			UpstreamURL: "https://idempotent-provider.example/v1",
			Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
			Models: optionalGroupModels{Set: true, Values: []GroupModel{{
				ID: "gpt-4o",
			}}},
			Keys: "sk-idempotent-provider",
		},
	)
	if err != nil {
		t.Fatalf("CreateGroupIdempotent() error = %v", err)
	}
	if reconcileCalls != 1 {
		t.Fatalf("registry reconcile calls = %d, want 1", reconcileCalls)
	}
	var persisted models.Group
	if err := fixture.db.First(&persisted, result.GroupID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.ProviderID == nil || *persisted.ProviderID != "openai" {
		t.Fatalf("persisted provider_id = %v", persisted.ProviderID)
	}
	view := fixture.manager.Current().Groups[result.GroupID]
	if view.ProviderID == nil || *view.ProviderID != "openai" {
		t.Fatalf("published provider_id = %v", view.ProviderID)
	}
}

func TestGroupMutationPublishesPricesBeforeRegistryAndConfig(t *testing.T) {
	fixture := newServiceFixture(t)
	beforeRevision := fixture.manager.Current().Revision
	var groupID uint
	callbackRan := false
	snapshot, err := fixture.service.writeGroupConfig(t.Context(), func(tx *gorm.DB) error {
		group := models.Group{
			Name: "ordered", UpstreamURL: "https://ordered.example/v1",
			Protocols: models.JSON(`["openai-completions"]`),
			Models:    models.JSON(`[{"id":"ordered-model","alias":""}]`),
			Config:    models.JSON(`{}`), Enabled: true,
		}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		groupID = group.ID
		return nil
	}, func() error {
		callbackRan = true
		identity := pricing.Identity{ScopeKey: "group:" + strconv.FormatUint(uint64(groupID), 10), ModelID: "ordered-model"}
		if _, exists := fixture.priceRuntime.Load().Lookup(identity); !exists {
			return errors.New("price table was not published before callback")
		}
		if _, published := fixture.manager.Current().GroupCatalog[groupID]; published {
			return errors.New("config snapshot was published before callback")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("writeGroupConfig() error = %v", err)
	}
	if !callbackRan {
		t.Fatal("registry callback did not run")
	}
	if snapshot == nil || snapshot.Revision != beforeRevision+1 {
		t.Fatalf("published snapshot = %#v", snapshot)
	}
	if _, exists := snapshot.GroupCatalog[groupID]; !exists {
		t.Fatal("config snapshot missed committed group")
	}
}

func TestFailedGroupMutationPublishesNeitherRuntime(t *testing.T) {
	fixture := newServiceFixture(t)
	beforePrice := fixture.priceRuntime.Load()
	beforeConfig := fixture.manager.Current()
	_, err := fixture.service.writeGroupConfig(t.Context(), func(tx *gorm.DB) error {
		if err := tx.Create(&models.Group{
			Name: "rolled-back", UpstreamURL: "https://rollback.example/v1",
			Protocols: models.JSON(`["openai-completions"]`), Models: models.JSON(`[]`),
			Config: models.JSON(`{}`), Enabled: true,
		}).Error; err != nil {
			return err
		}
		return errPriceTestMutation
	}, nil)
	if !errors.Is(err, errPriceTestMutation) {
		t.Fatalf("writeGroupConfig() error = %v", err)
	}
	if fixture.priceRuntime.Load() != beforePrice || fixture.manager.Current() != beforeConfig {
		t.Fatal("failed Group mutation published a runtime")
	}
}

func TestProviderChangeMaterializesNewScopeAndPreservesOldManualRow(t *testing.T) {
	fixture := newServiceFixture(t)
	openAI := "openai"
	anthropic := "anthropic"
	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		"openai": {
			ID: "openai",
			Models: map[string]catalog.Model{
				"shared-model": {ID: "shared-model", Cost: &catalog.ModelCost{Prices: pricing.Prices{
					Input: priceTestValue(1_000_000_000),
				}}},
			},
		},
		"anthropic": {
			ID: "anthropic",
			Models: map[string]catalog.Model{
				"shared-model": {ID: "shared-model", Cost: &catalog.ModelCost{Prices: pricing.Prices{
					Input: priceTestValue(2_000_000_000),
				}}},
			},
		},
	}})
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ProviderID:  &openAI,
		UpstreamURL: "https://proxy.invalid/openai/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{Set: true, Values: []GroupModel{{
			ID: "shared-model",
		}}},
		Keys: "sk-provider-change",
	})
	if err != nil {
		t.Fatal(err)
	}
	manualValue := int64(9_000_000_000)
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ? AND model_id = ?", "provider:openai", "shared-model").
		Updates(map[string]any{
			"input_price_nano_usd_per_million_tokens": manualValue,
			"is_manual":     true,
			"updated_at_ms": 77,
		}).Error; err != nil {
		t.Fatal(err)
	}
	var before models.ModelPrice
	if err := fixture.db.Where("price_scope_key = ? AND model_id = ?", "provider:openai", "shared-model").
		Take(&before).Error; err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		ProviderID: optionalField[string]{Set: true, Value: anthropic},
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings() error = %v", err)
	}
	if got.ProviderID == nil || *got.ProviderID != anthropic || got.UpstreamURL != "https://proxy.invalid/openai/v1" {
		t.Fatalf("updated settings = %#v", got)
	}
	var oldAfter models.ModelPrice
	if err := fixture.db.First(&oldAfter, before.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(oldAfter, before) {
		t.Fatalf("old manual row changed\nbefore=%#v\nafter=%#v", before, oldAfter)
	}
	newRule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{
		ScopeKey: "provider:anthropic", ModelID: "shared-model",
	})
	if !exists || !newRule.Prices.Input.Set || newRule.Prices.Input.NanoUSDPerMillion != 2_000_000_000 {
		t.Fatalf("new provider rule = %#v, %t", newRule, exists)
	}
}

func TestProviderIDCanBeExplicitlyClearedWithoutURLInference(t *testing.T) {
	fixture := newServiceFixture(t)
	providerID := "openai"
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ProviderID:  &providerID,
		UpstreamURL: "https://api.openai.com/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "gpt-4o"}}},
		Keys:        "sk-provider-clear",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		ProviderID: optionalField[string]{Set: true, Null: true},
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings(provider_id=null) error = %v", err)
	}
	if got.ProviderID != nil {
		t.Fatalf("provider_id after explicit clear = %v", got.ProviderID)
	}
	scopeKey, _ := pricing.GroupScopeKey(created.GroupID)
	rule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{
		ScopeKey: scopeKey, ModelID: "gpt-4o",
	})
	if !exists || priceRuleHasConfiguredValue(rule) || rule.IsManual {
		t.Fatalf("custom pending rule after clear = %#v, %t", rule, exists)
	}
}

func TestProviderChangeRequiresSelectableProviderButAllowsPersistedMissingProvider(t *testing.T) {
	fixture := newServiceFixture(t)
	legacyProviderID := "legacy-provider"
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name:        "legacy",
		ProviderID:  &legacyProviderID,
		UpstreamURL: "https://legacy.example/v1",
		Protocols:   models.JSON(`["openai-completions"]`),
		Models:      models.JSON(`[]`),
		Config:      models.JSON(`{}`),
		Enabled:     true,
	})
	if _, err := fixture.manager.Publish(mustBuildCompileInput(t, fixture.db)); err != nil {
		t.Fatal(err)
	}

	got, err := fixture.service.UpdateGroupSettings(t.Context(), group.ID, GroupSettingsUpdateRequest{
		Name: optionalField[string]{Set: true, Value: "legacy renamed"},
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings(existing missing provider) error = %v", err)
	}
	if got.ProviderID == nil || *got.ProviderID != legacyProviderID {
		t.Fatalf("persisted provider_id = %v, want %q", got.ProviderID, legacyProviderID)
	}

	unknownProviderID := "unknown-provider"
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), group.ID, GroupSettingsUpdateRequest{
		ProviderID: optionalField[string]{Set: true, Value: unknownProviderID},
	}); !errors.Is(err, app_errors.ErrValidation) {
		t.Fatalf("UpdateGroupSettings(unknown provider) error = %v, want ErrValidation", err)
	}

	fixture.catalogRuntime.Publish(&catalog.Snapshot{Providers: map[string]catalog.Provider{
		unknownProviderID: {ID: unknownProviderID},
	}})
	got, err = fixture.service.UpdateGroupSettings(t.Context(), group.ID, GroupSettingsUpdateRequest{
		ProviderID: optionalField[string]{Set: true, Value: unknownProviderID},
	})
	if err != nil {
		t.Fatalf("UpdateGroupSettings(catalog provider) error = %v", err)
	}
	if got.ProviderID == nil || *got.ProviderID != unknownProviderID {
		t.Fatalf("updated provider_id = %v, want %q", got.ProviderID, unknownProviderID)
	}
}

func TestDeleteGroupRemovesCustomScopeAndUnreferencedAutomaticProviderPrice(t *testing.T) {
	fixture := newServiceFixture(t)
	custom, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://custom-delete.example/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "custom-model"}}},
		Keys:        "sk-custom-delete",
	})
	if err != nil {
		t.Fatal(err)
	}
	customScope, _ := pricing.GroupScopeKey(custom.GroupID)
	manualValue := int64(4_000_000_000)
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ? AND model_id = ?", customScope, "custom-model").
		Updates(map[string]any{
			"input_price_nano_usd_per_million_tokens": manualValue,
			"is_manual": true,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.DeleteGroup(t.Context(), custom.GroupID); err != nil {
		t.Fatalf("DeleteGroup(custom) error = %v", err)
	}
	var customRows int64
	if err := fixture.db.Model(&models.ModelPrice{}).Where("price_scope_key = ?", customScope).Count(&customRows).Error; err != nil {
		t.Fatal(err)
	}
	if customRows != 0 {
		t.Fatalf("custom rows after delete = %d, want 0", customRows)
	}

	providerID := "openai"
	provider, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ProviderID:  &providerID,
		UpstreamURL: "https://provider-delete.example/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models:      optionalGroupModels{Set: true, Values: []GroupModel{{ID: "provider-model"}}},
		Keys:        "sk-provider-delete",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.DeleteGroup(t.Context(), provider.GroupID); err != nil {
		t.Fatalf("DeleteGroup(provider) error = %v", err)
	}
	var providerRows int64
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ? AND model_id = ?", "provider:openai", "provider-model").
		Count(&providerRows).Error; err != nil {
		t.Fatal(err)
	}
	if providerRows != 0 {
		t.Fatalf("provider rows after delete = %d, want 0", providerRows)
	}
}

func TestGroupMutationCleansUnreferencedAutomaticPricesWhileAutoSyncDisabled(t *testing.T) {
	fixture := newServiceFixture(t)
	disabled := false
	fixture.service.modelsDevAutoSyncOverride = &disabled
	providerID := "openai"
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		ProviderID:  &providerID,
		UpstreamURL: "https://cleanup.example/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: "automatic-model"},
		}},
		Keys: "sk-cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}
	manualValue := int64(5)
	if err := fixture.db.Create(&models.ModelPrice{
		PriceScopeKey:                     "provider:openai",
		ModelID:                           "manual-unreferenced",
		InputPriceNanoUSDPerMillionTokens: &manualValue,
		IsManual:                          true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.UpdateGroupModels(t.Context(), created.GroupID, GroupModelsUpdateRequest{
		Models: optionalGroupModels{Set: true, Values: []GroupModel{}},
	}); err != nil {
		t.Fatal(err)
	}
	var automaticCount int64
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ? AND model_id = ?", "provider:openai", "automatic-model").
		Count(&automaticCount).Error; err != nil {
		t.Fatal(err)
	}
	if automaticCount != 0 {
		t.Fatalf("unreferenced automatic rows = %d, want 0", automaticCount)
	}
	var manualCount int64
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ? AND model_id = ?", "provider:openai", "manual-unreferenced").
		Count(&manualCount).Error; err != nil {
		t.Fatal(err)
	}
	if manualCount != 1 {
		t.Fatalf("unreferenced manual rows = %d, want 1", manualCount)
	}
}

func TestDeleteProviderGroupRemovesHistoricalCustomScopeAndAutomaticProviderRows(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		UpstreamURL: "https://custom-then-provider.example/v1",
		Protocols:   []protocol.Protocol{protocol.OpenAICompletions},
		Models: optionalGroupModels{Set: true, Values: []GroupModel{
			{ID: "automatic-pending"},
			{ID: "manual-priced"},
			{ID: "manual-unpriced"},
		}},
		Keys: "sk-custom-then-provider",
	})
	if err != nil {
		t.Fatal(err)
	}
	groupScope, _ := pricing.GroupScopeKey(created.GroupID)
	manualPrice := int64(7_000_000_000)
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ? AND model_id = ?", groupScope, "manual-priced").
		Updates(map[string]any{
			"input_price_nano_usd_per_million_tokens": manualPrice,
			"is_manual": true,
		}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ? AND model_id = ?", groupScope, "manual-unpriced").
		Update("is_manual", true).Error; err != nil {
		t.Fatal(err)
	}
	providerID := "openai"
	if _, err := fixture.service.UpdateGroupSettings(t.Context(), created.GroupID, GroupSettingsUpdateRequest{
		ProviderID: optionalField[string]{Set: true, Value: providerID},
	}); err != nil {
		t.Fatalf("UpdateGroupSettings(provider) error = %v", err)
	}

	if err := fixture.service.DeleteGroup(t.Context(), created.GroupID); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
	var groupRows int64
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ?", groupScope).
		Count(&groupRows).Error; err != nil {
		t.Fatal(err)
	}
	if groupRows != 0 {
		t.Fatalf("historical Group-scope rows after delete = %d, want 0", groupRows)
	}
	var providerRows int64
	if err := fixture.db.Model(&models.ModelPrice{}).
		Where("price_scope_key = ?", "provider:openai").
		Count(&providerRows).Error; err != nil {
		t.Fatal(err)
	}
	if providerRows != 0 {
		t.Fatalf("provider rows after delete = %d, want 0", providerRows)
	}
}

func TestEnsureInitialStateRejectsInvalidProviderIDOnDisabledZeroModelGroup(t *testing.T) {
	fixture := newServiceFixture(t)
	invalidProviderID := "OpenAI"
	if err := fixture.db.Create(&models.Group{
		Name:        "corrupt-provider-startup",
		ProviderID:  &invalidProviderID,
		UpstreamURL: "https://corrupt-provider.example/v1",
		Protocols:   models.JSON(`["openai-responses"]`),
		Models:      models.JSON(`[]`),
		Config:      models.JSON(`{}`),
		Enabled:     false,
	}).Error; err != nil {
		t.Fatal(err)
	}
	beforePrices := fixture.priceRuntime.Load()

	if err := fixture.service.EnsureInitialState(t.Context()); err == nil {
		t.Fatal("EnsureInitialState() error = nil, want persisted provider_id corruption")
	}
	if fixture.priceRuntime.Load() != beforePrices {
		t.Fatal("failed startup published a PriceTable")
	}
	assertAccessKeyCount(t, fixture, 0)
	assertBootstrapMarkerCount(t, fixture, 0)
}

func TestEnsureInitialStateWithEmptyCatalogMaterializesPendingPrices(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createPriceTestGroup(t, fixture.db, models.Group{
		Name: "startup", UpstreamURL: "https://startup.example/v1",
		Protocols: models.JSON(`["openai-completions"]`),
		Models:    models.JSON(`[{"id":"startup-model","alias":""}]`),
		Config:    models.JSON(`{}`), Enabled: true,
	})
	if err := fixture.service.EnsureInitialState(context.Background()); err != nil {
		t.Fatalf("EnsureInitialState() error = %v", err)
	}
	scope, _ := pricing.GroupScopeKey(group.ID)
	rule, exists := fixture.priceRuntime.Load().Lookup(pricing.Identity{
		ScopeKey: scope, ModelID: "startup-model",
	})
	if !exists || priceTestPricesHaveValue(rule.Prices) {
		t.Fatalf("startup pending rule = %#v, %t", rule, exists)
	}
}

func TestEnsureInitialStateWithoutCatalogCleansUnreferencedAutomaticPrices(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.catalogRuntime = &catalog.Runtime{}
	manualValue := int64(7)
	for _, row := range []models.ModelPrice{
		{PriceScopeKey: "provider:openai", ModelID: "automatic-orphan"},
		{
			PriceScopeKey: "provider:openai", ModelID: "manual-orphan",
			InputPriceNanoUSDPerMillionTokens: &manualValue,
			IsManual:                          true,
		},
	} {
		if err := fixture.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := fixture.service.EnsureInitialState(t.Context()); err != nil {
		t.Fatal(err)
	}
	var rows []models.ModelPrice
	if err := fixture.db.Order("model_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ModelID != "manual-orphan" || !rows[0].IsManual {
		t.Fatalf("startup price rows = %#v, want only manual orphan", rows)
	}
}

func createPriceTestGroup(t *testing.T, db *gorm.DB, group models.Group) models.Group {
	t.Helper()
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create price test group: %v", err)
	}
	return group
}

func priceTestValue(value pricing.NanoUSD) pricing.Price {
	return pricing.Price{NanoUSDPerMillion: value, Set: true}
}

func priceTestInt64Pointer(value int64) *int64 {
	return &value
}

func priceTestRowHasValue(row models.ModelPrice) bool {
	return row.InputPriceNanoUSDPerMillionTokens != nil ||
		row.OutputPriceNanoUSDPerMillionTokens != nil ||
		row.CacheReadPriceNanoUSDPerMillionTokens != nil ||
		row.CacheWritePriceNanoUSDPerMillionTokens != nil ||
		len(row.ContextPriceTiers) != 0
}

func priceTestPricesHaveValue(prices pricing.Prices) bool {
	return prices.Input.Set || prices.Output.Set || prices.CacheRead.Set || prices.CacheWrite.Set
}

func mustLoadPriceTable(t *testing.T, db *gorm.DB) *pricing.Table {
	t.Helper()
	table, err := loadPriceTable(t.Context(), db)
	if err != nil {
		t.Fatalf("loadPriceTable() error = %v", err)
	}
	return table
}
