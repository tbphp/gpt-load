package control

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"gpt-load/internal/storage/models"
)

type GroupCollectionSort string

const (
	GroupCollectionSortRecent      GroupCollectionSort = "recent"
	GroupCollectionSortStatus      GroupCollectionSort = "status"
	GroupCollectionSortName        GroupCollectionSort = "name"
	GroupCollectionSortCredentials GroupCollectionSort = "credentials"
	GroupCollectionSortCreated     GroupCollectionSort = "created"
)

type GroupCollectionQuery struct {
	Query          string
	Status         *GroupCollectionStatus
	ConnectionType *models.ConnectionType
	Sort           GroupCollectionSort
	Page           int64
	PageSize       int64
}

type GroupCollectionSummary struct {
	Total       int64 `json:"total"`
	Available   int64 `json:"available"`
	Unavailable int64 `json:"unavailable"`
	Disabled    int64 `json:"disabled"`
}

type GroupCollectionPagination struct {
	Page       int64 `json:"page"`
	PageSize   int64 `json:"page_size"`
	TotalItems int64 `json:"total_items"`
	TotalPages int64 `json:"total_pages"`
}

type GroupCollectionResponse struct {
	ObservedAtMS int64                     `json:"observed_at_ms"`
	Summary      GroupCollectionSummary    `json:"summary"`
	Items        []GroupCollectionItem     `json:"items"`
	Pagination   GroupCollectionPagination `json:"pagination"`
}

func (s *Service) ListGroupCollection(
	ctx context.Context,
	query GroupCollectionQuery,
) (GroupCollectionResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	observedAtMS, records, err := s.captureGroupCollectionRecords(
		ctx,
		groupCollectionSortUsesActivity(query.Sort),
	)
	if parentErr := ctx.Err(); parentErr != nil {
		return GroupCollectionResponse{}, parentErr
	}
	if err != nil {
		return GroupCollectionResponse{}, err
	}
	return queryGroupCollectionRecords(observedAtMS, records, query), nil
}

func groupCollectionSortUsesActivity(sortBy GroupCollectionSort) bool {
	switch sortBy {
	case GroupCollectionSortStatus,
		GroupCollectionSortName,
		GroupCollectionSortCredentials,
		GroupCollectionSortCreated:
		return false
	default:
		return true
	}
}

func queryGroupCollectionRecords(
	observedAtMS int64,
	records []groupCollectionRecord,
	query GroupCollectionQuery,
) GroupCollectionResponse {
	summary := summarizeGroupCollectionRecords(records)
	filtered := make([]groupCollectionRecord, 0, len(records))
	for _, record := range records {
		if !matchesGroupCollectionQuery(record, query) {
			continue
		}
		filtered = append(filtered, record)
	}
	sortGroupCollectionRecords(filtered, query.Sort)

	totalItems := int64(len(filtered))
	pagination := GroupCollectionPagination{
		Page: query.Page, PageSize: query.PageSize, TotalItems: totalItems,
		TotalPages: groupCollectionTotalPages(totalItems, query.PageSize),
	}
	items := groupCollectionPageItems(filtered, query.Page, query.PageSize)
	return GroupCollectionResponse{
		ObservedAtMS: observedAtMS,
		Summary:      summary,
		Items:        items,
		Pagination:   pagination,
	}
}

func summarizeGroupCollectionRecords(
	records []groupCollectionRecord,
) GroupCollectionSummary {
	summary := GroupCollectionSummary{Total: int64(len(records))}
	for _, record := range records {
		switch record.Status {
		case GroupCollectionStatusAvailable:
			summary.Available++
		case GroupCollectionStatusUnavailable:
			summary.Unavailable++
		case GroupCollectionStatusDisabled:
			summary.Disabled++
		}
	}
	return summary
}

func matchesGroupCollectionQuery(
	record groupCollectionRecord,
	query GroupCollectionQuery,
) bool {
	if query.ConnectionType != nil && record.ConnectionType != *query.ConnectionType {
		return false
	}
	if query.Status != nil && record.Status != *query.Status {
		return false
	}
	if query.Query == "" {
		return true
	}
	if groupCollectionContainsFold(record.Name, query.Query) ||
		groupCollectionContainsFold(string(record.ChannelID), query.Query) ||
		groupCollectionContainsFold(string(record.Params), query.Query) {
		return true
	}
	return false
}

func groupCollectionContainsFold(value, query string) bool {
	return strings.Contains(groupCollectionFold(value), groupCollectionFold(query))
}

func groupCollectionFold(value string) string {
	var folded strings.Builder
	folded.Grow(len(value))
	for _, runeValue := range value {
		folded.WriteRune(groupCollectionFoldRune(runeValue))
	}
	return folded.String()
}

func groupCollectionFoldRune(value rune) rune {
	folded := value
	for candidate := unicode.SimpleFold(value); candidate != value; candidate = unicode.SimpleFold(candidate) {
		if candidate < folded {
			folded = candidate
		}
	}
	return folded
}

func sortGroupCollectionRecords(
	records []groupCollectionRecord,
	sortBy GroupCollectionSort,
) {
	sort.Slice(records, func(leftIndex, rightIndex int) bool {
		left, right := records[leftIndex], records[rightIndex]
		switch sortBy {
		case GroupCollectionSortRecent:
			return compareGroupCollectionRecentActivity(left, right)
		case GroupCollectionSortName:
			return compareGroupCollectionNames(left, right)
		case GroupCollectionSortCredentials:
			leftTotal, rightTotal := groupCollectionCredentialTotal(left), groupCollectionCredentialTotal(right)
			if leftTotal != rightTotal {
				return leftTotal > rightTotal
			}
			return compareGroupCollectionNames(left, right)
		case GroupCollectionSortCreated:
			if left.CreatedAtMS != right.CreatedAtMS {
				return left.CreatedAtMS > right.CreatedAtMS
			}
			return left.ID > right.ID
		case GroupCollectionSortStatus:
			if leftStatus, rightStatus := groupCollectionStatusOrder(left.Status), groupCollectionStatusOrder(right.Status); leftStatus != rightStatus {
				return leftStatus < rightStatus
			}
			return compareGroupCollectionNames(left, right)
		default:
			return compareGroupCollectionRecentActivity(left, right)
		}
	})
}

func compareGroupCollectionRecentActivity(left, right groupCollectionRecord) bool {
	if left.LastActiveAtMS != nil && right.LastActiveAtMS != nil {
		if *left.LastActiveAtMS != *right.LastActiveAtMS {
			return *left.LastActiveAtMS > *right.LastActiveAtMS
		}
		if left.LastActiveHourRequestCount != right.LastActiveHourRequestCount {
			return left.LastActiveHourRequestCount > right.LastActiveHourRequestCount
		}
	} else if left.LastActiveAtMS != nil {
		return true
	} else if right.LastActiveAtMS != nil {
		return false
	}
	return compareGroupCollectionNames(left, right)
}

func groupCollectionCredentialTotal(record groupCollectionRecord) int64 {
	return record.CredentialCounts.Total
}

func groupCollectionStatusOrder(value GroupCollectionStatus) int {
	switch value {
	case GroupCollectionStatusUnavailable:
		return 0
	case GroupCollectionStatusAvailable:
		return 1
	case GroupCollectionStatusDisabled:
		return 2
	default:
		return 3
	}
}

func compareGroupCollectionNames(left, right groupCollectionRecord) bool {
	leftFolded, rightFolded := groupCollectionFold(left.Name), groupCollectionFold(right.Name)
	if leftFolded != rightFolded {
		return leftFolded < rightFolded
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.ID < right.ID
}

func groupCollectionTotalPages(totalItems, pageSize int64) int64 {
	if totalItems == 0 || pageSize <= 0 {
		return 0
	}
	pages := totalItems / pageSize
	if totalItems%pageSize != 0 {
		pages++
	}
	return pages
}

func groupCollectionPageItems(
	records []groupCollectionRecord,
	page, pageSize int64,
) []GroupCollectionItem {
	if page <= 0 || pageSize <= 0 {
		return []GroupCollectionItem{}
	}
	itemCount := int64(len(records))
	if page-1 > (itemCount / pageSize) {
		return []GroupCollectionItem{}
	}
	offset := (page - 1) * pageSize
	if offset >= itemCount {
		return []GroupCollectionItem{}
	}
	end := offset + pageSize
	if end < offset || end > itemCount {
		end = itemCount
	}
	items := make([]GroupCollectionItem, end-offset)
	for index, record := range records[offset:end] {
		items[index] = cloneGroupCollectionItem(record.GroupCollectionItem)
	}
	return items
}

func cloneGroupCollectionItem(value GroupCollectionItem) GroupCollectionItem {
	cloned := value
	cloned.Params = append([]byte(nil), value.Params...)
	return cloned
}
