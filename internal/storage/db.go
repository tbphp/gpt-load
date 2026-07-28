package storage

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/securefile"
	"gpt-load/internal/storage/models"
)

// CurrentSchemaVersion identifies the SQLite schema supported by this binary.
const CurrentSchemaVersion uint = 2

const sqliteBusyTimeoutMS = 5000

type schemaInfo struct {
	Version uint `gorm:"primaryKey;autoIncrement:false"`
}

type sqliteTarget struct {
	fileBacked   bool
	databasePath string
	directory    string
}

var databaseLogger = logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
	SlowThreshold:        200 * time.Millisecond,
	LogLevel:             logger.Warn,
	ParameterizedQueries: true,
	Colorful:             true,
})

var hardenManagedFileIfExists = securefile.HardenManagedFileIfExists

func (schemaInfo) TableName() string {
	return "schema_info"
}

// Open opens a SQLite database using a fully resolved DSN.
// Resolving an empty DSN to DATA_DIR belongs to platform/config.
func Open(dsn string) (*gorm.DB, error) {
	return OpenWithSource(dsn, config.DatabaseSourceExternal)
}

// OpenWithSource opens a SQLite database and applies file controls only when
// the application owns the managed database location.
func OpenWithSource(dsn string, source config.DatabaseSource) (*gorm.DB, error) {
	dsn = strings.TrimSpace(dsn)
	target, err := parseSQLiteTarget(dsn)
	if err != nil {
		return nil, err
	}
	switch source {
	case config.DatabaseSourceManaged:
		if target.fileBacked {
			if err := secureManagedSQLiteTarget(target); err != nil {
				return nil, err
			}
		}
	case config.DatabaseSourceExternal:
		logrus.WithField("database_source", config.DatabaseSourceExternal).
			Info("SQLite database storage is managed by the operator")
	default:
		return nil, fmt.Errorf("open SQLite database: unsupported database source")
	}

	runtimeDSN, err := withSQLiteRuntimeOptions(dsn, target.fileBacked)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(runtimeDSN), &gorm.Config{Logger: databaseLogger})
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get SQLite connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := verifySQLiteRuntime(db, target.fileBacked); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	if source == config.DatabaseSourceManaged && target.fileBacked {
		if err := hardenSQLiteRecoverySet(target); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}

	return db, nil
}

// AutoMigrate creates the current persistence schema and initializes schema_info.
func AutoMigrate(db *gorm.DB) error {
	return autoMigrate(db, nil, "")
}

// AutoMigrateWithEncryption upgrades credential-bearing legacy schemas before
// runtime loading. dataDir is used only to back up a managed encryption.key.
func AutoMigrateWithEncryption(
	db *gorm.DB,
	encryptionService encryption.Service,
	dataDir string,
) error {
	return autoMigrate(db, encryptionService, dataDir)
}

func autoMigrate(
	db *gorm.DB,
	encryptionService encryption.Service,
	dataDir string,
) error {
	if db == nil {
		return fmt.Errorf("auto-migrate SQLite database: db is nil")
	}

	if !db.Migrator().HasTable(&schemaInfo{}) {
		tables, err := db.Migrator().GetTables()
		if err != nil {
			return fmt.Errorf("list SQLite tables: %w", err)
		}
		for _, table := range tables {
			if !strings.HasPrefix(table, "sqlite_") {
				return fmt.Errorf("initialize SQLite schema: non-empty database without schema_info")
			}
		}
		return db.Transaction(func(tx *gorm.DB) error {
			if err := migrateCurrentSchema(tx); err != nil {
				return err
			}
			if err := tx.Create(&schemaInfo{Version: CurrentSchemaVersion}).Error; err != nil {
				return fmt.Errorf("initialize schema_info: %w", err)
			}
			return nil
		})
	}

	version, err := readSchemaVersion(db)
	if err != nil {
		return err
	}
	switch version {
	case CurrentSchemaVersion:
		return db.Transaction(migrateCurrentSchema)
	case 1:
		var accessKeyCount int64
		if db.Migrator().HasTable(&models.AccessKey{}) {
			if err := db.Table("access_keys").Count(&accessKeyCount).Error; err != nil {
				return fmt.Errorf("count access_keys before schema upgrade: %w", err)
			}
		}
		if accessKeyCount > 0 && encryptionService == nil {
			return fmt.Errorf(
				"upgrade SQLite schema version 1: encryption service is required",
			)
		}
		if err := backupSchemaV1(db, dataDir); err != nil {
			return err
		}
		return db.Transaction(func(tx *gorm.DB) error {
			if err := migrateAccessKeysV1ToV2(tx, encryptionService); err != nil {
				return err
			}
			if err := migrateCurrentSchema(tx); err != nil {
				return err
			}
			result := tx.Model(&schemaInfo{}).
				Where("version = ?", uint(1)).
				Update("version", CurrentSchemaVersion)
			if result.Error != nil {
				return fmt.Errorf("update schema_info: %w", result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("update schema_info: version changed concurrently")
			}
			return nil
		})
	default:
		return fmt.Errorf(
			"unsupported schema version %d, want %d",
			version,
			CurrentSchemaVersion,
		)
	}
}

func migrateCurrentSchema(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&models.Group{},
		&models.UpstreamKey{},
		&models.AccessKey{},
		&models.RequestLog{},
		&models.UsageStat{},
		&models.ModelPrice{},
		&models.SystemSetting{},
		&models.Job{},
		&models.ControlOperation{},
		&schemaInfo{},
	); err != nil {
		return fmt.Errorf("auto-migrate SQLite schema: %w", err)
	}
	if err := rebuildModelPricesIfNeeded(db); err != nil {
		return err
	}
	return nil
}

func rebuildModelPricesIfNeeded(db *gorm.DB) error {
	if !db.Migrator().HasTable(&models.ModelPrice{}) {
		return nil
	}

	type columnInfo struct {
		Name    string
		NotNull int `gorm:"column:notnull"`
	}
	var columns []columnInfo
	if err := db.Raw("PRAGMA table_info('model_prices')").Scan(&columns).Error; err != nil {
		return fmt.Errorf("inspect model_prices columns: %w", err)
	}
	for _, column := range columns {
		switch column.Name {
		case "input_price", "output_price", "cache_read_price", "cache_write_5m_price", "cache_write_1h_price":
			if column.NotNull != 0 {
				return rebuildModelPrices(db)
			}
		}
	}
	return nil
}

func rebuildModelPrices(db *gorm.DB) error {
	if err := db.Exec(`CREATE TABLE model_prices__m4_rebuild (
	id integer PRIMARY KEY AUTOINCREMENT,
	pattern varchar(255) NOT NULL,
	input_price real,
	output_price real,
	cache_read_price real,
	cache_write_5m_price real,
	cache_write_1h_price real,
	source varchar(32) NOT NULL,
	created_at datetime,
	updated_at datetime,
	CONSTRAINT chk_model_price_source CHECK (source = 'user')
)`).Error; err != nil {
		return fmt.Errorf("create rebuilt model_prices table: %w", err)
	}
	if err := db.Exec(`INSERT INTO model_prices__m4_rebuild (
	id, pattern, input_price, output_price, cache_read_price, cache_write_5m_price, cache_write_1h_price, source, created_at, updated_at
)
SELECT id, pattern, input_price, output_price, cache_read_price, cache_write_5m_price, cache_write_1h_price, source, created_at, updated_at
FROM model_prices`).Error; err != nil {
		return fmt.Errorf("copy model_prices into rebuilt table: %w", err)
	}
	if err := db.Exec("DROP TABLE model_prices").Error; err != nil {
		return fmt.Errorf("drop previous model_prices table: %w", err)
	}
	if err := db.Exec("ALTER TABLE model_prices__m4_rebuild RENAME TO model_prices").Error; err != nil {
		return fmt.Errorf("rename rebuilt model_prices table: %w", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_model_prices_pattern ON model_prices(pattern)").Error; err != nil {
		return fmt.Errorf("create rebuilt model_prices Pattern index: %w", err)
	}
	return nil
}

func validateSchemaVersion(db *gorm.DB) error {
	version, err := readSchemaVersion(db)
	if err != nil {
		return err
	}
	if version != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d, want %d", version, CurrentSchemaVersion)
	}
	return nil
}

func readSchemaVersion(db *gorm.DB) (uint, error) {
	var count int64
	if err := db.Model(&schemaInfo{}).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count schema_info rows: %w", err)
	}
	if count != 1 {
		return 0, fmt.Errorf("schema_info contains %d rows, want exactly one", count)
	}

	var info schemaInfo
	if err := db.First(&info).Error; err != nil {
		return 0, fmt.Errorf("read schema_info: %w", err)
	}
	return info.Version, nil
}

func migrateAccessKeysV1ToV2(
	db *gorm.DB,
	encryptionService encryption.Service,
) error {
	if !db.Migrator().HasTable(&models.AccessKey{}) {
		return nil
	}
	if db.Migrator().HasColumn(&models.AccessKey{}, "key_suffix") {
		return fmt.Errorf("upgrade access_keys schema: unexpected key_suffix column")
	}
	if err := db.Exec("ALTER TABLE access_keys ADD COLUMN key_suffix char(4)").Error; err != nil {
		return fmt.Errorf("add access_keys key_suffix: %w", err)
	}

	type encryptedAccessKey struct {
		ID       uint
		KeyValue string `gorm:"column:key_value"`
	}
	var rows []encryptedAccessKey
	if err := db.Table("access_keys").
		Select("id", "key_value").
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return fmt.Errorf("read access_keys for schema upgrade: %w", err)
	}
	for _, row := range rows {
		if encryptionService == nil {
			return fmt.Errorf("upgrade access_keys schema: encryption service is required")
		}
		plaintext, err := encryptionService.Decrypt(row.KeyValue)
		if err != nil || !validAccessKeyPlaintext(plaintext) {
			return fmt.Errorf("upgrade access_keys row %d: credential validation failed", row.ID)
		}
		suffix := plaintext[len(plaintext)-4:]
		result := db.Table("access_keys").
			Where("id = ? AND key_suffix IS NULL", row.ID).
			Update("key_suffix", suffix)
		if result.Error != nil {
			return fmt.Errorf("backfill access_keys key_suffix: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("backfill access_keys key_suffix: row changed concurrently")
		}
	}

	if err := db.Exec(`CREATE TABLE access_keys__v2 (
		id integer PRIMARY KEY AUTOINCREMENT,
		name varchar(255) NOT NULL,
		key_value text NOT NULL,
		key_hash varchar(128) NOT NULL,
		key_suffix char(4) NOT NULL,
		status varchar(32) NOT NULL DEFAULT 'active',
		filters json,
		rpm_limit integer NOT NULL DEFAULT 0,
		daily_cost_limit real NOT NULL DEFAULT 0,
		monthly_cost_limit real NOT NULL DEFAULT 0,
		created_at datetime,
		updated_at datetime,
		CONSTRAINT chk_access_key_suffix
			CHECK (key_suffix GLOB '[0-9a-f][0-9a-f][0-9a-f][0-9a-f]'),
		CONSTRAINT chk_access_key_status
			CHECK (status IN ('active','disabled'))
	)`).Error; err != nil {
		return fmt.Errorf("create rebuilt access_keys table: %w", err)
	}
	if err := db.Exec(`INSERT INTO access_keys__v2 (
		id,name,key_value,key_hash,key_suffix,status,filters,rpm_limit,
		daily_cost_limit,monthly_cost_limit,created_at,updated_at
	)
	SELECT
		id,name,key_value,key_hash,key_suffix,status,filters,rpm_limit,
		daily_cost_limit,monthly_cost_limit,created_at,updated_at
	FROM access_keys`).Error; err != nil {
		return fmt.Errorf("copy rebuilt access_keys table: %w", err)
	}
	if err := db.Exec("DROP TABLE access_keys").Error; err != nil {
		return fmt.Errorf("drop previous access_keys table: %w", err)
	}
	if err := db.Exec("ALTER TABLE access_keys__v2 RENAME TO access_keys").Error; err != nil {
		return fmt.Errorf("rename rebuilt access_keys table: %w", err)
	}
	if err := db.Exec(
		"CREATE UNIQUE INDEX idx_access_keys_key_hash ON access_keys(key_hash)",
	).Error; err != nil {
		return fmt.Errorf("create rebuilt access_keys key hash index: %w", err)
	}
	return nil
}

func validAccessKeyPlaintext(value string) bool {
	const accessKeyPrefix = "sk-gl-"
	if len(value) != len(accessKeyPrefix)+32 || !strings.HasPrefix(value, accessKeyPrefix) {
		return false
	}
	for _, character := range value[len(accessKeyPrefix):] {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func backupSchemaV1(db *gorm.DB, dataDir string) error {
	type databaseInfo struct {
		Name string
		File string
	}
	var databases []databaseInfo
	if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		return fmt.Errorf("inspect SQLite database for schema backup: %w", err)
	}
	var databasePath string
	for _, database := range databases {
		if database.Name == "main" {
			databasePath = database.File
			break
		}
	}
	if databasePath == "" {
		return nil
	}

	suffix, err := schemaBackupSuffix()
	if err != nil {
		return fmt.Errorf("prepare schema backup identity: %w", err)
	}
	databaseBackup := databasePath + ".schema-v1-backup-" + suffix
	escapedBackup := strings.ReplaceAll(databaseBackup, "'", "''")
	if err := db.Exec("VACUUM INTO '" + escapedBackup + "'").Error; err != nil {
		return fmt.Errorf("back up SQLite schema version 1: %w", err)
	}
	if err := securefile.HardenManagedFileIfExists(databaseBackup); err != nil {
		return fmt.Errorf("secure SQLite schema backup: %w", err)
	}

	if dataDir == "" {
		return nil
	}
	keyPath := filepath.Join(dataDir, encryption.KeyFileName)
	if _, err := os.Lstat(keyPath); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect encryption key for schema backup: %w", err)
	}
	keyBackup := keyPath + ".schema-v1-backup-" + suffix
	if err := copySchemaBackup(keyPath, keyBackup); err != nil {
		return err
	}
	return nil
}

func schemaBackupSuffix() (string, error) {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405.000000000Z") +
		"-" + hex.EncodeToString(randomBytes), nil
}

func copySchemaBackup(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open encryption key for schema backup: %w", err)
	}
	defer func() {
		_ = source.Close()
	}()
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("inspect encryption key for schema backup")
	}

	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create encryption key schema backup: %w", err)
	}
	success := false
	defer func() {
		_ = target.Close()
		if !success {
			_ = os.Remove(targetPath)
		}
	}()
	if _, err := io.Copy(target, source); err != nil {
		return fmt.Errorf("copy encryption key schema backup: %w", err)
	}
	if err := target.Sync(); err != nil {
		return fmt.Errorf("sync encryption key schema backup: %w", err)
	}
	if err := target.Close(); err != nil {
		return fmt.Errorf("close encryption key schema backup: %w", err)
	}
	if err := securefile.HardenManagedFileIfExists(targetPath); err != nil {
		return fmt.Errorf("secure encryption key schema backup: %w", err)
	}
	success = true
	return nil
}

func withSQLiteRuntimeOptions(dsn string, fileBacked bool) (string, error) {
	base, rawQuery, _ := strings.Cut(dsn, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("open SQLite database: invalid DSN query: %w", err)
	}
	query.Set("_txlock", "immediate")

	pragmas := make([]string, 0, len(query["_pragma"])+3)
	for _, pragma := range query["_pragma"] {
		name := strings.ToLower(strings.TrimSpace(pragma))
		if index := strings.IndexAny(name, "(="); index >= 0 {
			name = strings.TrimSpace(name[:index])
		}
		switch name {
		case "foreign_keys", "busy_timeout", "journal_mode":
			continue
		default:
			pragmas = append(pragmas, pragma)
		}
	}
	pragmas = append(pragmas,
		"foreign_keys(1)",
		fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMS),
	)
	if fileBacked {
		pragmas = append(pragmas, "journal_mode(WAL)")
	}
	query["_pragma"] = pragmas
	return base + "?" + query.Encode(), nil
}

func isSQLiteMemoryDSN(dsn string) bool {
	base, _, _ := strings.Cut(dsn, "?")
	if base == ":memory:" {
		return true
	}
	if !strings.HasPrefix(dsn, "file:") {
		return false
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Query().Get("mode"), "memory") {
		return true
	}
	return parsed.Path == ":memory:" || parsed.Opaque == ":memory:"
}

func verifySQLiteRuntime(db *gorm.DB, fileBacked bool) error {
	var foreignKeys, busyTimeout int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		return fmt.Errorf("verify SQLite foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("verify SQLite foreign_keys: got %d, want 1", foreignKeys)
	}
	if err := db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		return fmt.Errorf("verify SQLite busy_timeout: %w", err)
	}
	if busyTimeout != sqliteBusyTimeoutMS {
		return fmt.Errorf("verify SQLite busy_timeout: got %d, want %d", busyTimeout, sqliteBusyTimeoutMS)
	}
	if fileBacked {
		var journalMode string
		if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
			return fmt.Errorf("verify SQLite journal_mode: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			return fmt.Errorf("verify SQLite journal_mode: got %q, want wal", journalMode)
		}
	}
	return nil
}

func parseSQLiteTarget(dsn string) (sqliteTarget, error) {
	if isSQLiteMemoryDSN(dsn) {
		return sqliteTarget{}, nil
	}
	if err := validateSQLiteDSN(dsn); err != nil {
		return sqliteTarget{}, err
	}

	databasePath, _, _ := strings.Cut(dsn, "?")
	parsed, err := url.Parse(dsn)
	if err != nil {
		return sqliteTarget{}, fmt.Errorf("open SQLite database: invalid DSN: %w", err)
	}
	if strings.HasPrefix(dsn, "file:") {
		databasePath = parsed.Path
		if databasePath == "" {
			databasePath = parsed.Opaque
			databasePath, err = url.PathUnescape(databasePath)
			if err != nil {
				return sqliteTarget{}, fmt.Errorf("open SQLite database: invalid file URI: %w", err)
			}
		}
		if runtime.GOOS == "windows" &&
			len(databasePath) >= 3 &&
			databasePath[0] == '/' &&
			databasePath[2] == ':' &&
			(databasePath[1] >= 'A' && databasePath[1] <= 'Z' ||
				databasePath[1] >= 'a' && databasePath[1] <= 'z') {
			databasePath = databasePath[1:]
		}
		databasePath = filepath.FromSlash(databasePath)
	}
	if databasePath == "" {
		return sqliteTarget{}, fmt.Errorf("open SQLite database: file path is empty")
	}

	directory := filepath.Dir(databasePath)
	return sqliteTarget{
		fileBacked:   true,
		databasePath: databasePath,
		directory:    directory,
	}, nil
}

func secureManagedSQLiteTarget(target sqliteTarget) error {
	if err := securefile.PrepareManagedDataDir(target.directory); err != nil {
		return fmt.Errorf("secure managed SQLite directory: %w", err)
	}
	return hardenSQLiteRecoverySet(target)
}

func hardenSQLiteRecoverySet(target sqliteTarget) error {
	for _, path := range []string{
		target.databasePath,
		target.databasePath + "-wal",
		target.databasePath + "-shm",
	} {
		if err := hardenManagedFileIfExists(path); err != nil {
			return fmt.Errorf("secure managed SQLite recovery set: %w", err)
		}
	}
	return nil
}

func validateSQLiteDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("open SQLite database: DSN is empty")
	}
	if dsn == ":memory:" || filepath.VolumeName(dsn) != "" {
		return nil
	}

	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("open SQLite database: invalid DSN: %w", err)
	}
	if parsed.Scheme == "file" && !strings.HasPrefix(dsn, "file:") {
		return fmt.Errorf("open SQLite database: unsupported non-canonical DSN scheme")
	}
	if parsed.Scheme != "" && parsed.Scheme != "file" {
		return fmt.Errorf("open SQLite database: unsupported DSN scheme %q", parsed.Scheme)
	}
	return nil
}
