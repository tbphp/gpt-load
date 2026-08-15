package migrations

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const ID0006 = "0006_upstream_protocol"

const (
	requestLogAttemptsTable = "request_log_attempts"
	legacyUpstreamAPIColumn = "upstream_api"
	upstreamProtocolColumn  = "upstream_protocol"
)

// Up0006 replaces the historical mixed wire/provider value with the canonical
// protocol field. Historical values are intentionally not converted: the new
// column starts empty while every other attempt field remains intact.
func Up0006(db *gorm.DB) error {
	state, err := inspectUpstreamProtocolState0006(db, true)
	if err != nil {
		return err
	}
	if !state.hasNew {
		if err := db.Exec(fmt.Sprintf(
			"ALTER TABLE %s ADD COLUMN %s varchar(32) NOT NULL DEFAULT ''",
			quoteIdentifier0006(db, requestLogAttemptsTable),
			quoteIdentifier0006(db, upstreamProtocolColumn),
		)).Error; err != nil {
			return fmt.Errorf("add request log upstream protocol: %w", err)
		}
	}
	if err := validateUpstreamProtocolColumn0006(db); err != nil {
		return err
	}
	if err := requireEmptyUpstreamProtocol0006(db); err != nil {
		return err
	}
	if state.hasOld {
		if err := db.Exec(fmt.Sprintf(
			"ALTER TABLE %s DROP COLUMN %s",
			quoteIdentifier0006(db, requestLogAttemptsTable),
			quoteIdentifier0006(db, legacyUpstreamAPIColumn),
		)).Error; err != nil {
			return fmt.Errorf("drop legacy request log upstream API: %w", err)
		}
	}
	return nil
}

// ValidateRecoverable0006 accepts only the known pre-migration and safe
// interrupted states. In particular, a partial new column must still be empty
// before MySQL may resume and irreversibly drop the legacy column.
func ValidateRecoverable0006(db *gorm.DB) error {
	_, err := inspectUpstreamProtocolState0006(db, true)
	return err
}

// Validate0006 verifies the final schema. It does not require the column to
// remain empty because completed migrations may already contain new records.
func Validate0006(db *gorm.DB) error {
	state, err := inspectUpstreamProtocolState0006(db, false)
	if err != nil {
		return err
	}
	if state.hasOld {
		return fmt.Errorf("validate upstream protocol schema: legacy column %q remains", legacyUpstreamAPIColumn)
	}
	if !state.hasNew {
		return fmt.Errorf("validate upstream protocol schema: column %q is missing", upstreamProtocolColumn)
	}
	return validateUpstreamProtocolColumn0006(db)
}

type upstreamProtocolState0006 struct {
	hasOld bool
	hasNew bool
}

func inspectUpstreamProtocolState0006(db *gorm.DB, requireRecoverable bool) (upstreamProtocolState0006, error) {
	if db == nil || db.Dialector == nil {
		return upstreamProtocolState0006{}, fmt.Errorf("inspect upstream protocol schema: database is unavailable")
	}
	if !db.Migrator().HasTable(requestLogAttemptsTable) {
		return upstreamProtocolState0006{}, fmt.Errorf("inspect upstream protocol schema: table %q is missing", requestLogAttemptsTable)
	}
	state := upstreamProtocolState0006{
		hasOld: db.Migrator().HasColumn(requestLogAttemptsTable, legacyUpstreamAPIColumn),
		hasNew: db.Migrator().HasColumn(requestLogAttemptsTable, upstreamProtocolColumn),
	}
	if !state.hasOld && !state.hasNew {
		return state, fmt.Errorf("inspect upstream protocol schema: neither legacy nor canonical column exists")
	}
	if state.hasNew {
		if err := validateUpstreamProtocolColumn0006(db); err != nil {
			return state, err
		}
		if requireRecoverable {
			if err := requireEmptyUpstreamProtocol0006(db); err != nil {
				return state, err
			}
		}
	}
	return state, nil
}

func validateUpstreamProtocolColumn0006(db *gorm.DB) error {
	columns, err := db.Migrator().ColumnTypes(requestLogAttemptsTable)
	if err != nil {
		return fmt.Errorf("inspect upstream protocol column: %w", err)
	}
	for _, column := range columns {
		if !strings.EqualFold(column.Name(), upstreamProtocolColumn) {
			continue
		}
		databaseType := strings.ToLower(strings.TrimSpace(column.DatabaseTypeName()))
		if !strings.Contains(databaseType, "char") {
			return fmt.Errorf("validate upstream protocol column: type %q is not varchar(32)", column.DatabaseTypeName())
		}
		length, hasLength := column.Length()
		if !hasLength || length != 32 {
			return fmt.Errorf("validate upstream protocol column: length is %d (known=%t), want 32", length, hasLength)
		}
		nullable, hasNullable := column.Nullable()
		if !hasNullable || nullable {
			return fmt.Errorf("validate upstream protocol column: nullable is %t (known=%t), want false", nullable, hasNullable)
		}
		defaultValue, hasDefault := column.DefaultValue()
		if !hasDefault || !isEmptyStringDefault0006(defaultValue) {
			return fmt.Errorf("validate upstream protocol column: default is %q (known=%t), want empty string", defaultValue, hasDefault)
		}
		return nil
	}
	return fmt.Errorf("validate upstream protocol column: column %q is missing", upstreamProtocolColumn)
}

func requireEmptyUpstreamProtocol0006(db *gorm.DB) error {
	var count int64
	if err := db.Table(requestLogAttemptsTable).
		Where(upstreamProtocolColumn + " IS NULL OR " + upstreamProtocolColumn + " <> ''").
		Count(&count).Error; err != nil {
		return fmt.Errorf("inspect upstream protocol values: %w", err)
	}
	if count != 0 {
		return fmt.Errorf("recover upstream protocol schema: %d nonempty canonical value(s) exist", count)
	}
	return nil
}

func quoteIdentifier0006(db *gorm.DB, value string) string {
	if db != nil && db.Dialector != nil && strings.EqualFold(db.Dialector.Name(), "mysql") {
		return "`" + value + "`"
	}
	return `"` + value + `"`
}

func isEmptyStringDefault0006(value string) bool {
	value = strings.TrimSpace(value)
	for strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")") {
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if separator := strings.Index(value, "::"); separator >= 0 {
		value = strings.TrimSpace(value[:separator])
	}
	return value == "" || value == "''" || value == `""`
}
