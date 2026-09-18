package migrations

import (
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

const ID0017 = "0017_request_log_operation_index"
const requestLogOperationIndex0017 = "idx_request_logs_operation_completed_id"

type requestLog0017 struct {
	Operation     string `gorm:"column:operation;index:idx_request_logs_operation_completed_id,priority:1"`
	CompletedAtMS int64  `gorm:"column:completed_at_ms;index:idx_request_logs_operation_completed_id,priority:2,sort:desc"`
	ID            string `gorm:"column:id;index:idx_request_logs_operation_completed_id,priority:3,sort:desc"`
}

func (requestLog0017) TableName() string { return "request_logs" }

// Up0017 为操作筛选增加与游标顺序一致的索引，不改历史日志。
func Up0017(db *gorm.DB) error {
	if err := ValidateRecoverable0017(db); err != nil {
		return err
	}
	if !db.Migrator().HasIndex(&requestLog0017{}, requestLogOperationIndex0017) {
		if err := db.Migrator().CreateIndex(&requestLog0017{}, requestLogOperationIndex0017); err != nil {
			return fmt.Errorf("create request log operation index: %w", err)
		}
	}
	return Validate0017(db)
}

func ValidateRecoverable0017(db *gorm.DB) error {
	if !db.Migrator().HasTable(&requestLog0017{}) {
		return fmt.Errorf("request log operation index: request_logs table is missing")
	}
	if db.Migrator().HasIndex(&requestLog0017{}, requestLogOperationIndex0017) {
		return Validate0017(db)
	}
	return nil
}

func Validate0017(db *gorm.DB) error {
	if db.Dialector.Name() == "postgres" {
		// GORM 的 PostgreSQL GetIndexes 未按 indkey 的位置排序，不能据此验证复合索引。
		var columns []struct {
			Name     string
			IsUnique bool
			IsValid  bool
		}
		if err := db.Raw(`
			SELECT attribute.attname AS name, definition.indisunique AS is_unique,
				definition.indisvalid AS is_valid
			FROM pg_index AS definition
			JOIN pg_class AS table_relation ON table_relation.oid = definition.indrelid
			JOIN pg_namespace AS namespace ON namespace.oid = table_relation.relnamespace
			JOIN pg_class AS index_relation ON index_relation.oid = definition.indexrelid
			CROSS JOIN LATERAL unnest(definition.indkey) WITH ORDINALITY AS key(attnum, position)
			JOIN pg_attribute AS attribute ON attribute.attrelid = table_relation.oid
				AND attribute.attnum = key.attnum
			WHERE namespace.nspname = current_schema() AND table_relation.relname = 'request_logs'
				AND index_relation.relname = ?
			ORDER BY key.position`, requestLogOperationIndex0017).Scan(&columns).Error; err != nil {
			return fmt.Errorf("read PostgreSQL request log operation index: %w", err)
		}
		want := []string{"operation", "completed_at_ms", "id"}
		if len(columns) != len(want) {
			return fmt.Errorf("request log operation index is missing or has an unexpected definition")
		}
		for position, column := range columns {
			if column.Name != want[position] || column.IsUnique || !column.IsValid {
				return fmt.Errorf("request log operation index has an unexpected definition")
			}
		}
		return nil
	}
	indexes, err := db.Migrator().GetIndexes(&requestLog0017{})
	if err != nil {
		return fmt.Errorf("read request log indexes: %w", err)
	}
	for _, index := range indexes {
		if index.Name() != requestLogOperationIndex0017 {
			continue
		}
		unique, known := index.Unique()
		if !known || unique || !reflect.DeepEqual(index.Columns(), []string{"operation", "completed_at_ms", "id"}) {
			return fmt.Errorf("request log operation index has an unexpected definition")
		}
		return nil
	}
	return fmt.Errorf("request log operation index is missing")
}
