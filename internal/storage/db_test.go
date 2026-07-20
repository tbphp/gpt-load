package storage_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestAutoMigrateKeepsUpstreamRuntimeFailuresOutOfDatabase(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	type columnInfo struct {
		Name         string
		DefaultValue *string `gorm:"column:dflt_value"`
	}
	var columns []columnInfo
	if err := db.Raw("PRAGMA table_info('upstream_keys')").Scan(&columns).Error; err != nil {
		t.Fatalf("inspect upstream_keys columns: %v", err)
	}

	var statusDefault string
	for _, column := range columns {
		if column.Name == "failure_count" {
			t.Fatal("upstream_keys contains failure_count; runtime failure state must not be persisted")
		}
		if column.Name == "status" && column.DefaultValue != nil {
			statusDefault = strings.Trim(*column.DefaultValue, "'\"")
		}
	}
	if statusDefault != string(models.UpstreamKeyStatusActive) {
		t.Fatalf("upstream_keys status default = %q, want %q", statusDefault, models.UpstreamKeyStatusActive)
	}
}

func TestUpstreamKeyStatusAcceptsOnlyDurableOperatorStates(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	group := models.Group{
		Name:        "status-parent",
		UpstreamURL: "https://status.example.com",
		Protocols:   models.JSON(`["openai"]`),
		Models:      models.JSON(`[]`),
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create parent group: %v", err)
	}

	for index, status := range []models.UpstreamKeyStatus{
		models.UpstreamKeyStatusActive,
		models.UpstreamKeyStatusDisabled,
	} {
		key := models.UpstreamKey{
			GroupID:  group.ID,
			KeyValue: "ciphertext",
			KeyHash:  "allowed-status-" + string(rune('a'+index)),
			Status:   status,
		}
		if err := db.Create(&key).Error; err != nil {
			t.Fatalf("create upstream key with status %q: %v", status, err)
		}
	}

	invalid := models.UpstreamKey{
		GroupID:  group.ID,
		KeyValue: "ciphertext",
		KeyHash:  "invalid-status",
		Status:   models.UpstreamKeyStatus("blacklisted"),
	}
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("create upstream key with runtime-only blacklisted status error = nil, want constraint error")
	}
}

func TestAccessKeyStatusAcceptsOnlyDurableOperatorStates(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	for index, status := range []string{"active", "disabled"} {
		key := models.AccessKey{
			Name:     "allowed-" + string(rune('a'+index)),
			KeyValue: "ciphertext",
			KeyHash:  "allowed-status-" + string(rune('a'+index)),
			Status:   status,
			Filters:  models.JSON(`{}`),
		}
		if err := db.Create(&key).Error; err != nil {
			t.Fatalf("create access key with status %q: %v", status, err)
		}
	}

	invalid := models.AccessKey{
		Name:     "invalid",
		KeyValue: "ciphertext",
		KeyHash:  "invalid-status",
		Status:   "blacklisted",
		Filters:  models.JSON(`{}`),
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

	for _, table := range []string{"upstream_keys", "access_keys"} {
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
		{name: "non sqlite scheme", dsn: "postgres://localhost/gpt-load"},
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

	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
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
	if err := blockerTx.Exec("UPDATE schema_info SET version = version").Error; err != nil {
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

func TestAutoMigrateCreatesNineTablesAndSchemaVersion(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)

	wantTables := []string{
		"groups",
		"upstream_keys",
		"access_keys",
		"request_logs",
		"usage_stats",
		"model_prices",
		"system_settings",
		"jobs",
		"schema_info",
	}
	for _, table := range wantTables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("AutoMigrate() did not create table %q", table)
		}
	}

	var versions []uint
	if err := db.Table("schema_info").Pluck("version", &versions).Error; err != nil {
		t.Fatalf("read schema_info: %v", err)
	}
	if len(versions) != 1 || versions[0] != storage.CurrentSchemaVersion {
		t.Fatalf("schema_info versions = %v, want [%d]", versions, storage.CurrentSchemaVersion)
	}

	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate() error = %v", err)
	}
	var count int64
	if err := db.Table("schema_info").Count(&count).Error; err != nil {
		t.Fatalf("count schema_info: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_info row count after a second migration = %d, want 1", count)
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

func TestAutoMigrateAllowsDuplicateUpstreamURLs(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)
	first := models.Group{
		Name:        "group-one",
		UpstreamURL: "https://same.example.com/v1",
		Protocols:   models.JSON(`["openai"]`),
		Models:      models.JSON(`[]`),
		Config:      models.JSON(`{}`),
		Enabled:     true,
	}
	second := first
	second.Name = "group-two"
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first group: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create group with duplicate upstream URL: %v", err)
	}
}

func TestAutoMigrateRejectsNonEmptyDatabaseWithoutSchemaInfo(t *testing.T) {
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
	if err == nil {
		t.Fatal("AutoMigrate() error = nil, want rejection for an unversioned non-empty database")
	}
	if !strings.Contains(err.Error(), "non-empty database without schema_info") {
		t.Fatalf("AutoMigrate() error = %q, want unversioned non-empty database error", err)
	}
	if db.Migrator().HasTable("groups") {
		t.Fatal("AutoMigrate() created groups in an unversioned non-empty database")
	}
	if !db.Migrator().HasTable("legacy_data") {
		t.Fatal("AutoMigrate() removed the pre-existing legacy table")
	}
}

func TestAutoMigrateCreatesCriticalUniqueConstraints(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)

	t.Run("group name", func(t *testing.T) {
		first := models.Group{
			Name:        "group-one",
			UpstreamURL: "https://one.example.com",
			Protocols:   models.JSON(`["openai"]`),
			Models:      models.JSON(`[]`),
			Config:      models.JSON(`{}`),
		}
		second := first
		second.ID = 0
		second.UpstreamURL = "https://two.example.com"

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("upstream key group and hash", func(t *testing.T) {
		group := models.Group{
			Name:        "upstream-key-parent",
			UpstreamURL: "https://keys.example.com",
			Protocols:   models.JSON(`["openai"]`),
			Models:      models.JSON(`[]`),
		}
		if err := db.Create(&group).Error; err != nil {
			t.Fatalf("create parent group: %v", err)
		}

		first := models.UpstreamKey{
			GroupID:  group.ID,
			KeyValue: "ciphertext-one",
			KeyHash:  "same-key-hash",
		}
		second := first
		second.ID = 0
		second.KeyValue = "ciphertext-two"

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("access key hash", func(t *testing.T) {
		first := models.AccessKey{
			Name:     "access-one",
			KeyValue: "ciphertext-one",
			KeyHash:  "same-access-key-hash",
			Filters:  models.JSON(`{}`),
		}
		second := first
		second.ID = 0
		second.Name = "access-two"
		second.KeyValue = "ciphertext-two"

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})

	t.Run("usage hour group and model", func(t *testing.T) {
		bucket := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
		first := models.UsageStat{
			HourBucket: bucket,
			GroupID:    202,
			Model:      "model-a",
		}
		second := first
		second.ID = 0

		assertDuplicateRejected(t, db.Create(&first).Error, db.Create(&second).Error)
	})
}

func TestAutoMigrateCreatesUpstreamKeyForeignKeyWithCascade(t *testing.T) {
	t.Parallel()

	db := openMigratedDatabase(t)

	type foreignKey struct {
		Table    string
		From     string
		To       string
		OnDelete string `gorm:"column:on_delete"`
	}
	var foreignKeys []foreignKey
	if err := db.Raw("PRAGMA foreign_key_list('upstream_keys')").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("inspect upstream_keys foreign keys: %v", err)
	}

	var groupForeignKey *foreignKey
	for i := range foreignKeys {
		if foreignKeys[i].Table == "groups" && foreignKeys[i].From == "group_id" && foreignKeys[i].To == "id" {
			groupForeignKey = &foreignKeys[i]
			break
		}
	}
	if groupForeignKey == nil {
		t.Fatalf("upstream_keys foreign keys = %+v, want group_id -> groups.id", foreignKeys)
	}
	if groupForeignKey.OnDelete != "CASCADE" {
		t.Fatalf("upstream_keys group foreign key ON DELETE = %q, want CASCADE", groupForeignKey.OnDelete)
	}

	group := models.Group{
		Name:        "cascade-parent",
		UpstreamURL: "https://cascade.example.com",
		Protocols:   models.JSON(`["openai"]`),
		Models:      models.JSON(`[]`),
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create parent group: %v", err)
	}
	key := models.UpstreamKey{GroupID: group.ID, KeyValue: "ciphertext", KeyHash: "cascade-key-hash"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("create upstream key: %v", err)
	}
	if err := db.Delete(&group).Error; err != nil {
		t.Fatalf("delete parent group: %v", err)
	}

	var keyCount int64
	if err := db.Model(&models.UpstreamKey{}).Where("id = ?", key.ID).Count(&keyCount).Error; err != nil {
		t.Fatalf("count child key: %v", err)
	}
	if keyCount != 0 {
		t.Fatalf("upstream key count after deleting group = %d, want 0", keyCount)
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
