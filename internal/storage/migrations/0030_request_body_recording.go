package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const ID0030 = "0030_request_body_recording"

type accessKey0030 struct {
	RecordRequestBody bool `gorm:"not null;default:false"`
}

func (accessKey0030) TableName() string { return "access_keys" }

type requestLogParent0030 struct {
	ID string `gorm:"type:varchar(36);primaryKey;not null"`
}

func (requestLogParent0030) TableName() string { return "request_logs" }

type requestLogBodyText0030 string

func (requestLogBodyText0030) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if strings.EqualFold(db.Dialector.Name(), "mysql") {
		return "longtext"
	}
	return "text"
}

type requestLogBody0030 struct {
	Request       requestLogParent0030    `gorm:"foreignKey:RequestID;references:ID;constraint:OnDelete:CASCADE"`
	RequestID     string                  `gorm:"type:varchar(36);primaryKey;not null"`
	State         string                  `gorm:"type:varchar(32);not null;check:chk_request_log_body_state,state IN ('original','truncated','non_json','too_large','queue_full')"`
	OriginalBytes int64                   `gorm:"not null;check:chk_request_log_body_original_bytes,original_bytes >= 0"`
	Body          *requestLogBodyText0030 `gorm:"check:chk_request_log_body_content,(state IN ('original','truncated') AND body IS NOT NULL) OR (state IN ('non_json','too_large','queue_full') AND body IS NULL)"`
}

func (requestLogBody0030) TableName() string { return "request_log_bodies" }

var requestLogBodyChecks0030 = []string{
	"chk_request_log_body_state",
	"chk_request_log_body_original_bytes",
	"chk_request_log_body_content",
}

// Up0030 增加访问密钥的请求内容记录开关（默认关闭）和随请求日志级联删除的原始请求体表。
func Up0030(db *gorm.DB) error {
	if err := ValidateRecoverable0030(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&accessKey0030{}, "record_request_body") {
		if err := db.Migrator().AddColumn(&accessKey0030{}, "RecordRequestBody"); err != nil {
			return fmt.Errorf("add access key request body recording: %w", err)
		}
	}
	if !db.Migrator().HasTable(&requestLogBody0030{}) {
		if err := db.Migrator().CreateTable(&requestLogBody0030{}); err != nil {
			return fmt.Errorf("create request log bodies: %w", err)
		}
	}
	if !db.Migrator().HasConstraint(&requestLogBody0030{}, "Request") {
		if err := db.Migrator().CreateConstraint(&requestLogBody0030{}, "Request"); err != nil {
			return fmt.Errorf("create request log body foreign key: %w", err)
		}
	}
	return Validate0030(db)
}

func ValidateRecoverable0030(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported request body recording migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable("access_keys") || !db.Migrator().HasTable("request_logs") {
		return fmt.Errorf("request body recording requires access_keys and request_logs")
	}
	if db.Migrator().HasColumn(&accessKey0030{}, "record_request_body") {
		if err := validateRecordColumn0030(db); err != nil {
			return err
		}
	}
	if db.Migrator().HasTable(&requestLogBody0030{}) {
		return validateBodyTable0030(db, false)
	}
	return nil
}

func Validate0030(db *gorm.DB) error {
	if err := validateRecordColumn0030(db); err != nil {
		return err
	}
	return validateBodyTable0030(db, true)
}

func validateRecordColumn0030(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes(&accessKey0030{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "record_request_body" {
			continue
		}
		kind := strings.ToLower(column.DatabaseTypeName())
		if kind != "bool" && kind != "boolean" && kind != "numeric" && !strings.Contains(kind, "int") {
			return fmt.Errorf("access key request body recording must be boolean")
		}
		if nullable, known := column.Nullable(); !known || nullable {
			return fmt.Errorf("access key request body recording must not be nullable")
		}
		value, known := column.DefaultValue()
		switch strings.ToLower(strings.Trim(strings.SplitN(value, "::", 2)[0], "'\"() ")) {
		case "0", "false":
		default:
			known = false
		}
		if !known {
			return fmt.Errorf("access key request body recording must default to false")
		}
		return nil
	}
	return fmt.Errorf("access key request body recording is missing")
}

// validateBodyTable0030 与 0021 的子表校验一致，只核对列与约束是否存在，
// 不额外读取列类型，避免 MySQL 每次启动多一次元数据查询。
func validateBodyTable0030(db *gorm.DB, requireForeignKey bool) error {
	model := &requestLogBody0030{}
	if !db.Migrator().HasTable(model) {
		return fmt.Errorf("request log bodies table is missing")
	}
	for _, name := range []string{"RequestID", "State", "OriginalBytes", "Body"} {
		if !db.Migrator().HasColumn(model, name) {
			return fmt.Errorf("request log body column %s is missing", name)
		}
	}
	for _, name := range requestLogBodyChecks0030 {
		if !db.Migrator().HasConstraint(model, name) {
			return fmt.Errorf("request log body constraint %s is missing", name)
		}
	}
	if requireForeignKey && !db.Migrator().HasConstraint(model, "Request") {
		return fmt.Errorf("request log body foreign key is missing")
	}
	return nil
}
