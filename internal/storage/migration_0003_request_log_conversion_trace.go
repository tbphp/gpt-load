package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

// Migration-local models freeze the nullable conversion-observation columns.
type requestLogConversionTraceMigration struct {
	ClientParameters models.JSON `gorm:"column:client_parameters_json;type:json"`
}

func (requestLogConversionTraceMigration) TableName() string { return "request_logs" }

type requestLogAttemptConversionTraceMigration struct {
	ConversionTrace models.JSON `gorm:"column:conversion_trace_json;type:json"`
}

func (requestLogAttemptConversionTraceMigration) TableName() string {
	return "request_log_attempts"
}

func addRequestLogConversionTrace(db *gorm.DB) error {
	request := &requestLogConversionTraceMigration{}
	if err := addMigrationColumns(db, request, "ClientParameters"); err != nil {
		return fmt.Errorf("add request log client parameter projection: %w", err)
	}
	attempt := &requestLogAttemptConversionTraceMigration{}
	if err := addMigrationColumns(db, attempt, "ConversionTrace"); err != nil {
		return fmt.Errorf("add request log attempt conversion trace: %w", err)
	}
	return widenRequestLogAttemptFailureCategory(db)
}

func widenRequestLogAttemptFailureCategory(db *gorm.DB) error {
	if db == nil || db.Dialector == nil {
		return fmt.Errorf("widen request log attempt failure category: database is nil")
	}
	if !strings.EqualFold(db.Dialector.Name(), "sqlite") {
		// GPT-Load 2.0 is SQLite-only; legacy dialect hooks do not participate in
		// the runtime migration contract.
		return nil
	}
	var createSQL string
	if err := db.Raw(
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
		"request_log_attempts",
	).Scan(&createSQL).Error; err != nil {
		return fmt.Errorf("read request log attempt schema: %w", err)
	}
	if strings.Contains(createSQL, "'conversion_unsupported'") {
		return nil
	}
	const temporaryTable = "request_log_attempts__conversion_trace_migration"
	temporarySQL, ok := renameSQLiteCreateTable(createSQL, "request_log_attempts", temporaryTable)
	if !ok {
		return fmt.Errorf("widen request log attempt failure category: unsupported SQLite table definition")
	}
	temporarySQL = strings.Replace(
		temporarySQL,
		"'client_error','downstream_cancel'",
		"'client_error','conversion_unsupported','downstream_cancel'",
		1,
	)
	if !strings.Contains(temporarySQL, "'conversion_unsupported'") {
		return fmt.Errorf("widen request log attempt failure category: existing constraint was not recognized")
	}
	var schemaObjects []struct {
		SQL string
	}
	if err := db.Raw(
		"SELECT sql FROM sqlite_master WHERE tbl_name = ? AND type IN ('index','trigger') AND sql IS NOT NULL ORDER BY type, name",
		"request_log_attempts",
	).Scan(&schemaObjects).Error; err != nil {
		return fmt.Errorf("read request log attempt indexes: %w", err)
	}
	statements := []string{
		temporarySQL,
		`INSERT INTO "` + temporaryTable + `" SELECT * FROM "request_log_attempts"`,
		`DROP TABLE "request_log_attempts"`,
		`ALTER TABLE "` + temporaryTable + `" RENAME TO "request_log_attempts"`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("rebuild request log attempt constraint: %w", err)
		}
	}
	for _, object := range schemaObjects {
		if err := db.Exec(object.SQL).Error; err != nil {
			return fmt.Errorf("restore request log attempt schema object: %w", err)
		}
	}
	return nil
}

func renameSQLiteCreateTable(source, oldName, newName string) (string, bool) {
	for _, quote := range []string{"`", `"`, ""} {
		oldToken := "CREATE TABLE " + quote + oldName + quote
		if strings.HasPrefix(strings.ToUpper(source), strings.ToUpper(oldToken)) {
			return "CREATE TABLE " + quote + newName + quote + source[len(oldToken):], true
		}
	}
	return "", false
}
