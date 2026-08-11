package control

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/health"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
)

// normalizeStoredCredential validates the single canonical object format used
// for encrypted channel credentials.
func normalizeStoredCredential(
	registry *channel.Registry,
	channelID channel.ID,
	plaintext string,
) (channel.Credential, error) {
	if registry == nil || channelID == "" {
		return channel.Credential{}, fmt.Errorf("credential registry is unavailable")
	}
	trimmed := strings.TrimSpace(plaintext)
	if trimmed == "" {
		return channel.Credential{}, fmt.Errorf("credential is empty")
	}
	return registry.ValidateCredential(channelID, json.RawMessage(trimmed))
}

const (
	credentialCollectionDefaultPage     = 1
	credentialCollectionDefaultPageSize = 50
	credentialCollectionStatsWindow     = int64(health.StatsWindow / time.Second)
)

func parseCredentialCollectionQuery(rawQuery string) (CredentialCollectionQuery, *app_errors.APIError) {
	query := CredentialCollectionQuery{
		Page: credentialCollectionDefaultPage, PageSize: credentialCollectionDefaultPageSize,
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return CredentialCollectionQuery{}, app_errors.ErrBadRequest
	}
	for key, entries := range values {
		switch key {
		case "q", "status", "page", "page_size":
		default:
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
		if len(entries) != 1 {
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["q"]; exists {
		query.Query = strings.TrimSpace(entries[0])
	}
	if entries, exists := values["status"]; exists {
		status := entries[0]
		switch status {
		case string(healthBucketAvailable), string(healthBucketCooldown),
			string(healthBucketBlacklisted), string(healthBucketDisabled):
			query.Status = &status
		default:
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
	}
	if entries, exists := values["page"]; exists {
		page, ok := parseCredentialCollectionPositiveInt(entries[0])
		if !ok {
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.Page = page
	}
	if entries, exists := values["page_size"]; exists {
		pageSize, ok := parseCredentialCollectionPositiveInt(entries[0])
		if !ok || pageSize != 20 && pageSize != 50 && pageSize != 100 {
			return CredentialCollectionQuery{}, app_errors.ErrBadRequest
		}
		query.PageSize = pageSize
	}
	return query, nil
}

func parseCredentialCollectionPositiveInt(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for index := range len(value) {
		if value[index] < '0' || value[index] > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil && parsed > 0
}

func maskCredential(plaintext string) (string, error) {
	mask := utils.MaskAPIKey(plaintext)
	if mask == "" {
		return "", fmt.Errorf("credential is empty: %w", app_errors.ErrInternalServer)
	}
	return mask, nil
}

func maskCanonicalCredential(canonical json.RawMessage) (string, error) {
	var values map[string]string
	if err := json.Unmarshal(canonical, &values); err != nil || values == nil {
		return "", fmt.Errorf("credential is invalid: %w", app_errors.ErrInternalServer)
	}
	for _, field := range []string{"api_key", "access_key", "client_id", "role_arn"} {
		if value := strings.TrimSpace(values[field]); value != "" {
			return maskCredential(value)
		}
	}
	if raw := strings.TrimSpace(values["service_account_json"]); raw != "" {
		var serviceAccount struct {
			ClientEmail string `json:"client_email"`
		}
		if json.Unmarshal([]byte(raw), &serviceAccount) == nil && strings.TrimSpace(serviceAccount.ClientEmail) != "" {
			return maskCredential(serviceAccount.ClientEmail)
		}
	}
	return "", fmt.Errorf("credential identity is unavailable: %w", app_errors.ErrInternalServer)
}

func mapCredentialRuntimeItem(
	mask string,
	credentialID uint,
	view state.CredentialRuntimeView,
	bucket healthBucket,
	stats health.CredentialStats,
	observedAt time.Time,
) (CredentialItemResponse, error) {
	weightMode := "auto"
	if view.WeightManual != nil {
		weightMode = "manual"
	}
	item := CredentialItemResponse{
		CredentialID:            credentialID,
		Mask:                    mask,
		ConfiguredStatus:        string(view.Status),
		EffectiveStatus:         string(bucket),
		WeightMode:              weightMode,
		RecentSuccessCount:      stats.Success,
		RecentFailureCount:      stats.Failure,
		ConsecutiveFailureCount: stats.ConsecutiveFailure,
		LastFailureCategory:     normalizeCredentialFailureCategory(stats.LastFailureCategory).String(),
		LastStatusCode:          optionalHealthStatusCode(stats.LastStatusCode),
	}
	switch bucket {
	case healthBucketAvailable:
		weight := view.WeightAuto
		if view.WeightManual != nil {
			weight = *view.WeightManual
		}
		if weight < 1 || weight > state.MaxWeight {
			return CredentialItemResponse{}, fmt.Errorf(
				"map credential %d weight: %w", credentialID, app_errors.ErrInternalServer,
			)
		}
		item.Weight = &weight
		item.Recovery = CredentialRecoveryResponse{Mode: "none"}
	case healthBucketCooldown:
		cooldownUntilMS, err := optionalSafeEpochMilliseconds(view.CooldownUntil)
		if err != nil {
			return CredentialItemResponse{}, fmt.Errorf(
				"map credential %d cooldown_until_ms: %w", credentialID, err,
			)
		}
		if cooldownUntilMS == nil || !view.CooldownUntil.After(observedAt) {
			return CredentialItemResponse{}, fmt.Errorf(
				"map credential %d invalid cooldown: %w", credentialID, app_errors.ErrInternalServer,
			)
		}
		item.CooldownUntilMS = cooldownUntilMS
		item.Recovery = CredentialRecoveryResponse{
			Mode: "cooldown", Automatic: true, AtMS: cooldownUntilMS,
		}
	case healthBucketBlacklisted:
		item.Recovery = CredentialRecoveryResponse{Mode: "probe", Automatic: true}
	case healthBucketDisabled:
		item.Recovery = CredentialRecoveryResponse{Mode: "manual"}
	default:
		return CredentialItemResponse{}, fmt.Errorf(
			"map credential %d unknown status: %w", credentialID, app_errors.ErrInternalServer,
		)
	}
	return item, nil
}

func normalizeCredentialFailureCategory(category health.FailureCategory) health.FailureCategory {
	if category == health.FailureCategoryOK {
		return health.FailureCategoryAmbiguous
	}
	return category
}

func credentialCollectionBucketOrder(bucket healthBucket) int {
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

func credentialCollectionTotalPages(totalItems, pageSize int) int {
	if totalItems == 0 {
		return 0
	}
	return (totalItems + pageSize - 1) / pageSize
}
