package control

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestListGroupCollectionCapturesThenQueries(t *testing.T) {
	fixture := newServiceFixture(t)
	observedAt := int64(1_700)
	fixture.service.now = func() time.Time { return time.UnixMilli(observedAt) }
	available := createGroupCollectionGroup(t, fixture, "available", true, nil)
	disabled := createGroupCollectionGroup(t, fixture, "disabled", false, nil)
	entries := []state.KeyEntry{
		createGroupCollectionKey(t, fixture, available.ID, models.UpstreamKeyStatusActive, nil),
		createGroupCollectionKey(t, fixture, disabled.ID, models.UpstreamKeyStatusActive, nil),
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

func TestListGroupCollectionQueryDoesNotSearchPersistedModelIDOrAlias(t *testing.T) {
	fixture := newServiceFixture(t)
	group := createGroupCollectionGroup(t, fixture, "not a model", true, nil)
	group.Models = models.JSON(`[{"id":"private-upstream-model","alias":"public-model-alias"}]`)
	if err := fixture.db.Model(group).Update("models", group.Models).Error; err != nil {
		t.Fatalf("update persisted models: %v", err)
	}
	entry := createGroupCollectionKey(t, fixture, group.ID, models.UpstreamKeyStatusActive, nil)
	publishGroupCollectionRuntime(t, fixture, []state.KeyEntry{entry})

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

func TestGroupCollectionQueryCombinesFiltersAndSummarizesCompleteCollection(t *testing.T) {
	available := GroupCollectionStatusAvailable
	anthropic := protocol.Anthropic
	records := []groupCollectionRecord{
		groupCollectionQueryRecord(1, "Alpha", GroupCollectionStatusAvailable, "https://alpha.example/v1", []protocol.Protocol{protocol.OpenAICompletions}, 1, 100),
		groupCollectionQueryRecord(2, "Alpha anthropic", GroupCollectionStatusAvailable, "https://alpha.example/v1", []protocol.Protocol{protocol.Anthropic}, 2, 200),
		groupCollectionQueryRecord(3, "Alpha disabled", GroupCollectionStatusDisabled, "https://alpha.example/v1", []protocol.Protocol{protocol.Anthropic}, 3, 300),
		groupCollectionQueryRecord(4, "Beta", GroupCollectionStatusUnavailable, "https://beta.example/v1", []protocol.Protocol{protocol.Anthropic}, 4, 400),
	}

	result := queryGroupCollectionRecords(1_700, records, GroupCollectionQuery{
		Query: "ALPHA", Status: &available, Protocol: &anthropic,
		Sort: GroupCollectionSortName, Page: 1, PageSize: 20,
	})
	if got, want := groupCollectionItemIDs(result.Items), []uint{2}; !reflect.DeepEqual(got, want) {
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
		{sort: GroupCollectionSortKeys, want: []uint{2, 3, 5, 4, 1}},
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

func TestGroupCollectionQueryUsesIDTieBreakAfterEachSortPrimaryAndName(t *testing.T) {
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
			sort: GroupCollectionSortKeys,
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
	return groupCollectionRecord{
		GroupCollectionItem: GroupCollectionItem{
			ID: id, Name: name, Status: status, UpstreamURL: upstreamURL,
			Protocols: protocols, ModelCount: 7,
			KeyCounts: GroupCollectionKeyCounts{Total: keys},
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
