package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
)

func TestProxyConfigMigrationAddsNullableEncryptedColumnsAndPreservesRows(t *testing.T) {
	db := openInitialTestDatabase(t)
	for _, migrate := range []func() error{
		func() error { return migrations.Up0001(db) },
		func() error { return migrations.Up0002(db) },
		func() error { return migrations.Up0003(db) },
	} {
		if err := migrate(); err != nil {
			t.Fatalf("prepare previous schema: %v", err)
		}
	}

	if err := db.Exec(`INSERT INTO groups (
		id, name, channel_id, connection_type, params, models, enabled, created_at_ms, updated_at_ms
	) VALUES (1, 'proxy migration', 'openai', 'api_key', '{}', '[]', true, 1, 1)`).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Exec(`INSERT INTO credentials (
		id, group_id, data, fingerprint, identity_fingerprint, secret_version,
		auth_state, auth_error_code, status, created_at_ms, updated_at_ms
	) VALUES (1, 1, 'credential-cipher', 'fingerprint', 'identity', 1,
		'ready', '', 'active', 1, 1)`).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}

	if err := migrations.Up0004(db); err != nil {
		t.Fatalf("Up0004() error = %v", err)
	}
	if err := migrations.Validate0004(db); err != nil {
		t.Fatalf("Validate0004() error = %v", err)
	}
	if !db.Migrator().HasColumn("groups", "proxy_config") {
		t.Fatal("groups.proxy_config is missing")
	}
	if !db.Migrator().HasColumn("credentials", "proxy_config") {
		t.Fatal("credentials.proxy_config is missing")
	}

	type proxyRow struct {
		ID          uint
		ProxyConfig *string
	}
	var groupRow, credentialRow proxyRow
	if err := db.Table("groups").Select("id", "proxy_config").Where("id = ?", 1).Take(&groupRow).Error; err != nil {
		t.Fatalf("read group proxy_config: %v", err)
	}
	if err := db.Table("credentials").Select("id", "proxy_config").Where("id = ?", 1).Take(&credentialRow).Error; err != nil {
		t.Fatalf("read credential proxy_config: %v", err)
	}
	if groupRow.ID != 1 || credentialRow.ID != 1 {
		t.Fatalf("existing row IDs after migration = %d/%d, want 1/1", groupRow.ID, credentialRow.ID)
	}
	if groupRow.ProxyConfig != nil || credentialRow.ProxyConfig != nil {
		t.Fatalf("existing rows proxy_config = %v/%v, want NULL/NULL", groupRow.ProxyConfig, credentialRow.ProxyConfig)
	}

	if err := migrations.Up0004(db); err != nil {
		t.Fatalf("second Up0004() error = %v", err)
	}
}

func TestProxyConfigMigrationRecoverableValidationAcceptsPartialColumns(t *testing.T) {
	for _, test := range []struct {
		name      string
		statement string
	}{
		{name: "no_columns"},
		{name: "groups_only", statement: "ALTER TABLE groups ADD COLUMN proxy_config text NULL"},
		{name: "credentials_only", statement: "ALTER TABLE credentials ADD COLUMN proxy_config text NULL"},
		{name: "both_columns", statement: "ALTER TABLE groups ADD COLUMN proxy_config text NULL; ALTER TABLE credentials ADD COLUMN proxy_config text NULL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openInitialTestDatabase(t)
			if err := migrations.Up0001(db); err != nil {
				t.Fatalf("Up0001() error = %v", err)
			}
			if test.statement != "" {
				if err := db.Exec(test.statement).Error; err != nil {
					t.Fatalf("prepare partial schema: %v", err)
				}
			}
			if err := migrations.ValidateRecoverable0004(db); err != nil {
				t.Fatalf("ValidateRecoverable0004() error = %v", err)
			}
			if err := migrations.Up0004(db); err != nil {
				t.Fatalf("Up0004() after partial schema error = %v", err)
			}
			if err := migrations.Validate0004(db); err != nil {
				t.Fatalf("Validate0004() after recovery error = %v", err)
			}
		})
	}
}
