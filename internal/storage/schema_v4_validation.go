package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const schemaV4InfoTableSQL = `CREATE TABLE schema_info (
	version integer PRIMARY KEY NOT NULL,
	CONSTRAINT chk_schema_info_version CHECK (version = 4)
)`

func createSchemaV4InfoTable(db *gorm.DB) error {
	if err := db.Exec(schemaV4InfoTableSQL).Error; err != nil {
		return fmt.Errorf("create schema v4 info table: %w", err)
	}
	return nil
}

func validateSchemaV4(db *gorm.DB) error {
	for _, expected := range append(schemaV4TableStatements(), schemaV4InfoTableSQL) {
		fields := strings.Fields(expected)
		if len(fields) < 3 {
			return fmt.Errorf("validate SQLite schema version 4: invalid canonical table DDL")
		}
		name := fields[2]
		actual, err := sqliteSchemaSQL(db, "table", name)
		if err != nil {
			return err
		}
		if normalizeSQLiteDDL(actual) != normalizeSQLiteDDL(expected) {
			return fmt.Errorf("validate SQLite schema version 4: table %s differs", name)
		}
	}

	for _, expected := range schemaV4IndexStatements() {
		fields := strings.Fields(expected)
		if len(fields) < 4 {
			return fmt.Errorf("validate SQLite schema version 4: invalid canonical index DDL")
		}
		nameIndex := 2
		if strings.EqualFold(fields[1], "unique") {
			nameIndex = 3
		}
		name := fields[nameIndex]
		actual, err := sqliteSchemaSQL(db, "index", name)
		if err != nil {
			return err
		}
		if normalizeSQLiteDDL(actual) != normalizeSQLiteDDL(expected) {
			return fmt.Errorf("validate SQLite schema version 4: critical index %s differs", name)
		}
	}
	return validateSchemaV4ForeignKeys(db)
}

func sqliteSchemaSQL(db *gorm.DB, objectType, name string) (string, error) {
	var statement string
	result := db.Raw(
		`SELECT sql FROM sqlite_master WHERE type = ? AND name = ?`,
		objectType,
		name,
	).Scan(&statement)
	if result.Error != nil {
		return "", fmt.Errorf(
			"validate SQLite schema version 4: read %s %s: %w",
			objectType,
			name,
			result.Error,
		)
	}
	if result.RowsAffected != 1 || strings.TrimSpace(statement) == "" {
		return "", fmt.Errorf(
			"validate SQLite schema version 4: %s %s is missing",
			objectType,
			name,
		)
	}
	return statement, nil
}

func normalizeSQLiteDDL(statement string) string {
	return strings.ToLower(strings.Join(strings.Fields(statement), " "))
}
