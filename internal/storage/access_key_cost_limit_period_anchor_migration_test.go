package storage

import (
	"testing"

	"gpt-load/internal/storage/models"
)

func TestAccessKeyCostLimitPeriodAnchorMigrationPreservesExistingRules(t *testing.T) {
	t.Parallel()
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrationRegistry(db, migrations[:17]); err != nil {
		t.Fatal(err)
	}
	key := models.AccessKey{
		Name: "period-anchor", KeyValue: "cipher", KeyHash: "period-anchor-hash",
		KeySuffix: "cafe", Status: "active", Filters: models.JSON(`{}`),
	}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO access_key_cost_limit_rules
		(access_key_id, kind, limit_nano_usd, period_seconds, rule_revision, created_at_ms, updated_at_ms)
		VALUES (?, 'periodic', 100, 86400, 1, 1000, 1000)`, key.ID).Error; err != nil {
		t.Fatal(err)
	}

	if err := applyMigrationRegistry(db, migrations); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationRegistry(db, migrations); err != nil {
		t.Fatal(err)
	}
	var rule models.AccessKeyCostLimitRule
	if err := db.Where("access_key_id = ?", key.ID).Take(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if rule.PeriodAnchor != models.AccessKeyCostLimitPeriodAnchorFirstRequest ||
		rule.PeriodTimezone != "" || rule.LimitNanoUSD != 100 {
		t.Fatalf("migrated rule = %#v", rule)
	}
	if err := db.Model(&rule).Updates(map[string]any{
		"period_anchor": "calendar_day", "period_timezone": "Asia/Shanghai",
	}).Error; err != nil {
		t.Fatalf("store calendar anchor: %v", err)
	}
	if err := db.Model(&rule).Update("period_anchor", "invalid").Error; err == nil {
		t.Fatal("invalid period anchor was accepted")
	}
}
