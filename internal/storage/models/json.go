package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

// JSON is a validated JSON document stored in a database JSON column.
// A nil or empty value is persisted as SQL NULL.
type JSON []byte

// Scan implements sql.Scanner and always takes ownership of an independent
// copy of a valid database value.
func (j *JSON) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}

	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = typed
	case string:
		raw = []byte(typed)
	default:
		return fmt.Errorf("scan JSON from unsupported type %T", value)
	}

	if !json.Valid(raw) {
		return fmt.Errorf("scan invalid JSON")
	}

	*j = append((*j)[:0], raw...)
	return nil
}

// Value implements driver.Valuer and rejects invalid JSON before persistence.
func (j JSON) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	if !json.Valid(j) {
		return nil, fmt.Errorf("persist invalid JSON")
	}
	return string(j), nil
}
