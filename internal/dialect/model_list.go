package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
)

const (
	maxModelListPages           = 100
	maxUniqueModelListEntries   = 100_000
	maxModelListPageBytes       = int64(8 << 20)
	maxUniqueModelListJSONBytes = int64(16 << 20)
)

type modelListCollector struct {
	values       []string
	seen         map[string]struct{}
	jsonBytes    int64
	maxJSONBytes int64
}

func newModelListCollector() *modelListCollector {
	return &modelListCollector{
		values:       make([]string, 0),
		seen:         make(map[string]struct{}),
		jsonBytes:    2,
		maxJSONBytes: maxUniqueModelListJSONBytes,
	}
}

func (collector *modelListCollector) Add(values []string) error {
	for _, value := range values {
		if _, duplicate := collector.seen[value]; duplicate {
			continue
		}
		if len(collector.values) == maxUniqueModelListEntries {
			return fmt.Errorf("model list unique-result limit exceeded")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode model ID: %w", err)
		}
		additional := int64(len(encoded))
		if len(collector.values) > 0 {
			additional++
		}
		if additional > collector.maxJSONBytes-collector.jsonBytes {
			return fmt.Errorf("model list encoded-size limit exceeded")
		}
		collector.seen[value] = struct{}{}
		collector.values = append(collector.values, value)
		collector.jsonBytes += additional
	}
	return nil
}

func (collector *modelListCollector) Full() bool {
	return len(collector.values) == maxUniqueModelListEntries ||
		collector.jsonBytes == collector.maxJSONBytes
}

func (collector *modelListCollector) Result() []string {
	result := make([]string, len(collector.values))
	copy(result, collector.values)
	return result
}

func decodeModelListPage(response *http.Response, target any) error {
	if response == nil || response.Body == nil || target == nil {
		return fmt.Errorf("model-list response is incomplete")
	}
	targetValue := reflect.ValueOf(target)
	if targetValue.Kind() != reflect.Ptr || targetValue.IsNil() {
		return fmt.Errorf("model-list target must be a non-nil pointer")
	}

	encodings := response.Header.Values("Content-Encoding")
	if len(encodings) > 1 {
		return fmt.Errorf("model-list response uses unsupported Content-Encoding")
	}
	if len(encodings) == 1 {
		encoding := strings.TrimSpace(encodings[0])
		if encoding != "" && !strings.EqualFold(encoding, "identity") {
			return fmt.Errorf("model-list response uses unsupported Content-Encoding")
		}
	}
	if response.ContentLength > maxModelListPageBytes {
		return fmt.Errorf("model-list response exceeds page limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxModelListPageBytes+1))
	if err != nil {
		return fmt.Errorf("read model-list response: %w", err)
	}
	if int64(len(body)) > maxModelListPageBytes {
		return fmt.Errorf("model-list response exceeds page limit")
	}

	staging := reflect.New(targetValue.Elem().Type())
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(staging.Interface()); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("model-list response contains multiple JSON values")
		}
		return fmt.Errorf("decode model-list response tail: %w", err)
	}
	targetValue.Elem().Set(staging.Elem())
	return nil
}
