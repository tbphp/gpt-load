package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

const (
	maxStrictPolicyRequestBytes = 512 * 1024 // 512 KiB
	maxStrictControlJSONDepth   = 32
)

func decodeStrictControlJSONObject(data []byte, target any) error {
	if int64(len(data)) > maxControlJSONBodyBytes {
		return fmt.Errorf("request body exceeds maximum allowed size (%d bytes)", maxControlJSONBodyBytes)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("request body must be a JSON object")
	}
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	if allowed, ok := canonicalOuterFields(target); ok {
		if err := validateCanonicalOuterJSONFields(data, allowed); err != nil {
			return err
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode JSON request: multiple values")
		}
		return err
	}
	return nil
}

func rejectDuplicateJSONFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var walk func(depth int) error
	walk = func(depth int) error {
		if depth > maxStrictControlJSONDepth {
			return fmt.Errorf("maximum JSON nesting depth exceeded (%d)", maxStrictControlJSONDepth)
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, isDelimiter := token.(json.Delim)
		if !isDelimiter {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key must be a string")
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate field %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected delimiter %q", delimiter)
		}
	}
	if err := walk(1); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func canonicalOuterFields(target any) (map[string]struct{}, bool) {
	if target == nil {
		return nil, false
	}
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil, false
	}
	elem := v.Elem()
	if elem.Kind() != reflect.Struct {
		return nil, false
	}
	allowed := make(map[string]struct{})
	var extract func(st reflect.Type)
	extract = func(st reflect.Type) {
		for i := 0; i < st.NumField(); i++ {
			field := st.Field(i)
			if field.Anonymous {
				ft := field.Type
				if ft.Kind() == reflect.Ptr {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					extract(ft)
				}
				continue
			}
			if !field.IsExported() {
				continue
			}
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name := strings.Split(tag, ",")[0]
			if name == "" {
				name = field.Name
			}
			allowed[name] = struct{}{}
		}
	}
	extract(elem.Type())
	return allowed, true
}

func validateCanonicalOuterJSONFields(data []byte, allowed map[string]struct{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return fmt.Errorf("request body must be a JSON object")
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("object key must be a string")
		}
		if _, isAllowed := allowed[key]; !isAllowed {
			return fmt.Errorf("unknown or non-canonical field %q", key)
		}
		var val json.RawMessage
		if err := decoder.Decode(&val); err != nil {
			return err
		}
	}
	return nil
}
