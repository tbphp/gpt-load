package control

import (
	"sort"
	"strings"
	"unicode"

	"gpt-load/internal/state"
)

const (
	defaultAccessKeyCollectionPage     int64 = 1
	defaultAccessKeyCollectionPageSize int64 = 20
)

type AccessKeyCollectionQuery struct {
	Query    string
	Status   *state.AccessKeyStatus
	Page     int64
	PageSize int64
}

func queryAccessKeyCollectionRecords(
	records []accessKeyCollectionRecord,
	query AccessKeyCollectionQuery,
) AccessKeyCollectionResponse {
	summary := summarizeAccessKeyCollectionRecords(records)
	filtered := make([]accessKeyCollectionRecord, 0, len(records))
	for _, record := range records {
		if matchesAccessKeyCollectionQuery(record, query) {
			filtered = append(filtered, record)
		}
	}
	sortAccessKeyCollectionRecords(filtered)

	totalItems := int64(len(filtered))
	return AccessKeyCollectionResponse{
		Summary: summary,
		Items:   accessKeyCollectionPageItems(filtered, query.Page, query.PageSize),
		Pagination: AccessKeyCollectionPagination{
			Page:       query.Page,
			PageSize:   query.PageSize,
			TotalItems: totalItems,
			TotalPages: accessKeyCollectionTotalPages(totalItems, query.PageSize),
		},
	}
}

func normalizeAccessKeyCollectionQuery(query AccessKeyCollectionQuery) AccessKeyCollectionQuery {
	if query.Page <= 0 {
		query.Page = defaultAccessKeyCollectionPage
	}
	if query.PageSize <= 0 {
		query.PageSize = defaultAccessKeyCollectionPageSize
	}
	return query
}

func summarizeAccessKeyCollectionRecords(
	records []accessKeyCollectionRecord,
) AccessKeyCollectionSummary {
	summary := AccessKeyCollectionSummary{Total: int64(len(records))}
	for _, record := range records {
		switch record.Status {
		case state.AccessKeyStatusActive:
			summary.Active++
		case state.AccessKeyStatusDisabled:
			summary.Disabled++
		}
	}
	return summary
}

func matchesAccessKeyCollectionQuery(
	record accessKeyCollectionRecord,
	query AccessKeyCollectionQuery,
) bool {
	if query.Status != nil && record.Status != *query.Status {
		return false
	}
	return query.Query == "" ||
		accessKeyCollectionContainsFold(record.Name, query.Query) ||
		accessKeyCollectionContainsFold(record.MaskedKey, query.Query)
}

func sortAccessKeyCollectionRecords(records []accessKeyCollectionRecord) {
	sort.Slice(records, func(leftIndex, rightIndex int) bool {
		left, right := records[leftIndex], records[rightIndex]
		if left.UpdatedAtMS != right.UpdatedAtMS {
			return left.UpdatedAtMS > right.UpdatedAtMS
		}
		return left.ID > right.ID
	})
}

func accessKeyCollectionTotalPages(totalItems, pageSize int64) int64 {
	if totalItems == 0 || pageSize <= 0 {
		return 0
	}
	pages := totalItems / pageSize
	if totalItems%pageSize != 0 {
		pages++
	}
	return pages
}

func accessKeyCollectionPageItems(
	records []accessKeyCollectionRecord,
	page, pageSize int64,
) []AccessKeyCollectionItem {
	if page <= 0 || pageSize <= 0 {
		return []AccessKeyCollectionItem{}
	}
	itemCount := int64(len(records))
	if page-1 > itemCount/pageSize {
		return []AccessKeyCollectionItem{}
	}
	offset := (page - 1) * pageSize
	if offset >= itemCount {
		return []AccessKeyCollectionItem{}
	}
	end := offset + pageSize
	if end < offset || end > itemCount {
		end = itemCount
	}
	items := make([]AccessKeyCollectionItem, end-offset)
	copy(items, accessKeyCollectionItems(records[offset:end]))
	return items
}

func accessKeyCollectionItems(records []accessKeyCollectionRecord) []AccessKeyCollectionItem {
	items := make([]AccessKeyCollectionItem, len(records))
	for index := range records {
		items[index] = records[index].AccessKeyCollectionItem
	}
	return items
}

func accessKeyCollectionContainsFold(value, query string) bool {
	return strings.Contains(accessKeyCollectionFold(value), accessKeyCollectionFold(query))
}

func accessKeyCollectionFold(value string) string {
	var folded strings.Builder
	folded.Grow(len(value))
	for _, runeValue := range value {
		folded.WriteRune(accessKeyCollectionFoldRune(runeValue))
	}
	return folded.String()
}

func accessKeyCollectionFoldRune(value rune) rune {
	folded := value
	for candidate := unicode.SimpleFold(value); candidate != value; candidate = unicode.SimpleFold(candidate) {
		if candidate < folded {
			folded = candidate
		}
	}
	return folded
}
