package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestParseAccessKeyCollectionQueryAcceptsStrictContract(t *testing.T) {
	active := state.AccessKeyStatusActive
	disabled := state.AccessKeyStatusDisabled
	unlimited := AccessKeyCollectionScopeUnlimited
	restricted := AccessKeyCollectionScopeRestricted
	q200 := strings.Repeat("猫", 200)

	tests := []struct {
		name       string
		rawQuery   string
		forceQuery bool
		want       AccessKeyCollectionQuery
	}{
		{name: "no query uses defaults", want: AccessKeyCollectionQuery{Page: 1, PageSize: 20}},
		{name: "q is trimmed", rawQuery: "q=++needle++", want: AccessKeyCollectionQuery{Query: "needle", Page: 1, PageSize: 20}},
		{name: "q may trim to empty", rawQuery: "q=+++", want: AccessKeyCollectionQuery{Page: 1, PageSize: 20}},
		{name: "q accepts 200 Unicode code points", rawQuery: "q=" + q200, want: AccessKeyCollectionQuery{Query: q200, Page: 1, PageSize: 20}},
		{name: "status active", rawQuery: "status=active", want: AccessKeyCollectionQuery{Status: &active, Page: 1, PageSize: 20}},
		{name: "status disabled", rawQuery: "status=disabled", want: AccessKeyCollectionQuery{Status: &disabled, Page: 1, PageSize: 20}},
		{name: "scope unlimited", rawQuery: "scope=unlimited", want: AccessKeyCollectionQuery{Scope: &unlimited, Page: 1, PageSize: 20}},
		{name: "scope restricted", rawQuery: "scope=restricted", want: AccessKeyCollectionQuery{Scope: &restricted, Page: 1, PageSize: 20}},
		{name: "page accepts maximum signed integer", rawQuery: "page=9223372036854775807", want: AccessKeyCollectionQuery{Page: 9223372036854775807, PageSize: 20}},
		{name: "page size accepts maximum", rawQuery: "page_size=100", want: AccessKeyCollectionQuery{Page: 1, PageSize: 100}},
		{name: "all filters combine", rawQuery: "q=+alpha+&status=active&scope=restricted&page=2&page_size=100", want: AccessKeyCollectionQuery{Query: "alpha", Status: &active, Scope: &restricted, Page: 2, PageSize: 100}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, apiErr := parseAccessKeyCollectionQuery(test.rawQuery, test.forceQuery)
			if apiErr != nil {
				t.Fatalf("parseAccessKeyCollectionQuery() error = %v", apiErr)
			}
			assertAccessKeyCollectionQueryEqual(t, got, test.want)
		})
	}
}

func TestParseAccessKeyCollectionQueryRejectsEveryInvalidForm(t *testing.T) {
	tests := []struct {
		name       string
		rawQuery   string
		forceQuery bool
	}{
		{name: "bare question mark", forceQuery: true},
		{name: "malformed escape", rawQuery: "q=%zz"},
		{name: "unknown key", rawQuery: "unknown=1"},
		{name: "q repeated", rawQuery: "q=one&q=two"},
		{name: "status repeated", rawQuery: "status=active&status=disabled"},
		{name: "scope repeated", rawQuery: "scope=unlimited&scope=restricted"},
		{name: "page repeated", rawQuery: "page=1&page=2"},
		{name: "page size repeated", rawQuery: "page_size=20&page_size=100"},
		{name: "q exceeds 200 Unicode code points", rawQuery: "q=" + strings.Repeat("猫", 201)},
		{name: "status empty", rawQuery: "status="},
		{name: "status unknown", rawQuery: "status=unavailable"},
		{name: "scope empty", rawQuery: "scope="},
		{name: "scope unknown", rawQuery: "scope=all"},
		{name: "page signed", rawQuery: "page=%2B1"},
		{name: "page leading zero", rawQuery: "page=01"},
		{name: "page empty", rawQuery: "page="},
		{name: "page negative", rawQuery: "page=-1"},
		{name: "page zero", rawQuery: "page=0"},
		{name: "page non numeric", rawQuery: "page=one"},
		{name: "page overflow", rawQuery: "page=9223372036854775808"},
		{name: "page size signed", rawQuery: "page_size=%2B1"},
		{name: "page size leading zero", rawQuery: "page_size=01"},
		{name: "page size empty", rawQuery: "page_size="},
		{name: "page size negative", rawQuery: "page_size=-1"},
		{name: "page size zero", rawQuery: "page_size=0"},
		{name: "page size non numeric", rawQuery: "page_size=twenty"},
		{name: "page size overflow", rawQuery: "page_size=9223372036854775808"},
		{name: "page size above maximum", rawQuery: "page_size=101"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, apiErr := parseAccessKeyCollectionQuery(test.rawQuery, test.forceQuery)
			if apiErr == nil || apiErr.Code != "BAD_REQUEST" {
				t.Fatalf("parseAccessKeyCollectionQuery() = %#v, %v; want BAD_REQUEST", got, apiErr)
			}
		})
	}
}

func TestAccessKeyCollectionHTTPReturnsAuthenticatedCollectionEnvelope(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(append(make([]byte, 16), bytes.Repeat([]byte{1}, 16)...))
	alpha, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "alpha"})
	if err != nil {
		t.Fatalf("create alpha access key: %v", err)
	}
	beta, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "beta", Filters: &AccessKeyFilters{Models: []string{"gpt-4o"}},
	})
	if err != nil {
		t.Fatalf("create beta access key: %v", err)
	}
	disabled := state.AccessKeyStatusDisabled
	if _, err := fixture.service.UpdateAccessKey(
		t.Context(), beta.ID, AccessKeyUpdateRequest{Status: &disabled},
	); err != nil {
		t.Fatalf("disable beta access key: %v", err)
	}
	var alphaRow, betaRow models.AccessKey
	if err := fixture.db.First(&alphaRow, alpha.ID).Error; err != nil {
		t.Fatalf("load alpha access key: %v", err)
	}
	if err := fixture.db.First(&betaRow, beta.ID).Error; err != nil {
		t.Fatalf("load beta access key: %v", err)
	}

	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)
	request := httptest.NewRequest(http.MethodGet, "/api/access-keys?status=active&scope=unlimited&page=1&page_size=100", nil)
	request.Header.Set("Authorization", "Bearer "+authTestKey)
	request.Header.Set("Accept-Language", "en-US")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/access-keys = %d %s, want 200", recorder.Code, recorder.Body.String())
	}

	data := decodeAccessKeyCollectionSuccessData(t, recorder)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("decode collection data object: %v", err)
	}
	if len(fields) != 3 || fields["summary"] == nil || fields["items"] == nil || fields["pagination"] == nil {
		t.Fatalf("collection data = %s, want object with summary/items/pagination only", data)
	}
	var result AccessKeyCollectionResponse
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode collection response: %v", err)
	}
	if result.Summary != (AccessKeyCollectionSummary{Total: 2, Active: 1, Disabled: 1}) ||
		result.Pagination != (AccessKeyCollectionPagination{Page: 1, PageSize: 100, TotalItems: 1, TotalPages: 1}) ||
		len(result.Items) != 1 || result.Items[0].Name != "alpha" ||
		result.Items[0].Scope != AccessKeyCollectionScopeUnlimited {
		t.Fatalf("collection result = %#v, want filtered summary/items/pagination", result)
	}
	for _, forbidden := range []string{alpha.Key, beta.Key, alphaRow.KeyValue, betaRow.KeyValue} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("collection response exposed %q: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestAccessKeyCollectionHTTPRejectsInvalidQueryBeforeServiceAccess(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, fixture.service).RegisterRoutes(engine)

	for _, target := range []string{
		"/api/access-keys?unknown=1",
		"/api/access-keys?page=01",
		"/api/access-keys?",
	} {
		t.Run(target, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, target, nil)
			request.Header.Set("Authorization", "Bearer "+authTestKey)
			request.Header.Set("Accept-Language", "en-US")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assertAccessKeyCollectionHTTPError(t, recorder, http.StatusBadRequest, "BAD_REQUEST")
		})
	}
}

func assertAccessKeyCollectionQueryEqual(t *testing.T, got, want AccessKeyCollectionQuery) {
	t.Helper()
	if got.Query != want.Query || got.Page != want.Page || got.PageSize != want.PageSize {
		t.Fatalf("query = %#v, want %#v", got, want)
	}
	if (got.Status == nil) != (want.Status == nil) || got.Status != nil && *got.Status != *want.Status {
		t.Fatalf("query status = %#v, want %#v", got.Status, want.Status)
	}
	if (got.Scope == nil) != (want.Scope == nil) || got.Scope != nil && *got.Scope != *want.Scope {
		t.Fatalf("query scope = %#v, want %#v", got.Scope, want.Scope)
	}
}

func decodeAccessKeyCollectionSuccessData(t *testing.T, recorder *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if len(fields) != 3 || string(fields["code"]) != "0" || string(fields["message"]) != `"Success"` || len(fields["data"]) == 0 {
		t.Fatalf("success envelope = %s, want only code/message/data", recorder.Body.String())
	}
	return fields["data"]
}

func assertAccessKeyCollectionHTTPError(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("response = %d %s, want %d", recorder.Code, recorder.Body.String(), wantStatus)
	}
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Code != wantCode {
		t.Fatalf("error code = %q, want %q; response = %s", envelope.Code, wantCode, recorder.Body.String())
	}
}
