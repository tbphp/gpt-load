package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0005 = "0005_proxy_config"

type groupProxyConfig0005 struct {
	ProxyConfig *string `gorm:"column:proxy_config;type:text"`
}

func (groupProxyConfig0005) TableName() string { return "groups" }

type credentialProxyConfig0005 struct {
	ProxyConfig *string `gorm:"column:proxy_config;type:text"`
}

func (credentialProxyConfig0005) TableName() string { return "credentials" }

// Up0005 adds encrypted proxy override storage without changing existing rows.
func Up0005(db *gorm.DB) error {
	for _, definition := range []struct {
		model any
		name  string
	}{
		{model: &groupProxyConfig0005{}, name: "groups"},
		{model: &credentialProxyConfig0005{}, name: "credentials"},
	} {
		if db.Migrator().HasColumn(definition.model, "proxy_config") {
			continue
		}
		if err := db.Migrator().AddColumn(definition.model, "ProxyConfig"); err != nil {
			return fmt.Errorf("add %s.proxy_config: %w", definition.name, err)
		}
	}
	return nil
}

func ValidateRecoverable0005(db *gorm.DB) error {
	for _, definition := range []struct {
		model any
		table string
	}{
		{model: &groupProxyConfig0005{}, table: "groups"},
		{model: &credentialProxyConfig0005{}, table: "credentials"},
	} {
		if !db.Migrator().HasTable(definition.model) {
			return fmt.Errorf("validate recoverable proxy config: table %q is missing", definition.table)
		}
		if db.Migrator().HasColumn(definition.model, "proxy_config") {
			if err := validateProxyColumn0005(db, definition.table); err != nil {
				return err
			}
		}
	}
	return nil
}

func Validate0005(db *gorm.DB) error {
	for _, table := range []string{"groups", "credentials"} {
		if !db.Migrator().HasColumn(table, "proxy_config") {
			return fmt.Errorf("validate proxy config: column %q.proxy_config is missing", table)
		}
		if err := validateProxyColumn0005(db, table); err != nil {
			return err
		}
	}
	return nil
}

func validateProxyColumn0005(db *gorm.DB, table string) error {
	columns, err := db.Migrator().ColumnTypes(table)
	if err != nil {
		return fmt.Errorf("inspect %s.proxy_config: %w", table, err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), "proxy_config") {
			continue
		}
		typeName := strings.ToLower(column.DatabaseTypeName())
		if !strings.Contains(typeName, "text") && !strings.Contains(typeName, "char") && typeName != "clob" {
			return fmt.Errorf("validate proxy config: column %q.proxy_config is not text", table)
		}
		if nullable, known := column.Nullable(); known && !nullable {
			return fmt.Errorf("validate proxy config: column %q.proxy_config is not nullable", table)
		}
		return nil
	}
	return fmt.Errorf("validate proxy config: column %q.proxy_config is missing", table)
}
