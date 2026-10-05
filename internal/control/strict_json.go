package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"gpt-load/internal/policy"
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
	maxDepth := maxStrictControlJSONDepth
	switch target.(type) {
	case *PolicyUpdateRequest:
		maxDepth = policy.MaxJSONDepth + 1
	}
	allowed, _ := canonicalOuterFields(target)
	if err := rejectDuplicateJSONFieldsAtDepth(data, maxDepth, allowed); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func rejectDuplicateJSONFields(data []byte) error {
	return rejectDuplicateJSONFieldsAtDepth(data, maxStrictControlJSONDepth, nil)
}

// rejectDuplicateJSONFieldsAtDepth 遍历 JSON token 树，拒绝重复字段与超深嵌套；
// canonical 非空时同时拒绝顶层非规范（含大小写别名）字段名。
func rejectDuplicateJSONFieldsAtDepth(data []byte, maxDepth int, canonical map[string]struct{}) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	// 规范字段名检查延后到整棵树走完再报错，保证深度/重复/尾随数据错误优先，与原有两遍扫描顺序一致。
	nonCanonicalKey := ""
	var walk func(depth int) error
	walk = func(depth int) error {
		if depth > maxDepth {
			return fmt.Errorf("maximum JSON nesting depth exceeded (%d)", maxDepth)
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
				key := keyToken.(string)
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate field %q", key)
				}
				seen[key] = struct{}{}
				if depth == 1 && canonical != nil && nonCanonicalKey == "" {
					if _, isAllowed := canonical[key]; !isAllowed {
						nonCanonicalKey = key
					}
				}
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
	if nonCanonicalKey != "" {
		return fmt.Errorf("unknown or non-canonical field %q", nonCanonicalKey)
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
