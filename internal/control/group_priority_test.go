package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
)

func TestGroupPriorityConfigRoundTrip(t *testing.T) {
	fixture := newServiceFixture(t)
	var request GroupCreateRequest
	if err := json.Unmarshal([]byte(`{"channel_id":"openai","connection_type":"api_key","params":{},"models":[{"id":"gpt-4o","alias":"","alias_enabled":false}],"credentials":"test-priority","priority":-100}`), &request); err != nil {
		t.Fatal(err)
	}
	created, err := fixture.service.CreateGroup(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := fixture.service.GetGroupSettings(t.Context(), created.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	assertPriorityJSON(t, settings, "priority", -100)
	assertPriorityJSON(t, fixture.manager.Current().Groups[created.GroupID], "Priority", -100)
	assertPriorityJSON(t, fixture.manager.Current().GroupCatalog[created.GroupID], "Priority", -100)

	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	path := "/api/groups/" + stringGroupID(created.GroupID) + "/settings"
	for _, test := range []struct {
		body string
		want int
	}{
		{`{"name":"priority-renamed"}`, -100},
		{`{"priority":2147483647}`, 2147483647},
		{`{"priority":-2147483648}`, -2147483648},
		{`{"priority":0}`, 0},
	} {
		response := serveGroupSettingsRequest(t, engine, http.MethodPut, path, "test-auth-key", test.body)
		if response.Code != http.StatusOK {
			t.Fatalf("update %s: %d %s", test.body, response.Code, response.Body.String())
		}
		var envelope struct{ Data json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		assertPriorityJSON(t, envelope.Data, "priority", test.want)
		assertPriorityJSON(t, fixture.manager.Current().Groups[created.GroupID], "Priority", test.want)
	}
	before := fixture.manager.Current().Revision
	for _, value := range []string{"null", "1.5", "2147483648", "-2147483649", `"1"`} {
		response := serveGroupSettingsRequest(t, engine, http.MethodPut, path, "test-auth-key", `{"priority":`+value+`}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid priority %s: %d", value, response.Code)
		}
	}
	if fixture.manager.Current().Revision != before {
		t.Fatal("invalid priority published configuration")
	}
}

func TestGroupPriorityCreateIdempotency(t *testing.T) {
	fixture := newServiceFixture(t)
	decode := func(priority string) GroupCreateRequest {
		t.Helper()
		var request GroupCreateRequest
		body := `{"channel_id":"openai","connection_type":"api_key","params":{},"models":[],"credentials":"test-priority"` + priority + `}`
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatal(err)
		}
		return request
	}
	const key = "218f47a2-9c35-4d6e-8b1a-1234567890ab"
	first, err := fixture.service.CreateGroupIdempotent(t.Context(), key, decode(""))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.service.CreateGroupIdempotent(t.Context(), key, decode(`,"priority":0`))
	if err != nil || replayed != first {
		t.Fatalf("default priority replay: %+v, %v", replayed, err)
	}
	_, err = fixture.service.CreateGroupIdempotent(t.Context(), key, decode(`,"priority":-1`))
	assertAPIErrorCode(t, err, app_errors.ErrIdempotencyKeyReused.Code)
	_, err = fixture.service.CreateGroup(t.Context(), decode(`,"priority":null`))
	assertAPIErrorCode(t, err, app_errors.ErrValidation.Code)
}

func assertPriorityJSON(t *testing.T, value any, field string, want int) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields[field]) != fmt.Sprint(want) {
		t.Fatalf("%s = %s, want %d", field, fields[field], want)
	}
}
