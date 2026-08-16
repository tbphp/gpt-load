package sqlitetest

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/storage"
)

var (
	templateOnce sync.Once
	templateSQL  string
	templateErr  error
)

// OpenMigrated returns an isolated in-memory SQLite database restored from one
// migrated schema template. Migration and SQLite-open safety behavior remain
// covered by storage tests; business-package fixtures only need the final DDL.
func OpenMigrated(t *testing.T) *gorm.DB {
	t.Helper()
	templateOnce.Do(func() {
		templateSQL, templateErr = buildTemplate()
	})
	if templateErr != nil {
		t.Fatalf("build migrated SQLite test template: %v", templateErr)
	}

	db, err := gorm.Open(
		gormsqlite.Open(
			":memory:?_txlock=immediate"+
				"&_pragma=foreign_keys(1)"+
				"&_pragma=busy_timeout(5000)",
		),
		&gorm.Config{
			Logger:         logger.Default.LogMode(logger.Silent),
			TranslateError: true,
		},
	)
	if err != nil {
		t.Fatalf("open migrated SQLite test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get migrated SQLite test connection pool: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close migrated SQLite test database: %v", err)
		}
	})
	if err := db.Exec(templateSQL).Error; err != nil {
		t.Fatalf("restore migrated SQLite test schema: %v", err)
	}
	return db
}

func buildTemplate() (string, error) {
	db, err := storage.Open(":memory:")
	if err != nil {
		return "", err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return "", err
	}
	defer func() { _ = sqlDB.Close() }()
	if err := storage.AutoMigrate(db); err != nil {
		return "", err
	}

	var objects []struct {
		SQL string
	}
	if err := db.Raw(`
		SELECT sql
		FROM sqlite_master
		WHERE sql IS NOT NULL
		  AND name NOT GLOB 'sqlite_*'
		ORDER BY CASE type
		  WHEN 'table' THEN 0
		  WHEN 'index' THEN 1
		  WHEN 'trigger' THEN 2
		  ELSE 3
		END, name
	`).Scan(&objects).Error; err != nil {
		return "", err
	}
	statements := make([]string, 0, len(objects))
	for _, object := range objects {
		statement := strings.TrimSuffix(strings.TrimSpace(object.SQL), ";")
		if statement != "" {
			statements = append(statements, statement)
		}
	}
	if len(statements) == 0 {
		return "", fmt.Errorf("migrated SQLite test schema is empty")
	}
	return strings.Join(statements, ";\n") + ";", nil
}
