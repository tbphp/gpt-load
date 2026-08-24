package migrations

import (
	"fmt"
	"strings"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const ID0006 = "0006_error_decision"

const (
	requestLogAttemptTable0006 = "request_log_attempts"

	failureCategoryConstraint0006 = "chk_request_log_attempt_failure_category"
	failureOriginConstraint0006   = "chk_request_log_attempt_failure_origin"
	failureScopeConstraint0006    = "chk_request_log_attempt_failure_scope"
	retryDirectiveConstraint0006  = "chk_request_log_attempt_retry_directive"
	effectConstraint0006          = "chk_request_log_attempt_effect"
)

var requestLogAttemptDecisionColumns0006 = []struct {
	field  string
	column string
}{
	{field: "FailureOrigin", column: "failure_origin"},
	{field: "FailureScope", column: "failure_scope"},
	{field: "RetryDirective", column: "retry_directive"},
	{field: "Effect", column: "effect"},
	{field: "RuleID", column: "rule_id"},
}

var requestLogAttemptIndexes0006 = []string{
	"idx_request_log_attempts_group_completed_request",
	"idx_request_log_attempts_channel_completed_request",
	"idx_request_log_attempts_credential_completed_request",
	"idx_request_log_attempts_model_completed_request",
	"idx_request_log_attempts_status_completed_request",
	"idx_request_log_attempts_failure_completed_request",
	"idx_request_log_attempts_error_completed_request",
}

type requestLogAttemptDecision0006 struct {
	FailureOrigin  string `gorm:"column:failure_origin;type:varchar(16);not null;default:''"`
	FailureScope   string `gorm:"column:failure_scope;type:varchar(16);not null;default:''"`
	RetryDirective string `gorm:"column:retry_directive;type:varchar(32);not null;default:''"`
	Effect         string `gorm:"column:effect;type:varchar(32);not null;default:''"`
	RuleID         string `gorm:"column:rule_id;type:varchar(128);not null;default:''"`
}

func (requestLogAttemptDecision0006) TableName() string { return requestLogAttemptTable0006 }

var decisionConstraints0006 = []struct {
	name       string
	expression string
}{
	{
		name:       failureOriginConstraint0006,
		expression: "failure_origin IN ('','client','upstream','downstream','internal')",
	},
	{
		name:       failureScopeConstraint0006,
		expression: "failure_scope IN ('','request','model','credential','group')",
	},
	{
		name:       retryDirectiveConstraint0006,
		expression: "retry_directive IN ('','none','refresh_credential','next_candidate')",
	},
	{
		name:       effectConstraint0006,
		expression: "effect IN ('','none','cooldown_credential','record_credential_failure','skip_group')",
	},
}

const failureCategoryExpression0006 = "failure_category IN ('ok','rate_limited','model_unavailable','invalid_key','upstream_host_error','client_error','conversion_unsupported','downstream_cancel','authentication_required','ambiguous')"

// Up0006 adds the normalized Judge decision fields and extends the retained
// legacy failure category with authentication_required.
func Up0006(db *gorm.DB) error {
	if !db.Migrator().HasTable(requestLogAttemptTable0006) {
		return fmt.Errorf("add error decision: table %q is missing", requestLogAttemptTable0006)
	}
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		if Validate0006(db) == nil {
			return nil
		}
		return rebuildSQLiteRequestLogAttempts0006(db)
	}

	model := &requestLogAttemptDecision0006{}
	for _, column := range requestLogAttemptDecisionColumns0006 {
		if db.Migrator().HasColumn(model, column.column) {
			continue
		}
		if err := db.Migrator().AddColumn(model, column.field); err != nil {
			return fmt.Errorf("add request_log_attempts.%s: %w", column.column, err)
		}
	}
	if db.Migrator().HasConstraint(requestLogAttemptTable0006, failureCategoryConstraint0006) {
		if err := dropCheckConstraint0006(db, failureCategoryConstraint0006); err != nil {
			return fmt.Errorf("replace request log failure category constraint: %w", err)
		}
	}
	if err := createCheckConstraint0006(db, failureCategoryConstraint0006, failureCategoryExpression0006); err != nil {
		return err
	}
	for _, constraint := range decisionConstraints0006 {
		if db.Migrator().HasConstraint(requestLogAttemptTable0006, constraint.name) {
			continue
		}
		if err := createCheckConstraint0006(db, constraint.name, constraint.expression); err != nil {
			return err
		}
	}
	return nil
}

func createCheckConstraint0006(db *gorm.DB, name, expression string) error {
	table, constraint := quoteMigrationIdentifier0006(db, requestLogAttemptTable0006), quoteMigrationIdentifier0006(db, name)
	if err := db.Exec(fmt.Sprintf(
		"ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s)", table, constraint, expression,
	)).Error; err != nil {
		return fmt.Errorf("create request log decision constraint %q: %w", name, err)
	}
	return nil
}

func dropCheckConstraint0006(db *gorm.DB, name string) error {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		if dialector, ok := db.Dialector.(*gormmysql.Dialector); ok && dialector.Config != nil &&
			mysqlRequiresCheckDropSyntax0003(dialector.ServerVersion) {
			return db.Exec(fmt.Sprintf(
				"ALTER TABLE `request_log_attempts` DROP CHECK `%s`", name,
			)).Error
		}
	}
	return db.Migrator().DropConstraint(requestLogAttemptTable0006, name)
}

func quoteMigrationIdentifier0006(db *gorm.DB, value string) string {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return "`" + value + "`"
	}
	return `"` + value + `"`
}

func rebuildSQLiteRequestLogAttempts0006(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE request_log_attempts__0006 (
			request_id varchar(36) NOT NULL,
			sequence integer NOT NULL,
			completed_at_ms integer NOT NULL,
			group_id integer NOT NULL,
			group_name varchar(255) NOT NULL,
			channel_id varchar(64) NOT NULL DEFAULT '',
			credential_id integer NOT NULL,
			operation varchar(64) NOT NULL DEFAULT '',
			route_mode varchar(32) NOT NULL DEFAULT '',
			upstream_model varchar(255) NOT NULL DEFAULT '',
			upstream_request_id varchar(255) NOT NULL DEFAULT '',
			dispatch_state varchar(32) NOT NULL DEFAULT '',
			response_started numeric NOT NULL DEFAULT false,
			upstream_protocol varchar(32) NOT NULL DEFAULT '',
			reasoning_mode varchar(64) NOT NULL DEFAULT '',
			reasoning_effort varchar(64) NOT NULL DEFAULT '',
			reasoning_budget_tokens integer,
			status_code integer NOT NULL,
			duration_ms integer NOT NULL,
			failure_category varchar(32) NOT NULL,
			failure_origin varchar(16) NOT NULL DEFAULT '',
			failure_scope varchar(16) NOT NULL DEFAULT '',
			retry_directive varchar(32) NOT NULL DEFAULT '',
			effect varchar(32) NOT NULL DEFAULT '',
			rule_id varchar(128) NOT NULL DEFAULT '',
			action varchar(32) NOT NULL,
			will_retry numeric NOT NULL DEFAULT false,
			error_code varchar(64) NOT NULL DEFAULT '',
			error_summary text NOT NULL,
			committed numeric NOT NULL DEFAULT false,
			pricing_receipt json,
			PRIMARY KEY (request_id, sequence),
			CONSTRAINT fk_request_log_attempts_request_log FOREIGN KEY (request_id)
				REFERENCES request_logs(id) ON DELETE CASCADE ON UPDATE CASCADE,
			CONSTRAINT chk_request_log_attempt_sequence CHECK (sequence > 0),
			CONSTRAINT chk_request_log_attempt_completed_at CHECK (completed_at_ms >= 0),
			CONSTRAINT chk_request_log_attempt_group CHECK (group_id > 0),
			CONSTRAINT chk_request_log_attempt_credential CHECK (credential_id > 0),
			CONSTRAINT chk_request_log_attempt_duration CHECK (duration_ms >= 0),
			CONSTRAINT chk_request_log_attempt_failure_category CHECK (` + failureCategoryExpression0006 + `),
			CONSTRAINT chk_request_log_attempt_failure_origin CHECK (failure_origin IN ('','client','upstream','downstream','internal')),
			CONSTRAINT chk_request_log_attempt_failure_scope CHECK (failure_scope IN ('','request','model','credential','group')),
			CONSTRAINT chk_request_log_attempt_retry_directive CHECK (retry_directive IN ('','none','refresh_credential','next_candidate')),
			CONSTRAINT chk_request_log_attempt_effect CHECK (effect IN ('','none','cooldown_credential','record_credential_failure','skip_group')),
			CONSTRAINT chk_request_log_attempt_action CHECK (action IN ('terminate','retry','cooldown_credential','fail_credential','skip_group'))
		)`,
		`INSERT INTO request_log_attempts__0006 (
			request_id, sequence, completed_at_ms, group_id, group_name, channel_id,
			credential_id, operation, route_mode, upstream_model, upstream_request_id,
			dispatch_state, response_started, upstream_protocol, reasoning_mode,
			reasoning_effort, reasoning_budget_tokens, status_code, duration_ms,
			failure_category, failure_origin, failure_scope, retry_directive, effect,
			rule_id, action, will_retry, error_code, error_summary, committed, pricing_receipt
		) SELECT
			request_id, sequence, completed_at_ms, group_id, group_name, channel_id,
			credential_id, operation, route_mode, upstream_model, upstream_request_id,
			dispatch_state, response_started, upstream_protocol, reasoning_mode,
			reasoning_effort, reasoning_budget_tokens, status_code, duration_ms,
			failure_category, '', '', '', '', '', action, will_retry, error_code,
			error_summary, committed, pricing_receipt
		FROM request_log_attempts`,
		`DROP TABLE request_log_attempts`,
		`ALTER TABLE request_log_attempts__0006 RENAME TO request_log_attempts`,
		`CREATE INDEX idx_request_log_attempts_group_completed_request ON request_log_attempts(group_id, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_channel_completed_request ON request_log_attempts(channel_id, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_credential_completed_request ON request_log_attempts(credential_id, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_model_completed_request ON request_log_attempts(upstream_model, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_status_completed_request ON request_log_attempts(status_code, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_failure_completed_request ON request_log_attempts(failure_category, completed_at_ms DESC, request_id)`,
		`CREATE INDEX idx_request_log_attempts_error_completed_request ON request_log_attempts(error_code, completed_at_ms DESC, request_id)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("rebuild SQLite request log attempts: %w", err)
		}
	}
	return nil
}

// ValidateRecoverable0006 accepts any idempotent prefix of the MySQL DDL.
func ValidateRecoverable0006(db *gorm.DB) error {
	if !db.Migrator().HasTable(requestLogAttemptTable0006) {
		return fmt.Errorf("validate recoverable error decision: table %q is missing", requestLogAttemptTable0006)
	}
	columns, err := db.Migrator().ColumnTypes(requestLogAttemptTable0006)
	if err != nil {
		return fmt.Errorf("inspect recoverable error decision columns: %w", err)
	}
	newColumns := make(map[string]struct{}, len(requestLogAttemptDecisionColumns0006))
	for _, column := range requestLogAttemptDecisionColumns0006 {
		newColumns[column.column] = struct{}{}
	}
	for _, column := range columns {
		if _, relevant := newColumns[strings.ToLower(column.Name())]; !relevant {
			continue
		}
		typeName := strings.ToLower(column.DatabaseTypeName())
		if !strings.Contains(typeName, "char") && !strings.Contains(typeName, "text") && typeName != "clob" {
			return fmt.Errorf("column %q has an incompatible type", column.Name())
		}
		if nullable, known := column.Nullable(); known && nullable &&
			!strings.EqualFold(db.Dialector.Name(), "sqlite") {
			return fmt.Errorf("column %q is nullable", column.Name())
		}
	}
	return nil
}

// Validate0006 verifies the normalized decision schema and retained indexes.
func Validate0006(db *gorm.DB) error {
	if err := ValidateRecoverable0006(db); err != nil {
		return err
	}
	for _, column := range requestLogAttemptDecisionColumns0006 {
		if !db.Migrator().HasColumn(requestLogAttemptTable0006, column.column) {
			return fmt.Errorf("validate error decision: column %q is missing", column.column)
		}
	}
	for _, name := range append([]string{failureCategoryConstraint0006}, decisionConstraintNames0006()...) {
		if !db.Migrator().HasConstraint(requestLogAttemptTable0006, name) {
			return fmt.Errorf("validate error decision: constraint %q is missing", name)
		}
	}
	for _, index := range requestLogAttemptIndexes0006 {
		if !db.Migrator().HasIndex(requestLogAttemptTable0006, index) {
			return fmt.Errorf("validate error decision: index %q is missing", index)
		}
	}
	definition, err := failureCategoryConstraintDefinition0006(db)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(definition), "authentication_required") {
		return fmt.Errorf("validate error decision: failure category constraint is stale")
	}
	return nil
}

func decisionConstraintNames0006() []string {
	result := make([]string, 0, len(decisionConstraints0006))
	for _, constraint := range decisionConstraints0006 {
		result = append(result, constraint.name)
	}
	return result
}

func failureCategoryConstraintDefinition0006(db *gorm.DB) (string, error) {
	var definition string
	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite":
		if err := db.Raw(
			"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
			requestLogAttemptTable0006,
		).Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect SQLite failure category constraint: %w", err)
		}
	case "mysql":
		if err := db.Raw(
			"SELECT CHECK_CLAUSE FROM information_schema.check_constraints WHERE constraint_schema = DATABASE() AND constraint_name = ?",
			failureCategoryConstraint0006,
		).Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect MySQL failure category constraint: %w", err)
		}
	case "postgres", "postgresql":
		if err := db.Raw(
			"SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = ? AND conrelid = 'request_log_attempts'::regclass",
			failureCategoryConstraint0006,
		).Scan(&definition).Error; err != nil {
			return "", fmt.Errorf("inspect PostgreSQL failure category constraint: %w", err)
		}
	default:
		return "", fmt.Errorf("inspect failure category constraint: unsupported driver %q", db.Dialector.Name())
	}
	if strings.TrimSpace(definition) == "" {
		return "", fmt.Errorf("failure category constraint definition is missing")
	}
	return definition, nil
}
