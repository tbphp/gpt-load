package models

import (
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type PolicyScope string

const (
	PolicyScopeGroup      PolicyScope = "group"
	PolicyScopeCredential PolicyScope = "credential"
)

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
	ID            uint        `gorm:"primaryKey;autoIncrement"`
	Scope         PolicyScope `gorm:"type:varchar(16);not null;uniqueIndex:idx_policy_bindings_target,priority:1"`
	GroupID       uint        `gorm:"not null;default:0;uniqueIndex:idx_policy_bindings_target,priority:2"`
	CredentialID  uint        `gorm:"not null;default:0;uniqueIndex:idx_policy_bindings_target,priority:3"`
	Revision      uint64      `gorm:"type:bigint;not null;default:1"`
	SchemaVersion int         `gorm:"not null;default:1"`
	Config        JSON        `gorm:"not null"`
	CreatedAtMS   int64       `gorm:"not null"`
	UpdatedAtMS   int64       `gorm:"not null"`
}

func (PolicyBinding) TableName() string {
	return "policy_bindings"
}
