package storage

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/storage/models"
)

// CurrentSchemaVersion identifies the SQLite schema supported by this binary.
const CurrentSchemaVersion uint = 1

const sqliteBusyTimeoutMS = 5000

type schemaInfo struct {
	Version uint `gorm:"primaryKey;autoIncrement:false"`
}

var databaseLogger = logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), logger.Config{
	SlowThreshold:        200 * time.Millisecond,
	LogLevel:             logger.Warn,
	ParameterizedQueries: true,
	Colorful:             true,
})

func (schemaInfo) TableName() string {
	return "schema_info"
}

// Open opens a SQLite database using a fully resolved DSN.
// Resolving an empty DSN to DATA_DIR belongs to platform/config.
func Open(dsn string) (*gorm.DB, error) {
	dsn = strings.TrimSpace(dsn)
	if err := validateSQLiteDSN(dsn); err != nil {
		return nil, err
	}
	if err := ensureSQLiteDirectory(dsn); err != nil {
		return nil, err
	}

	fileBacked := !isSQLiteMemoryDSN(dsn)
	runtimeDSN, err := withSQLiteRuntimeOptions(dsn, fileBacked)
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
	if err := verifySQLiteRuntime(db, fileBacked); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	return db, nil
}

// AutoMigrate creates the current persistence schema and initializes schema_info.
func AutoMigrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("auto-migrate SQLite database: db is nil")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Migrator().HasTable(&schemaInfo{}) {
			if err := validateSchemaVersion(tx); err != nil {
				return err
			}
			return migrateCurrentSchema(tx)
		}

		tables, err := tx.Migrator().GetTables()
		if err != nil {
			return fmt.Errorf("list SQLite tables: %w", err)
		}
		for _, table := range tables {
			if !strings.HasPrefix(table, "sqlite_") {
				return fmt.Errorf("initialize SQLite schema: non-empty database without schema_info")
			}
		}

		if err := migrateCurrentSchema(tx); err != nil {
			return err
		}
		if err := tx.Create(&schemaInfo{Version: CurrentSchemaVersion}).Error; err != nil {
			return fmt.Errorf("initialize schema_info: %w", err)
		}
		return nil
	})
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
		&schemaInfo{},
	); err != nil {
		return fmt.Errorf("auto-migrate SQLite schema: %w", err)
	}
	return nil
}

func validateSchemaVersion(db *gorm.DB) error {
	var count int64
	if err := db.Model(&schemaInfo{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count schema_info rows: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("schema_info contains %d rows, want exactly one", count)
	}

	var info schemaInfo
	if err := db.First(&info).Error; err != nil {
		return fmt.Errorf("read schema_info: %w", err)
	}
	if info.Version != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d, want %d", info.Version, CurrentSchemaVersion)
	}
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
	if dsn == ":memory:" {
		return true
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Query().Get("mode"), "memory") {
		return true
	}
	return strings.EqualFold(parsed.Scheme, "file") &&
		(parsed.Path == ":memory:" || parsed.Opaque == ":memory:")
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

func ensureSQLiteDirectory(dsn string) error {
	if dsn == ":memory:" {
		return nil
	}

	databasePath := dsn
	if parsed, err := url.Parse(dsn); err == nil && strings.EqualFold(parsed.Scheme, "file") {
		databasePath = parsed.Path
		if databasePath == "" {
			databasePath = parsed.Opaque
		}
	}
	directory := filepath.Dir(databasePath)
	if directory == "." || directory == "" {
		return nil
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create SQLite database directory: %w", err)
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
	if parsed.Scheme != "" && !strings.EqualFold(parsed.Scheme, "file") {
		return fmt.Errorf("open SQLite database: unsupported DSN scheme %q", parsed.Scheme)
	}
	return nil
}
