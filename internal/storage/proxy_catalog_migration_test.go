package storage

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/encryption"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func TestProxyCatalogMigrationContract(t *testing.T) {
	testProxyCatalogMigration(t, openInternalMigrationTestDatabase)
}

func TestExternalProxyCatalogMigrationContract(t *testing.T) {
	dsn := os.Getenv("GPT_LOAD_DATABASE_TEST_DSN")
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	testProxyCatalogMigration(t, func(t *testing.T) *gorm.DB { return openExternalIncrementalMigrationDatabase(t, dsn) })
}

func testProxyCatalogMigration(t *testing.T, open func(*testing.T) *gorm.DB) {
	t.Run("reject_incompatible", func(t *testing.T) {
		db := open(t)
		if err := applyMigrationRegistry(db, migrations[:24]); err != nil {
			t.Fatal(err)
		}
		if err := migrations[24].Up(db); err != nil {
			t.Fatal(err)
		}
		if err := db.Migrator().DropIndex(&models.Proxy{}, "ux_proxies_fingerprint"); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("CREATE INDEX ux_proxies_fingerprint ON proxies (fingerprint)").Error; err != nil {
			t.Fatal(err)
		}
		if err := AutoMigrate(db); err == nil {
			t.Fatal("migration accepted a non-unique proxy identity index")
		}
	})
	for _, scenario := range []string{"fresh", "upgrade", "interrupted", "missing_indexes"} {
		t.Run(scenario, func(t *testing.T) {
			db := open(t)
			if len(migrations) < 25 {
				t.Fatal("proxy catalog migration is missing")
			}
			if scenario != "fresh" {
				if err := applyMigrationRegistry(db, migrations[:24]); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" || scenario == "missing_indexes" {
				entry := migrations[24]
				up := entry.Up
				entry.Up = func(tx *gorm.DB) error {
					if err := up(tx); err != nil {
						return err
					}
					if scenario == "missing_indexes" {
						for _, name := range []string{"ux_proxies_fingerprint", "idx_proxies_enabled"} {
							if err := tx.Migrator().DropIndex(&models.Proxy{}, name); err != nil {
								return err
							}
						}
					}
					return fmt.Errorf("interrupt proxy migration")
				}
				if err := applyMigrationRegistry(db, append(append([]migration(nil), migrations[:24]...), entry)); err == nil {
					t.Fatal("expected interrupted migration")
				}
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasTable("proxies") {
				t.Fatal("missing proxies table")
			}
			if err := AutoMigrate(db); err != nil {
				t.Fatal(err)
			}
			testProxyDataConversion(t, db)
		})
	}
}

func testProxyDataConversion(t *testing.T, db *gorm.DB) {
	t.Helper()
	crypto, err := encryption.NewService("proxy-catalog-three-driver-contract-key")
	if err != nil {
		t.Fatal(err)
	}
	encrypt := func(raw string) string {
		t.Helper()
		result, err := crypto.Encrypt(raw)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := encrypt(`{"mode":"custom","url":"http://proxy.example:8080"}`)
	second := encrypt(`{"mode":"custom","url":"http://PROXY.EXAMPLE:8080/"}`)
	direct := encrypt(`{"mode":"direct"}`)
	groups := []models.Group{
		{Name: "first", ChannelID: "openai", Params: models.JSON(`{}`), Models: models.JSON(`[]`), ProxyConfig: &first},
		{Name: "second", ChannelID: "openai", Params: models.JSON(`{}`), Models: models.JSON(`[]`), ProxyConfig: &second},
		{Name: "direct", ChannelID: "openai", Params: models.JSON(`{}`), Models: models.JSON(`[]`), ProxyConfig: &direct},
		{Name: "inherit", ChannelID: "openai", Params: models.JSON(`{}`), Models: models.JSON(`[]`)},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	bad := "invalid-encrypted-proxy"
	credential := models.Credential{GroupID: groups[0].ID, Data: encrypt(`{"api_key":"fixture"}`), Fingerprint: "proxy-fixture", ProxyConfig: &bad}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.SystemSetting{Key: outboundproxy.SystemSettingKey, Value: first}).Error; err != nil {
		t.Fatal(err)
	}
	beforeFailure := readProxyMigrationSnapshot(t, db)
	if err := stateloader.MigrateLegacyProxyOverrides(t.Context(), db, crypto); err == nil {
		t.Fatal("corrupt proxy was silently inherited")
	}
	if !reflect.DeepEqual(beforeFailure, readProxyMigrationSnapshot(t, db)) {
		t.Fatal("failed conversion changed source data")
	}
	var count int64
	if err := db.Model(&models.Proxy{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed conversion was not rolled back: %d, %v", count, err)
	}
	if err := db.Model(&credential).Update("proxy_config", second).Error; err != nil {
		t.Fatal(err)
	}
	if err := stateloader.MigrateLegacyProxyOverrides(t.Context(), db, crypto); err != nil {
		t.Fatal(err)
	}
	converted := readProxyMigrationSnapshot(t, db)
	for range 3 {
		if err := AutoMigrate(db); err != nil {
			t.Fatal(err)
		}
		if err := stateloader.MigrateLegacyProxyOverrides(t.Context(), db, crypto); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(converted, readProxyMigrationSnapshot(t, db)) {
			t.Fatal("repeated migration changed proxy rows, references, ciphertext, or timestamps")
		}
	}
	var proxies []models.Proxy
	if err := db.Find(&proxies).Error; err != nil || len(proxies) != 1 {
		t.Fatalf("deduplication: count=%d error=%v", len(proxies), err)
	}
	var stored []models.Group
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		plaintext, err := crypto.Decrypt(*stored[index].ProxyConfig)
		if err != nil {
			t.Fatal(err)
		}
		config, err := outboundproxy.Decode(plaintext)
		if err != nil || config.ProxyID != proxies[0].ID || config.URL != "" {
			t.Fatal("old group was not converted to shared reference")
		}
	}
	if stored[2].ProxyConfig == nil || *stored[2].ProxyConfig != direct || stored[3].ProxyConfig != nil {
		t.Fatal("conversion changed direct/inherited policy")
	}
	for _, ciphertext := range []string{converted.Settings[0].Value, *converted.Credentials[0].ProxyConfig} {
		plaintext, err := crypto.Decrypt(ciphertext)
		if err != nil {
			t.Fatal(err)
		}
		config, err := outboundproxy.Decode(plaintext)
		if err != nil || config.ProxyID != proxies[0].ID || config.URL != "" {
			t.Fatal("global or credential override did not reuse the same proxy reference")
		}
	}
	oldID := proxies[0].ID
	if err := db.Delete(&proxies[0]).Error; err != nil {
		t.Fatal(err)
	}
	replacement := models.Proxy{Name: "replacement", Config: first, Fingerprint: proxies[0].Fingerprint, Enabled: true}
	if err := db.Create(&replacement).Error; err != nil {
		t.Fatal(err)
	}
	if replacement.ID == oldID {
		t.Fatal("deleted proxy identity was reused")
	}
	if err := stateloader.MigrateLegacyProxyOverrides(t.Context(), db, crypto); err != nil {
		t.Fatal(err)
	}
	catalog, err := stateloader.LoadProxyCatalog(t.Context(), db, crypto)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Resolve(&outboundproxy.Config{Mode: outboundproxy.ModeCustom, ProxyID: oldID}) != nil {
		t.Fatal("recreated proxy took over deleted reference")
	}
}

type proxyMigrationSnapshot struct {
	Proxies     []models.Proxy
	Groups      []models.Group
	Credentials []models.Credential
	Settings    []models.SystemSetting
}

func readProxyMigrationSnapshot(t *testing.T, db *gorm.DB) proxyMigrationSnapshot {
	t.Helper()
	var result proxyMigrationSnapshot
	if err := db.Order("id ASC").Find(&result.Proxies).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Order("id ASC").Find(&result.Groups).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Order("id ASC").Find(&result.Credentials).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Order(clause.OrderByColumn{Column: clause.Column{Name: "key"}}).Find(&result.Settings).Error; err != nil {
		t.Fatal(err)
	}
	return result
}
