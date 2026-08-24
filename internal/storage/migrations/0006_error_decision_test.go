package migrations_test

import (
	"testing"

	"gpt-load/internal/storage/migrations"
	"gpt-load/internal/storage/models"
)

func TestErrorDecisionMigrationPreservesAttemptsAndAddsDecisionContract(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatalf("Up0001() error = %v", err)
	}
	request := models.RequestLog{
		ID: "00000000-0000-4000-8000-000000000006", CompletedAtMS: 1000,
		AccessKeyID: 1, Protocol: "openai-completions", ClientModel: "gpt-test",
		UpstreamModel: "gpt-test", ModelConsistency: "not_applicable",
		Status: "error", StatusCode: 502, DurationMs: 10, ErrorSummary: "failed",
		UsageState: "not_applicable", CostState: "not_applicable",
		PricingCompleteness: "not_applicable",
	}
	if err := db.Create(&request).Error; err != nil {
		t.Fatalf("create request log: %v", err)
	}
	attempt := models.RequestLogAttempt{
		RequestID: request.ID, Sequence: 1, CompletedAtMS: 1000,
		GroupID: 1, GroupName: "group", ChannelID: "openai", CredentialID: 1,
		StatusCode: 429, DurationMs: 5, FailureCategory: "rate_limited",
		Action: "cooldown_credential", ErrorSummary: "limited",
	}
	if err := db.Omit(
		"FailureOrigin", "FailureScope", "RetryDirective", "Effect", "RuleID",
	).Create(&attempt).Error; err != nil {
		t.Fatalf("create legacy attempt: %v", err)
	}

	if err := migrations.Up0006(db); err != nil {
		t.Fatalf("Up0006() error = %v", err)
	}
	if err := migrations.Validate0006(db); err != nil {
		t.Fatalf("Validate0006() error = %v", err)
	}
	for _, column := range []string{
		"failure_origin", "failure_scope", "retry_directive", "effect", "rule_id",
	} {
		if !db.Migrator().HasColumn("request_log_attempts", column) {
			t.Fatalf("request_log_attempts.%s is missing", column)
		}
	}
	var preserved models.RequestLogAttempt
	if err := db.Take(&preserved, "request_id = ? AND sequence = 1", request.ID).Error; err != nil {
		t.Fatalf("read preserved attempt: %v", err)
	}
	if preserved.FailureCategory != "rate_limited" || preserved.Action != "cooldown_credential" ||
		preserved.FailureOrigin != "" || preserved.FailureScope != "" ||
		preserved.RetryDirective != "" || preserved.Effect != "" || preserved.RuleID != "" {
		t.Fatalf("preserved attempt = %#v", preserved)
	}

	newAttempt := models.RequestLogAttempt{
		RequestID: request.ID, Sequence: 2, CompletedAtMS: 1001,
		GroupID: 1, GroupName: "group", ChannelID: "openai", CredentialID: 1,
		StatusCode: 401, DurationMs: 6, FailureCategory: "authentication_required",
		FailureOrigin: "upstream", FailureScope: "credential",
		RetryDirective: "refresh_credential", Effect: "none", RuleID: "auth.refresh_required",
		Action: "retry", ErrorSummary: "refresh required",
	}
	if err := db.Create(&newAttempt).Error; err != nil {
		t.Fatalf("create decision attempt: %v", err)
	}
	invalid := newAttempt
	invalid.Sequence = 3
	invalid.FailureOrigin = "unknown"
	if err := db.Create(&invalid).Error; err == nil {
		t.Fatal("migration accepted an invalid failure origin")
	}

	if err := migrations.Up0006(db); err != nil {
		t.Fatalf("repeated Up0006() error = %v", err)
	}
}

func TestErrorDecisionMigrationPreservesAttemptIndexesAndForeignKey(t *testing.T) {
	db := openInitialTestDatabase(t)
	if err := migrations.Up0001(db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up0006(db); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{
		"idx_request_log_attempts_group_completed_request",
		"idx_request_log_attempts_channel_completed_request",
		"idx_request_log_attempts_credential_completed_request",
		"idx_request_log_attempts_model_completed_request",
		"idx_request_log_attempts_status_completed_request",
		"idx_request_log_attempts_failure_completed_request",
		"idx_request_log_attempts_error_completed_request",
	} {
		if !db.Migrator().HasIndex("request_log_attempts", index) {
			t.Fatalf("request_log_attempts index %q is missing", index)
		}
	}
	var foreignKeys []struct {
		Table    string
		From     string
		To       string
		OnDelete string `gorm:"column:on_delete"`
	}
	if err := db.Raw("PRAGMA foreign_key_list('request_log_attempts')").Scan(&foreignKeys).Error; err != nil {
		t.Fatal(err)
	}
	if len(foreignKeys) != 1 || foreignKeys[0].Table != "request_logs" ||
		foreignKeys[0].From != "request_id" || foreignKeys[0].To != "id" ||
		foreignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("request_log_attempts foreign keys = %#v", foreignKeys)
	}
}
