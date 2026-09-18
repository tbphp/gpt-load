package migrations

import (
	"fmt"

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

type requestLogOperationIndexColumn0017 struct {
	Name       string
	Descending bool
	IsUnique   bool
	IsValid    bool
}

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
	columns, err := requestLogOperationIndexColumns0017(db)
	if err != nil {
		return err
	}
	want := []struct {
		name string
		desc bool
	}{
		{name: "operation"},
		{name: "completed_at_ms", desc: true},
		{name: "id", desc: true},
	}
	if len(columns) != len(want) {
		return fmt.Errorf("request log operation index is missing or has an unexpected definition")
	}
	for position, column := range columns {
		if column.Name != want[position].name || column.Descending != want[position].desc || column.IsUnique || !column.IsValid {
			return fmt.Errorf("request log operation index has an unexpected definition")
		}
	}
	return nil
}

func requestLogOperationIndexColumns0017(db *gorm.DB) ([]requestLogOperationIndexColumn0017, error) {
	var columns []requestLogOperationIndexColumn0017
	var query string
	var arguments []any
	switch db.Dialector.Name() {
	case "postgres":
		// GORM 的 PostgreSQL GetIndexes 未按 indkey 的位置排序，也不返回排序方向。
		query = `
			SELECT attribute.attname AS name,
				(definition.indoption[key.position - 1] & 1) <> 0 AS descending,
				definition.indisunique AS is_unique, definition.indisvalid AS is_valid
			FROM pg_index AS definition
			JOIN pg_class AS table_relation ON table_relation.oid = definition.indrelid
			JOIN pg_namespace AS namespace ON namespace.oid = table_relation.relnamespace
			JOIN pg_class AS index_relation ON index_relation.oid = definition.indexrelid
			CROSS JOIN LATERAL unnest(definition.indkey) WITH ORDINALITY AS key(attnum, position)
			JOIN pg_attribute AS attribute ON attribute.attrelid = table_relation.oid
				AND attribute.attnum = key.attnum
			WHERE namespace.nspname = current_schema() AND table_relation.relname = 'request_logs'
				AND index_relation.relname = ?
			ORDER BY key.position`
		arguments = []any{requestLogOperationIndex0017}
	case "mysql":
		query = `
			SELECT column_name AS name, collation = 'D' AS descending,
				non_unique = 0 AS is_unique, 1 AS is_valid
			FROM information_schema.statistics
			WHERE table_schema = DATABASE() AND table_name = 'request_logs' AND index_name = ?
			ORDER BY seq_in_index`
		arguments = []any{requestLogOperationIndex0017}
	case "sqlite":
		query = `
			SELECT column_info.name AS name, column_info.desc <> 0 AS descending,
				index_list."unique" <> 0 AS is_unique, 1 AS is_valid
			FROM pragma_index_xinfo(?) AS column_info
			JOIN pragma_index_list('request_logs') AS index_list ON index_list.name = ?
			WHERE column_info.key = 1
			ORDER BY column_info.seqno`
		arguments = []any{requestLogOperationIndex0017, requestLogOperationIndex0017}
	default:
		return nil, fmt.Errorf("request log operation index: unsupported database driver %q", db.Dialector.Name())
	}
	if err := db.Raw(query, arguments...).Scan(&columns).Error; err != nil {
		return nil, fmt.Errorf("read request log operation index: %w", err)
	}
	return columns, nil
}
