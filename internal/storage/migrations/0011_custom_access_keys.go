package migrations

import (
	"fmt"
	"strings"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const ID0011 = "0011_custom_access_keys"
const accessKeySuffixCheck0011 = "chk_access_key_suffix"
const accessKeySuffixExpression0011 = "length(key_suffix) = 4"
const priorAccessKeySuffixExpression0011 = "length(key_suffix) = 4 AND substr(key_suffix, 1, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 2, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 3, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f') AND substr(key_suffix, 4, 1) IN ('0','1','2','3','4','5','6','7','8','9','a','b','c','d','e','f')"

// Up0011 保留四位尾号字段，允许自定义字符及短密钥的全遮罩。
func Up0011(db *gorm.DB) error {
	if err := ValidateRecoverable0011(db); err != nil {
		return err
	}
	if Validate0011(db) == nil {
		return nil
	}
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		if err := rebuildAccessKeysSQLite0011(db); err != nil {
			return err
		}
	} else {
		drop := "DROP CONSTRAINT"
		if dialector, ok := db.Dialector.(*gormmysql.Dialector); ok && dialector.Config != nil && mysqlRequiresCheckDropSyntax0003(dialector.ServerVersion) {
			drop = "DROP CHECK"
		}
		// MySQL 在同一条 DDL 内原子替换，building 标记可安全恢复。
		if err := db.Exec("ALTER TABLE access_keys " + drop + " chk_access_key_suffix, ADD CONSTRAINT chk_access_key_suffix CHECK (" + accessKeySuffixExpression0011 + ")").Error; err != nil {
			return err
		}
	}
	return Validate0011(db)
}

func ValidateRecoverable0011(db *gorm.DB) error {
	if !db.Migrator().HasTable("access_keys") || !db.Migrator().HasColumn("access_keys", "key_suffix") || !db.Migrator().HasConstraint("access_keys", accessKeySuffixCheck0011) {
		return fmt.Errorf("custom access key suffix schema is incomplete")
	}
	return nil
}

func Validate0011(db *gorm.DB) error {
	if err := ValidateRecoverable0011(db); err != nil {
		return err
	}
	definition, err := accessKeySuffixConstraint0011(db)
	if err != nil {
		return err
	}
	normalized := strings.NewReplacer(" ", "", "\n", "", "\t", "", "`", "", `"`, "", "(", "", ")", "", "::text", "").Replace(strings.ToLower(definition))
	if strings.EqualFold(db.Dialector.Name(), "sqlite") {
		if !strings.Contains(normalized, "constraintchk_access_key_suffixchecklengthkey_suffix=4,") && !strings.HasSuffix(normalized, "constraintchk_access_key_suffixchecklengthkey_suffix=4") {
			return fmt.Errorf("custom access key suffix constraint is invalid")
		}
	} else if normalized != "lengthkey_suffix=4" && normalized != "checklengthkey_suffix=4" {
		return fmt.Errorf("custom access key suffix constraint is invalid")
	}
	return nil
}

func accessKeySuffixConstraint0011(db *gorm.DB) (string, error) {
	var definition string
	var err error
	switch strings.ToLower(db.Dialector.Name()) {
	case "sqlite":
		err = db.Raw("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'access_keys'").Scan(&definition).Error
	case "mysql":
		err = db.Raw("SELECT CHECK_CLAUSE FROM information_schema.check_constraints WHERE constraint_schema = DATABASE() AND constraint_name = ?", accessKeySuffixCheck0011).Scan(&definition).Error
	case "postgres", "postgresql":
		err = db.Raw("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = ? AND conrelid = 'access_keys'::regclass", accessKeySuffixCheck0011).Scan(&definition).Error
	default:
		return "", fmt.Errorf("unsupported custom access key migration driver")
	}
	return definition, err
}

func rebuildAccessKeysSQLite0011(db *gorm.DB) error {
	var foreignKeys int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		return err
	}
	if foreignKeys != 0 {
		return fmt.Errorf("access key rebuild requires migration foreign key isolation")
	}
	ddl, err := accessKeySuffixConstraint0011(db)
	if err != nil {
		return err
	}
	if strings.Count(ddl, priorAccessKeySuffixExpression0011) != 1 {
		return fmt.Errorf("unexpected prior access key suffix constraint")
	}
	start := strings.Index(ddl, "(")
	if start < 0 {
		return fmt.Errorf("invalid access key table definition")
	}
	ddl = "CREATE TABLE access_keys__0011 " + strings.Replace(ddl[start:], priorAccessKeySuffixExpression0011, accessKeySuffixExpression0011, 1)
	var objects []string
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE tbl_name = 'access_keys' AND type IN ('index','trigger') AND sql IS NOT NULL ORDER BY type, name").Scan(&objects).Error; err != nil {
		return err
	}
	var sequence int64
	if err := db.Raw("SELECT COALESCE(MAX(seq), 0) FROM sqlite_sequence WHERE name = 'access_keys'").Scan(&sequence).Error; err != nil {
		return err
	}
	columns, err := db.Migrator().ColumnTypes("access_keys")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		if strings.ContainsAny(column.Name(), "\"`\x00") {
			return fmt.Errorf("invalid access key column")
		}
		names = append(names, `"`+column.Name()+`"`)
	}
	projection := strings.Join(names, ",")
	for _, statement := range []string{ddl,
		"INSERT INTO access_keys__0011 (" + projection + ") SELECT " + projection + " FROM access_keys",
		"DROP TABLE access_keys", "ALTER TABLE access_keys__0011 RENAME TO access_keys"} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	if err := db.Exec("UPDATE sqlite_sequence SET seq = MAX(seq, ?) WHERE name = 'access_keys'", sequence).Error; err != nil {
		return err
	}
	for _, statement := range objects {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}
