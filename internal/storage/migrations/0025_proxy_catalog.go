package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const ID0025 = "0025_proxy_catalog"

type proxy0025 struct {
	ID                 uint   `gorm:"primaryKey;autoIncrement"`
	Name               string `gorm:"type:varchar(255);not null"`
	Config             string `gorm:"type:text;not null"`
	Fingerprint        string `gorm:"type:varchar(128);not null;uniqueIndex:ux_proxies_fingerprint"`
	Enabled            bool   `gorm:"not null;default:true;index:idx_proxies_enabled"`
	LastTestURL        string `gorm:"column:last_test_url;type:varchar(2048);not null;default:''"`
	LastTestAtMS       *int64 `gorm:"column:last_test_at_ms"`
	LastTestDurationMS *int64 `gorm:"column:last_test_duration_ms"`
	LastTestStatusCode *int   `gorm:"column:last_test_status_code"`
	LastTestError      string `gorm:"type:varchar(64);not null;default:''"`
	CreatedAtMS        int64  `gorm:"column:created_at_ms;not null;autoCreateTime:milli"`
	UpdatedAtMS        int64  `gorm:"column:updated_at_ms;not null;autoUpdateTime:milli"`
}

func (proxy0025) TableName() string { return "proxies" }

// Up0025 仅建立存储；加密旧数据在持有原密钥的启动加载阶段事务化转换。
func Up0025(db *gorm.DB) error {
	if err := ValidateRecoverable0025(db); err != nil {
		return err
	}
	if !db.Migrator().HasTable(&proxy0025{}) {
		if err := db.Migrator().CreateTable(&proxy0025{}); err != nil {
			return err
		}
	}
	for _, name := range []string{"ux_proxies_fingerprint", "idx_proxies_enabled"} {
		if !db.Migrator().HasIndex(&proxy0025{}, name) {
			if err := db.Migrator().CreateIndex(&proxy0025{}, name); err != nil {
				return err
			}
		}
	}
	return Validate0025(db)
}

func ValidateRecoverable0025(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported proxy migration driver")
	}
	if !db.Migrator().HasTable(&proxy0025{}) {
		return nil
	}
	columns, err := db.Migrator().ColumnTypes(&proxy0025{})
	if err != nil {
		return err
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(&proxy0025{}); err != nil {
		return err
	}
	found := make(map[string]bool, len(columns))
	for _, column := range columns {
		field := statement.Schema.LookUpField(column.Name())
		if field == nil {
			return fmt.Errorf("unexpected proxies column %s", column.Name())
		}
		found[column.Name()] = true
		typeName := strings.ToLower(column.DatabaseTypeName())
		if (field.DataType == "int" || field.DataType == "uint") && !strings.Contains(typeName, "int") {
			return fmt.Errorf("proxies column %s must be integer", column.Name())
		}
		if field.DataType == "string" && !strings.Contains(typeName, "char") && !strings.Contains(typeName, "text") {
			return fmt.Errorf("proxies column %s must be text", column.Name())
		}
		if field.DataType == "bool" && typeName != "bool" && typeName != "boolean" && typeName != "numeric" && !strings.Contains(typeName, "int") {
			return fmt.Errorf("proxies column %s must be boolean", column.Name())
		}
		if nullable, known := column.Nullable(); field.NotNull && known && nullable {
			return fmt.Errorf("proxies column %s must be non-null", column.Name())
		}
		if primary, known := column.PrimaryKey(); field.PrimaryKey && known && !primary {
			return fmt.Errorf("proxies column %s must be primary key", column.Name())
		}
	}
	for _, field := range statement.Schema.Fields {
		if !found[field.DBName] {
			return fmt.Errorf("incomplete proxies table: missing %s", field.DBName)
		}
	}
	// SQLite 驱动在索引元数据读取中强制 Debug，避免启动时输出内部查询。
	indexes, err := db.Session(&gorm.Session{Logger: logger.Discard}).Migrator().GetIndexes(&proxy0025{})
	if err != nil {
		return err
	}
	for _, index := range indexes {
		column := ""
		unique := false
		switch index.Name() {
		case "ux_proxies_fingerprint":
			column, unique = "fingerprint", true
		case "idx_proxies_enabled":
			column = "enabled"
		default:
			continue
		}
		actualUnique, known := index.Unique()
		if fields := index.Columns(); len(fields) != 1 || fields[0] != column || !known || actualUnique != unique {
			return fmt.Errorf("incompatible proxy index %s", index.Name())
		}
	}
	return nil
}

func Validate0025(db *gorm.DB) error {
	if !db.Migrator().HasTable(&proxy0025{}) {
		return fmt.Errorf("proxies table is missing")
	}
	if err := ValidateRecoverable0025(db); err != nil {
		return err
	}
	for _, name := range []string{"ux_proxies_fingerprint", "idx_proxies_enabled"} {
		if !db.Migrator().HasIndex(&proxy0025{}, name) {
			return fmt.Errorf("proxy index %s is missing", name)
		}
	}
	return nil
}
