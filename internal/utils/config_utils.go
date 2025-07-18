package utils

import (
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/types"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

// GenerateSettingsMetadata dynamically generates metadata from SystemSettings struct using reflection
func GenerateSettingsMetadata(s *types.SystemSettings) []models.SystemSettingInfo {
	var settingsInfo []models.SystemSettingInfo
	v := reflect.ValueOf(s).Elem()
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)
		fieldValue := v.Field(i)

		jsonTag := field.Tag.Get("json")
		if jsonTag == "" {
			continue
		}

		nameTag := field.Tag.Get("name")
		descTag := field.Tag.Get("desc")
		defaultTag := field.Tag.Get("default")
		validateTag := field.Tag.Get("validate")
		categoryTag := field.Tag.Get("category")

		var minValue *int
		if strings.HasPrefix(validateTag, "min=") {
			valStr := strings.TrimPrefix(validateTag, "min=")
			if val, err := strconv.Atoi(valStr); err == nil {
				minValue = &val
			}
		}

		info := models.SystemSettingInfo{
			Key:          jsonTag,
			Name:         nameTag,
			Value:        fieldValue.Interface(),
			Type:         field.Type.String(),
			DefaultValue: defaultTag,
			Description:  descTag,
			Category:     categoryTag,
			MinValue:     minValue,
		}
		settingsInfo = append(settingsInfo, info)
	}
	return settingsInfo
}

// DefaultSystemSettings returns the default system configuration
func DefaultSystemSettings() types.SystemSettings {
	s := types.SystemSettings{}
	v := reflect.ValueOf(&s).Elem()
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)
		defaultTag := field.Tag.Get("default")
		if defaultTag == "" {
			continue
		}

		fieldValue := v.Field(i)
		if fieldValue.CanSet() {
			if err := SetFieldFromString(fieldValue, defaultTag); err != nil {
				logrus.Warnf("Failed to set default value for field %s: %v", field.Name, err)
			}
		}
	}
	return s
}

// SetFieldFromString sets a struct field's value from a string, based on the field's kind.
func SetFieldFromString(fieldValue reflect.Value, value string) error {
	if !fieldValue.CanSet() {
		return fmt.Errorf("field cannot be set")
	}

	switch fieldValue.Kind() {
	case reflect.Int:
		intVal, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid integer value '%s': %w", value, err)
		}
		fieldValue.SetInt(int64(intVal))
	case reflect.Bool:
		boolVal, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean value '%s': %w", value, err)
		}
		fieldValue.SetBool(boolVal)
	case reflect.String:
		fieldValue.SetString(value)
	default:
		return fmt.Errorf("unsupported field kind: %s", fieldValue.Kind())
	}
	return nil
}

// ParseInteger parses integer environment variable
func ParseInteger(value string, defaultValue int) int {
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return defaultValue
}

// ParseBoolean parses boolean environment variable
func ParseBoolean(value string, defaultValue bool) bool {
	if value == "" {
		return defaultValue
	}

	lowerValue := strings.ToLower(value)
	switch lowerValue {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return defaultValue
	}
}

// ParseArray parses array environment variable (comma-separated)
func ParseArray(value string, defaultValue []string) []string {
	if value == "" {
		return defaultValue
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}

	if len(result) == 0 {
		return defaultValue
	}
	return result
}

// GetEnvOrDefault gets environment variable or default value
func GetEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
