package control

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func TestQueryAccessKeyCollectionRecordsSummarizesBeforeFiltering(t *testing.T) {
	active := state.AccessKeyStatusActive
	records := []accessKeyCollectionRecord{
		accessKeyCollectionQueryRecord(1, "Alpha", "0001", active, 100),
		accessKeyCollectionQueryRecord(2, "Bravo", "0002", state.AccessKeyStatusDisabled, 200),
		accessKeyCollectionQueryRecord(3, "Charlie", "0003", active, 300),
	}

	result := queryAccessKeyCollectionRecords(records, AccessKeyCollectionQuery{
		Query: "bravo", Status: &active, Page: 1, PageSize: 20,
	})
	if got, want := result.Summary, (AccessKeyCollectionSummary{
		Total: 3, Active: 2, Disabled: 1,
	}); got != want {
		t.Fatalf("summary = %#v, want %#v", got, want)
	}
	if got, want := accessKeyCollectionItemIDs(result.Items), []uint{}; !reflect.DeepEqual(got, want) {
		t.Fatalf("item IDs = %#v, want %#v", got, want)
	}
}

func TestQueryAccessKeyCollectionRecordsFiltersCaseFoldedNameAndMaskedSuffix(t *testing.T) {
	records := []accessKeyCollectionRecord{
		accessKeyCollectionQueryRecord(1, "München", "cafe", state.AccessKeyStatusActive, 100),
		accessKeyCollectionQueryRecord(2, "Other", "beef", state.AccessKeyStatusActive, 200),
	}

	tests := []struct {
		name  string
		query string
		want  []uint
	}{
		{name: "case-folded name", query: "MÜN", want: []uint{1}},
		{name: "masked suffix", query: "****BEEF", want: []uint{2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := queryAccessKeyCollectionRecords(records, AccessKeyCollectionQuery{
				Query: test.query, Page: 1, PageSize: 20,
			})
			if got := accessKeyCollectionItemIDs(result.Items); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("item IDs = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestQueryAccessKeyCollectionRecordsFiltersStatus(t *testing.T) {
	active := state.AccessKeyStatusActive
	disabled := state.AccessKeyStatusDisabled
	records := []accessKeyCollectionRecord{
		accessKeyCollectionQueryRecord(1, "active one", "0001", active, 100),
		accessKeyCollectionQueryRecord(2, "active two", "0002", active, 200),
		accessKeyCollectionQueryRecord(3, "disabled one", "0003", disabled, 300),
		accessKeyCollectionQueryRecord(4, "disabled two", "0004", disabled, 400),
	}

	tests := []struct {
		name  string
		query AccessKeyCollectionQuery
		want  []uint
	}{
		{name: "active", query: AccessKeyCollectionQuery{Status: &active, Page: 1, PageSize: 20}, want: []uint{2, 1}},
		{name: "disabled", query: AccessKeyCollectionQuery{Status: &disabled, Page: 1, PageSize: 20}, want: []uint{4, 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := queryAccessKeyCollectionRecords(records, test.query)
			if got := accessKeyCollectionItemIDs(result.Items); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("item IDs = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestQueryAccessKeyCollectionRecordsSortsByUpdatedAtThenIDDescending(t *testing.T) {
	records := []accessKeyCollectionRecord{
		accessKeyCollectionQueryRecord(1, "one", "0001", state.AccessKeyStatusActive, 100),
		accessKeyCollectionQueryRecord(4, "four", "0004", state.AccessKeyStatusDisabled, 300),
		accessKeyCollectionQueryRecord(3, "three", "0003", state.AccessKeyStatusActive, 300),
		accessKeyCollectionQueryRecord(2, "two", "0002", state.AccessKeyStatusDisabled, 200),
	}

	result := queryAccessKeyCollectionRecords(records, AccessKeyCollectionQuery{Page: 1, PageSize: 20})
	if got, want := accessKeyCollectionItemIDs(result.Items), []uint{4, 3, 2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("item IDs = %#v, want %#v", got, want)
	}
}

func TestQueryAccessKeyCollectionRecordsPaginatesAndHandlesZeroRecords(t *testing.T) {
	records := []accessKeyCollectionRecord{
		accessKeyCollectionQueryRecord(1, "one", "0001", state.AccessKeyStatusActive, 100),
		accessKeyCollectionQueryRecord(2, "two", "0002", state.AccessKeyStatusActive, 200),
		accessKeyCollectionQueryRecord(3, "three", "0003", state.AccessKeyStatusActive, 300),
	}

	tests := []struct {
		name     string
		records  []accessKeyCollectionRecord
		query    AccessKeyCollectionQuery
		wantIDs  []uint
		wantPage AccessKeyCollectionPagination
	}{
		{
			name: "page one", records: records,
			query: AccessKeyCollectionQuery{Page: 1, PageSize: 2}, wantIDs: []uint{3, 2},
			wantPage: AccessKeyCollectionPagination{Page: 1, PageSize: 2, TotalItems: 3, TotalPages: 2},
		},
		{
			name: "page beyond end", records: records,
			query: AccessKeyCollectionQuery{Page: 3, PageSize: 2}, wantIDs: []uint{},
			wantPage: AccessKeyCollectionPagination{Page: 3, PageSize: 2, TotalItems: 3, TotalPages: 2},
		},
		{
			name: "zero records", records: nil,
			query: AccessKeyCollectionQuery{Page: 1, PageSize: 20}, wantIDs: []uint{},
			wantPage: AccessKeyCollectionPagination{Page: 1, PageSize: 20, TotalItems: 0, TotalPages: 0},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := queryAccessKeyCollectionRecords(test.records, test.query)
			if got := accessKeyCollectionItemIDs(result.Items); !reflect.DeepEqual(got, test.wantIDs) {
				t.Fatalf("item IDs = %#v, want %#v", got, test.wantIDs)
			}
			if got := result.Pagination; got != test.wantPage {
				t.Fatalf("pagination = %#v, want %#v", got, test.wantPage)
			}
		})
	}
}

func TestListAccessKeyCollectionReadsMappedMetadataWithoutDecrypting(t *testing.T) {
	fixture := newServiceFixture(t)
	fixture.service.random = bytes.NewReader(make([]byte, 16))
	created, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{Name: "collection"})
	if err != nil {
		t.Fatalf("CreateAccessKey() error = %v", err)
	}
	if err := fixture.db.Model(&models.AccessKey{}).Where("id = ?", created.ID).UpdateColumn("key_value", "corrupt").Error; err != nil {
		t.Fatalf("corrupt ciphertext: %v", err)
	}

	result, err := fixture.service.ListAccessKeyCollection(context.Background(), AccessKeyCollectionQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("ListAccessKeyCollection() error = %v", err)
	}
	if got, want := result.Items, []AccessKeyCollectionItem{{
		AccessKeyMetadata: created.AccessKeyMetadata,
	}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("items = %#v, want %#v", got, want)
	}
}

func TestListAccessKeyCollectionRejectsCanceledContextAndInvalidMappedMetadata(t *testing.T) {
	fixture := newServiceFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.service.ListAccessKeyCollection(ctx, AccessKeyCollectionQuery{Page: 1, PageSize: 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListAccessKeyCollection() canceled error = %v, want context.Canceled", err)
	}

	row := models.AccessKey{
		Name: "invalid", KeyValue: "ciphertext", KeyHash: "hash", KeySuffix: "ZZZZ",
		Status: string(state.AccessKeyStatusActive), Filters: models.JSON(`{}`),
	}
	if err := fixture.db.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
		t.Fatalf("disable check constraints: %v", err)
	}
	if err := fixture.db.Create(&row).Error; err != nil {
		t.Fatalf("create invalid access key: %v", err)
	}
	if err := fixture.db.Exec("PRAGMA ignore_check_constraints = OFF").Error; err != nil {
		t.Fatalf("restore check constraints: %v", err)
	}
	if _, err := fixture.service.ListAccessKeyCollection(t.Context(), AccessKeyCollectionQuery{Page: 1, PageSize: 20}); err == nil {
		t.Fatal("ListAccessKeyCollection() error = nil for invalid mapped metadata")
	}
}

func accessKeyCollectionQueryRecord(id uint, name, suffix string, status state.AccessKeyStatus, updatedAtMS int64) accessKeyCollectionRecord {
	return accessKeyCollectionRecord{AccessKeyCollectionItem: AccessKeyCollectionItem{
		AccessKeyMetadata: AccessKeyMetadata{
			ID: id, Name: name, MaskedKey: maskedAccessKey(suffix), Status: status, UpdatedAtMS: updatedAtMS,
		},
	}}
}

func accessKeyCollectionItemIDs(items []AccessKeyCollectionItem) []uint {
	result := make([]uint, len(items))
	for index := range items {
		result[index] = items[index].ID
	}
	return result
}
