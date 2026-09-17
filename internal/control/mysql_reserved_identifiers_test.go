package control

import (
	"strings"
	"testing"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/storage/models"
)

func TestHomeCredentialRowsScopeQuotesGroupsForMySQL(t *testing.T) {
	t.Parallel()
	db := openReservedIdentifierMySQLDryRun(t)

	result := homeCredentialRowsScope(db).Find(&[]homeCredentialRow{})
	if result.Error != nil {
		t.Fatalf("home credential query error = %v", result.Error)
	}
	sql := result.Statement.SQL.String()
	if strings.Contains(sql, "JOIN groups") || !strings.Contains(sql, "FROM `groups`") {
		t.Fatalf("generated SQL = %q, want a GORM-quoted groups source", sql)
	}
}

func TestGlobalProxyConfigScopeQuotesKeyForMySQL(t *testing.T) {
	t.Parallel()
	db := openReservedIdentifierMySQLDryRun(t)

	result := globalProxyConfigScope(db).Take(&models.SystemSetting{})
	if result.Error != nil {
		t.Fatalf("global proxy query error = %v", result.Error)
	}
	sql := result.Statement.SQL.String()
	if strings.Contains(sql, "SELECT key") || strings.Contains(sql, "WHERE key") ||
		!strings.Contains(sql, "`system_settings`.`key`") {
		t.Fatalf("generated SQL = %q, want a GORM-quoted system setting key", sql)
	}
}

func openReservedIdentifierMySQLDryRun(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		DSN:                       "user:password@tcp(127.0.0.1:3306)/gpt_load",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	return db
}
