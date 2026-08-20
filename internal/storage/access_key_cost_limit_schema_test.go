package storage_test

import (
	"testing"

	"gpt-load/internal/storage/models"
)

func TestAccessKeyCostLimitSchemaEnforcesRuleIdentityAndCascade(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	accessKey := models.AccessKey{
		Name: "quota-schema", KeyValue: "ciphertext", KeyHash: "quota-schema-hash",
		KeySuffix: "cafe", Filters: models.JSON(`{}`),
	}
	if err := db.Create(&accessKey).Error; err != nil {
		t.Fatalf("create access key: %v", err)
	}

	if err := db.Exec(`INSERT INTO access_key_cost_limit_rules
		(access_key_id, kind, limit_nano_usd, period_seconds, rule_revision, created_at_ms, updated_at_ms)
		VALUES (?, 'total', 1000000000, 0, 1, 0, 0)`, accessKey.ID).Error; err != nil {
		t.Fatalf("create total rule: %v", err)
	}
	if err := db.Exec(`INSERT INTO access_key_cost_limit_rules
		(access_key_id, kind, limit_nano_usd, period_seconds, rule_revision, created_at_ms, updated_at_ms)
		VALUES (?, 'total', 2000000000, 0, 1, 0, 0)`, accessKey.ID).Error; err == nil {
		t.Fatal("create duplicate total rule error = nil, want unique constraint error")
	}
	if err := db.Exec(`INSERT INTO access_key_cost_limit_rules
		(access_key_id, kind, limit_nano_usd, period_seconds, rule_revision, created_at_ms, updated_at_ms)
		VALUES (?, 'periodic', 500000000, 18000, 1, 0, 0)`, accessKey.ID).Error; err != nil {
		t.Fatalf("create periodic rule: %v", err)
	}
	if err := db.Exec(`INSERT INTO access_key_cost_limit_rules
		(access_key_id, kind, limit_nano_usd, period_seconds, rule_revision, created_at_ms, updated_at_ms)
		VALUES (?, 'periodic', 600000000, 18000, 1, 0, 0)`, accessKey.ID).Error; err == nil {
		t.Fatal("create duplicate periodic duration error = nil, want unique constraint error")
	}

	var ruleID uint
	if err := db.Raw(`SELECT id FROM access_key_cost_limit_rules
		WHERE access_key_id = ? AND kind = 'periodic'`, accessKey.ID).Scan(&ruleID).Error; err != nil {
		t.Fatalf("read periodic rule: %v", err)
	}
	if ruleID == 0 {
		t.Fatal("periodic rule ID = 0")
	}
	if err := db.Exec(`INSERT INTO access_key_cost_limit_states
		(rule_id, rule_revision, used_nano_usd, window_started_at_ms, window_ends_at_ms,
		 window_generation, snapshot_version, updated_at_ms)
		VALUES (?, 1, 0, NULL, NULL, 0, 1, 0)`, ruleID).Error; err != nil {
		t.Fatalf("create periodic state: %v", err)
	}

	if err := db.Delete(&accessKey).Error; err != nil {
		t.Fatalf("delete access key: %v", err)
	}
	for _, table := range []string{"access_key_cost_limit_rules", "access_key_cost_limit_states"} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			t.Fatalf("count %s after cascade: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count after cascade = %d, want 0", table, count)
		}
	}
}

func TestAccessKeyCostLimitSchemaRejectsInvalidRuleAndWindowState(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	accessKey := models.AccessKey{
		Name: "quota-validation", KeyValue: "ciphertext", KeyHash: "quota-validation-hash",
		KeySuffix: "beef", Filters: models.JSON(`{}`),
	}
	if err := db.Create(&accessKey).Error; err != nil {
		t.Fatalf("create access key: %v", err)
	}

	invalidRules := []string{
		`VALUES (?, 'total', 0, 0, 1, 0, 0)`,
		`VALUES (?, 'total', 1, 60, 1, 0, 0)`,
		`VALUES (?, 'periodic', 1, 59, 1, 0, 0)`,
		`VALUES (?, 'periodic', 1, 31536001, 1, 0, 0)`,
		`VALUES (?, 'unknown', 1, 0, 1, 0, 0)`,
	}
	for _, values := range invalidRules {
		query := `INSERT INTO access_key_cost_limit_rules
			(access_key_id, kind, limit_nano_usd, period_seconds, rule_revision, created_at_ms, updated_at_ms) ` + values
		if err := db.Exec(query, accessKey.ID).Error; err == nil {
			t.Fatalf("invalid rule %q error = nil", values)
		}
	}

	if err := db.Exec(`INSERT INTO access_key_cost_limit_rules
		(access_key_id, kind, limit_nano_usd, period_seconds, rule_revision, created_at_ms, updated_at_ms)
		VALUES (?, 'periodic', 1, 300, 1, 0, 0)`, accessKey.ID).Error; err != nil {
		t.Fatalf("create valid rule: %v", err)
	}
	var ruleID uint
	if err := db.Raw(`SELECT id FROM access_key_cost_limit_rules WHERE access_key_id = ?`, accessKey.ID).
		Scan(&ruleID).Error; err != nil {
		t.Fatalf("read rule: %v", err)
	}

	invalidStates := []string{
		`VALUES (?, 1, -1, NULL, NULL, 0, 1, 0)`,
		`VALUES (?, 1, 0, 100, NULL, 1, 1, 0)`,
		`VALUES (?, 1, 0, 100, 100, 1, 1, 0)`,
		`VALUES (?, 0, 0, NULL, NULL, 0, 1, 0)`,
		`VALUES (?, 1, 0, NULL, NULL, 0, 0, 0)`,
	}
	for _, values := range invalidStates {
		query := `INSERT INTO access_key_cost_limit_states
			(rule_id, rule_revision, used_nano_usd, window_started_at_ms, window_ends_at_ms,
			 window_generation, snapshot_version, updated_at_ms) ` + values
		if err := db.Exec(query, ruleID).Error; err == nil {
			t.Fatalf("invalid state %q error = nil", values)
		}
	}
}
