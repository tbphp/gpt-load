package redact

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/sirupsen/logrus"
)

const maxValueDepth = 16

type Hook struct {
	redactor *Redactor
}

func NewHook(redactor *Redactor) logrus.Hook {
	return &Hook{redactor: redactor}
}

func (h *Hook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *Hook) Fire(entry *logrus.Entry) error {
	if h == nil || h.redactor == nil || entry == nil {
		return nil
	}
	entry.Message = h.redactor.String(entry.Message)
	for name, value := range entry.Data {
		if sensitiveFieldName(name) {
			entry.Data[name] = Placeholder
			continue
		}
		entry.Data[name] = h.redactValue(value)
	}
	return nil
}

func (h *Hook) redactValue(value any) any {
	return h.redactValueAtDepth(value, 0)
}

func (h *Hook) redactValueAtDepth(value any, depth int) any {
	if depth >= maxValueDepth {
		return Placeholder
	}
	switch typed := value.(type) {
	case string:
		return h.redactor.String(typed)
	case []byte:
		return h.redactor.Bytes(typed)
	case error:
		return h.redactor.String(typed.Error())
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for name, nested := range typed {
			if sensitiveFieldName(name) {
				cloned[name] = Placeholder
			} else {
				cloned[name] = h.redactValueAtDepth(nested, depth+1)
			}
		}
		return cloned
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for name, nested := range typed {
			if sensitiveFieldName(name) {
				cloned[name] = Placeholder
			} else {
				cloned[name] = h.redactor.String(nested)
			}
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, nested := range typed {
			cloned[index] = h.redactValueAtDepth(nested, depth+1)
		}
		return cloned
	case []string:
		cloned := make([]string, len(typed))
		for index, nested := range typed {
			cloned[index] = h.redactor.String(nested)
		}
		return cloned
	case fmt.Stringer:
		return h.redactor.String(typed.String())
	default:
		return h.redactReflectedCollection(value, depth)
	}
}

func (h *Hook) redactReflectedCollection(value any, depth int) any {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil
	}
	switch reflected.Kind() {
	case reflect.Interface, reflect.Pointer:
		if reflected.IsNil() {
			return nil
		}
		nested := reflected.Elem()
		if !nested.CanInterface() {
			return Placeholder
		}
		return h.redactValueAtDepth(nested.Interface(), depth+1)
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return Placeholder
		}
		cloned := make(map[string]any, reflected.Len())
		iterator := reflected.MapRange()
		for iterator.Next() {
			name := iterator.Key().String()
			if sensitiveFieldName(name) {
				cloned[name] = Placeholder
			} else {
				cloned[name] = h.redactValueAtDepth(iterator.Value().Interface(), depth+1)
			}
		}
		return cloned
	case reflect.Slice, reflect.Array:
		if reflected.Type().Elem().Kind() == reflect.Uint8 {
			body := make([]byte, reflected.Len())
			for index := range body {
				body[index] = byte(reflected.Index(index).Uint())
			}
			return h.redactor.Bytes(body)
		}
		cloned := make([]any, reflected.Len())
		for index := range cloned {
			cloned[index] = h.redactValueAtDepth(reflected.Index(index).Interface(), depth+1)
		}
		return cloned
	case reflect.Struct:
		return Placeholder
	default:
		return value
	}
}

func sensitiveFieldName(name string) bool {
	normalized := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(name))
	switch normalized {
	case "authorization", "apikey", "xapikey", "xgoogapikey", "accesskey", "key", "token":
		return true
	default:
		return false
	}
}
