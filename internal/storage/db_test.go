package storage_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestCredentialStatusAcceptsOnlyDurableOperatorStates(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "credential-status-parent", ChannelID: "openai_compatible",
		Params: models.JSON(`{"base_url":"https://credential-status.example.com"}`), Models: models.JSON(`[]`),
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create parent group: %v", err)
	}
	for index, status := range []models.CredentialStatus{
		models.CredentialStatusActive,
		models.CredentialStatusDisabled,
	} {
		credential := models.Credential{
			GroupID: group.ID, Data: "encrypted-data",
			Fingerprint: "allowed-credential-status-" + string(rune('a'+index)),
			Status:      status,
		}
		if err := db.Create(&credential).Error; err != nil {
			t.Fatalf("create credential with status %q: %v", status, err)
		}
	}
	invalid := models.Credential{
		GroupID: group.ID, Data: "encrypted-data", Fingerprint: "invalid-credential-status",
		Status: models.CredentialStatus("blacklisted"),
	}
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("create credential with runtime-only blacklisted status error = nil, want constraint error")
	}
}

func TestGroupNormalizesMissingChannelParamsToEmptyObject(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name: "normalized-channel-params", ChannelID: "staged-channel",
		Models: models.JSON(`[]`),
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group without explicit channel params: %v", err)
	}
	var params string
	if err := db.Table("groups").Select("params").Where("id = ?", group.ID).Scan(&params).Error; err != nil {
		t.Fatalf("load normalized channel params: %v", err)
	}
	if params != `{}` {
		t.Fatalf("normalized channel params = %q, want {}", params)
	}
}

func TestAccessKeyStatusAcceptsOnlyDurableOperatorStates(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	for index, status := range []string{"active", "disabled"} {
		key := models.AccessKey{
			Name:      "allowed-" + string(rune('a'+index)),
			KeyValue:  "ciphertext",
			KeyHash:   "allowed-status-" + string(rune('a'+index)),
			KeySuffix: "7f2a",
			Status:    status,
			Filters:   models.JSON(`{}`),
		}
		if err := db.Create(&key).Error; err != nil {
			t.Fatalf("create access key with status %q: %v", status, err)
		}
	}

	invalid := models.AccessKey{
		Name:      "invalid",
		KeyValue:  "ciphertext",
		KeyHash:   "invalid-status",
		KeySuffix: "7f2a",
		Status:    "blacklisted",
		Filters:   models.JSON(`{}`),
	}
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("blacklisted status error = nil, want CHECK constraint error")
	}
}

func TestAutoMigrateCreatesReviewedIndexesAndPrimaryKeys(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	type pragmaColumn struct {
		Name    string
		NotNull int `gorm:"column:notnull"`
	}
	type pragmaIndex struct {
		Name string
	}

	for _, table := range []string{"request_logs", "jobs", "system_settings"} {
		var columns []pragmaColumn
		if err := db.Raw("PRAGMA table_info('" + table + "')").Scan(&columns).Error; err != nil {
			t.Fatalf("inspect %s columns: %v", table, err)
		}

		var found bool
		for _, column := range columns {
			keyName := "id"
			if table == "system_settings" {
				keyName = "key"
			}
			if column.Name == keyName {
				found = true
				if column.NotNull != 1 {
					t.Errorf("%s.%s notnull = %d, want 1", table, keyName, column.NotNull)
				}
			}
		}
		if !found {
			t.Errorf("%s does not contain primary key column", table)
		}
	}

	if db.Migrator().HasTable("upstream_keys") {
		t.Fatal("retired upstream_keys table exists")
	}
	for _, table := range []string{"credentials", "access_keys"} {
		var indexes []pragmaIndex
		if err := db.Raw("PRAGMA index_list('" + table + "')").Scan(&indexes).Error; err != nil {
			t.Fatalf("inspect %s indexes: %v", table, err)
		}
		for _, index := range indexes {
			if index.Name == "idx_"+table+"_status" {
				t.Errorf("%s has ordinary status index %q", table, index.Name)
			}
		}
	}
}

func TestOpenRejectsInvalidDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
	}{
		{name: "empty", dsn: ""},
		{name: "unsupported scheme", dsn: "redis://localhost/gpt-load"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := storage.Open(tt.dsn); err == nil {
				t.Fatalf("Open(%q) error = nil, want a validation error", tt.dsn)
			}
		})
	}
}

func TestOpenCreatesSQLiteDatabase(t *testing.T) {
	t.Parallel()

	dsn := filepath.Join(t.TempDir(), "nested", "gpt-load.db")
	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceManaged)
	if err != nil {
		t.Fatalf("OpenWithSource(%q, managed) error = %v", dsn, err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestOpenWithSourceExternalRejectsMissingParentWithoutCreation(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "operator-must-create")
	dsn := filepath.Join(parent, "external.db")

	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if db != nil {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	}
	if err == nil {
		t.Fatal("OpenWithSource(external) error = nil, want missing-parent rejection")
	}
	if _, statErr := os.Stat(parent); !os.IsNotExist(statErr) {
		t.Fatalf("external parent stat error = %v, want not exist", statErr)
	}
}

func TestOpenAllowsUnversionedFileWithExternalTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external-tables.db")
	db, err := storage.Open(path)
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	if err := db.Exec("CREATE TABLE legacy_data (id integer PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + "-wal"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Remove(path + "-shm"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	reopened, err := storage.Open(path)
	if err != nil {
		t.Fatalf("Open(database with external table) error = %v, want success", err)
	}
	reopenedSQL, err := reopened.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopenedSQL.Close() })
	if err := storage.AutoMigrate(reopened); err != nil {
		t.Fatalf("AutoMigrate(database with external table) error = %v", err)
	}
	if !reopened.Migrator().HasTable("legacy_data") {
		t.Fatal("AutoMigrate() removed the external table")
	}
}

func TestOpenReopensMigratedRelativeDatabase(t *testing.T) {
	workingDir := t.TempDir()
	previousWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousWorkingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	if err := os.Mkdir("data", 0o700); err != nil {
		t.Fatal(err)
	}

	const dsn = "data/gpt-load.db"
	first, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("first Open(relative DSN) error = %v", err)
	}
	if err := storage.AutoMigrate(first); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	firstSQL, err := first.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := firstSQL.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("second Open(relative DSN) error = %v", err)
	}
	secondSQL, err := second.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := secondSQL.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenWithSourceExternalHintDoesNotExposeDSN(t *testing.T) {
	const sensitiveFilename = "distinctive-operator-secret.db"
	dsn := filepath.Join(t.TempDir(), sensitiveFilename)

	var logs bytes.Buffer
	standard := logrus.StandardLogger()
	previousOutput := standard.Out
	standard.SetOutput(&logs)
	t.Cleanup(func() {
		standard.SetOutput(previousOutput)
	})

	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatalf("OpenWithSource(external) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	output := logs.String()
	if !strings.Contains(output, "database_source=external") {
		t.Fatalf("external storage hint = %q, want database_source=external", output)
	}
	for _, forbidden := range []string{dsn, sensitiveFilename} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("external storage hint exposed %q: %s", forbidden, output)
		}
	}
}

func TestOpenWithSourceFileModeMemoryURIStaysInMemory(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "named-memory.db")
	dsn := "file:" + filepath.ToSlash(databasePath) + "?mode=memory&cache=shared"

	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceManaged)
	if err != nil {
		t.Fatalf("OpenWithSource(file mode=memory) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	assertSQLiteMemoryDatabase(t, db)
	for _, path := range []string{
		databasePath,
		databasePath + "-wal",
		databasePath + "-shm",
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("memory URI path stat error = %v, want not exist", statErr)
		}
	}
}

func TestOpenWithSourceColonMemoryQueryStaysInMemory(t *testing.T) {
	db, err := storage.OpenWithSource(
		":memory:?cache=shared",
		config.DatabaseSourceExternal,
	)
	if err != nil {
		t.Fatalf("OpenWithSource(:memory:?cache=shared) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	assertSQLiteMemoryDatabase(t, db)
}

func assertSQLiteMemoryDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.Exec(
		"CREATE TABLE memory_probe (value TEXT NOT NULL)",
	).Error; err != nil {
		t.Fatalf("create memory probe table: %v", err)
	}
	if err := db.Exec(
		"INSERT INTO memory_probe(value) VALUES ('probe')",
	).Error; err != nil {
		t.Fatalf("write memory probe: %v", err)
	}

	var databases []struct {
		Name string
		File string
	}
	if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		t.Fatalf("read database list: %v", err)
	}
	for _, database := range databases {
		if database.Name == "main" {
			if database.File != "" {
				t.Fatalf("main database file = %q, want in-memory database", database.File)
			}
			return
		}
	}
	t.Fatal("PRAGMA database_list did not contain main database")
}

func TestOpenOverridesSQLiteRuntimeOptions(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "runtime.db") +
		"?_txlock=deferred&_pragma=foreign_keys(0)" +
		"&_pragma=busy_timeout(1)&_pragma=journal_mode(DELETE)"
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	var foreignKeys, busyTimeout int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if err := db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") || foreignKeys != 1 || busyTimeout != 5000 {
		t.Fatalf("runtime = journal:%q foreign_keys:%d busy_timeout:%d", journalMode, foreignKeys, busyTimeout)
	}
	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
}

func TestOpenDoesNotForceWALForMemoryDatabase(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	var mode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(mode, "wal") || sqlDB.Stats().MaxOpenConnections != 1 {
		t.Fatalf("memory mode/pool = %q/%d", mode, sqlDB.Stats().MaxOpenConnections)
	}
}

func TestOpenUsesImmediateTransactions(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "immediate.db")
	appDB, err := storage.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	appSQL, err := appDB.DB()
	if err != nil {
		t.Fatalf("app DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := appSQL.Close(); err != nil {
			t.Errorf("close app database: %v", err)
		}
	})
	if err := storage.AutoMigrate(appDB); err != nil {
		t.Fatal(err)
	}
	if err := appDB.Exec("PRAGMA busy_timeout = 1").Error; err != nil {
		t.Fatal(err)
	}

	blockerDB, err := storage.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	blockerSQL, err := blockerDB.DB()
	if err != nil {
		t.Fatalf("blocker DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := blockerSQL.Close(); err != nil {
			t.Errorf("close blocker database: %v", err)
		}
	})
	blockerTx := blockerDB.Begin()
	if blockerTx.Error != nil {
		t.Fatal(blockerTx.Error)
	}
	blockerTxActive := true
	t.Cleanup(func() {
		if !blockerTxActive {
			return
		}
		if err := blockerTx.Rollback().Error; err != nil {
			t.Errorf("rollback blocker transaction during cleanup: %v", err)
		}
	})
	if err := blockerTx.Exec("UPDATE schema_migrations SET id = id").Error; err != nil {
		t.Fatal(err)
	}

	callbackEntered := false
	err = appDB.Transaction(func(*gorm.DB) error {
		callbackEntered = true
		return nil
	})
	if err == nil || callbackEntered {
		t.Fatalf("Transaction() error/callback = %v/%t, want lock before callback", err, callbackEntered)
	}
	if err := blockerTx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	blockerTxActive = false
	callbackEntered = false
	if err := appDB.Transaction(func(*gorm.DB) error {
		callbackEntered = true
		return nil
	}); err != nil || !callbackEntered {
		t.Fatalf("Transaction() after release = %v/%t", err, callbackEntered)
	}
}

func TestOpenConfiguresParameterizedSQLLogging(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	filter, ok := db.Logger.(gorm.ParamsFilter)
	if !ok {
		t.Fatalf("database logger %T does not implement gorm.ParamsFilter", db.Logger)
	}
	const query = "INSERT INTO secrets(value) VALUES (?)"
	const secret = "known-plaintext-secret"
	filteredQuery, params := filter.ParamsFilter(context.Background(), query, secret)
	if filteredQuery != query {
		t.Fatalf("filtered query = %q, want %q", filteredQuery, query)
	}
	if len(params) != 0 {
		t.Fatalf("parameterized SQL logger retained %d parameter(s), want 0", len(params))
	}
}

func TestAutoMigrateCreatesUsageJournalAndMigrationLedger(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)

	wantTables := []string{
		"groups",
		"credentials",
		"access_keys",
		"access_key_cost_limit_rules",
		"access_key_cost_limit_states",
		"request_logs",
		"usage_aggregation_journal",
		"usage_stats",
		"model_prices",
		"system_settings",
		"jobs",
		"control_operations",
		"credential_stages",
		"credential_observations",
		"credential_reset_operations",
		"credential_attempt_stats",
		"schema_migrations",
	}
	for _, table := range wantTables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("AutoMigrate() did not create table %q", table)
		}
	}

	var migrationIDs []string
	if err := db.Table("schema_migrations").Order("id ASC").Pluck("id", &migrationIDs).Error; err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	wantMigrationIDs := []string{
		"0001_initial",
		"0002_access_key_cost_limits",
		"0003_remove_observation_fresh_until",
		"0004_usage_stats_group_activity_index",
		"0005_proxy_config",
	}
	if !reflect.DeepEqual(migrationIDs, wantMigrationIDs) {
		t.Fatalf("schema_migrations IDs = %v, want %v", migrationIDs, wantMigrationIDs)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate() error = %v", err)
	}
	var count int64
	if err := db.Table("schema_migrations").Count(&count).Error; err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != int64(len(wantMigrationIDs)) {
		t.Fatalf("schema_migrations row count after a second migration = %d, want %d", count, len(wantMigrationIDs))
	}
}

func TestAutoMigrateRejectsRetiredV2MigrationLedgers(t *testing.T) {
	for _, retiredID := range []string{"0001_initial_v2", "0001_final_v2"} {
		t.Run(retiredID, func(t *testing.T) {
			db, err := storage.Open(":memory:")
			if err != nil {
				t.Fatalf("Open(:memory:) error = %v", err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("db.DB() error = %v", err)
			}
			t.Cleanup(func() {
				if err := sqlDB.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			})

			if err := db.Exec(`CREATE TABLE schema_migrations (
				id varchar(255) PRIMARY KEY NOT NULL
			)`).Error; err != nil {
				t.Fatalf("create legacy migration ledger: %v", err)
			}
			if err := db.Exec("INSERT INTO schema_migrations(id) VALUES (?)", retiredID).Error; err != nil {
				t.Fatalf("seed legacy migration ledger: %v", err)
			}

			err = storage.AutoMigrate(db)
			if err == nil || !strings.Contains(err.Error(), "unknown or non-contiguous migration") {
				t.Fatalf("AutoMigrate() error = %v, want retired v2 ledger rejection", err)
			}
			if db.Migrator().HasTable("groups") {
				t.Fatal("AutoMigrate() modified a retired v2 database")
			}
		})
	}
}

func TestAutoMigrateRejectsAppliedMigrationWithIncompleteSchema(t *testing.T) {
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := db.Exec(`CREATE TABLE schema_migrations (
		id varchar(255) PRIMARY KEY NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if err := db.Exec("INSERT INTO schema_migrations(id) VALUES ('0001_initial')").Error; err != nil {
		t.Fatalf("seed migration ledger: %v", err)
	}
	if err := db.Exec(`CREATE TABLE groups (
		id integer PRIMARY KEY,
		name varchar(255) NOT NULL,
		channel_id varchar(64) NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create incomplete pre-Beta schema: %v", err)
	}

	err = storage.AutoMigrate(db)
	if err == nil || !strings.Contains(err.Error(), "validate applied migration 0001_initial") {
		t.Fatalf("AutoMigrate() error = %v, want incomplete applied migration rejection", err)
	}
}

func TestAutoMigrateAllowsEmptyLedgerBesideExistingExternalTables(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := db.Exec(`CREATE TABLE schema_migrations (
		id varchar(255) PRIMARY KEY NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create empty migration ledger: %v", err)
	}
	if err := db.Exec("CREATE TABLE legacy_data (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	err = storage.AutoMigrate(db)
	if err != nil {
		t.Fatalf("AutoMigrate() error = %v, want success with external table", err)
	}
	if !db.Migrator().HasTable("groups") {
		t.Fatal("AutoMigrate() did not create the application schema")
	}
	if !db.Migrator().HasTable("legacy_data") {
		t.Fatal("AutoMigrate() removed the external table")
	}
}

func TestAutoMigrateCreatesRequestLogFieldsAndCompositeIndexes(t *testing.T) {
	t.Parallel()

	dsn := filepath.Join(t.TempDir(), "fresh-request-log-v1.db")
	db, err := storage.Open(dsn)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", dsn, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}

	type columnInfo struct {
		Name string
	}
	var columns []columnInfo
	if err := db.Raw("PRAGMA table_info('request_logs')").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect request_logs columns: %v", err)
	}
	columnNames := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		columnNames[column.Name] = struct{}{}
	}
	for _, name := range []string{
		"error_code",
		"error_summary",
		"reasoning_mode",
		"reasoning_effort",
		"reasoning_budget_tokens",
		"upstream_reported_model",
		"model_consistency",
	} {
		if _, ok := columnNames[name]; !ok {
			t.Errorf("request_logs column %q is missing", name)
		}
	}

	type indexInfo struct {
		Name string
	}
	var indexes []indexInfo
	if err := db.Raw("PRAGMA index_list('request_logs')").Scan(&indexes).Error; err != nil {
		t.Fatalf("inspect request_logs indexes: %v", err)
	}
	indexNames := make(map[string]struct{}, len(indexes))
	for _, index := range indexes {
		indexNames[index.Name] = struct{}{}
	}

	wantIndexes := map[string][]struct {
		name string
		desc int
	}{
		"idx_request_logs_completed_id": {
			{name: "completed_at_ms", desc: 1},
			{name: "id", desc: 1},
		},
		"idx_request_logs_access_completed_id": {
			{name: "access_key_id"},
			{name: "completed_at_ms", desc: 1},
			{name: "id", desc: 1},
		},
		"idx_request_logs_status_completed_id": {
			{name: "status"},
			{name: "completed_at_ms", desc: 1},
			{name: "id", desc: 1},
		},
		"idx_request_logs_model_completed_id": {
			{name: "client_model"},
			{name: "completed_at_ms", desc: 1},
			{name: "id", desc: 1},
		},
		"idx_request_logs_upstream_model_completed_id": {
			{name: "upstream_model"},
			{name: "completed_at_ms", desc: 1},
			{name: "id", desc: 1},
		},
	}
	type indexedColumn struct {
		Sequence int    `gorm:"column:seqno"`
		Name     string `gorm:"column:name"`
		Desc     int    `gorm:"column:desc"`
		Key      int    `gorm:"column:key"`
	}
	for indexName, wantColumns := range wantIndexes {
		if _, ok := indexNames[indexName]; !ok {
			t.Errorf("request_logs index %q is missing", indexName)
			continue
		}
		var indexedColumns []indexedColumn
		if err := db.Raw("PRAGMA index_xinfo('" + indexName + "')").Scan(&indexedColumns).Error; err != nil {
			t.Fatalf("inspect %s columns: %v", indexName, err)
		}
		gotColumns := make([]indexedColumn, 0, len(indexedColumns))
		for _, column := range indexedColumns {
			if column.Key == 1 {
				gotColumns = append(gotColumns, column)
			}
		}
		if len(gotColumns) != len(wantColumns) {
			t.Errorf("%s columns = %+v, want %d key columns", indexName, gotColumns, len(wantColumns))
			continue
		}
		for position, want := range wantColumns {
			got := gotColumns[position]
			if got.Sequence != position || got.Name != want.name || got.Desc != want.desc {
				t.Errorf("%s column %d = seq:%d name:%q desc:%d, want seq:%d name:%q desc:%d",
					indexName, position, got.Sequence, got.Name, got.Desc, position, want.name, want.desc)
			}
		}
	}

}

func TestAutoMigrateOmitsGroupSignature(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	type columnInfo struct {
		Name string
	}
	var columns []columnInfo
	if err := db.Raw("PRAGMA table_info('groups')").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect groups columns: %v", err)
	}
	for _, column := range columns {
		if column.Name == "signature" {
			t.Fatal("fresh groups schema still contains signature")
		}
	}
}

func TestAutoMigrateAllowsDuplicateChannelTargets(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	first := models.Group{
		Name:      "group-one",
		ChannelID: "openai_compatible",
		Params:    models.JSON(`{"base_url":"https://same.example.com/v1"}`),
		Models:    models.JSON(`[]`),
		Overrides: models.JSON(`{}`),
		Enabled:   true,
	}
	second := first
	second.Name = "group-two"
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first group: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create group with duplicate channel target: %v", err)
	}
}

func TestAutoMigrateAllowsUnversionedDatabaseWithExternalTables(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := db.Exec("CREATE TABLE legacy_data (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}

	err = storage.AutoMigrate(db)
	if err != nil {
		t.Fatalf("AutoMigrate() error = %v, want success for an unversioned database with external tables", err)
	}
	if !db.Migrator().HasTable("groups") {
		t.Fatal("AutoMigrate() did not create the application schema")
	}
	if !db.Migrator().HasTable("legacy_data") {
		t.Fatal("AutoMigrate() removed the external table")
	}
}

func TestAutoMigrateRejectsFirstInitializationWithExistingGroups(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := db.Exec("CREATE TABLE groups (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create existing groups table: %v", err)
	}

	err = storage.AutoMigrate(db)
	if err == nil || !strings.Contains(err.Error(), "groups table already exists") {
		t.Fatalf("AutoMigrate() error = %v, want existing groups rejection", err)
	}
	if db.Migrator().HasTable("schema_migrations") {
		t.Fatal("AutoMigrate() created the migration ledger after rejecting initial groups table")
	}
}

func TestAutoMigrateCreatesCriticalUniqueConstraints(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)

	t.Run("group name", func(t *testing.T) {
		first := models.Group{
			Name:      "group-one",
			ChannelID: "openai_compatible",
			Params:    models.JSON(`{"base_url":"https://one.example.com"}`),
			Models:    models.JSON(`[]`),
			Overrides: models.JSON(`{}`),
		}
		second := first
		second.ID = 0
		second.Params = models.JSON(`{"base_url":"https://two.example.com"}`)

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("credential group and fingerprint", func(t *testing.T) {
		group := models.Group{
			Name:      "credential-parent",
			ChannelID: "openai_compatible",
			Params:    models.JSON(`{"base_url":"https://credentials.example.com"}`),
			Models:    models.JSON(`[]`),
		}
		if err := db.Create(&group).Error; err != nil {
			t.Fatalf("create credential parent group: %v", err)
		}
		first := models.Credential{
			GroupID: group.ID, Data: "encrypted-one", Fingerprint: "same-fingerprint",
		}
		second := first
		second.ID = 0
		second.Data = "encrypted-two"
		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("model price channel and model", func(t *testing.T) {
		first := models.ModelPrice{ChannelID: "openai", ModelID: "same-model"}
		duplicate := first
		otherChannel := first
		otherChannel.ChannelID = "anthropic"
		if err := db.Create(&first).Error; err != nil {
			t.Fatalf("create model price: %v", err)
		}
		if err := db.Create(&otherChannel).Error; err != nil {
			t.Fatalf("create same model for another channel: %v", err)
		}
		if err := db.Create(&duplicate).Error; err == nil {
			t.Fatal("create duplicate channel/model price error = nil, want unique constraint error")
		}
	})

	t.Run("access key hash", func(t *testing.T) {
		first := models.AccessKey{
			Name:      "access-one",
			KeyValue:  "ciphertext-one",
			KeyHash:   "same-access-key-hash",
			KeySuffix: "7f2a",
			Filters:   models.JSON(`{}`),
		}
		second := first
		second.ID = 0
		second.Name = "access-two"
		second.KeyValue = "ciphertext-two"

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("usage bucket access group and model", func(t *testing.T) {
		bucket := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
		first := models.UsageStat{
			BucketStartMS: bucket.UnixMilli(),
			AccessKeyID:   101,
			GroupID:       202,
			Model:         "model-a",
		}
		second := first
		second.ID = 0

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})
}

func TestAutoMigrateCreatesCredentialForeignKeyWithCascade(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	type foreignKey struct {
		Table    string
		From     string
		To       string
		OnDelete string `gorm:"column:on_delete"`
	}
	var foreignKeys []foreignKey
	if err := db.Raw("PRAGMA foreign_key_list('credentials')").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("inspect credentials foreign keys: %v", err)
	}
	var groupForeignKey *foreignKey
	for index := range foreignKeys {
		candidate := &foreignKeys[index]
		if candidate.Table == "groups" && candidate.From == "group_id" && candidate.To == "id" {
			groupForeignKey = candidate
			break
		}
	}
	if groupForeignKey == nil || groupForeignKey.OnDelete != "CASCADE" {
		t.Fatalf("credentials foreign keys = %+v, want cascading group_id -> groups.id", foreignKeys)
	}

	group := models.Group{
		Name:      "credential-cascade-parent",
		ChannelID: "openai_compatible",
		Params:    models.JSON(`{"base_url":"https://credential-cascade.example.com"}`),
		Models:    models.JSON(`[]`),
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create credential parent group: %v", err)
	}
	credential := models.Credential{
		GroupID: group.ID, Data: "encrypted-data", Fingerprint: "cascade-fingerprint",
	}
	if err := db.Create(&credential).Error; err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if err := db.Delete(&group).Error; err != nil {
		t.Fatalf("delete credential parent group: %v", err)
	}
	var count int64
	if err := db.Model(&models.Credential{}).Where("id = ?", credential.ID).Count(&count).Error; err != nil {
		t.Fatalf("count credential after deleting group: %v", err)
	}
	if count != 0 {
		t.Fatalf("credential count after deleting group = %d, want 0", count)
	}
}

func openMigratedDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return db
}

func assertDuplicateRejected(t *testing.T, firstErr, duplicateErr error) {
	t.Helper()

	if firstErr != nil {
		t.Fatalf("create initial record: %v", firstErr)
	}
	if duplicateErr == nil {
		t.Fatal("create duplicate record error = nil, want unique constraint error")
	}
}
