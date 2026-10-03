package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0029 = "0029_policy_bindings"

type policyBinding0029 struct {
	ID            uint   `gorm:"primaryKey;autoIncrement"`
	Scope         string `gorm:"type:varchar(32);not null;uniqueIndex:idx_policy_bindings_target,priority:1"`
	GroupID       uint   `gorm:"not null;uniqueIndex:idx_policy_bindings_target,priority:2"`
	CredentialID  uint   `gorm:"not null;default:0;uniqueIndex:idx_policy_bindings_target,priority:3"`
	Revision      uint64 `gorm:"not null;default:1"`
	SchemaVersion int    `gorm:"not null;default:1"`
	Config        string `gorm:"type:text;not null"`
	CreatedAtMS   int64  `gorm:"column:created_at_ms;not null;autoCreateTime:milli"`
	UpdatedAtMS   int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli"`
}

func (policyBinding0029) TableName() string { return "policy_bindings" }

func Up0029(db *gorm.DB) error {
	if err := ValidateRecoverable0029(db); err != nil {
		return err
	}
	if !db.Migrator().HasTable(&policyBinding0029{}) {
		if err := db.Migrator().CreateTable(&policyBinding0029{}); err != nil {
			return fmt.Errorf("create policy bindings table: %w", err)
		}
	}
	if !db.Migrator().HasIndex(&policyBinding0029{}, "idx_policy_bindings_target") {
		if err := db.Migrator().CreateIndex(&policyBinding0029{}, "idx_policy_bindings_target"); err != nil {
			return fmt.Errorf("create policy bindings target index: %w", err)
		}
	}
	return Validate0029(db)
}

func ValidateRecoverable0029(db *gorm.DB) error {
	switch strings.ToLower(db.Dialector.Name()) {
	case "mysql", "postgres", "sqlite":
	default:
		return fmt.Errorf("policy bindings: unsupported database driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable(&policyBinding0029{}) {
		return nil
	}
	columns, err := db.Migrator().ColumnTypes(&policyBinding0029{})
	if err != nil {
		return err
	}
	expected := map[string]struct{}{
		"id":             {},
		"scope":          {},
		"group_id":       {},
		"credential_id":  {},
		"revision":       {},
		"schema_version": {},
		"config":         {},
		"created_at_ms":  {},
		"updated_at_ms":  {},
	}
	for _, column := range columns {
		delete(expected, strings.ToLower(column.Name()))
	}
	if len(expected) > 0 {
		return fmt.Errorf("policy bindings table is missing expected columns")
	}
	return nil
}

func Validate0029(db *gorm.DB) error {
	if !db.Migrator().HasTable(&policyBinding0029{}) {
		return fmt.Errorf("policy bindings table is missing")
	}
	if err := ValidateRecoverable0029(db); err != nil {
		return err
	}
	if !db.Migrator().HasIndex(&policyBinding0029{}, "idx_policy_bindings_target") {
		return fmt.Errorf("policy bindings index is missing")
	}
	return nil
}
