package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
)

func TestPriorityManualMigrationAddsNullableColumnsAndPreservesRows(t *testing.T) {
	t.Parallel()
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatalf("Up0001() error = %v", err)
	}

	if err := db.Exec(`INSERT INTO groups (
		id, name, channel_id, connection_type, params, models, enabled, created_at_ms, updated_at_ms
	) VALUES (1, 'priority migration', 'openai', 'api_key', '{}', '[]', true, 1, 1)`).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Exec(`INSERT INTO credentials (
		id, group_id, data, fingerprint, identity_fingerprint, secret_version,
		auth_state, auth_error_code, status, created_at_ms, updated_at_ms
	) VALUES (1, 1, 'credential-cipher', 'fingerprint', 'identity', 1,
		'ready', '', 'active', 1, 1)`).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}

	if err := migrations.Up0023(db); err != nil {
		t.Fatalf("Up0023() error = %v", err)
	}
	if err := migrations.Validate0023(db); err != nil {
		t.Fatalf("Validate0023() error = %v", err)
	}
	if !db.Migrator().HasColumn("groups", "priority_manual") {
		t.Fatal("groups.priority_manual is missing")
	}
	if !db.Migrator().HasColumn("credentials", "priority_manual") {
		t.Fatal("credentials.priority_manual is missing")
	}

	type priorityRow struct {
		ID             uint
		PriorityManual *int
	}
	var groupRow, credentialRow priorityRow
	if err := db.Table("groups").Select("id", "priority_manual").Where("id = ?", 1).Take(&groupRow).Error; err != nil {
		t.Fatalf("read group priority_manual: %v", err)
	}
	if err := db.Table("credentials").Select("id", "priority_manual").Where("id = ?", 1).Take(&credentialRow).Error; err != nil {
		t.Fatalf("read credential priority_manual: %v", err)
	}
	if groupRow.ID != 1 || credentialRow.ID != 1 {
		t.Fatalf("existing row IDs after migration = %d/%d, want 1/1", groupRow.ID, credentialRow.ID)
	}
	if groupRow.PriorityManual != nil || credentialRow.PriorityManual != nil {
		t.Fatalf("existing rows priority_manual = %v/%v, want NULL/NULL", groupRow.PriorityManual, credentialRow.PriorityManual)
	}

	if err := migrations.Up0023(db); err != nil {
		t.Fatalf("second Up0023() error = %v", err)
	}
}

func TestPriorityManualMigrationRecoverableValidationAcceptsPartialColumns(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		statement string
	}{
		{name: "no_columns"},
		{name: "groups_only", statement: "ALTER TABLE groups ADD COLUMN priority_manual integer NULL"},
		{name: "credentials_only", statement: "ALTER TABLE credentials ADD COLUMN priority_manual integer NULL"},
		{name: "both_columns", statement: "ALTER TABLE groups ADD COLUMN priority_manual integer NULL; ALTER TABLE credentials ADD COLUMN priority_manual integer NULL"},
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
			if err := migrations.ValidateRecoverable0023(db); err != nil {
				t.Fatalf("ValidateRecoverable0023() error = %v", err)
			}
			if err := migrations.Up0023(db); err != nil {
				t.Fatalf("Up0023() after partial schema error = %v", err)
			}
			if err := migrations.Validate0023(db); err != nil {
				t.Fatalf("Validate0023() after recovery error = %v", err)
			}
		})
	}
}
