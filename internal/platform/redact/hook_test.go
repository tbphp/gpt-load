package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

type hookNamedStrings []string

type hookNamedBytes []byte

func TestHookRedactsMessageAndNestedFields(t *testing.T) {
	const secret = "sk-proj-hook-secret-123456789"
	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logger.AddHook(NewHook(New()))

	logger.WithFields(logrus.Fields{
		"authorization": "Bearer " + secret,
		"error":         errors.New("request failed for " + secret),
		"nested": map[string]any{
			"api_key": secret,
			"safe":    "gpt-4o",
		},
		"list":        []any{"safe", "token=" + secret},
		"string_map":  map[string]string{"token": secret, "safe": "kept"},
		"string_list": []string{"safe", "token=" + secret},
	}).Error("upstream rejected " + secret)

	logs := output.String()
	if strings.Contains(logs, secret) {
		t.Fatalf("hook leaked secret: %s", logs)
	}
	if !strings.Contains(logs, Placeholder) || !strings.Contains(logs, "gpt-4o") {
		t.Fatalf("hook output = %s, want redaction while preserving safe data", logs)
	}
}

func TestHookCopiesCollectionsAndRedactsSupportedFieldTypes(t *testing.T) {
	const secret = "sk-proj-hook-copy-secret-123456789"
	body := []byte("token=" + secret)
	stringMap := map[string]string{
		"X-Goog-Api-Key": "Bearer " + secret,
		"safe":           "kept",
	}
	list := []any{
		"safe",
		errors.New("list failed for " + secret),
		stringMap,
	}
	nested := map[string]any{
		"api_key": "prefix " + secret + " suffix",
		"body":    body,
		"error":   errors.New("request failed for " + secret),
		"list":    list,
	}
	entry := logrus.NewEntry(logrus.New())
	entry.Message = "upstream rejected " + secret
	entry.Data = logrus.Fields{
		"authorization": "Bearer " + secret,
		"nested":        nested,
	}

	if err := NewHook(New()).Fire(entry); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}

	if strings.Contains(entry.Message, secret) || !strings.Contains(entry.Message, Placeholder) {
		t.Fatalf("message = %q, want redacted", entry.Message)
	}
	if got := entry.Data["authorization"]; got != Placeholder {
		t.Fatalf("authorization = %#v, want whole-field replacement", got)
	}

	redactedNested, ok := entry.Data["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested type = %T, want map[string]any", entry.Data["nested"])
	}
	if got := redactedNested["api_key"]; got != Placeholder {
		t.Fatalf("nested api_key = %#v, want whole-field replacement", got)
	}
	redactedBody, ok := redactedNested["body"].([]byte)
	if !ok || bytes.Contains(redactedBody, []byte(secret)) || !bytes.Contains(redactedBody, []byte(Placeholder)) {
		t.Fatalf("nested body = %q, want redacted []byte", redactedBody)
	}
	redactedError, ok := redactedNested["error"].(string)
	if !ok || strings.Contains(redactedError, secret) || !strings.Contains(redactedError, Placeholder) {
		t.Fatalf("nested error = %#v, want redacted string", redactedNested["error"])
	}
	redactedList, ok := redactedNested["list"].([]any)
	if !ok {
		t.Fatalf("nested list type = %T, want []any", redactedNested["list"])
	}
	redactedStringMap, ok := redactedList[2].(map[string]string)
	if !ok {
		t.Fatalf("string map type = %T, want map[string]string", redactedList[2])
	}
	if got := redactedStringMap["X-Goog-Api-Key"]; got != Placeholder {
		t.Fatalf("X-Goog-Api-Key = %q, want whole-field replacement", got)
	}
	if got := redactedStringMap["safe"]; got != "kept" {
		t.Fatalf("safe field = %q, want kept", got)
	}

	if got := string(body); got != "token="+secret {
		t.Fatalf("source body mutated: %q", got)
	}
	if got := nested["api_key"]; got != "prefix "+secret+" suffix" {
		t.Fatalf("source nested map mutated: %#v", got)
	}
	if !reflect.DeepEqual(stringMap, map[string]string{
		"X-Goog-Api-Key": "Bearer " + secret,
		"safe":           "kept",
	}) {
		t.Fatalf("source string map mutated: %#v", stringMap)
	}
}

func TestHookRedactsNamedCollectionsInJSONOutput(t *testing.T) {
	const secret = "sk-proj-hook-named-secret-123456789"
	headers := http.Header{
		"Authorization": {"Bearer " + secret},
		"X-Safe":        {"kept-header"},
	}
	values := hookNamedStrings{"kept-list", "token=" + secret}
	nested := logrus.Fields{
		"headers": headers,
		"values":  values,
		"safe":    "kept-field",
		"unknown": map[int]string{1: "token=" + secret},
	}
	wantHeaders := headers.Clone()
	wantValues := append(hookNamedStrings(nil), values...)
	wantNested := logrus.Fields{
		"headers": wantHeaders,
		"values":  wantValues,
		"safe":    "kept-field",
		"unknown": map[int]string{1: "token=" + secret},
	}

	var output bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&output)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	logger.AddHook(NewHook(New()))
	logger.WithField("context", nested).Error("named collection test")

	logs := output.String()
	if strings.Contains(logs, secret) {
		t.Fatalf("hook leaked secret from named collection: %s", logs)
	}
	for _, safe := range []string{"kept-header", "kept-list", "kept-field"} {
		if !strings.Contains(logs, safe) {
			t.Fatalf("hook output = %s, want safe value %q", logs, safe)
		}
	}
	if !strings.Contains(logs, Placeholder) {
		t.Fatalf("hook output = %s, want redaction placeholder", logs)
	}
	var record struct {
		Context map[string]any `json:"context"`
	}
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode hook output: %v", err)
	}
	redactedHeaders, ok := record.Context["headers"].(map[string]any)
	if !ok || redactedHeaders["Authorization"] != Placeholder {
		t.Fatalf("redacted headers = %#v, want whole Authorization replacement", record.Context["headers"])
	}
	if !reflect.DeepEqual(headers, wantHeaders) {
		t.Fatalf("source headers mutated: %#v", headers)
	}
	if !reflect.DeepEqual(values, wantValues) {
		t.Fatalf("source named slice mutated: %#v", values)
	}
	if !reflect.DeepEqual(nested, wantNested) {
		t.Fatalf("source named map mutated: %#v", nested)
	}
}

func TestHookRedactsNamedBytesAndLimitsCollectionDepth(t *testing.T) {
	const secret = "sk-proj-hook-named-bytes-123456789"
	source := hookNamedBytes("token=" + secret)
	wantSource := append(hookNamedBytes(nil), source...)
	deep := any("bottom-safe")
	for range 32 {
		deep = []any{deep}
	}
	entry := logrus.NewEntry(logrus.New())
	entry.Data = logrus.Fields{
		"body":    source,
		"deep":    deep,
		"unknown": map[int]string{1: "token=" + secret},
	}

	if err := NewHook(New()).Fire(entry); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}

	redactedBody, ok := entry.Data["body"].([]byte)
	if !ok || bytes.Contains(redactedBody, []byte(secret)) || !bytes.Contains(redactedBody, []byte(Placeholder)) {
		t.Fatalf("named bytes = %q (%T), want redacted []byte", entry.Data["body"], entry.Data["body"])
	}
	if got := entry.Data["unknown"]; got != Placeholder {
		t.Fatalf("non-string-key map = %#v, want fail-safe placeholder", got)
	}
	encodedDeep, err := json.Marshal(entry.Data["deep"])
	if err != nil {
		t.Fatalf("marshal deep redacted value: %v", err)
	}
	if bytes.Contains(encodedDeep, []byte("bottom-safe")) || !bytes.Contains(encodedDeep, []byte(Placeholder)) {
		t.Fatalf("deep value = %s, want depth-limit placeholder", encodedDeep)
	}
	if !reflect.DeepEqual(source, wantSource) {
		t.Fatalf("source named bytes mutated: %q", source)
	}
}

func TestHookCoversEveryLogrusLevel(t *testing.T) {
	if got := NewHook(New()).Levels(); !reflect.DeepEqual(got, logrus.AllLevels) {
		t.Fatalf("Levels() = %v, want %v", got, logrus.AllLevels)
	}
}
