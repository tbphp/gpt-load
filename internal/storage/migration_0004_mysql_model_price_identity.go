package storage

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// migrateMySQLModelPriceIdentity makes MySQL enforce the same exact model-ID
// identity that SQLite and PostgreSQL already provide. A connection collation
// does not change the unique index's column collation, so this is schema work
// rather than a DSN-only setting.
func migrateMySQLModelPriceIdentity(db *gorm.DB) error {
	statements, err := mysqlModelPriceIdentityStatements(db.Dialector.Name())
	if err != nil {
		return err
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("set MySQL model price identity collation: %w", err)
		}
	}
	return nil
}

func mysqlModelPriceIdentityStatements(driver string) ([]string, error) {
	switch strings.ToLower(driver) {
	case "mysql":
		return []string{
			"ALTER TABLE model_prices MODIFY COLUMN model_id varchar(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL",
		}, nil
	case "sqlite", "postgres", "postgresql":
		return nil, nil
	default:
		return nil, fmt.Errorf("migrate MySQL model price identity: unsupported database driver %q", driver)
	}
}
