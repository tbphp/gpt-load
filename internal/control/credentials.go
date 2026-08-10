package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type CredentialImportRequest struct {
	Credentials string `json:"credentials"`
}

type CredentialImportResult struct {
	GroupID               uint `json:"group_id"`
	CredentialsAdded      int  `json:"credentials_added"`
	CredentialsDuplicated int  `json:"credentials_duplicated"`
}

type CredentialUpdateRequest struct {
	Status       optionalField[state.CredentialStatus] `json:"status"`
	WeightManual optionalField[int]                    `json:"weight_manual"`
}

type CredentialRevealResult struct {
	CredentialID uint            `json:"credential_id"`
	Credential   json.RawMessage `json:"credential"`
	RevealedAtMS int64           `json:"revealed_at_ms"`
}

type CredentialCollectionQuery struct {
	Query    string
	Status   *string
	Page     int
	PageSize int
}

type CredentialCollectionResponse struct {
	ObservedAtMS       int64                        `json:"observed_at_ms"`
	StatsWindowSeconds int64                        `json:"stats_window_seconds"`
	Summary            CredentialSummaryResponse    `json:"summary"`
	Items              []CredentialItemResponse     `json:"items"`
	Pagination         CredentialPaginationResponse `json:"pagination"`
}

type CredentialSummaryResponse struct {
	Total       int `json:"total"`
	Available   int `json:"available"`
	Cooldown    int `json:"cooldown"`
	Blacklisted int `json:"blacklisted"`
	Disabled    int `json:"disabled"`
}

type CredentialItemResponse struct {
	CredentialID            uint                       `json:"credential_id"`
	Mask                    string                     `json:"mask"`
	ConfiguredStatus        string                     `json:"configured_status"`
	EffectiveStatus         string                     `json:"effective_status"`
	WeightMode              string                     `json:"weight_mode"`
	Weight                  *int                       `json:"weight"`
	RecentSuccessCount      uint64                     `json:"recent_success_count"`
	RecentFailureCount      uint64                     `json:"recent_failure_count"`
	ConsecutiveFailureCount uint64                     `json:"consecutive_failure_count"`
	LastFailureCategory     string                     `json:"last_failure_category"`
	LastStatusCode          *int                       `json:"last_status_code"`
	CooldownUntilMS         *int64                     `json:"cooldown_until_ms"`
	Recovery                CredentialRecoveryResponse `json:"recovery"`
}

type CredentialRecoveryResponse struct {
	Mode      string `json:"mode"`
	Automatic bool   `json:"automatic"`
	AtMS      *int64 `json:"at_ms"`
}

type CredentialPaginationResponse struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalItems int `json:"total_items"`
	TotalPages int `json:"total_pages"`
}

type CredentialBatchAction string

const (
	CredentialBatchEnable  CredentialBatchAction = "enable"
	CredentialBatchDisable CredentialBatchAction = "disable"
	CredentialBatchDelete  CredentialBatchAction = "delete"
)

type CredentialBatchRequest struct {
	Action        CredentialBatchAction `json:"action"`
	CredentialIDs []uint                `json:"credential_ids"`
}

type CredentialBatchResponse struct {
	AffectedCredentialIDs []uint                    `json:"affected_credential_ids"`
	Summary               CredentialSummaryResponse `json:"summary"`
}

type credentialCapture struct {
	group      models.Group
	rows       []models.Credential
	views      []state.CredentialRuntimeView
	observedAt time.Time
}

type credentialObservation struct {
	group      models.Group
	rows       []models.Credential
	runtime    map[uint]state.CredentialRuntimeView
	observedAt time.Time
}

type credentialCollectionRecord struct {
	item   CredentialItemResponse
	bucket healthBucket
}

func (s *Service) ImportGroupCredentials(
	ctx context.Context,
	groupID uint,
	request CredentialImportRequest,
) (CredentialImportResult, error) {
	if groupID == 0 {
		return CredentialImportResult{}, app_errors.ErrValidation
	}
	result := CredentialImportResult{GroupID: groupID}
	var entries []state.CredentialEntry
	err := s.writeCredentialConfig(ctx, groupID, 0, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if group.ChannelID == "" {
			return app_errors.ErrValidation
		}
		normalized, err := s.normalizeCredentials(channel.ID(group.ChannelID), request.Credentials)
		if err != nil {
			return err
		}
		result.CredentialsAdded, result.CredentialsDuplicated, err =
			s.persistCredentials(tx, groupID, normalized)
		if err != nil {
			return err
		}
		entries, err = stateloader.BuildGroupCredentialEntries(ctx, tx, groupID)
		if err != nil {
			return err
		}
		return state.ValidateCredentialEntries(entries)
	}, func() error {
		_, err := s.reconcileRegistryGroup(groupID, entries)
		return err
	})
	if err != nil {
		return CredentialImportResult{}, err
	}
	return result, nil
}

func (s *Service) ListGroupCredentials(
	ctx context.Context,
	groupID uint,
	query CredentialCollectionQuery,
) (CredentialCollectionResponse, error) {
	if groupID == 0 {
		return CredentialCollectionResponse{}, app_errors.ErrBadRequest
	}
	if query.Page < 1 || (query.PageSize != 20 && query.PageSize != 50 && query.PageSize != 100) {
		return CredentialCollectionResponse{}, app_errors.ErrBadRequest
	}
	capture, err := s.captureCredentials(ctx, groupID)
	if err != nil {
		return CredentialCollectionResponse{}, err
	}
	observation, err := validateCredentialCapture(capture)
	if err != nil {
		return CredentialCollectionResponse{}, err
	}
	return s.mapCredentialCollection(observation, query)
}

func (s *Service) captureCredentials(ctx context.Context, groupID uint) (credentialCapture, error) {
	s.writeMu.RLock()
	var group models.Group
	groupErr := s.db.WithContext(ctx).Where("id = ?", groupID).Take(&group).Error
	rows := make([]models.Credential, 0)
	var rowsErr error
	if groupErr == nil {
		rowsErr = s.db.WithContext(ctx).Where("group_id = ?", groupID).Order("id ASC").Find(&rows).Error
	}
	var views []state.CredentialRuntimeView
	var observedAt time.Time
	if groupErr == nil && rowsErr == nil {
		views = s.registry.Snapshot()
		observedAt = s.now().UTC()
	}
	s.writeMu.RUnlock()
	if groupErr != nil {
		if errors.Is(groupErr, gorm.ErrRecordNotFound) {
			return credentialCapture{}, groupNotFoundError()
		}
		return credentialCapture{}, app_errors.ParseDBError(groupErr)
	}
	if group.ChannelID == "" {
		return credentialCapture{}, app_errors.ErrValidation
	}
	if rowsErr != nil {
		return credentialCapture{}, app_errors.ParseDBError(rowsErr)
	}
	return credentialCapture{group: group, rows: rows, views: views, observedAt: observedAt}, nil
}

func validateCredentialCapture(capture credentialCapture) (credentialObservation, error) {
	groupID := capture.group.ID
	byID := make(map[uint]state.CredentialRuntimeView, len(capture.views))
	for _, view := range capture.views {
		byID[view.ID] = view
	}
	persisted := make(map[uint]struct{}, len(capture.rows))
	for _, row := range capture.rows {
		view, exists := byID[row.ID]
		if !exists {
			return credentialObservation{}, dbRegistryMismatch(mismatchMissingRegistry, groupID, row.ID)
		}
		if view.GroupID != groupID {
			return credentialObservation{}, dbRegistryMismatch(mismatchGroupID, groupID, row.ID)
		}
		if view.Status != state.CredentialStatus(row.Status) {
			return credentialObservation{}, dbRegistryMismatch(mismatchStatus, groupID, row.ID)
		}
		if !equalOptionalWeight(view.WeightManual, row.WeightManual) {
			return credentialObservation{}, dbRegistryMismatch(mismatchWeightManual, groupID, row.ID)
		}
		if view.Version != groupCollectionCredentialVersion(row.UpdatedAtMS) ||
			view.IdentityGeneration != groupCollectionCredentialIdentity(row.Fingerprint) {
			return credentialObservation{}, dbRegistryMismatch(mismatchIdentity, groupID, row.ID)
		}
		persisted[row.ID] = struct{}{}
	}
	for _, view := range capture.views {
		if view.GroupID == groupID {
			if _, exists := persisted[view.ID]; !exists {
				return credentialObservation{}, dbRegistryMismatch(mismatchExtraRegistry, groupID, view.ID)
			}
		}
	}
	return credentialObservation{group: capture.group, rows: capture.rows, runtime: byID, observedAt: capture.observedAt}, nil
}

func (s *Service) decodeCredential(group models.Group, row models.Credential) (json.RawMessage, string, error) {
	plaintext, err := s.encryption.Decrypt(row.Data)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt credential %d: %w", row.ID, app_errors.ErrInternalServer)
	}
	validated, err := normalizeStoredCredential(s.channelRegistry, channel.ID(group.ChannelID), plaintext)
	if err != nil {
		return nil, "", fmt.Errorf("validate credential %d: %w", row.ID, app_errors.ErrInternalServer)
	}
	apiKey, _ := validated.Value("api_key")
	return validated.CanonicalJSON(), apiKey, nil
}

func (s *Service) mapCredentialCollection(
	observation credentialObservation,
	query CredentialCollectionQuery,
) (CredentialCollectionResponse, error) {
	if s == nil || s.encryption == nil || s.stats == nil || s.channelRegistry == nil {
		return CredentialCollectionResponse{}, fmt.Errorf("credential collection dependencies unavailable: %w", app_errors.ErrInternalServer)
	}
	observedAtMS, err := safeEpochMilliseconds(observation.observedAt)
	if err != nil {
		return CredentialCollectionResponse{}, err
	}
	group := state.GroupCatalogView{ID: observation.group.ID, Name: observation.group.Name,
		Enabled: observation.group.Enabled, WeightManual: cloneInt(observation.group.WeightManual)}
	records := make([]credentialCollectionRecord, 0, len(observation.rows))
	for _, row := range observation.rows {
		canonical, _, err := s.decodeCredential(observation.group, row)
		if err != nil {
			return CredentialCollectionResponse{}, err
		}
		mask, err := maskCanonicalCredential(canonical)
		if err != nil {
			return CredentialCollectionResponse{}, err
		}
		view := observation.runtime[row.ID]
		bucket := classifyHealthKey(group, view, observation.observedAt)
		item, err := mapCredentialRuntimeItem(
			mask, row.ID, view, bucket, s.stats.Snapshot(row.ID, observation.observedAt), observation.observedAt,
		)
		if err != nil {
			return CredentialCollectionResponse{}, err
		}
		records = append(records, credentialCollectionRecord{item: item, bucket: bucket})
	}
	summary := summarizeCredentialCollection(records)
	filtered := make([]credentialCollectionRecord, 0, len(records))
	for _, record := range records {
		if credentialCollectionMatches(record, query) {
			filtered = append(filtered, record)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		left, right := filtered[i], filtered[j]
		if credentialCollectionBucketOrder(left.bucket) != credentialCollectionBucketOrder(right.bucket) {
			return credentialCollectionBucketOrder(left.bucket) < credentialCollectionBucketOrder(right.bucket)
		}
		return left.item.CredentialID < right.item.CredentialID
	})
	total := len(filtered)
	return CredentialCollectionResponse{
		ObservedAtMS: observedAtMS, StatsWindowSeconds: credentialCollectionStatsWindow,
		Summary: summary, Items: credentialCollectionPage(filtered, query.Page, query.PageSize),
		Pagination: CredentialPaginationResponse{Page: query.Page, PageSize: query.PageSize,
			TotalItems: total, TotalPages: credentialCollectionTotalPages(total, query.PageSize)},
	}, nil
}

func summarizeCredentialCollection(records []credentialCollectionRecord) CredentialSummaryResponse {
	summary := CredentialSummaryResponse{Total: len(records)}
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

func credentialCollectionMatches(record credentialCollectionRecord, query CredentialCollectionQuery) bool {
	if query.Status != nil && record.item.EffectiveStatus != *query.Status {
		return false
	}
	return query.Query == "" || strings.Contains(strings.ToLower(record.item.Mask), strings.ToLower(query.Query))
}

func credentialCollectionPage(records []credentialCollectionRecord, page, pageSize int) []CredentialItemResponse {
	if page < 1 || pageSize < 1 || page-1 > len(records)/pageSize {
		return []CredentialItemResponse{}
	}
	offset := (page - 1) * pageSize
	if offset < 0 || offset >= len(records) {
		return []CredentialItemResponse{}
	}
	end := offset + pageSize
	if end < offset || end > len(records) {
		end = len(records)
	}
	items := make([]CredentialItemResponse, end-offset)
	for index, record := range records[offset:end] {
		items[index] = record.item
	}
	return items
}
