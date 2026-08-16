package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
)

func TestModelPriceModeSchedulesMigrationAddsNullableJSONColumn(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO model_prices (
		channel_id, model_id, input_price_nano_usd_per_million_tokens,
		is_manual, created_at_ms, updated_at_ms
	) VALUES ('openai', 'gpt-fast', 2, 1, 10, 10)`).Error; err != nil {
		t.Fatal(err)
	}

	if err := migrations.Up0007(db); err != nil {
		t.Fatalf("Up0007() error = %v", err)
	}
	if err := migrations.Validate0007(db); err != nil {
		t.Fatalf("Validate0007() error = %v", err)
	}
	var row struct {
		InputPriceNanoUSDPerMillionTokens *int64
		IsManual                          bool
		ModePriceSchedules                *string
	}
	if err := db.Table("model_prices").Where("model_id = ?", "gpt-fast").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.InputPriceNanoUSDPerMillionTokens == nil || *row.InputPriceNanoUSDPerMillionTokens != 2 ||
		!row.IsManual || row.ModePriceSchedules != nil {
		t.Fatalf("migrated row = %#v", row)
	}
	if err := migrations.Up0007(db); err != nil {
		t.Fatalf("repeat Up0007() error = %v", err)
	}
}

func TestModelPriceModeSchedulesMigrationRejectsUnsafePartialState(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`ALTER TABLE model_prices ADD COLUMN mode_price_schedules JSON NULL`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO model_prices (
		channel_id, model_id, mode_price_schedules, is_manual, created_at_ms, updated_at_ms
	) VALUES ('openai', 'gpt-fast', '{"fast":{"prices":{}}}', 0, 10, 10)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrations.ValidateRecoverable0007(db); err == nil {
		t.Fatal("ValidateRecoverable0007() error = nil")
	}
}
