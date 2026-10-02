package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0028 = "0028_request_log_client_ip"
const requestLogIPIndex0028 = "idx_request_logs_ip_completed_id"

type requestLog0028 struct {
	ClientIP      *string `gorm:"column:client_ip;type:varchar(45);index:idx_request_logs_ip_completed_id,priority:1"`
	CompletedAtMS int64   `gorm:"column:completed_at_ms;index:idx_request_logs_ip_completed_id,priority:2,sort:desc"`
	ID            string  `gorm:"column:id;index:idx_request_logs_ip_completed_id,priority:3,sort:desc"`
}

func (requestLog0028) TableName() string { return "request_logs" }

type requestLogIPIndexColumn0028 struct {
	Name       string
	Descending bool
	IsUnique   bool
	IsValid    bool
}

// Up0028 增加可空来源 IP 与精确搜索索引，历史日志不回填。
func Up0028(db *gorm.DB) error {
	if err := ValidateRecoverable0028(db); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&requestLog0028{}, "client_ip") {
		if err := db.Migrator().AddColumn(&requestLog0028{}, "ClientIP"); err != nil {
			return fmt.Errorf("add request log client IP: %w", err)
		}
	}
	if !db.Migrator().HasIndex(&requestLog0028{}, requestLogIPIndex0028) {
		if err := db.Migrator().CreateIndex(&requestLog0028{}, requestLogIPIndex0028); err != nil {
			return fmt.Errorf("create request log client IP index: %w", err)
		}
	}
	return Validate0028(db)
}

func ValidateRecoverable0028(db *gorm.DB) error {
	if !db.Migrator().HasTable(&requestLog0028{}) {
		return fmt.Errorf("request log client IP index: request_logs table is missing")
	}
	if db.Migrator().HasColumn(&requestLog0028{}, "client_ip") {
		if err := validateClientIPColumn0028(db); err != nil {
			return err
		}
	}
	if db.Migrator().HasIndex(&requestLog0028{}, requestLogIPIndex0028) {
		return Validate0028(db)
	}
	return nil
}

func Validate0028(db *gorm.DB) error {
	if err := validateClientIPColumn0028(db); err != nil {
		return err
	}
	columns, err := requestLogIPIndexColumns0028(db)
	if err != nil {
		return err
	}
	want := []struct {
		name string
		desc bool
	}{
		{name: "client_ip"},
		{name: "completed_at_ms", desc: true},
		{name: "id", desc: true},
	}
	if len(columns) != len(want) {
		return fmt.Errorf("request log client IP index is missing or has an unexpected definition")
	}
	for position, column := range columns {
		if column.Name != want[position].name || column.Descending != want[position].desc || column.IsUnique || !column.IsValid {
			return fmt.Errorf("request log client IP index has an unexpected definition")
		}
	}
	return nil
}

func requestLogIPIndexColumns0028(db *gorm.DB) ([]requestLogIPIndexColumn0028, error) {
	var columns []requestLogIPIndexColumn0028
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
		arguments = []any{requestLogIPIndex0028}
	case "mysql":
		query = `
			SELECT column_name AS name, collation = 'D' AS descending,
				non_unique = 0 AS is_unique, 1 AS is_valid
			FROM information_schema.statistics
			WHERE table_schema = DATABASE() AND table_name = 'request_logs' AND index_name = ?
			ORDER BY seq_in_index`
		arguments = []any{requestLogIPIndex0028}
	case "sqlite":
		query = `
			SELECT column_info.name AS name, column_info.desc <> 0 AS descending,
				index_list."unique" <> 0 AS is_unique, 1 AS is_valid
			FROM pragma_index_xinfo(?) AS column_info
			JOIN pragma_index_list('request_logs') AS index_list ON index_list.name = ?
			WHERE column_info.key = 1
			ORDER BY column_info.seqno`
		arguments = []any{requestLogIPIndex0028, requestLogIPIndex0028}
	default:
		return nil, fmt.Errorf("request log client IP index: unsupported database driver %q", db.Dialector.Name())
	}
	if err := db.Raw(query, arguments...).Scan(&columns).Error; err != nil {
		return nil, fmt.Errorf("read request log client IP index: %w", err)
	}
	return columns, nil
}

func validateClientIPColumn0028(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes("request_logs")
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "client_ip" {
			continue
		}
		kind := strings.ToLower(column.DatabaseTypeName())
		if kind != "varchar" && kind != "character varying" {
			return fmt.Errorf("client_ip must be varchar(45)")
		}
		if length, known := column.Length(); !known || length != 45 {
			return fmt.Errorf("client_ip must have length 45")
		}
		if nullable, known := column.Nullable(); !known || !nullable {
			return fmt.Errorf("client_ip must be nullable")
		}
		return nil
	}
	return fmt.Errorf("client_ip column is missing")
}
