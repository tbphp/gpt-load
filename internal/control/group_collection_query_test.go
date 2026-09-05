package control

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"gpt-load/internal/channel"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestListGroupCollectionCapturesThenQueries(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	observedAt := int64(1_700)
	fixture.service.now = func() time.Time { return time.UnixMilli(observedAt) }
	available := createGroupCollectionGroup(t, fixture, "available", true, nil)
	disabled := createGroupCollectionGroup(t, fixture, "disabled", false, nil)
	entries := []state.CredentialEntry{
		createGroupCollectionKey(t, fixture, available.ID, models.CredentialStatusActive, nil),
		createGroupCollectionKey(t, fixture, disabled.ID, models.CredentialStatusActive, nil),
	}
	publishGroupCollectionRuntime(t, fixture, entries)

	result, err := fixture.service.ListGroupCollection(context.Background(), GroupCollectionQuery{
		Sort: GroupCollectionSortStatus, Page: 1, PageSize: 1,
	})
	if err != nil {
		t.Fatalf("ListGroupCollection() error = %v", err)
	}
	if got, want := groupCollectionItemIDs(result.Items), []uint{available.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ListGroupCollection() item IDs = %#v, want %#v", got, want)
	}
	if got, want := result.Summary, (GroupCollectionSummary{
		Total: 2, Available: 1, Disabled: 1,
	}); got != want {
		t.Fatalf("ListGroupCollection() summary = %#v, want %#v", got, want)
	}
	if result.ObservedAtMS != observedAt {
		t.Fatalf("ListGroupCollection() observed_at_ms = %d, want %d", result.ObservedAtMS, observedAt)
	}
}

func TestListGroupCollectionSortsRecentActivityByHourThenRequestCount(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	older := createGroupCollectionGroup(t, fixture, "older", true, nil)
	recentLow := createGroupCollectionGroup(t, fixture, "alpha recent", true, nil)
	recentHigh := createGroupCollectionGroup(t, fixture, "zulu recent", true, nil)
	unusedZulu := createGroupCollectionGroup(t, fixture, "zulu unused", true, nil)
	unusedAlpha := createGroupCollectionGroup(t, fixture, "alpha unused", true, nil)
	entries := []state.CredentialEntry{
		createGroupCollectionKey(t, fixture, older.ID, models.CredentialStatusActive, nil),
		createGroupCollectionKey(t, fixture, recentLow.ID, models.CredentialStatusActive, nil),
		createGroupCollectionKey(t, fixture, recentHigh.ID, models.CredentialStatusActive, nil),
		createGroupCollectionKey(t, fixture, unusedZulu.ID, models.CredentialStatusActive, nil),
		createGroupCollectionKey(t, fixture, unusedAlpha.ID, models.CredentialStatusActive, nil),
	}
	publishGroupCollectionRuntime(t, fixture, entries)

	const (
		olderHour  = int64(1_700_000_000_000)
		recentHour = olderHour + 3_600_000
	)
	createGroupCollectionUsageStat(t, fixture, older.ID, olderHour, 50, "older")
	createGroupCollectionUsageStat(t, fixture, recentLow.ID, recentHour, 3, "recent-low")
	createGroupCollectionUsageStat(t, fixture, recentHigh.ID, recentHour, 2, "recent-high-a")
	createGroupCollectionUsageStat(t, fixture, recentHigh.ID, recentHour, 7, "recent-high-b")

	result, err := fixture.service.ListGroupCollection(t.Context(), GroupCollectionQuery{
		Sort: GroupCollectionSortRecent, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("ListGroupCollection() error = %v", err)
	}
	if got, want := groupCollectionItemIDs(result.Items), []uint{
		recentHigh.ID, recentLow.ID, older.ID, unusedAlpha.ID, unusedZulu.ID,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent activity IDs = %#v, want %#v", got, want)
	}
}

func TestGroupCollectionLatestActivityScopeQuotesGroupsForMySQL(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		DSN:                       "user:password@tcp(127.0.0.1:3306)/gpt_load",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}

	result := groupCollectionLatestActivityScope(db).Find(&[]groupCollectionActivityRow{})
	if result.Error != nil {
		t.Fatalf("latest activity query error = %v", result.Error)
	}
	sql := result.Statement.SQL.String()
	if strings.Contains(sql, "JOIN groups") {
		t.Fatalf("generated SQL = %q, must not contain an unquoted groups join", sql)
	}
	if !strings.Contains(sql, "FROM `groups`") {
		t.Fatalf("generated SQL = %q, want GORM-quoted groups subquery", sql)
	}
}

func TestListGroupCollectionSkipsActivityReadForNonRecentSort(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	group := createGroupCollectionGroup(t, fixture, "name sort", true, nil)
	entry := createGroupCollectionKey(t, fixture, group.ID, models.CredentialStatusActive, nil)
	publishGroupCollectionRuntime(t, fixture, []state.CredentialEntry{entry})
	if err := fixture.db.Migrator().DropTable(&models.UsageStat{}); err != nil {
		t.Fatalf("drop usage_stats: %v", err)
	}

	result, err := fixture.service.ListGroupCollection(t.Context(), GroupCollectionQuery{
		Sort: GroupCollectionSortName, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("ListGroupCollection() error = %v", err)
	}
	if got, want := groupCollectionItemIDs(result.Items), []uint{group.ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("name-sort IDs = %#v, want %#v", got, want)
	}
}

func TestListGroupCollectionQueryDoesNotSearchPersistedModelIDOrAlias(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	group := createGroupCollectionGroup(t, fixture, "not a model", true, nil)
	group.Models = models.JSON(`[{"id":"private-upstream-model","alias":"public-model-alias"}]`)
	if err := fixture.db.Model(group).Update("models", group.Models).Error; err != nil {
		t.Fatalf("update persisted models: %v", err)
	}
	entry := createGroupCollectionKey(t, fixture, group.ID, models.CredentialStatusActive, nil)
	publishGroupCollectionRuntime(t, fixture, []state.CredentialEntry{entry})

	for _, query := range []string{"private-upstream-model", "public-model-alias"} {
		t.Run(query, func(t *testing.T) {
			result, err := fixture.service.ListGroupCollection(t.Context(), GroupCollectionQuery{
				Query: query, Sort: GroupCollectionSortName, Page: 1, PageSize: 20,
			})
			if err != nil {
				t.Fatalf("ListGroupCollection() error = %v", err)
			}
			if len(result.Items) != 0 || result.Pagination.TotalItems != 0 || result.Summary.Total != 1 {
				t.Fatalf("query %q response = %#v, want no matched item from one captured group", query, result)
			}
		})
	}
}

func TestGroupCollectionQueryFiltersUnicodeInsensitiveFieldsWithoutModels(t *testing.T) {
	t.Parallel()
	records := []groupCollectionRecord{
		groupCollectionQueryRecord(1, "München", GroupCollectionStatusAvailable, "https://muenchen.example.net/v1", []protocol.Protocol{protocol.OpenAICompletions}, 7, 100),
		groupCollectionQueryRecord(2, "URL only", GroupCollectionStatusUnavailable, "https://Api.Example.Net/v1", []protocol.Protocol{protocol.Gemini}, 3, 200),
		groupCollectionQueryRecord(3, "Protocol only", GroupCollectionStatusDisabled, "https://other.example.net/v1", []protocol.Protocol{protocol.Anthropic}, 99, 300),
	}

	tests := []struct {
		name  string
		query string
		want  []uint
	}{
		{name: "Unicode name", query: "MÜN", want: []uint{1}},
		{name: "URL", query: "api.example.net", want: []uint{2}},
		{name: "protocol", query: "ANTHROPIC", want: []uint{3}},
		{name: "does not search models", query: "model-not-in-record", want: []uint{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := queryGroupCollectionRecords(1_700, records, GroupCollectionQuery{
				Query: test.query, Sort: GroupCollectionSortName, Page: 1, PageSize: 20,
			})
			if got := groupCollectionItemIDs(result.Items); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("query %q items = %#v, want %#v", test.query, got, test.want)
			}
		})
	}
}

func TestGroupCollectionQueryMatchesUnicodeSimpleFoldAndSortsWithIt(t *testing.T) {
	t.Parallel()
	records := []groupCollectionRecord{
		groupCollectionQueryRecord(2, "ΟΣ", GroupCollectionStatusAvailable, "https://two.example/v1", nil, 1, 100),
		groupCollectionQueryRecord(1, "ος", GroupCollectionStatusAvailable, "https://one.example/v1", nil, 1, 100),
	}

	matched := queryGroupCollectionRecords(1_700, records, GroupCollectionQuery{
		Query: "ος", Sort: GroupCollectionSortName, Page: 1, PageSize: 20,
	})
	if got, want := groupCollectionItemIDs(matched.Items), []uint{2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("simple-fold query item IDs = %#v, want %#v", got, want)
	}
}

func TestGroupCollectionQueryFiltersByConnectionType(t *testing.T) {
	t.Parallel()
	subscription := models.ConnectionTypeSubscription
	records := []groupCollectionRecord{
		groupCollectionQueryRecord(1, "API key", GroupCollectionStatusAvailable, "https://api-key.example", nil, 1, 100),
		groupCollectionQueryRecord(2, "Subscription", GroupCollectionStatusAvailable, "https://subscription.example", nil, 1, 200),
		groupCollectionQueryRecord(3, "Another API key", GroupCollectionStatusUnavailable, "https://another-api-key.example", nil, 1, 300),
	}
	records[1].ConnectionType = subscription

	result := queryGroupCollectionRecords(1_700, records, GroupCollectionQuery{
		ConnectionType: &subscription, Sort: GroupCollectionSortName, Page: 1, PageSize: 20,
	})
	if got, want := groupCollectionItemIDs(result.Items), []uint{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("connection type filter item IDs = %#v, want %#v", got, want)
	}
	if got, want := result.Pagination.TotalItems, int64(1); got != want {
		t.Fatalf("connection type filter total items = %d, want %d", got, want)
	}
}

func TestGroupCollectionQueryCombinesFiltersAndSummarizesCompleteCollection(t *testing.T) {
	t.Parallel()
	available := GroupCollectionStatusAvailable
	records := []groupCollectionRecord{
		groupCollectionQueryRecord(1, "Alpha", GroupCollectionStatusAvailable, "https://alpha.example/v1", []protocol.Protocol{protocol.OpenAICompletions}, 1, 100),
		groupCollectionQueryRecord(2, "Alpha anthropic", GroupCollectionStatusAvailable, "https://alpha.example/v1", []protocol.Protocol{protocol.Anthropic}, 2, 200),
		groupCollectionQueryRecord(3, "Alpha disabled", GroupCollectionStatusDisabled, "https://alpha.example/v1", []protocol.Protocol{protocol.Anthropic}, 3, 300),
		groupCollectionQueryRecord(4, "Beta", GroupCollectionStatusUnavailable, "https://beta.example/v1", []protocol.Protocol{protocol.Anthropic}, 4, 400),
	}

	result := queryGroupCollectionRecords(1_700, records, GroupCollectionQuery{
		Query: "ALPHA", Status: &available,
		Sort: GroupCollectionSortName, Page: 1, PageSize: 20,
	})
	if got, want := groupCollectionItemIDs(result.Items), []uint{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("combined filter item IDs = %#v, want %#v", got, want)
	}
	if got, want := result.Summary, (GroupCollectionSummary{
		Total: 4, Available: 2, Unavailable: 1, Disabled: 1,
	}); got != want {
		t.Fatalf("summary = %#v, want %#v", got, want)
	}
	if result.ObservedAtMS != 1_700 {
		t.Fatalf("observed_at_ms = %d, want 1700", result.ObservedAtMS)
	}
}

func TestGroupCollectionQueryUsesFixedSortsWithIDTieBreak(t *testing.T) {
	t.Parallel()
	records := []groupCollectionRecord{
		groupCollectionQueryRecord(5, "Bravo", GroupCollectionStatusAvailable, "https://five.example/v1", nil, 3, 500),
		groupCollectionQueryRecord(4, "bravo", GroupCollectionStatusAvailable, "https://four.example/v1", nil, 3, 400),
		groupCollectionQueryRecord(3, "Álpha", GroupCollectionStatusUnavailable, "https://three.example/v1", nil, 9, 300),
		groupCollectionQueryRecord(2, "Alpha", GroupCollectionStatusUnavailable, "https://two.example/v1", nil, 9, 200),
		groupCollectionQueryRecord(1, "Zero", GroupCollectionStatusDisabled, "https://one.example/v1", nil, 1, 100),
	}

	tests := []struct {
		sort GroupCollectionSort
		want []uint
	}{
		{sort: GroupCollectionSortStatus, want: []uint{2, 3, 5, 4, 1}},
		{sort: GroupCollectionSortName, want: []uint{2, 5, 4, 1, 3}},
		{sort: GroupCollectionSortCredentials, want: []uint{2, 3, 5, 4, 1}},
		{sort: GroupCollectionSortCreated, want: []uint{5, 4, 3, 2, 1}},
	}
	for _, test := range tests {
		t.Run(string(test.sort), func(t *testing.T) {
			result := queryGroupCollectionRecords(1_700, records, GroupCollectionQuery{
				Sort: test.sort, Page: 1, PageSize: 20,
			})
			if got := groupCollectionItemIDs(result.Items); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("sort %q item IDs = %#v, want %#v", test.sort, got, test.want)
			}
		})
	}
}

func TestGroupCollectionQueryRecentActivityFallsBackToNameWithoutUsage(t *testing.T) {
	t.Parallel()
	records := []groupCollectionRecord{
		groupCollectionQueryRecord(2, "Zulu", GroupCollectionStatusUnavailable, "https://zulu.example/v1", nil, 1, 200),
		groupCollectionQueryRecord(1, "Alpha", GroupCollectionStatusDisabled, "https://alpha.example/v1", nil, 1, 100),
	}

	result := queryGroupCollectionRecords(1_700, records, GroupCollectionQuery{Page: 1, PageSize: 20})
	if got, want := groupCollectionItemIDs(result.Items), []uint{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recent activity fallback IDs = %#v, want %#v", got, want)
	}
}

func TestGroupCollectionSortUsesActivityOnlyForRecentOrdering(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		sortBy GroupCollectionSort
		want   bool
	}{
		{name: "default", sortBy: "", want: true},
		{name: "recent", sortBy: GroupCollectionSortRecent, want: true},
		{name: "status", sortBy: GroupCollectionSortStatus, want: false},
		{name: "name", sortBy: GroupCollectionSortName, want: false},
		{name: "credentials", sortBy: GroupCollectionSortCredentials, want: false},
		{name: "created", sortBy: GroupCollectionSortCreated, want: false},
		{name: "unknown defaults to recent", sortBy: "unknown", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := groupCollectionSortUsesActivity(test.sortBy); got != test.want {
				t.Fatalf("groupCollectionSortUsesActivity(%q) = %t, want %t", test.sortBy, got, test.want)
			}
		})
	}
}

func TestGroupCollectionQueryUsesIDTieBreakAfterEachSortPrimaryAndName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		sort    GroupCollectionSort
		records []groupCollectionRecord
		want    []uint
	}{
		{
			name: "status ascending ID after same status and name",
			sort: GroupCollectionSortStatus,
			records: []groupCollectionRecord{
				groupCollectionQueryRecord(10, "same", GroupCollectionStatusUnavailable, "https://ten.example/v1", nil, 1, 100),
				groupCollectionQueryRecord(9, "same", GroupCollectionStatusUnavailable, "https://nine.example/v1", nil, 2, 200),
			},
			want: []uint{9, 10},
		},
		{
			name: "name ascending ID after same folded and raw name",
			sort: GroupCollectionSortName,
			records: []groupCollectionRecord{
				groupCollectionQueryRecord(10, "same", GroupCollectionStatusAvailable, "https://ten.example/v1", nil, 1, 100),
				groupCollectionQueryRecord(9, "same", GroupCollectionStatusDisabled, "https://nine.example/v1", nil, 2, 200),
			},
			want: []uint{9, 10},
		},
		{
			name: "keys ascending ID after same keys and name",
			sort: GroupCollectionSortCredentials,
			records: []groupCollectionRecord{
				groupCollectionQueryRecord(10, "same", GroupCollectionStatusAvailable, "https://ten.example/v1", nil, 5, 100),
				groupCollectionQueryRecord(9, "same", GroupCollectionStatusDisabled, "https://nine.example/v1", nil, 5, 200),
			},
			want: []uint{9, 10},
		},
		{
			name: "created descending ID after same created time",
			sort: GroupCollectionSortCreated,
			records: []groupCollectionRecord{
				groupCollectionQueryRecord(9, "left", GroupCollectionStatusAvailable, "https://nine.example/v1", nil, 1, 100),
				groupCollectionQueryRecord(10, "right", GroupCollectionStatusDisabled, "https://ten.example/v1", nil, 2, 100),
			},
			want: []uint{10, 9},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := queryGroupCollectionRecords(1_700, test.records, GroupCollectionQuery{
				Sort: test.sort, Page: 1, PageSize: 20,
			})
			if got := groupCollectionItemIDs(result.Items); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("sort %q item IDs = %#v, want %#v", test.sort, got, test.want)
			}
		})
	}
}

func TestGroupCollectionQueryPaginatesWithoutLeakingCreatedAt(t *testing.T) {
	t.Parallel()
	records := []groupCollectionRecord{
		groupCollectionQueryRecord(1, "one", GroupCollectionStatusAvailable, "https://one.example/v1", nil, 1, 10),
		groupCollectionQueryRecord(2, "two", GroupCollectionStatusAvailable, "https://two.example/v1", nil, 1, 20),
		groupCollectionQueryRecord(3, "three", GroupCollectionStatusAvailable, "https://three.example/v1", nil, 1, 30),
		groupCollectionQueryRecord(4, "four", GroupCollectionStatusAvailable, "https://four.example/v1", nil, 1, 40),
		groupCollectionQueryRecord(5, "five", GroupCollectionStatusAvailable, "https://five.example/v1", nil, 1, 50),
	}

	tests := []struct {
		name     string
		query    GroupCollectionQuery
		wantIDs  []uint
		wantPage GroupCollectionPagination
	}{
		{
			name:     "first page",
			query:    GroupCollectionQuery{Sort: GroupCollectionSortCreated, Page: 1, PageSize: 2},
			wantIDs:  []uint{5, 4},
			wantPage: GroupCollectionPagination{Page: 1, PageSize: 2, TotalItems: 5, TotalPages: 3},
		},
		{
			name:     "last page",
			query:    GroupCollectionQuery{Sort: GroupCollectionSortCreated, Page: 3, PageSize: 2},
			wantIDs:  []uint{1},
			wantPage: GroupCollectionPagination{Page: 3, PageSize: 2, TotalItems: 5, TotalPages: 3},
		},
		{
			name:     "empty filter page",
			query:    GroupCollectionQuery{Query: "missing", Sort: GroupCollectionSortCreated, Page: 1, PageSize: 2},
			wantIDs:  []uint{},
			wantPage: GroupCollectionPagination{Page: 1, PageSize: 2, TotalItems: 0, TotalPages: 0},
		},
		{
			name:     "out of range page",
			query:    GroupCollectionQuery{Sort: GroupCollectionSortCreated, Page: 4, PageSize: 2},
			wantIDs:  []uint{},
			wantPage: GroupCollectionPagination{Page: 4, PageSize: 2, TotalItems: 5, TotalPages: 3},
		},
		{
			name:     "overflow safe offset",
			query:    GroupCollectionQuery{Sort: GroupCollectionSortCreated, Page: math.MaxInt64, PageSize: 2},
			wantIDs:  []uint{},
			wantPage: GroupCollectionPagination{Page: math.MaxInt64, PageSize: 2, TotalItems: 5, TotalPages: 3},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := queryGroupCollectionRecords(1_700, records, test.query)
			if got := groupCollectionItemIDs(result.Items); !reflect.DeepEqual(got, test.wantIDs) {
				t.Fatalf("item IDs = %#v, want %#v", got, test.wantIDs)
			}
			if got := result.Pagination; got != test.wantPage {
				t.Fatalf("pagination = %#v, want %#v", got, test.wantPage)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("json.Marshal(response) error = %v", err)
			}
			if string(encoded) == "" || containsJSONToken(encoded, "created_at_ms") {
				t.Fatalf("response JSON leaks created_at_ms: %s", encoded)
			}
		})
	}
}

func groupCollectionQueryRecord(
	id uint,
	name string,
	status GroupCollectionStatus,
	upstreamURL string,
	protocols []protocol.Protocol,
	keys int64,
	createdAtMS int64,
) groupCollectionRecord {
	channelID := channel.OpenAICompatible
	for _, selectedProtocol := range protocols {
		switch selectedProtocol {
		case protocol.Anthropic:
			channelID = channel.Anthropic
		case protocol.Gemini:
			channelID = channel.Gemini
		}
	}
	params, err := json.Marshal(map[string]string{"base_url": upstreamURL})
	if err != nil {
		panic(err)
	}
	return groupCollectionRecord{
		GroupCollectionItem: GroupCollectionItem{
			ID: id, Name: name, Status: status, ChannelID: channelID,
			ConnectionType: models.ConnectionTypeAPIKey,
			Params:         params, ModelCount: 7,
			CredentialCounts: GroupCollectionCredentialCounts{Total: keys},
		},
		CreatedAtMS: createdAtMS,
	}
}

func groupCollectionItemIDs(items []GroupCollectionItem) []uint {
	result := make([]uint, len(items))
	for index := range items {
		result[index] = items[index].ID
	}
	return result
}

func createGroupCollectionUsageStat(
	t *testing.T,
	fixture serviceFixture,
	groupID uint,
	bucketStartMS int64,
	requestCount int64,
	model string,
) {
	t.Helper()
	row := models.UsageStat{
		BucketStartMS: bucketStartMS,
		AccessKeyID:   1,
		ChannelID:     string(channel.OpenAICompatible),
		GroupID:       groupID,
		CredentialID:  1,
		Model:         model,
		RequestCount:  requestCount,
		SuccessCount:  requestCount,
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatalf("create usage stat for group %d: %v", groupID, err)
	}
}
