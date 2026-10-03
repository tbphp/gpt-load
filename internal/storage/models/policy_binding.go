package models

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type PolicyScope string

const (
	PolicyScopeGroup      PolicyScope = "group"
	PolicyScopeCredential PolicyScope = "credential"
)

// PolicyRevision 封装 64 位无符号版本号，实现 driver.Valuer 和 sql.Scanner，
// 保证在 SQLite (int64 存储) 及各类数据库驱动下均可无损存取满位无符号整数 (包括 ^uint64(0))，
// 避免标准库驱动拦截高位 (uint64 values with high bit set are not supported)。
type PolicyRevision uint64

func (PolicyRevision) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	return "bigint"
}

func (r PolicyRevision) Value() (driver.Value, error) {
	return int64(r), nil
}

func (r *PolicyRevision) Scan(value any) error {
	switch v := value.(type) {
	case int64:
		*r = PolicyRevision(uint64(v))
		return nil
	case uint64:
		*r = PolicyRevision(v)
		return nil
	case []byte:
		return r.scanString(string(v))
	case string:
		return r.scanString(v)
	default:
		return fmt.Errorf("cannot scan %T into PolicyRevision", value)
	}
}

func (r *PolicyRevision) scanString(raw string) error {
	s := strings.TrimSpace(raw)
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		*r = PolicyRevision(n)
		return nil
	}
	// 兼容以有符号负数形式持久化的 SQLite highbit 旧记录 (如 "-1" 代表 ^uint64(0))
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*r = PolicyRevision(uint64(n))
		return nil
	}
	return fmt.Errorf("cannot parse %q into PolicyRevision", raw)
}

// GormDBDataType 仅作为针对 policy_bindings.Config 字段的动态 schema hook，
// 确保 MySQL 下该字段获得 mediumtext/longtext 存储规格以容纳合同规定的 256KiB 正文，
// 对于其他所有业务表或非 Config 字段一律返回空字符串以退回 GORM 默认方言处理，
// 绝不改变全局其他业务 JSON 列的数据类型。
func (JSON) GormDBDataType(db *gorm.DB, field *schema.Field) string {
	if field != nil && field.Schema != nil && field.Schema.Table == "policy_bindings" && field.Name == "Config" {
		if strings.EqualFold(db.Dialector.Name(), "mysql") {
			return "longtext"
		}
		return "text"
	}
	return ""
}

type PolicyBinding struct {
	ID            uint           `gorm:"primaryKey;autoIncrement"`
	Scope         PolicyScope    `gorm:"type:varchar(16);not null;uniqueIndex:idx_policy_bindings_target,priority:1"`
	GroupID       uint           `gorm:"not null;default:0;uniqueIndex:idx_policy_bindings_target,priority:2"`
	CredentialID  uint           `gorm:"not null;default:0;uniqueIndex:idx_policy_bindings_target,priority:3"`
	Revision      PolicyRevision `gorm:"type:bigint;not null;default:1"`
	SchemaVersion int            `gorm:"not null;default:1"`
	Config        JSON           `gorm:"not null"`
	CreatedAtMS   int64          `gorm:"not null"`
	UpdatedAtMS   int64          `gorm:"not null"`
}

func (PolicyBinding) TableName() string {
	return "policy_bindings"
}
