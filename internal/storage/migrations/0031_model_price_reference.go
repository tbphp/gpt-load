package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0031 = "0031_model_price_reference"

type modelPrice0031 struct {
	ID            uint            `gorm:"primaryKey;autoIncrement"`
	SourcePriceID *uint           `gorm:"index:idx_model_prices_source"`
	SourcePrice   *modelPrice0031 `gorm:"foreignKey:SourcePriceID;references:ID;constraint:OnDelete:RESTRICT"`
}

func (modelPrice0031) TableName() string { return "model_prices" }

// Up0031 保存模型间的价格关联；价格值仍在原列中持久化。
func Up0031(db *gorm.DB) error {
	if err := ValidateRecoverable0031(db); err != nil {
		return err
	}
	model := &modelPrice0031{}
	if !db.Migrator().HasColumn(model, "SourcePriceID") {
		if err := db.Migrator().AddColumn(model, "SourcePriceID"); err != nil {
			return fmt.Errorf("add model price source: %w", err)
		}
	}
	if !db.Migrator().HasIndex(model, "idx_model_prices_source") {
		if err := db.Migrator().CreateIndex(model, "idx_model_prices_source"); err != nil {
			return err
		}
	}
	if !db.Migrator().HasConstraint(model, "SourcePrice") {
		if db.Dialector.Name() == "sqlite" {
			if err := addModelPriceSourceSQLite0031(db); err != nil {
				return err
			}
		} else if err := db.Migrator().CreateConstraint(model, "SourcePrice"); err != nil {
			return err
		}
	}
	return Validate0031(db)
}

func addModelPriceSourceSQLite0031(db *gorm.DB) error {
	var ddl string
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'model_prices'").Scan(&ddl).Error; err != nil {
		return err
	}
	start, end := strings.Index(ddl, "("), strings.LastIndex(ddl, ")")
	if start < 0 || end <= start {
		return fmt.Errorf("invalid model price table definition")
	}
	ddl = "CREATE TABLE model_prices__0031 " + ddl[start:end] +
		", CONSTRAINT fk_model_prices_source_price FOREIGN KEY (source_price_id) REFERENCES model_prices (id) ON DELETE RESTRICT" + ddl[end:]
	var objects []string
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE tbl_name = 'model_prices' AND type IN ('index','trigger') AND sql IS NOT NULL ORDER BY type, name").Scan(&objects).Error; err != nil {
		return err
	}
	var sequence int64
	if err := db.Raw("SELECT COALESCE(MAX(seq), 0) FROM sqlite_sequence WHERE name = 'model_prices'").Scan(&sequence).Error; err != nil {
		return err
	}
	columns, err := db.Migrator().ColumnTypes("model_prices")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		if strings.ContainsAny(column.Name(), "\"`\x00") {
			return fmt.Errorf("invalid model price column")
		}
		names = append(names, `"`+column.Name()+`"`)
	}
	projection := strings.Join(names, ",")
	// 迁移执行器已持有写事务；直接重建表，避免驱动再次 BEGIN，并恢复原索引和 ID 序列。
	for _, statement := range []string{ddl,
		"INSERT INTO model_prices__0031 (" + projection + ") SELECT " + projection + " FROM model_prices",
		"DROP TABLE model_prices", "ALTER TABLE model_prices__0031 RENAME TO model_prices"} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	if err := db.Exec("UPDATE sqlite_sequence SET seq = MAX(seq, ?) WHERE name = 'model_prices'", sequence).Error; err != nil {
		return err
	}
	for _, statement := range objects {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func ValidateRecoverable0031(db *gorm.DB) error {
	switch db.Dialector.Name() {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("unsupported model price reference migration driver %q", db.Dialector.Name())
	}
	if !db.Migrator().HasTable(&modelPrice0031{}) {
		return fmt.Errorf("model_prices table is missing")
	}
	return validateReferenceColumn0031(db, false)
}

func Validate0031(db *gorm.DB) error {
	if err := validateReferenceColumn0031(db, true); err != nil {
		return err
	}
	model := &modelPrice0031{}
	if !db.Migrator().HasIndex(model, "idx_model_prices_source") || !db.Migrator().HasConstraint(model, "SourcePrice") {
		return fmt.Errorf("model price source index or foreign key is missing")
	}
	return nil
}

func validateReferenceColumn0031(db *gorm.DB, required bool) error {
	columns, err := db.Migrator().ColumnTypes(&modelPrice0031{})
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() != "source_price_id" {
			continue
		}
		if !strings.Contains(strings.ToLower(column.DatabaseTypeName()), "int") {
			return fmt.Errorf("model price source must be an integer")
		}
		if nullable, known := column.Nullable(); !known || !nullable {
			return fmt.Errorf("model price source must be nullable")
		}
		if value, known := column.DefaultValue(); known && !strings.EqualFold(strings.TrimSpace(value), "null") {
			return fmt.Errorf("model price source must default to null")
		}
		return nil
	}
	if required {
		return fmt.Errorf("model price source column is missing")
	}
	return nil
}
