package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0022 = "0022_rpm_stats"

type rpmStat0022 struct {
	ID        uint   `gorm:"primaryKey;autoIncrement"`
	Kind      string `gorm:"type:varchar(16);not null;check:chk_rpm_stats_kind,kind IN ('access_key','credential');uniqueIndex:idx_rpm_stats_identity,priority:1"`
	KeyID     uint   `gorm:"not null;check:chk_rpm_stats_key,key_id > 0;uniqueIndex:idx_rpm_stats_identity,priority:2"`
	MinuteMS  int64  `gorm:"column:minute_ms;not null;check:chk_rpm_stats_minute,minute_ms >= 0;uniqueIndex:idx_rpm_stats_identity,priority:3;index:idx_rpm_stats_retention"`
	SessionID string `gorm:"type:varchar(32);not null;uniqueIndex:idx_rpm_stats_identity,priority:4"`
	Requests  int64  `gorm:"not null;check:chk_rpm_stats_requests,requests >= 0"`
	Rejected  int64  `gorm:"not null;check:chk_rpm_stats_rejected,rejected >= 0 AND rejected <= requests"`
	Peak      int64  `gorm:"not null;check:chk_rpm_stats_peak,peak >= 0"`
}

func (rpmStat0022) TableName() string { return "rpm_stats" }

// Up0022 使用冻结模型扩展三数据库的同一增量链。
func Up0022(db *gorm.DB) error {
	if Validate0022(db) == nil {
		return nil
	}
	if err := ValidateRecoverable0022(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(&rpmStat0022{}); err != nil {
		return fmt.Errorf("create RPM statistics: %w", err)
	}
	return Validate0022(db)
}

func ValidateRecoverable0022(db *gorm.DB) error {
	if !db.Migrator().HasTable(&rpmStat0022{}) {
		return nil
	}
	if Validate0022(db) == nil {
		return nil
	}
	var count int64
	if err := db.Model(&rpmStat0022{}).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("incomplete RPM statistics schema contains data")
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&rpmStat0022{}); err != nil {
		return err
	}
	columns, err := db.Migrator().ColumnTypes(&rpmStat0022{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if statement.Schema.LookUpField(column.Name()) == nil {
			return fmt.Errorf("RPM statistics contains unexpected column %q", column.Name())
		}
	}
	return nil
}

func Validate0022(db *gorm.DB) error {
	model := &rpmStat0022{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("RPM statistics table is missing")
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
			return fmt.Errorf("RPM statistics contains unexpected column %q", column.Name())
		}
		found[column.Name()] = true
		integer := field.DataType == "int" || field.DataType == "uint"
		if integer && !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("RPM statistics column %s must be integer", column.Name())
		}
		if field.NotNull {
			if nullable, known := column.Nullable(); known && nullable {
				return fmt.Errorf("RPM statistics column %s must be non-null", column.Name())
			}
		}
	}
	for _, field := range statement.Schema.Fields {
		if !found[field.DBName] {
			return fmt.Errorf("RPM statistics column %s is missing", field.DBName)
		}
	}
	for _, index := range statement.Schema.ParseIndexes() {
		if !db.Migrator().HasIndex(model, index.Name) {
			return fmt.Errorf("RPM statistics index %s is missing", index.Name)
		}
	}
	for name := range statement.Schema.ParseCheckConstraints() {
		if !db.Migrator().HasConstraint(model, name) {
			return fmt.Errorf("RPM statistics constraint %s is missing", name)
		}
	}
	return nil
}
