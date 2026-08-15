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
	"gpt-load/internal/codex"
	"gpt-load/internal/connection"
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
	CredentialID            uint                           `json:"credential_id"`
	ConnectionType          string                         `json:"connection_type"`
	SecretVersion           uint64                         `json:"secret_version"`
	Mask                    string                         `json:"mask"`
	Account                 CredentialStageAccount         `json:"account"`
	AuthState               string                         `json:"auth_state"`
	AuthErrorCode           string                         `json:"auth_error_code,omitempty"`
	Observation             *CredentialObservationResponse `json:"observation,omitempty"`
	ConfiguredStatus        string                         `json:"configured_status"`
	EffectiveStatus         string                         `json:"effective_status"`
	WeightMode              string                         `json:"weight_mode"`
	Weight                  *int                           `json:"weight"`
	RecentSuccessCount      uint64                         `json:"recent_success_count"`
	RecentFailureCount      uint64                         `json:"recent_failure_count"`
	ConsecutiveFailureCount uint64                         `json:"consecutive_failure_count"`
	LastFailureCategory     string                         `json:"last_failure_category"`
	LastStatusCode          *int                           `json:"last_status_code"`
	CooldownUntilMS         *int64                         `json:"cooldown_until_ms"`
	LastUsedAtMS            *int64                         `json:"last_used_at_ms,omitempty"`
	DailyUsage              *CredentialDailyUsageResponse  `json:"daily_usage,omitempty"`
	Recovery                CredentialRecoveryResponse     `json:"recovery"`
}

// CredentialDailyUsageResponse 汇报固定 24 小时窗口内的上游尝试结果分布。
// recent_success_count / recent_failure_count 来自 health 的 5 分钟内存窗口，
// 是调度判定用的，重启即清零；这里的计数来自账号小时聚合，用于给人看。
type CredentialDailyUsageResponse struct {
	WindowSeconds int64 `json:"window_seconds"`
	SuccessCount  int64 `json:"success_count"`
	FailureCount  int64 `json:"failure_count"`
	// DataComplete 为 false 表示统计数据未覆盖完整窗口，计数偏低。
	DataComplete bool `json:"data_complete"`
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
	group        models.Group
	rows         []models.Credential
	observations []models.CredentialObservation
	views        []state.CredentialRuntimeView
	observedAt   time.Time
}

type credentialObservation struct {
	group        models.Group
	rows         []models.Credential
	subscription map[uint]models.CredentialObservation
	runtime      map[uint]state.CredentialRuntimeView
	observedAt   time.Time
}

type credentialCollectionRecord struct {
	item   CredentialItemResponse
	bucket healthBucket
}

func normalizeGroupConnectionType(value models.ConnectionType) models.ConnectionType {
	return models.ConnectionType(connection.Normalize(string(value)))
}

func credentialPresentation(
	group models.Group,
	row models.Credential,
	canonical json.RawMessage,
	identity string,
) (string, CredentialStageAccount, error) {
	if normalizeGroupConnectionType(group.ConnectionType) == models.ConnectionTypeSubscription {
		credential, err := codex.ParseCredentialJSON(canonical)
		if err != nil {
			return "", CredentialStageAccount{}, err
		}
		account := codexCredentialAccount(credential)
		mask := account.EmailMask
		if mask == "" {
			mask = fmt.Sprintf("Subscription #%d", row.ID)
		}
		return mask, account, nil
	}
	mask, err := maskCanonicalCredential(canonical)
	return mask, CredentialStageAccount{}, err
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
		if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeAPIKey {
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
	return s.mapCredentialCollection(ctx, observation, query)
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
	observations := make([]models.CredentialObservation, 0)
	var observationsErr error
	if groupErr == nil && rowsErr == nil && len(rows) > 0 && normalizeGroupConnectionType(group.ConnectionType) == models.ConnectionTypeSubscription {
		observationsErr = s.db.WithContext(ctx).
			Where("credential_id IN (?)", credentialIDs(rows)).Find(&observations).Error
	}
	var views []state.CredentialRuntimeView
	var observedAt time.Time
	if groupErr == nil && rowsErr == nil && observationsErr == nil {
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
	if observationsErr != nil {
		return credentialCapture{}, app_errors.ParseDBError(observationsErr)
	}
	return credentialCapture{group: group, rows: rows, observations: observations, views: views, observedAt: observedAt}, nil
}

func credentialIDs(rows []models.Credential) []uint {
	ids := make([]uint, len(rows))
	for index := range rows {
		ids[index] = rows[index].ID
	}
	return ids
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
		if view.AuthState != normalizeRuntimeCredentialAuthState(row.AuthState) {
			return credentialObservation{}, dbRegistryMismatch(mismatchStatus, groupID, row.ID)
		}
		if !equalOptionalWeight(view.WeightManual, row.WeightManual) {
			return credentialObservation{}, dbRegistryMismatch(mismatchWeightManual, groupID, row.ID)
		}
		if view.Version != groupCollectionCredentialVersion(row.SecretVersion) ||
			view.IdentityGeneration != groupCollectionCredentialIdentity(row.IdentityFingerprint, capture.group) {
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
	subscription := make(map[uint]models.CredentialObservation, len(capture.observations))
	for _, row := range capture.observations {
		subscription[row.CredentialID] = row
	}
	return credentialObservation{group: capture.group, rows: capture.rows, subscription: subscription, runtime: byID, observedAt: capture.observedAt}, nil
}

func normalizeRuntimeCredentialAuthState(value models.CredentialAuthState) state.CredentialAuthState {
	if value == "" {
		return state.CredentialAuthStateReady
	}
	return state.CredentialAuthState(value)
}

func (s *Service) decodeCredential(group models.Group, row models.Credential) (json.RawMessage, string, error) {
	plaintext, err := s.encryption.Decrypt(row.Data)
	if err != nil {
		return nil, "", fmt.Errorf("decrypt credential %d: %w", row.ID, app_errors.ErrInternalServer)
	}
	if normalizeGroupConnectionType(group.ConnectionType) == models.ConnectionTypeSubscription {
		credential, err := codex.ParseCredentialJSON([]byte(plaintext))
		plaintext = ""
		if err != nil {
			return nil, "", fmt.Errorf("validate subscription credential %d: %w", row.ID, app_errors.ErrInternalServer)
		}
		canonical, err := codex.MarshalCredential(credential)
		if err != nil {
			return nil, "", fmt.Errorf("encode subscription credential %d: %w", row.ID, app_errors.ErrInternalServer)
		}
		return canonical, credential.Email, nil
	}
	validated, err := normalizeStoredCredential(s.channelRegistry, channel.ID(group.ChannelID), plaintext)
	plaintext = ""
	if err != nil {
		return nil, "", fmt.Errorf("validate credential %d: %w", row.ID, app_errors.ErrInternalServer)
	}
	apiKey, _ := validated.Value("api_key")
	return validated.CanonicalJSON(), apiKey, nil
}

func (s *Service) mapCredentialCollection(
	ctx context.Context,
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
		canonical, identity, err := s.decodeCredential(observation.group, row)
		if err != nil {
			return CredentialCollectionResponse{}, err
		}
		mask, account, err := credentialPresentation(observation.group, row, canonical, identity)
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
		item.ConnectionType = string(normalizeGroupConnectionType(observation.group.ConnectionType))
		item.SecretVersion = row.SecretVersion
		item.AuthState = string(row.AuthState)
		item.AuthErrorCode = safeInternalErrorCode(row.AuthErrorCode)
		item.Account = account
		if item.ConnectionType == string(models.ConnectionTypeSubscription) {
			item.Observation = presentCredentialObservation(observation.subscription[row.ID], row.IdentityFingerprint, observation.observedAt)
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
	items := credentialCollectionPage(filtered, query.Page, query.PageSize)
	// 配额窗口的本地用量只在账号详情被展开时读取，避免列表按卡片聚合。
	s.enrichCredentialActivities(ctx, items)
	return CredentialCollectionResponse{
		ObservedAtMS: observedAtMS, StatsWindowSeconds: credentialCollectionStatsWindow,
		Summary: summary, Items: items,
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
