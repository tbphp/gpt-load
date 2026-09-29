package control

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"gpt-load/internal/storage/models"
)

func TestAccessKeyConcurrencyInheritanceHTTP(t *testing.T) {
	fixture, engine := newAccessKeyCostLimitHTTPFixture(t)
	created := serveAccessKeyCostLimitRequest(t, engine, http.MethodPost, "/api/access-keys", `{"name":"limited","concurrency_limit":3}`, "00000000-0000-4000-8000-000000009941")
	if created.Code != http.StatusOK {
		t.Fatalf("create concurrency setting returned %d", created.Code)
	}
	var envelope struct {
		Data struct {
			ID               uint
			ConcurrencyLimit *int64 `json:"concurrency_limit"`
		}
	}
	if err := json.Unmarshal(created.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ConcurrencyLimit == nil || *envelope.Data.ConcurrencyLimit != 3 {
		t.Fatal("created limit missing")
	}
	release, ok := fixture.manager.Concurrency().TryAcquireRequest(envelope.Data.ID, 0, 3)
	if !ok {
		t.Fatal("request admission failed")
	}
	defer release()
	for range 2 {
		rotated, err := fixture.service.RotateAccessKeyIdempotent(t.Context(), "00000000-0000-4000-8000-000000009942", envelope.Data.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rotated.ConcurrencyLimit == nil || *rotated.ConcurrencyLimit != 3 || rotated.Concurrency != (ConcurrencyView{Current: 1, Limit: 3}) {
			t.Fatal("rotation or replay lost configured limit or in-flight counts")
		}
	}
	path := fmt.Sprintf("/api/access-keys/%d", envelope.Data.ID)
	for _, test := range []struct {
		body string
		want *int64
	}{
		{`{"name":"renamed"}`, ptrConcurrency(3)},
		{`{"concurrency_limit":0}`, ptrConcurrency(0)},
		{`{"concurrency_limit":null}`, nil},
	} {
		response := serveAccessKeyCostLimitRequest(t, engine, http.MethodPut, path, test.body, "")
		if response.Code != http.StatusOK {
			t.Fatalf("update %s returned %d", test.body, response.Code)
		}
		var row models.AccessKey
		if err := fixture.db.First(&row, envelope.Data.ID).Error; err != nil {
			t.Fatal(err)
		}
		if (row.ConcurrencyLimit == nil) != (test.want == nil) || (test.want != nil && *row.ConcurrencyLimit != *test.want) {
			t.Fatalf("update %s lost configured inheritance", test.body)
		}
		snapshot := fixture.manager.Current()
		got := snapshot.AccessKeysByID[row.ID].ConcurrencyLimit
		if (got == nil) != (test.want == nil) || (test.want != nil && *got != *test.want) {
			t.Fatal("limit was not published")
		}
	}
}
func ptrConcurrency(n int64) *int64 { return &n }

func TestConcurrencySettingsPublishAndPreserveCounts(t *testing.T) {
	fixture := newServiceFixture(t)
	group := validControlGroup("concurrency-settings")
	if err := fixture.db.Create(group).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Publish(mustBuildCompileInput(t, fixture.db)); err != nil {
		t.Fatal(err)
	}
	release, ok := fixture.manager.Concurrency().TryAcquireGroup(group.ID, 0)
	if !ok {
		t.Fatal("unlimited rejected")
	}
	defer release()
	_, err := fixture.service.UpdateSettings(t.Context(), SettingsUpdateRequest{Settings: map[string]json.RawMessage{
		"global_concurrency_limit":             json.RawMessage(`9`),
		"default_access_key_concurrency_limit": json.RawMessage(`3`),
		"default_group_concurrency_limit":      json.RawMessage(`2`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.manager.Current().Groups[group.ID].ConcurrencyLimit != 2 {
		t.Fatal("group default not published")
	}
	for _, test := range []struct {
		body string
		want int64
	}{
		{`{"overrides":{"concurrency_limit":0,"first_byte_timeout":15}}`, 0},
		{`{"name":"renamed"}`, 0},
		{`{"overrides":{"first_byte_timeout":15}}`, 2},
	} {
		var request GroupSettingsUpdateRequest
		if err := json.Unmarshal([]byte(test.body), &request); err != nil {
			t.Fatal(err)
		}
		got, err := fixture.service.UpdateGroupSettings(t.Context(), group.ID, request)
		if err != nil {
			t.Fatal(err)
		}
		if got.Effective.ConcurrencyLimit != test.want || got.Effective.FirstByteTimeout != 15 {
			t.Fatalf("lost group overrides: %+v", got.Effective)
		}
	}
	workspace, err := fixture.service.ListModernGroups(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(workspace.Items)
	if err != nil {
		t.Fatal(err)
	}
	var items []struct {
		Concurrency *ConcurrencyView `json:"concurrency"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Concurrency == nil || *items[0].Concurrency != (ConcurrencyView{Current: 1, Limit: 2}) {
		t.Fatal("modern group view must report current concurrency and effective limit")
	}
	if fixture.manager.Concurrency().Snapshot().Groups[group.ID] != 1 {
		t.Fatal("publication reset active counters")
	}
}
