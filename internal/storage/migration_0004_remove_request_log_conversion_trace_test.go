package storage

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRemoveRequestLogConversionTracePreservesRequestLogSchema(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			testRemoveRequestLogConversionTracePreservesRequestLogSchema(t, driver)
		})
	}
}

func testRemoveRequestLogConversionTracePreservesRequestLogSchema(t *testing.T, driver string) {
	t.Helper()
	dialector := gorm.Dialector(sqlite.Open("file:remove-conversion-trace-migration-" + driver + "?mode=memory&cache=shared"))
	if driver != "sqlite" {
		dialector = namedMigrationDialector{Dialector: dialector, name: driver}
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE request_logs (
			id TEXT PRIMARY KEY NOT NULL,
			client_parameters_json JSON,
			status TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX idx_request_logs_status ON request_logs(status)`,
		`CREATE TRIGGER trg_request_logs_status
			AFTER UPDATE OF status ON request_logs
			BEGIN SELECT NEW.status; END`,
		`CREATE TABLE request_log_attempts (
			request_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			conversion_trace_json JSON,
			failure_category TEXT NOT NULL CONSTRAINT chk_request_log_attempt_failure_category
				CHECK (failure_category IN ('ok','conversion_unsupported')),
			PRIMARY KEY (request_id, sequence),
			CONSTRAINT fk_attempt_request FOREIGN KEY (request_id) REFERENCES request_logs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_attempt_failure ON request_log_attempts(failure_category)`,
		`CREATE TRIGGER trg_request_log_attempts_failure
			AFTER UPDATE OF failure_category ON request_log_attempts
			BEGIN SELECT NEW.failure_category; END`,
		`INSERT INTO request_logs(id, client_parameters_json, status) VALUES ('request-1', '{"entries":[]}', 'error')`,
		`INSERT INTO request_log_attempts(request_id, sequence, conversion_trace_json, failure_category) VALUES ('request-1', 1, '{"changes":[]}', 'conversion_unsupported')`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("execute fixture schema: %v", err)
		}
	}

	if err := removeRequestLogConversionTrace(db); err != nil {
		t.Fatalf("remove request log conversion trace: %v", err)
	}
	if err := removeRequestLogConversionTrace(db); err != nil {
		t.Fatalf("second remove request log conversion trace: %v", err)
	}
	for table, column := range map[string]string{
		"request_logs":         requestLogConversionTraceColumn,
		"request_log_attempts": requestLogAttemptConversionTraceColumn,
	} {
		if db.Migrator().HasColumn(table, column) {
			t.Errorf("%s.%s was not removed", table, column)
		}
	}
	for table, schemaObject := range map[string]struct{ index, trigger string }{
		"request_logs":         {"idx_request_logs_status", "trg_request_logs_status"},
		"request_log_attempts": {"idx_attempt_failure", "trg_request_log_attempts_failure"},
	} {
		if !db.Migrator().HasIndex(table, schemaObject.index) {
			t.Errorf("%s index %s was lost", table, schemaObject.index)
		}
		var count int
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?", schemaObject.trigger).Scan(&count).Error; err != nil || count != 1 {
			t.Errorf("%s trigger %s was lost: count=%d error=%v", table, schemaObject.trigger, count, err)
		}
	}
	var historicalAttempt struct {
		FailureCategory string
	}
	result := db.Raw("SELECT failure_category FROM request_log_attempts WHERE request_id = ?", "request-1").Scan(&historicalAttempt)
	if result.Error != nil || result.RowsAffected != 1 || historicalAttempt.FailureCategory != "conversion_unsupported" {
		t.Fatalf("historical attempt = %#v, rows=%d, error=%v", historicalAttempt, result.RowsAffected, result.Error)
	}
	if err := db.Exec("INSERT INTO request_log_attempts(request_id, sequence, failure_category) VALUES (?, ?, ?)", "request-1", 2, "conversion_unsupported").Error; err != nil {
		t.Fatalf("conversion_unsupported constraint was not preserved: %v", err)
	}
	if err := db.Exec("DELETE FROM request_logs WHERE id = ?", "request-1").Error; err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err := db.Raw("SELECT COUNT(*) FROM request_log_attempts WHERE request_id = ?", "request-1").Scan(&attempts).Error; err != nil || attempts != 0 {
		t.Fatalf("cascade attempts = %d, error = %v", attempts, err)
	}
	var violations []struct {
		Table string
	}
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("foreign key violations = %#v", violations)
	}
}

type namedMigrationDialector struct {
	gorm.Dialector
	name string
}

func (dialector namedMigrationDialector) Name() string {
	return dialector.name
}
