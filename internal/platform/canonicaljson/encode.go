// Package canonicaljson implements the restricted JSON Canonicalization Scheme
// subset used by GPT-Load control-plane contracts. Contract numbers are signed
// 64-bit integers; floating-point values are rejected.
package canonicaljson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// Marshal encodes a typed value as canonical JSON.
func Marshal(value any) ([]byte, error) {
	if err := rejectUnsupportedTypedValue(reflect.ValueOf(value)); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical JSON input: %w", err)
	}
	return Canonicalize(raw)
}

// Canonicalize validates and canonicalizes one JSON value.
func Canonicalize(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeUniqueValue(decoder)
	if err != nil {
		return nil, fmt.Errorf("decode canonical JSON input: %w", err)
	}
	if _, err := decoder.Token(); err == nil {
		return nil, fmt.Errorf("decode canonical JSON input: multiple values")
	} else if err != io.EOF {
		return nil, fmt.Errorf("decode canonical JSON trailing input: %w", err)
	}

	var output bytes.Buffer
	if err := appendCanonical(&output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decodeUniqueValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}

	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object key is not a string")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate object key %q", key)
			}
			value, err := decodeUniqueValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		end, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if end != json.Delim('}') {
			return nil, fmt.Errorf("object is not closed")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if end != json.Delim(']') {
			return nil, fmt.Errorf("array is not closed")
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func rejectUnsupportedTypedValue(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return rejectUnsupportedTypedValue(value.Elem())
	case reflect.Float32, reflect.Float64:
		return fmt.Errorf("canonical JSON floats are not supported")
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("canonical JSON object keys must be strings")
		}
		iter := value.MapRange()
		for iter.Next() {
			if err := rejectUnsupportedTypedValue(iter.Value()); err != nil {
				return err
			}
		}
	case reflect.Array, reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}
		for index := 0; index < value.Len(); index++ {
			if err := rejectUnsupportedTypedValue(value.Index(index)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		if _, ok := value.Interface().(json.Number); ok {
			return nil
		}
		for index := 0; index < value.NumField(); index++ {
			if value.Type().Field(index).IsExported() {
				if err := rejectUnsupportedTypedValue(value.Field(index)); err != nil {
					return err
				}
			}
		}
	case reflect.Func, reflect.Chan, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
		return fmt.Errorf("canonical JSON does not support %s", value.Kind())
	}
	return nil
}

func appendCanonical(output *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if typed {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		return appendString(output, typed)
	case json.Number:
		integer, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			return fmt.Errorf("canonical JSON number %q is not a signed integer", typed)
		}
		output.WriteString(strconv.FormatInt(integer, 10))
	case []any:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := appendCanonical(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			if !utf8.ValidString(key) {
				return fmt.Errorf("canonical JSON object key is not valid UTF-8")
			}
			keys = append(keys, key)
		}
		sort.Slice(keys, func(left, right int) bool {
			return compareUTF16(keys[left], keys[right]) < 0
		})
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := appendString(output, key); err != nil {
				return err
			}
			output.WriteByte(':')
			if err := appendCanonical(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("canonical JSON decoded unsupported type %T", value)
	}
	return nil
}

func appendString(output *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("canonical JSON string is not valid UTF-8")
	}
	output.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"':
			output.WriteString(`\"`)
		case '\\':
			output.WriteString(`\\`)
		case '\b':
			output.WriteString(`\b`)
		case '\t':
			output.WriteString(`\t`)
		case '\n':
			output.WriteString(`\n`)
		case '\f':
			output.WriteString(`\f`)
		case '\r':
			output.WriteString(`\r`)
		default:
			if character < 0x20 {
				output.WriteString(`\u00`)
				output.WriteString(strconv.FormatInt(int64(character>>4), 16))
				output.WriteString(strconv.FormatInt(int64(character&0xf), 16))
				continue
			}
			output.WriteRune(character)
		}
	}
	output.WriteByte('"')
	return nil
}

func compareUTF16(left, right string) int {
	leftUnits := utf16.Encode([]rune(left))
	rightUnits := utf16.Encode([]rune(right))
	limit := len(leftUnits)
	if len(rightUnits) < limit {
		limit = len(rightUnits)
	}
	for index := 0; index < limit; index++ {
		if leftUnits[index] < rightUnits[index] {
			return -1
		}
		if leftUnits[index] > rightUnits[index] {
			return 1
		}
	}
	switch {
	case len(leftUnits) < len(rightUnits):
		return -1
	case len(leftUnits) > len(rightUnits):
		return 1
	default:
		return 0
	}
}
