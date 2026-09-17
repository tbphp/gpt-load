package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0016 = "0016_credential_quota_history"

type quotaHistory0016 struct {
	ID              uint   `gorm:"primaryKey;autoIncrement"`
	GroupID         uint   `gorm:"not null;check:chk_quota_history_group,group_id > 0"`
	CredentialID    uint   `gorm:"not null;uniqueIndex:idx_quota_history_identity,priority:1;check:chk_quota_history_credential,credential_id > 0"`
	TargetIdentity  string `gorm:"type:varchar(16);not null;uniqueIndex:idx_quota_history_identity,priority:2"`
	WindowKey       string `gorm:"type:varchar(64);not null;uniqueIndex:idx_quota_history_identity,priority:3"`
	WindowID        string `gorm:"type:varchar(128);not null"`
	SourceID        string `gorm:"type:varchar(128);not null;default:''"`
	Label           string `gorm:"type:varchar(255);not null"`
	LabelKey        string `gorm:"type:varchar(64);not null;default:''"`
	Scope           string `gorm:"type:varchar(255);not null"`
	ObservedAtMS    int64  `gorm:"column:observed_at_ms;not null;uniqueIndex:idx_quota_history_identity,priority:4;index:idx_quota_history_retention;check:chk_quota_history_time,observed_at_ms >= 0"`
	UsedBasisPoints int64  `gorm:"not null;check:chk_quota_history_percent,used_basis_points >= 0 AND used_basis_points <= 10000"`
	ResetAtMS       *int64 `gorm:"column:reset_at_ms;check:chk_quota_history_reset,reset_at_ms IS NULL OR reset_at_ms >= 0"`
	WindowSeconds   *int64 `gorm:"check:chk_quota_history_period,window_seconds IS NULL OR window_seconds > 0"`
}

func (quotaHistory0016) TableName() string { return "credential_quota_histories" }

// Up0016 使用冻结模型扩展三数据库的同一增量链。
func Up0016(db *gorm.DB) error {
	if Validate0016(db) == nil {
		return nil
	}
	if err := ValidateRecoverable0016(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(&quotaHistory0016{}); err != nil {
		return fmt.Errorf("create credential quota history: %w", err)
	}
	return Validate0016(db)
}

func ValidateRecoverable0016(db *gorm.DB) error {
	if !db.Migrator().HasTable(&quotaHistory0016{}) {
		return nil
	}
	if Validate0016(db) == nil {
		return nil
	}
	var count int64
	if err := db.Model(&quotaHistory0016{}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("incomplete quota history schema contains data")
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&quotaHistory0016{}); err != nil {
		return err
	}
	columns, err := db.Migrator().ColumnTypes(&quotaHistory0016{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if statement.Schema.LookUpField(column.Name()) == nil {
			return fmt.Errorf("quota history contains unexpected column %q", column.Name())
		}
	}
	return nil
}

func Validate0016(db *gorm.DB) error {
	model := &quotaHistory0016{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("credential quota history table is missing")
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(model); err != nil {
		return err
	}
	columns, err := db.Migrator().ColumnTypes(model)
	if err != nil {
		return err
	}
	found := make(map[string]bool)
	for _, column := range columns {
		field := statement.Schema.LookUpField(column.Name())
		if field == nil {
			return fmt.Errorf("quota history contains unexpected column %q", column.Name())
		}
		found[column.Name()] = true
		integer := field.DataType == "int" || field.DataType == "uint"
		if integer && !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("quota history column %s must be integer", column.Name())
		}
		if field.NotNull {
			if nullable, known := column.Nullable(); known && nullable {
				return fmt.Errorf("quota history column %s must be non-null", column.Name())
			}
		}
	}
	for _, field := range statement.Schema.Fields {
		if !found[field.DBName] {
			return fmt.Errorf("quota history column %s is missing", field.DBName)
		}
	}
	for _, index := range statement.Schema.ParseIndexes() {
		if !db.Migrator().HasIndex(model, index.Name) {
			return fmt.Errorf("quota history index %s is missing", index.Name)
		}
	}
	for name := range statement.Schema.ParseCheckConstraints() {
		if !db.Migrator().HasConstraint(model, name) {
			return fmt.Errorf("quota history constraint %s is missing", name)
		}
	}
	return nil
}
