package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestParseGroupCollectionQueryAcceptsStrictContract(t *testing.T) {
	available := GroupCollectionStatusAvailable
	unavailable := GroupCollectionStatusUnavailable
	disabled := GroupCollectionStatusDisabled
	openAICompletions := protocol.OpenAICompletions
	openAIResponses := protocol.OpenAIResponses
	anthropic := protocol.Anthropic
	gemini := protocol.Gemini
	q200 := strings.Repeat("猫", 200)

	tests := []struct {
		name       string
		rawQuery   string
		forceQuery bool
		want       GroupCollectionQuery
	}{
		{
			name: "no query uses defaults",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "q is trimmed",
			rawQuery: "q=++needle++",
			want: GroupCollectionQuery{
				Query: "needle", Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "q may trim to empty",
			rawQuery: "q=+++",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "q accepts 200 Unicode code points",
			rawQuery: "q=" + q200,
			want: GroupCollectionQuery{
				Query: q200, Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "status available",
			rawQuery: "status=available",
			want: GroupCollectionQuery{
				Status: &available, Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "status unavailable",
			rawQuery: "status=unavailable",
			want: GroupCollectionQuery{
				Status: &unavailable, Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "status disabled",
			rawQuery: "status=disabled",
			want: GroupCollectionQuery{
				Status: &disabled, Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "protocol openai completions",
			rawQuery: "protocol=openai-completions",
			want: GroupCollectionQuery{
				Protocol: &openAICompletions, Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "protocol openai responses",
			rawQuery: "protocol=openai-responses",
			want: GroupCollectionQuery{
				Protocol: &openAIResponses, Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "protocol anthropic",
			rawQuery: "protocol=anthropic",
			want: GroupCollectionQuery{
				Protocol: &anthropic, Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "protocol gemini",
			rawQuery: "protocol=gemini",
			want: GroupCollectionQuery{
				Protocol: &gemini, Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "sort status",
			rawQuery: "sort=status",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortStatus, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "sort name",
			rawQuery: "sort=name",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortName, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "sort keys",
			rawQuery: "sort=keys",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortKeys, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "sort created",
			rawQuery: "sort=created",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortCreated, Page: 1, PageSize: 20,
			},
		},
		{
			name:     "page accepts positive integer",
			rawQuery: "page=9223372036854775807",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortStatus, Page: 9223372036854775807, PageSize: 20,
			},
		},
		{
			name:     "page size accepts one",
			rawQuery: "page_size=1",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortStatus, Page: 1, PageSize: 1,
			},
		},
		{
			name:     "page size accepts one hundred",
			rawQuery: "page_size=100",
			want: GroupCollectionQuery{
				Sort: GroupCollectionSortStatus, Page: 1, PageSize: 100,
			},
		},
		{
			name:     "all fields combine",
			rawQuery: "q=+alpha+&status=available&protocol=anthropic&sort=created&page=2&page_size=100",
			want: GroupCollectionQuery{
				Query: "alpha", Status: &available, Protocol: &anthropic,
				Sort: GroupCollectionSortCreated, Page: 2, PageSize: 100,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, apiErr := parseGroupCollectionQuery(test.rawQuery, test.forceQuery)
			if apiErr != nil {
				t.Fatalf("parseGroupCollectionQuery() error = %v", apiErr)
			}
			assertGroupCollectionQueryEqual(t, got, test.want)
		})
	}
}

func TestParseGroupCollectionQueryRejectsEveryInvalidForm(t *testing.T) {
	tests := []struct {
		name       string
		rawQuery   string
		forceQuery bool
	}{
		{name: "bare question mark", forceQuery: true},
		{name: "malformed escape", rawQuery: "q=%zz"},
		{name: "unknown key", rawQuery: "unknown=1"},
		{name: "q repeated", rawQuery: "q=one&q=two"},
		{name: "status repeated", rawQuery: "status=available&status=disabled"},
		{name: "protocol repeated", rawQuery: "protocol=anthropic&protocol=gemini"},
		{name: "sort repeated", rawQuery: "sort=name&sort=keys"},
		{name: "page repeated", rawQuery: "page=1&page=2"},
		{name: "page size repeated", rawQuery: "page_size=20&page_size=100"},
		{name: "q exceeds 200 Unicode code points", rawQuery: "q=" + strings.Repeat("猫", 201)},
		{name: "status empty", rawQuery: "status="},
		{name: "status all", rawQuery: "status=all"},
		{name: "status unknown", rawQuery: "status=healthy"},
		{name: "protocol empty", rawQuery: "protocol="},
		{name: "protocol unknown", rawQuery: "protocol=openai"},
		{name: "sort empty", rawQuery: "sort="},
		{name: "sort unknown", rawQuery: "sort=ascending"},
		{name: "page signed", rawQuery: "page=%2B1"},
		{name: "page leading zero", rawQuery: "page=01"},
		{name: "page empty", rawQuery: "page="},
		{name: "page negative", rawQuery: "page=-1"},
		{name: "page zero", rawQuery: "page=0"},
		{name: "page overflow", rawQuery: "page=9223372036854775808"},
		{name: "page size signed", rawQuery: "page_size=%2B1"},
		{name: "page size leading zero", rawQuery: "page_size=01"},
		{name: "page size empty", rawQuery: "page_size="},
		{name: "page size negative", rawQuery: "page_size=-1"},
		{name: "page size zero", rawQuery: "page_size=0"},
		{name: "page size overflow", rawQuery: "page_size=9223372036854775808"},
		{name: "page size above maximum", rawQuery: "page_size=101"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, apiErr := parseGroupCollectionQuery(test.rawQuery, test.forceQuery)
			if apiErr == nil || apiErr.Code != "BAD_REQUEST" {
				t.Fatalf("parseGroupCollectionQuery() = %#v, %v; want BAD_REQUEST", got, apiErr)
			}
		})
	}
}

func assertGroupCollectionQueryEqual(
	t *testing.T,
	got GroupCollectionQuery,
	want GroupCollectionQuery,
) {
	t.Helper()
	if got.Query != want.Query || got.Sort != want.Sort ||
		got.Page != want.Page || got.PageSize != want.PageSize {
		t.Fatalf("query = %#v, want %#v", got, want)
	}
	if (got.Status == nil) != (want.Status == nil) ||
		got.Status != nil && *got.Status != *want.Status {
		t.Fatalf("query status = %#v, want %#v", got.Status, want.Status)
	}
	if (got.Protocol == nil) != (want.Protocol == nil) ||
		got.Protocol != nil && *got.Protocol != *want.Protocol {
		t.Fatalf("query protocol = %#v, want %#v", got.Protocol, want.Protocol)
	}
}

func TestGroupCollectionHTTPReturnsExactCollectionAndOptionsContracts(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	createGroupOptionGroup(
		t,
		fixture,
		10,
		"alpha",
		true,
		`["openai-completions","gemini"]`,
		`[{"id":"private-alpha","alias":"public-alpha"},{"id":"second-alpha","alias":""}]`,
	)
	createGroupOptionGroup(
		t,
		fixture,
		20,
		"zulu",
		false,
		`["anthropic"]`,
		`[{"id":"private-zulu","alias":"public-zulu"}]`,
	)
	entry := createGroupCollectionKey(
		t,
		fixture,
		10,
		models.UpstreamKeyStatusActive,
		nil,
	)
	publishGroupCollectionRuntime(t, fixture, []state.KeyEntry{entry})
	observedAt := time.Date(2026, time.August, 1, 9, 10, 11, 0, time.UTC)
	fixture.service.now = func() time.Time { return observedAt }

	engine := gin.New()
	NewServer(
		&config.Config{AuthKey: authTestKey},
		fixture.service,
	).RegisterRoutes(engine)

	collection := performGroupCollectionRequest(
		engine,
		"/api/groups?sort=name&page=1&page_size=1",
		"Bearer "+authTestKey,
	)
	if collection.Code != http.StatusOK {
		t.Fatalf("collection response = %d %s, want 200", collection.Code, collection.Body.String())
	}
	collectionData := decodeGroupCollectionSuccess(t, collection)
	if collectionData.ObservedAtMS != observedAt.UnixMilli() ||
		collectionData.Summary != (GroupCollectionSummary{
			Total: 2, Available: 1, Disabled: 1,
		}) ||
		collectionData.Pagination != (GroupCollectionPagination{
			Page: 1, PageSize: 1, TotalItems: 2, TotalPages: 2,
		}) {
		t.Fatalf("collection data = %#v, want exact observation/summary/pagination", collectionData)
	}
	if len(collectionData.Items) != 1 {
		t.Fatalf("collection items = %#v, want one paginated item", collectionData.Items)
	}
	item := collectionData.Items[0]
	if item.ID != 10 || item.Name != "alpha" ||
		item.Status != GroupCollectionStatusAvailable ||
		item.UpstreamURL != "https://alpha.example/v1" ||
		!reflect.DeepEqual(item.Protocols, []protocol.Protocol{
			protocol.OpenAICompletions,
			protocol.Gemini,
		}) ||
		item.ModelCount != 2 ||
		item.KeyCounts != (GroupCollectionKeyCounts{Total: 1, Available: 1}) {
		t.Fatalf("collection item = %#v, want exact public fields", item)
	}

	options := performGroupCollectionRequest(
		engine,
		"/api/groups/options",
		"Bearer "+authTestKey,
	)
	if options.Code != http.StatusOK {
		t.Fatalf("options response = %d %s, want 200", options.Code, options.Body.String())
	}
	optionData := decodeGroupOptionsSuccess(t, options)
	if len(optionData) != 2 ||
		optionData[0].ID != 10 || optionData[0].Name != "alpha" ||
		!optionData[0].Enabled ||
		!reflect.DeepEqual(optionData[0].Protocols, []protocol.Protocol{protocol.OpenAICompletions, protocol.Gemini}) ||
		len(optionData[0].Models) != 2 ||
		optionData[0].Models[0] != "public-alpha" ||
		optionData[0].Models[1] != "second-alpha" ||
		optionData[1].ID != 20 || optionData[1].Name != "zulu" ||
		optionData[1].Enabled ||
		!reflect.DeepEqual(optionData[1].Protocols, []protocol.Protocol{protocol.Anthropic}) ||
		len(optionData[1].Models) != 1 || optionData[1].Models[0] != "public-zulu" {
		t.Fatalf("options data = %#v, want exact ID-ordered directory", optionData)
	}

	combined := strings.ToLower(options.Body.String())
	for _, forbidden := range []string{
		"upstream_url", "key_value", "key_hash", "keyvalue", "keyhash", "cipher", "hash", "secret",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("group HTTP responses exposed %q: %s", forbidden, combined)
		}
	}
}

func TestGroupOptionsHTTPRejectsAnyQueryIncludingBareQuestionMark(t *testing.T) {
	initControlI18n(t)
	engine := gin.New()
	NewServer(&config.Config{AuthKey: authTestKey}, nil).RegisterRoutes(engine)

	for _, target := range []string{
		"/api/groups/options?q=alpha",
		"/api/groups/options?unknown=1",
		"/api/groups/options?",
	} {
		t.Run(target, func(t *testing.T) {
			recorder := performGroupCollectionRequest(
				engine,
				target,
				"Bearer "+authTestKey,
			)
			assertGroupCollectionHTTPError(
				t,
				recorder,
				http.StatusBadRequest,
				"BAD_REQUEST",
			)
		})
	}
}

func TestGroupCollectionHTTPRejectsInvalidQueryBeforeServiceAccess(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	engine := gin.New()
	NewServer(
		&config.Config{AuthKey: authTestKey},
		fixture.service,
	).RegisterRoutes(engine)

	for _, target := range []string{
		"/api/groups?unknown=1",
		"/api/groups?page=01",
		"/api/groups?",
	} {
		t.Run(target, func(t *testing.T) {
			recorder := performGroupCollectionRequest(
				engine,
				target,
				"Bearer "+authTestKey,
			)
			assertGroupCollectionHTTPError(
				t,
				recorder,
				http.StatusBadRequest,
				"BAD_REQUEST",
			)
		})
	}
}

func TestGroupCollectionHTTPMapsServiceErrorsThroughStandardEnvelope(t *testing.T) {
	initControlI18n(t)
	for _, target := range []string{"/api/groups", "/api/groups/options"} {
		t.Run(target, func(t *testing.T) {
			fixture := newServiceFixture(t)
			sqlDB, err := fixture.db.DB()
			if err != nil {
				t.Fatalf("fixture DB(): %v", err)
			}
			if err := sqlDB.Close(); err != nil {
				t.Fatalf("close fixture database: %v", err)
			}
			engine := gin.New()
			NewServer(
				&config.Config{AuthKey: authTestKey},
				fixture.service,
			).RegisterRoutes(engine)

			recorder := performGroupCollectionRequest(
				engine,
				target,
				"Bearer "+authTestKey,
			)
			assertGroupCollectionHTTPError(
				t,
				recorder,
				http.StatusInternalServerError,
				"DATABASE_ERROR",
			)
		})
	}
}

func performGroupCollectionRequest(
	engine *gin.Engine,
	target string,
	authorization string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("Accept-Language", "en-US")
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeGroupCollectionSuccess(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) GroupCollectionResponse {
	t.Helper()
	data := decodeGroupCollectionSuccessData(t, recorder)
	var result GroupCollectionResponse
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode collection data: %v", err)
	}
	return result
}

func decodeGroupOptionsSuccess(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) []GroupOption {
	t.Helper()
	data := decodeGroupCollectionSuccessData(t, recorder)
	var rawOptions []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawOptions); err != nil {
		t.Fatalf("decode raw options data: %v", err)
	}
	for _, rawOption := range rawOptions {
		if len(rawOption) != 5 {
			t.Fatalf("option fields = %#v, want exactly id/name/enabled/protocols/models", rawOption)
		}
		for _, field := range []string{"id", "name", "enabled", "protocols", "models"} {
			if _, ok := rawOption[field]; !ok {
				t.Fatalf("option fields = %#v, missing %q", rawOption, field)
			}
		}
	}
	var result []GroupOption
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode options data: %v", err)
	}
	return result
}

func decodeGroupCollectionSuccessData(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode success envelope: %v", err)
	}
	if len(fields) != 3 || string(fields["code"]) != "0" ||
		string(fields["message"]) != `"Success"` || len(fields["data"]) == 0 {
		t.Fatalf("success envelope = %s, want only code/message/data", recorder.Body.String())
	}
	return fields["data"]
}

func assertGroupCollectionHTTPError(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
) {
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
