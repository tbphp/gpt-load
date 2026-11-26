package storage

import "testing"

func TestCredentialQuotaReserveUpgrade(t *testing.T) {
	db := openInternalMigrationTestDatabase(t)
	if err := applyMigrationRegistry(db, migrations[:31]); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("groups").Create(map[string]any{"id": 1, "name": "existing", "channel_id": "openai", "params": "{}", "models": "[]", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("credentials").Create(map[string]any{"id": 1, "group_id": 1, "data": "cipher", "fingerprint": "fingerprint", "identity_fingerprint": "identity", "created_at_ms": 1, "updated_at_ms": 1}).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := AutoMigrate(db); err != nil {
			t.Fatal(err)
		}
		var row struct {
			Data                string
			QuotaReservePercent int
		}
		if err := db.Table("credentials").Select("data", "quota_reserve_percent").Where("id = 1").Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		if row.QuotaReservePercent != 0 || row.Data != "cipher" {
			t.Fatalf("upgrade changed existing credential: %+v", row)
		}
	}
}
