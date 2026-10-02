package models

import (
	"database/sql/driver"
	"fmt"
	"strconv"
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
		n, err := strconv.ParseUint(string(v), 10, 64)
		if err != nil {
			return err
		}
		*r = PolicyRevision(n)
		return nil
	case string:
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return err
		}
		*r = PolicyRevision(n)
		return nil
	default:
		return fmt.Errorf("cannot scan %T into PolicyRevision", value)
	}
}

type PolicyBinding struct {
	ID            uint           `gorm:"primaryKey;autoIncrement"`
	Scope         PolicyScope    `gorm:"type:varchar(16);not null;index:idx_policy_bindings_target,priority:1,unique"`
	GroupID       uint           `gorm:"not null;default:0;index:idx_policy_bindings_target,priority:2,unique"`
	CredentialID  uint           `gorm:"not null;default:0;index:idx_policy_bindings_target,priority:3,unique"`
	Revision      PolicyRevision `gorm:"not null;default:1"`
	SchemaVersion int            `gorm:"not null;default:1"`
	Config        JSON           `gorm:"type:text;not null"`
	CreatedAtMS   int64          `gorm:"not null"`
	UpdatedAtMS   int64          `gorm:"not null"`
}

func (PolicyBinding) TableName() string {
	return "policy_bindings"
}
