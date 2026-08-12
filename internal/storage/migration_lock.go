package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	migrationLockName    = "gpt-load:schema-migrations:v2"
	migrationLockTimeout = time.Minute
)

func acquireMigrationLock(db *gorm.DB) error {
	switch strings.ToLower(db.Dialector.Name()) {
	case "mysql":
		var result sql.NullInt64
		if err := db.Raw("SELECT GET_LOCK(?, ?)", migrationLockName, int(migrationLockTimeout/time.Second)).Scan(&result).Error; err != nil {
			return fmt.Errorf("acquire MySQL migration lock: %w", err)
		}
		if !result.Valid || result.Int64 != 1 {
			return fmt.Errorf("acquire MySQL migration lock: timed out")
		}
		return nil
	case "postgres", "postgresql":
		if err := db.Exec("SELECT pg_advisory_lock(hashtext(?))", migrationLockName).Error; err != nil {
			return fmt.Errorf("acquire PostgreSQL migration lock: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("acquire migration lock: unsupported database driver %q", db.Dialector.Name())
	}
}

func releaseMigrationLock(db *gorm.DB) error {
	switch strings.ToLower(db.Dialector.Name()) {
	case "mysql":
		var result sql.NullInt64
		if err := db.Raw("SELECT RELEASE_LOCK(?)", migrationLockName).Scan(&result).Error; err != nil {
			return fmt.Errorf("release MySQL migration lock: %w", err)
		}
		if !result.Valid || result.Int64 != 1 {
			return fmt.Errorf("release MySQL migration lock: lock was not held")
		}
		return nil
	case "postgres", "postgresql":
		var released bool
		if err := db.Raw("SELECT pg_advisory_unlock(hashtext(?))", migrationLockName).Scan(&released).Error; err != nil {
			return fmt.Errorf("release PostgreSQL migration lock: %w", err)
		}
		if !released {
			return fmt.Errorf("release PostgreSQL migration lock: lock was not held")
		}
		return nil
	default:
		return fmt.Errorf("release migration lock: unsupported database driver %q", db.Dialector.Name())
	}
}
