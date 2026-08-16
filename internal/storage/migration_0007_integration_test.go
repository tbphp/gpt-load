package storage

import "testing"

func TestExternalDatabaseModelPriceModeSchedulesMigration(t *testing.T) {
	db := openIsolatedExternalMigrationDatabase(t, "mode_schedule_upgrade")
	if err := applyMigrationRegistry(db, migrations[:6]); err != nil {
		t.Fatalf("apply migrations through 0006: %v", err)
	}
	if err := db.Exec(`INSERT INTO model_prices (
		channel_id, model_id, input_price_nano_usd_per_million_tokens,
		is_manual, created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, ?)`, "openai", "gpt-fast", 2, true, 10, 10).Error; err != nil {
		t.Fatalf("insert pre-0007 model price: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("upgrade through 0007: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("repeat migration chain: %v", err)
	}
	if !db.Migrator().HasColumn("model_prices", "mode_price_schedules") {
		t.Fatal("mode_price_schedules column is missing")
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
		t.Fatalf("migrated model price = %#v", row)
	}
}

func TestExternalDatabaseMySQLInterruptedModelPriceModeSchedulesRecovery(t *testing.T) {
	if externalDatabaseScheme(t) != "mysql" {
		t.Skip("interrupted 0007 recovery is specific to MySQL")
	}

	t.Run("empty partial column", func(t *testing.T) {
		db := openIsolatedExternalMigrationDatabase(t, "mode_schedule_safe")
		if err := applyMigrationRegistry(db, migrations[:6]); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`ALTER TABLE model_prices ADD COLUMN mode_price_schedules JSON NULL`).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&schemaMigration{ID: migrationResumeMarker(migrations[6].ID)}).Error; err != nil {
			t.Fatal(err)
		}
		if err := AutoMigrate(db); err != nil {
			t.Fatalf("recover 0007: %v", err)
		}
		assertInternalMigrationComplete(t, db, []string{
			migrations[0].ID, migrations[1].ID, migrations[2].ID, migrations[3].ID,
			migrations[4].ID, migrations[5].ID, migrations[6].ID,
		})
	})

	t.Run("nonempty partial column", func(t *testing.T) {
		db := openIsolatedExternalMigrationDatabase(t, "mode_schedule_unsafe")
		if err := applyMigrationRegistry(db, migrations[:6]); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`ALTER TABLE model_prices ADD COLUMN mode_price_schedules JSON NULL`).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO model_prices (
			channel_id, model_id, mode_price_schedules, is_manual, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?)`, "openai", "gpt-fast", `{"fast":{"prices":{}}}`, false, 10, 10).Error; err != nil {
			t.Fatal(err)
		}
		if err := AutoMigrate(db); err == nil {
			t.Fatal("unsafe 0007 recovery unexpectedly succeeded")
		}
	})
}
