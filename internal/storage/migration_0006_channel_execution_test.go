package storage_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

type legacyChannelGroup struct {
	ID              uint        `gorm:"primaryKey;autoIncrement"`
	Name            string      `gorm:"type:varchar(255);not null;uniqueIndex"`
	ProviderID      *string     `gorm:"type:varchar(255)"`
	UpstreamURL     string      `gorm:"type:text;not null"`
	Protocols       models.JSON `gorm:"type:json;not null"`
	Models          models.JSON `gorm:"type:json;not null"`
	ConvertEnabled  bool        `gorm:"not null;default:false"`
	WeightManual    *int
	ValidationModel *string     `gorm:"type:varchar(255)"`
	Config          models.JSON `gorm:"type:json"`
	Enabled         bool        `gorm:"not null;default:true"`
	CreatedAtMS     int64       `gorm:"column:created_at_ms;not null"`
	UpdatedAtMS     int64       `gorm:"column:updated_at_ms;not null"`
}

func (legacyChannelGroup) TableName() string { return "groups" }

type legacyChannelKey struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	GroupID      uint   `gorm:"not null;uniqueIndex:idx_upstream_keys_group_hash,priority:1"`
	KeyValue     string `gorm:"type:text;not null"`
	KeyHash      string `gorm:"type:varchar(128);not null;uniqueIndex:idx_upstream_keys_group_hash,priority:2"`
	Status       string `gorm:"type:varchar(32);not null"`
	WeightManual *int
	CreatedAtMS  int64 `gorm:"column:created_at_ms;not null"`
	UpdatedAtMS  int64 `gorm:"column:updated_at_ms;not null"`
}

func (legacyChannelKey) TableName() string { return "upstream_keys" }

type legacyGlobalPrice struct {
	ID                                     uint   `gorm:"primaryKey;autoIncrement"`
	ModelID                                string `gorm:"type:varchar(255);not null;uniqueIndex:idx_model_prices_model"`
	InputPriceNanoUSDPerMillionTokens      *int64
	OutputPriceNanoUSDPerMillionTokens     *int64
	CacheReadPriceNanoUSDPerMillionTokens  *int64
	CacheWritePriceNanoUSDPerMillionTokens *int64
	ContextPriceTiers                      models.JSON `gorm:"type:json"`
	IsManual                               bool        `gorm:"not null;default:false"`
	CreatedAtMS                            int64       `gorm:"column:created_at_ms;not null"`
	UpdatedAtMS                            int64       `gorm:"column:updated_at_ms;not null"`
}

func (legacyGlobalPrice) TableName() string { return "model_prices" }

func TestAutoMigrateUpgradesExistingV2ChannelsCredentialsAndPrices(t *testing.T) {
	db := openInitialV2TestDatabase(t)
	runChannelExecutionMigrationContract(t, db)
	assertChannelMigrationIdentitySequences(t, db)
}

func TestExternalDatabaseChannelExecutionMigration(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_MIGRATION_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_MIGRATION_TEST_DSN is not set")
	}
	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("OpenWithSource() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	tables, err := db.Migrator().GetTables()
	if err != nil {
		t.Fatalf("GetTables() error = %v", err)
	}
	if len(tables) != 0 {
		t.Fatalf("migration contract requires an empty dedicated database, found %v", tables)
	}

	runChannelExecutionMigrationContract(t, db)
	assertChannelMigrationIdentitySequences(t, db)
}

func runChannelExecutionMigrationContract(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(
		&legacyChannelGroup{},
		&legacyChannelKey{},
		&legacyGlobalPrice{},
	); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := db.Exec(`CREATE TABLE schema_migrations (
		id varchar(255) PRIMARY KEY NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	for _, id := range []string{
		"0001_initial_v2",
		"0002_request_log_reasoning",
		"0003_global_model_prices",
		"0004_mysql_model_price_identity",
		"0005_request_log_model_consistency",
	} {
		if err := db.Exec("INSERT INTO schema_migrations(id) VALUES (?)", id).Error; err != nil {
			t.Fatalf("seed migration %s: %v", id, err)
		}
	}
	openAI, deepSeek := "openai", "deepseek"
	groups := []legacyChannelGroup{
		{
			ID: 1, Name: "official", ProviderID: &openAI,
			UpstreamURL: "https://api.openai.com/v1/",
			Protocols:   models.JSON(`["openai-completions","openai-responses"]`),
			Models:      models.JSON(`[{"id":"shared-model"}]`),
			Config:      models.JSON(`{"request_timeout":31}`), Enabled: true,
			CreatedAtMS: 1, UpdatedAtMS: 2,
		},
		{
			ID: 2, Name: "curated", ProviderID: &deepSeek,
			UpstreamURL: "https://api.deepseek.com/v1",
			Protocols:   models.JSON(`["openai-completions","anthropic"]`),
			Models:      models.JSON(`[{"id":"shared-model"}]`), Enabled: true,
			CreatedAtMS: 3, UpdatedAtMS: 4,
		},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatalf("seed legacy groups: %v", err)
	}
	keys := []legacyChannelKey{
		{ID: 11, GroupID: 1, KeyValue: "cipher-openai", KeyHash: "hash-openai", Status: "active", CreatedAtMS: 1, UpdatedAtMS: 2},
		{ID: 21, GroupID: 2, KeyValue: "cipher-deepseek", KeyHash: "hash-deepseek", Status: "disabled", CreatedAtMS: 3, UpdatedAtMS: 4},
	}
	if err := db.Create(&keys).Error; err != nil {
		t.Fatalf("seed legacy keys: %v", err)
	}
	inputPrice, outputPrice := int64(100), int64(200)
	if err := db.Create(&legacyGlobalPrice{
		ID: 31, ModelID: "shared-model",
		InputPriceNanoUSDPerMillionTokens:  &inputPrice,
		OutputPriceNanoUSDPerMillionTokens: &outputPrice,
		IsManual:                           true, CreatedAtMS: 5, UpdatedAtMS: 6,
	}).Error; err != nil {
		t.Fatalf("seed legacy price: %v", err)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	var migratedGroups []models.Group
	if err := db.Order("id ASC").Find(&migratedGroups).Error; err != nil {
		t.Fatalf("load migrated groups: %v", err)
	}
	if len(migratedGroups) != 2 || migratedGroups[0].ChannelID != "openai" ||
		migratedGroups[1].ChannelID != "deepseek" {
		t.Fatalf("migrated groups = %#v", migratedGroups)
	}
	assertMigrationJSON(t, migratedGroups[0].Params, `{}`)
	assertMigrationJSON(t, migratedGroups[0].Overrides, `{"request_timeout":31}`)
	assertMigrationJSON(t, migratedGroups[1].Params, `{}`)
	var credentials []models.Credential
	if err := db.Order("id ASC").Find(&credentials).Error; err != nil {
		t.Fatalf("load migrated credentials: %v", err)
	}
	if got, want := credentials, []models.Credential{
		{ID: 11, GroupID: 1, Data: "cipher-openai", Fingerprint: "hash-openai", Status: models.CredentialStatusActive, CreatedAtMS: 1, UpdatedAtMS: 2},
		{ID: 21, GroupID: 2, Data: "cipher-deepseek", Fingerprint: "hash-deepseek", Status: models.CredentialStatusDisabled, CreatedAtMS: 3, UpdatedAtMS: 4},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated credentials = %#v, want %#v", got, want)
	}
	var prices []models.ModelPrice
	if err := db.Order("channel_id ASC").Find(&prices).Error; err != nil {
		t.Fatalf("load migrated prices: %v", err)
	}
	if len(prices) != 2 || prices[0].ChannelID != "deepseek" || prices[1].ChannelID != "openai" ||
		prices[0].ModelID != "shared-model" || prices[1].ModelID != "shared-model" {
		t.Fatalf("migrated prices = %#v", prices)
	}
	if db.Migrator().HasTable("upstream_keys") {
		t.Fatal("legacy upstream_keys table remains")
	}
	for _, retired := range []string{"provider_id", "upstream_url", "protocols", "convert_enabled", "config"} {
		if db.Migrator().HasColumn("groups", retired) {
			t.Errorf("groups retains %s", retired)
		}
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate() error = %v", err)
	}
	var ledger []string
	if err := db.Table("schema_migrations").Order("id ASC").Pluck("id", &ledger).Error; err != nil {
		t.Fatalf("load migration ledger: %v", err)
	}
	if want := []string{
		"0001_initial_v2", "0002_request_log_reasoning", "0003_global_model_prices",
		"0004_mysql_model_price_identity", "0005_request_log_model_consistency",
		"0006_channel_execution",
	}; !reflect.DeepEqual(ledger, want) {
		t.Fatalf("migration ledger = %#v, want %#v", ledger, want)
	}
}

func assertMigrationJSON(t *testing.T, got models.JSON, want string) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode migrated JSON %q: %v", got, err)
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected JSON %q: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("migrated JSON = %s, want %s", got, want)
	}
}

func assertChannelMigrationIdentitySequences(t *testing.T, db *gorm.DB) {
	t.Helper()
	group := models.Group{
		Name: "post-migration-sequence", ChannelID: "openai", Params: models.JSON(`{}`),
		Models: models.JSON(`[]`), Overrides: models.JSON(`{}`), Enabled: true,
		CreatedAtMS: 7, UpdatedAtMS: 7,
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create post-migration group: %v", err)
	}
	if group.ID <= 2 {
		t.Fatalf("post-migration group id = %d, want > 2", group.ID)
	}
	credential := models.Credential{
		GroupID: group.ID, Data: "cipher-new", Fingerprint: "hash-new",
		Status: models.CredentialStatusActive, CreatedAtMS: 7, UpdatedAtMS: 7,
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("create post-migration credential: %v", err)
	}
	if credential.ID <= 21 {
		t.Fatalf("post-migration credential id = %d, want > 21", credential.ID)
	}
	price := models.ModelPrice{
		ChannelID: "openai", ModelID: "post-migration-model", CreatedAtMS: 7, UpdatedAtMS: 7,
	}
	if err := db.Create(&price).Error; err != nil {
		t.Fatalf("create post-migration price: %v", err)
	}
	if price.ID <= 31 {
		t.Fatalf("post-migration price id = %d, want > 31", price.ID)
	}
}
