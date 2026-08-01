package control

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
)

const (
	groupKeyCollectionDefaultPage     = 1
	groupKeyCollectionDefaultPageSize = 50
	groupKeyCollectionStatsWindow     = int64(health.StatsWindow / time.Second)
)

type GroupKeyCollectionQuery struct {
	Query    string
	Status   *string
	Page     int
	PageSize int
}

type GroupKeyCollectionResponse struct {
	ObservedAtMS       int64                      `json:"observed_at_ms"`
	StatsWindowSeconds int64                      `json:"stats_window_seconds"`
	Summary            GroupKeySummaryResponse    `json:"summary"`
	Items              []GroupKeyItemResponse     `json:"items"`
	Pagination         GroupKeyPaginationResponse `json:"pagination"`
}

type GroupKeySummaryResponse struct {
	Total       int `json:"total"`
	Available   int `json:"available"`
	Cooldown    int `json:"cooldown"`
	Blacklisted int `json:"blacklisted"`
	Disabled    int `json:"disabled"`
}

type GroupKeyItemResponse struct {
	ID                      uint                     `json:"id"`
	Mask                    string                   `json:"mask"`
	ConfiguredStatus        string                   `json:"configured_status"`
	EffectiveStatus         string                   `json:"effective_status"`
	WeightMode              string                   `json:"weight_mode"`
	Weight                  *int                     `json:"weight"`
	RecentSuccessCount      uint64                   `json:"recent_success_count"`
	RecentFailureCount      uint64                   `json:"recent_failure_count"`
	ConsecutiveFailureCount uint64                   `json:"consecutive_failure_count"`
	LastFailureCategory     string                   `json:"last_failure_category"`
	LastStatusCode          *int                     `json:"last_status_code"`
	CooldownUntilMS         *int64                   `json:"cooldown_until_ms"`
	Recovery                GroupKeyRecoveryResponse `json:"recovery"`
}

type GroupKeyRecoveryResponse struct {
	Mode      string `json:"mode"`
	Automatic bool   `json:"automatic"`
	AtMS      *int64 `json:"at_ms"`
}

type GroupKeyPaginationResponse struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

type groupKeyCollectionRecord struct {
	item   GroupKeyItemResponse
	bucket healthBucket
}

func parseGroupKeyCollectionQuery(rawQuery string) (GroupKeyCollectionQuery, *app_errors.APIError) {
	query := GroupKeyCollectionQuery{
		Page: groupKeyCollectionDefaultPage, PageSize: groupKeyCollectionDefaultPageSize,
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return GroupKeyCollectionQuery{}, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		switch key {
		case "q", "status", "page", "page_size":
		default:
			return GroupKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
		if len(entries) != 1 {
			return GroupKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["q"]; exists {
		query.Query = strings.TrimSpace(entries[0])
	}
	if entries, exists := values["status"]; exists {
		status := entries[0]
		switch status {
		case string(healthBucketAvailable), string(healthBucketCooldown), string(healthBucketBlacklisted), string(healthBucketDisabled):
			query.Status = &status
		default:
			return GroupKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["page"]; exists {
		page, ok := parseGroupKeyCollectionPositiveInt(entries[0])
		if !ok {
			return GroupKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.Page = page
	}
	if entries, exists := values["page_size"]; exists {
		pageSize, ok := parseGroupKeyCollectionPositiveInt(entries[0])
		if !ok || (pageSize != 20 && pageSize != 50 && pageSize != 100) {
			return GroupKeyCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.PageSize = pageSize
	}
	return query, nil
}

func parseGroupKeyCollectionPositiveInt(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, false
	}
	return parsed, true
}

func (s *Service) ListGroupKeys(
	ctx context.Context,
	groupID uint,
	query GroupKeyCollectionQuery,
) (GroupKeyCollectionResponse, error) {
	if groupID == 0 {
		return GroupKeyCollectionResponse{}, app_errors.ErrBadRequest
	}
	if query.Page < 1 || (query.PageSize != 20 && query.PageSize != 50 && query.PageSize != 100) {
		return GroupKeyCollectionResponse{}, app_errors.ErrBadRequest
	}
	capture, err := s.captureGroupKeys(ctx, groupID)
	if err != nil {
		return GroupKeyCollectionResponse{}, err
	}
	observation, err := validateGroupKeysCapture(capture)
	if err != nil {
		return GroupKeyCollectionResponse{}, err
	}
	return s.mapGroupKeyCollection(observation, query)
}

func (s *Service) mapGroupKeyCollection(
	observation groupKeysObservation,
	query GroupKeyCollectionQuery,
) (GroupKeyCollectionResponse, error) {
	if s == nil || s.encryption == nil || s.stats == nil {
		return GroupKeyCollectionResponse{}, fmt.Errorf("group key collection dependencies unavailable: %w", app_errors.ErrInternalServer)
	}
	observedAtMS, err := safeEpochMilliseconds(observation.observedAt)
	if err != nil {
		return GroupKeyCollectionResponse{}, fmt.Errorf("map group key collection observed_at_ms: %w", err)
	}
	group := state.GroupCatalogView{
		ID:           observation.group.ID,
		Name:         observation.group.Name,
		Enabled:      observation.group.Enabled,
		WeightManual: cloneInt(observation.group.WeightManual),
	}
	records := make([]groupKeyCollectionRecord, 0, len(observation.rows))
	for _, row := range observation.rows {
		view := observation.runtime[row.ID]
		plaintext, err := s.encryption.Decrypt(row.KeyValue)
		if err != nil {
			return GroupKeyCollectionResponse{}, fmt.Errorf("decrypt group key %d: %w", row.ID, app_errors.ErrInternalServer)
		}
		mask, err := maskGroupKeyCollection(plaintext)
		if err != nil {
			return GroupKeyCollectionResponse{}, fmt.Errorf("mask group key %d: %w", row.ID, err)
		}
		bucket := classifyHealthKey(group, view, observation.observedAt)
		item, err := mapGroupKeyCollectionItem(mask, row.ID, view, bucket, s.stats.Snapshot(row.ID, observation.observedAt), observation.observedAt)
		if err != nil {
			return GroupKeyCollectionResponse{}, err
		}
		records = append(records, groupKeyCollectionRecord{item: item, bucket: bucket})
	}

	summary := summarizeGroupKeyCollection(records)
	filtered := make([]groupKeyCollectionRecord, 0, len(records))
	for _, record := range records {
		if groupKeyCollectionMatches(record, query) {
			filtered = append(filtered, record)
		}
	}
	sort.Slice(filtered, func(leftIndex, rightIndex int) bool {
		left, right := filtered[leftIndex], filtered[rightIndex]
		if groupKeyCollectionBucketOrder(left.bucket) != groupKeyCollectionBucketOrder(right.bucket) {
			return groupKeyCollectionBucketOrder(left.bucket) < groupKeyCollectionBucketOrder(right.bucket)
		}
		return left.item.ID < right.item.ID
	})

	totalItems := len(filtered)
	pagination := GroupKeyPaginationResponse{
		Page: query.Page, PageSize: query.PageSize, TotalItems: totalItems,
		TotalPages: groupKeyCollectionTotalPages(totalItems, query.PageSize),
	}
	items := groupKeyCollectionPage(filtered, query.Page, query.PageSize)
	return GroupKeyCollectionResponse{
		ObservedAtMS: observedAtMS, StatsWindowSeconds: groupKeyCollectionStatsWindow,
		Summary: summary, Items: items, Pagination: pagination,
	}, nil
}

func maskGroupKeyCollection(plaintext string) (string, error) {
	if len(plaintext) < 4 {
		return "", fmt.Errorf("credential shorter than safe suffix: %w", app_errors.ErrInternalServer)
	}
	return "sk-gl-****" + plaintext[len(plaintext)-4:], nil
}

func mapGroupKeyCollectionItem(
	mask string,
	keyID uint,
	view state.KeyRuntimeView,
	bucket healthBucket,
	stats health.KeyStats,
	observedAt time.Time,
) (GroupKeyItemResponse, error) {
	weightMode := "auto"
	if view.WeightManual != nil {
		weightMode = "manual"
	}
	item := GroupKeyItemResponse{
		ID: keyID, Mask: mask, ConfiguredStatus: string(view.Status), EffectiveStatus: string(bucket),
		WeightMode: weightMode, RecentSuccessCount: stats.Success, RecentFailureCount: stats.Failure,
		ConsecutiveFailureCount: stats.ConsecutiveFailure,
		LastFailureCategory:     normalizeGroupKeyFailureCategory(stats.LastFailureCategory).String(),
		LastStatusCode:          optionalHealthStatusCode(stats.LastStatusCode),
	}
	switch bucket {
	case healthBucketAvailable:
		weight := view.WeightAuto
		if view.WeightManual != nil {
			weight = *view.WeightManual
		}
		if weight < 1 || weight > state.MaxWeight {
			return GroupKeyItemResponse{}, fmt.Errorf("map group key %d weight: %w", keyID, app_errors.ErrInternalServer)
		}
		item.Weight = &weight
		item.Recovery = GroupKeyRecoveryResponse{Mode: "none"}
	case healthBucketCooldown:
		cooldownUntilMS, err := optionalSafeEpochMilliseconds(view.CooldownUntil)
		if err != nil {
			return GroupKeyItemResponse{}, fmt.Errorf("map group key %d cooldown_until_ms: %w", keyID, err)
		}
		if cooldownUntilMS == nil || !view.CooldownUntil.After(observedAt) {
			return GroupKeyItemResponse{}, fmt.Errorf("map group key %d invalid cooldown: %w", keyID, app_errors.ErrInternalServer)
		}
		item.CooldownUntilMS = cooldownUntilMS
		item.Recovery = GroupKeyRecoveryResponse{Mode: "cooldown", Automatic: true, AtMS: cooldownUntilMS}
	case healthBucketBlacklisted:
		item.Recovery = GroupKeyRecoveryResponse{Mode: "probe", Automatic: true}
	case healthBucketDisabled:
		item.Recovery = GroupKeyRecoveryResponse{Mode: "manual"}
	default:
		return GroupKeyItemResponse{}, fmt.Errorf("map group key %d unknown status: %w", keyID, app_errors.ErrInternalServer)
	}
	return item, nil
}

func normalizeGroupKeyFailureCategory(category health.FailureCategory) health.FailureCategory {
	if category == health.FailureCategoryOK {
		return health.FailureCategoryAmbiguous
	}
	return category
}

func summarizeGroupKeyCollection(records []groupKeyCollectionRecord) GroupKeySummaryResponse {
	summary := GroupKeySummaryResponse{Total: len(records)}
	for _, record := range records {
		switch record.bucket {
		case healthBucketAvailable:
			summary.Available++
		case healthBucketCooldown:
			summary.Cooldown++
		case healthBucketBlacklisted:
			summary.Blacklisted++
		case healthBucketDisabled:
			summary.Disabled++
		}
	}
	return summary
}

func groupKeyCollectionMatches(record groupKeyCollectionRecord, query GroupKeyCollectionQuery) bool {
	if query.Status != nil && record.item.EffectiveStatus != *query.Status {
		return false
	}
	return query.Query == "" || strings.Contains(strings.ToLower(record.item.Mask), strings.ToLower(query.Query))
}

func groupKeyCollectionBucketOrder(bucket healthBucket) int {
	switch bucket {
	case healthBucketBlacklisted:
		return 0
	case healthBucketCooldown:
		return 1
	case healthBucketAvailable:
		return 2
	case healthBucketDisabled:
		return 3
	default:
		return 4
	}
}

func groupKeyCollectionTotalPages(totalItems, pageSize int) int {
	if totalItems == 0 {
		return 0
	}
	return (totalItems + pageSize - 1) / pageSize
}

func groupKeyCollectionPage(records []groupKeyCollectionRecord, page, pageSize int) []GroupKeyItemResponse {
	offset := (page - 1) * pageSize
	if offset < 0 || offset >= len(records) {
		return []GroupKeyItemResponse{}
	}
	end := offset + pageSize
	if end < offset || end > len(records) {
		end = len(records)
	}
	items := make([]GroupKeyItemResponse, end-offset)
	for index, record := range records[offset:end] {
		items[index] = record.item
	}
	return items
}
