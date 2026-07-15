package storage

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

// CurrentSchemaVersion is the database schema version created by M0.
const CurrentSchemaVersion uint = 1

type schemaInfo struct {
	Version uint `gorm:"primaryKey;autoIncrement:false"`
}

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

	db, err := gorm.Open(sqlite.Open(withForeignKeysEnabled(dsn)), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}

	if dsn == ":memory:" {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("get SQLite connection pool: %w", err)
		}
		sqlDB.SetMaxOpenConns(1)
	}

	return db, nil
}

// AutoMigrate creates the M0 persistence schema and initializes schema_info.
func AutoMigrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("auto-migrate SQLite database: db is nil")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(
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

		var count int64
		if err := tx.Model(&schemaInfo{}).Count(&count).Error; err != nil {
			return fmt.Errorf("count schema_info rows: %w", err)
		}
		if count == 0 {
			if err := tx.Create(&schemaInfo{Version: CurrentSchemaVersion}).Error; err != nil {
				return fmt.Errorf("initialize schema_info: %w", err)
			}
			return nil
		}
		if count != 1 {
			return fmt.Errorf("schema_info contains %d rows, want exactly one", count)
		}

		var info schemaInfo
		if err := tx.First(&info).Error; err != nil {
			return fmt.Errorf("read schema_info: %w", err)
		}
		if info.Version != CurrentSchemaVersion {
			return fmt.Errorf("unsupported schema version %d, want %d", info.Version, CurrentSchemaVersion)
		}
		return nil
	})
}

func withForeignKeysEnabled(dsn string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "_pragma=foreign_keys(1)"
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
