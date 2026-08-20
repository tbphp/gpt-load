package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const (
	// ID0002 adds AccessKey estimated-cost limit rules and restart checkpoints.
	ID0002 = "0002_access_key_cost_limits"
)

type accessKeyCostLimitRule0002 struct {
	ID            uint              `gorm:"primaryKey;autoIncrement"`
	AccessKeyID   uint              `gorm:"not null;uniqueIndex:idx_access_key_cost_limit_rules_identity,priority:1"`
	Kind          string            `gorm:"type:varchar(16);not null;uniqueIndex:idx_access_key_cost_limit_rules_identity,priority:2;check:chk_ak_cost_rule_kind,kind IN ('total','periodic')"`
	LimitNanoUSD  int64             `gorm:"column:limit_nano_usd;not null;check:chk_ak_cost_rule_limit,limit_nano_usd > 0"`
	PeriodSeconds int64             `gorm:"not null;default:0;uniqueIndex:idx_access_key_cost_limit_rules_identity,priority:3;check:chk_ak_cost_rule_period,(kind = 'total' AND period_seconds = 0) OR (kind = 'periodic' AND period_seconds BETWEEN 60 AND 31536000)"`
	RuleRevision  uint64            `gorm:"not null;default:1;check:chk_ak_cost_rule_revision,rule_revision > 0"`
	AccessKey     *initialAccessKey `gorm:"foreignKey:AccessKeyID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	CreatedAtMS   int64             `gorm:"column:created_at_ms;not null;autoCreateTime:milli;check:chk_ak_cost_rule_created_at,created_at_ms >= 0"`
	UpdatedAtMS   int64             `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_ak_cost_rule_updated_at,updated_at_ms >= 0"`
}

func (accessKeyCostLimitRule0002) TableName() string {
	return "access_key_cost_limit_rules"
}

type accessKeyCostLimitState0002 struct {
	RuleID            uint                        `gorm:"primaryKey;not null"`
	RuleRevision      uint64                      `gorm:"not null;check:chk_ak_cost_state_revision,rule_revision > 0"`
	UsedNanoUSD       int64                       `gorm:"column:used_nano_usd;not null;default:0;check:chk_ak_cost_state_used,used_nano_usd >= 0"`
	WindowStartedAtMS *int64                      `gorm:"column:window_started_at_ms;check:chk_ak_cost_state_window,(window_started_at_ms IS NULL AND window_ends_at_ms IS NULL) OR (window_started_at_ms IS NOT NULL AND window_ends_at_ms IS NOT NULL AND window_started_at_ms >= 0 AND window_ends_at_ms > window_started_at_ms)"`
	WindowEndsAtMS    *int64                      `gorm:"column:window_ends_at_ms"`
	WindowGeneration  uint64                      `gorm:"not null;default:0"`
	SnapshotVersion   uint64                      `gorm:"not null;default:1;check:chk_ak_cost_state_version,snapshot_version > 0"`
	Rule              *accessKeyCostLimitRule0002 `gorm:"foreignKey:RuleID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	UpdatedAtMS       int64                       `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli;check:chk_ak_cost_state_updated_at,updated_at_ms >= 0"`
}

func (accessKeyCostLimitState0002) TableName() string {
	return "access_key_cost_limit_states"
}

// Up0002 adds normalized AccessKey cost-limit rules and their latest checkpoint.
func Up0002(db *gorm.DB) error {
	if err := db.AutoMigrate(SchemaModels0002()...); err != nil {
		return fmt.Errorf("create access key cost limit schema: %w", err)
	}
	return nil
}

// SchemaModels0002 returns the migration-local models in deterministic DDL order.
func SchemaModels0002() []any {
	return []any{
		&accessKeyCostLimitRule0002{},
		&accessKeyCostLimitState0002{},
	}
}

// TableNames0002 returns the application tables created by this migration.
func TableNames0002() []string {
	return []string{
		"access_key_cost_limit_rules",
		"access_key_cost_limit_states",
	}
}

type schemaDefinition0002 struct {
	model       any
	table       string
	columns     map[string]struct{}
	indexes     []string
	constraints []string
}

func schemaDefinitions0002(db *gorm.DB) ([]schemaDefinition0002, error) {
	definitions := make([]schemaDefinition0002, 0, len(SchemaModels0002()))
	for _, model := range SchemaModels0002() {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			return nil, fmt.Errorf("parse access key cost limit schema model: %w", err)
		}
		definition := schemaDefinition0002{
			model: model, table: statement.Schema.Table, columns: make(map[string]struct{}),
		}
		for _, field := range statement.Schema.Fields {
			if field.DBName != "" {
				definition.columns[strings.ToLower(field.DBName)] = struct{}{}
			}
		}
		for _, index := range statement.Schema.ParseIndexes() {
			definition.indexes = append(definition.indexes, index.Name)
		}
		for name := range statement.Schema.ParseCheckConstraints() {
			definition.constraints = append(definition.constraints, name)
		}
		for name := range statement.Schema.ParseUniqueConstraints() {
			definition.constraints = append(definition.constraints, name)
		}
		for _, relationship := range statement.Schema.Relationships.Relations {
			if constraint := relationship.ParseConstraint(); constraint != nil {
				definition.constraints = append(definition.constraints, constraint.Name)
			}
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

// ValidateRecoverable0002 rejects unsafe partial MySQL migration state.
func ValidateRecoverable0002(db *gorm.DB) error {
	definitions, err := schemaDefinitions0002(db)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if !db.Migrator().HasTable(definition.model) {
			continue
		}
		var count int64
		if err := db.Table(definition.table).Count(&count).Error; err != nil {
			return fmt.Errorf("count interrupted access key cost limit table %q: %w", definition.table, err)
		}
		if count != 0 {
			return fmt.Errorf("table %q contains data", definition.table)
		}
		columns, err := db.Migrator().ColumnTypes(definition.table)
		if err != nil {
			return fmt.Errorf("inspect interrupted access key cost limit table %q: %w", definition.table, err)
		}
		for _, column := range columns {
			if _, expected := definition.columns[strings.ToLower(column.Name())]; !expected {
				return fmt.Errorf("table %q contains unexpected column %q", definition.table, column.Name())
			}
		}
	}
	return nil
}

// Validate0002 verifies the tables, columns, indexes, and constraints owned by 0002.
func Validate0002(db *gorm.DB) error {
	definitions, err := schemaDefinitions0002(db)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		if !db.Migrator().HasTable(definition.model) {
			return fmt.Errorf("validate access key cost limit schema: table %q is missing", definition.table)
		}
		for column := range definition.columns {
			if !db.Migrator().HasColumn(definition.model, column) {
				return fmt.Errorf(
					"validate access key cost limit schema: column %q.%q is missing",
					definition.table,
					column,
				)
			}
		}
		for _, index := range definition.indexes {
			if !db.Migrator().HasIndex(definition.model, index) {
				return fmt.Errorf(
					"validate access key cost limit schema: index %q on %q is missing",
					index,
					definition.table,
				)
			}
		}
		for _, constraint := range definition.constraints {
			if !db.Migrator().HasConstraint(definition.model, constraint) {
				return fmt.Errorf(
					"validate access key cost limit schema: constraint %q on %q is missing",
					constraint,
					definition.table,
				)
			}
		}
	}
	return nil
}
