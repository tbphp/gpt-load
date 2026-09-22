package loader

import (
	"strings"
	"testing"

	gormsqlite "github.com/glebarez/sqlite"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/storage/models"
)

func TestSystemSettingKeyScopeQuotesKeyForSupportedDrivers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		open             func(*testing.T) *gorm.DB
		quotedIdentifier string
	}{
		{
			name:             "sqlite",
			open:             openReservedIdentifierSQLiteDryRun,
			quotedIdentifier: "`system_settings`.`key`",
		},
		{
			name:             "mysql",
			open:             openReservedIdentifierMySQLDryRun,
			quotedIdentifier: "`system_settings`.`key`",
		},
		{
			name:             "postgres",
			open:             openReservedIdentifierPostgresDryRun,
			quotedIdentifier: `"system_settings"."key"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := test.open(t)
			statements := []*gorm.DB{
				systemSettingKeyScope(db, "automatic_models").Take(&models.SystemSetting{}),
				systemSettingKeyScope(db.Model(&models.SystemSetting{}), "automatic_models").Update("value", "ciphertext"),
			}
			for _, statement := range statements {
				if statement.Error != nil {
					t.Fatalf("build statement: %v", statement.Error)
				}
				sql := statement.Statement.SQL.String()
				if !strings.Contains(sql, test.quotedIdentifier) {
					t.Fatalf("generated SQL = %q, want quoted identifier %q", sql, test.quotedIdentifier)
				}
			}
		})
	}
}

func openReservedIdentifierSQLiteDryRun(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormsqlite.Open(":memory:"), reservedIdentifierGORMConfig())
	if err != nil {
		t.Fatalf("open SQLite DryRun database: %v", err)
	}
	return db
}

func openReservedIdentifierMySQLDryRun(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		DSN:                       "user:password@tcp(127.0.0.1:3306)/gpt_load",
		SkipInitializeWithVersion: true,
	}), reservedIdentifierGORMConfig())
	if err != nil {
		t.Fatalf("open MySQL DryRun database: %v", err)
	}
	return db
}

func openReservedIdentifierPostgresDryRun(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormpostgres.New(gormpostgres.Config{
		DSN:                  "postgres://user:password@127.0.0.1:5432/gpt_load?sslmode=disable",
		PreferSimpleProtocol: true,
	}), reservedIdentifierGORMConfig())
	if err != nil {
		t.Fatalf("open PostgreSQL DryRun database: %v", err)
	}
	return db
}

func reservedIdentifierGORMConfig() *gorm.Config {
	return &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	}
}
