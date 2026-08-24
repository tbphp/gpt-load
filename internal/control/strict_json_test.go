package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type strictJSONTestTarget struct {
	Name   string `json:"name"`
	Nested struct {
		Value string `json:"value"`
	} `json:"nested"`
	Items []struct {
		Value string `json:"value"`
	} `json:"items"`
	Payload string      `json:"payload"`
	Count   json.Number `json:"count"`
}

func TestBindStrictJSONRejectsDuplicateKeysAtEveryObjectDepth(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "top level duplicate", body: `{"name":"first","name":"second"}`},
		{name: "nested duplicate", body: `{"nested":{"value":"first","value":"second"}}`},
		{name: "array object duplicate", body: `{"items":[{"value":"first","value":"second"}]}`},
		{name: "malformed", body: `{"name":"client"`},
		{name: "trailing value", body: `{"name":"client"} {}`},
		{name: "unknown field", body: `{"unknown":true}`},
		{name: "null", body: `null`},
		{name: "array", body: `[]`},
		{name: "boolean", body: `true`},
		{name: "string", body: `"value"`},
		{name: "number", body: `1`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var target strictJSONTestTarget
			if err := bindStrictJSONForTest(test.body, &target); err == nil {
				t.Fatalf("bindStrictJSON(%s) accepted", test.body)
			}
		})
	}

	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "same field in sibling objects",
			body: `{"items":[{"value":"first"},{"value":"second"}]}`,
		},
		{
			name: "JSON-looking string is opaque",
			body: `{"payload":"{\"value\":\"first\",\"value\":\"second\"}"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var target strictJSONTestTarget
			if err := bindStrictJSONForTest(test.body, &target); err != nil {
				t.Fatalf("bindStrictJSON(%s) error = %v", test.body, err)
			}
		})
	}
}

func TestBindStrictJSONPreservesJSONNumber(t *testing.T) {
	t.Parallel()
	const largeInteger = "900719925474099312345678901234567890"
	var target strictJSONTestTarget
	if err := bindStrictJSONForTest(`{"count":`+largeInteger+`}`, &target); err != nil {
		t.Fatalf("bindStrictJSON() error = %v", err)
	}
	if got := target.Count.String(); got != largeInteger {
		t.Fatalf("count = %q, want %q", got, largeInteger)
	}
}

func bindStrictJSONForTest(body string, target any) error {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body))
	return bindStrictJSON(context, target)
}
