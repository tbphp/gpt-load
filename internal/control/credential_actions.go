package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

// CredentialDownloadResult contains the canonical subscription credential and
// the safe filename suggested to the management UI.
type CredentialDownloadResult struct {
	Filename   string          `json:"filename"`
	Credential json.RawMessage `json:"credential"`
}

// CredentialDownloadAllResult contains one downloadable file per subscription
// credential in a Group, ordered by credential ID.
type CredentialDownloadAllResult struct {
	Files []CredentialDownloadResult `json:"files"`
}

// RefreshGroupCredential forces only the subscription token refresh. Account
// observations are refreshed through their dedicated explicit action.
func (s *Service) RefreshGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (CredentialItemResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialItemResponse{}, app_errors.ErrBadRequest
	}
	group, credential, err := s.loadSubscriptionCredentialTarget(ctx, groupID, credentialID)
	if err != nil {
		return CredentialItemResponse{}, err
	}
	if s.prepareSubscriptionCredential == nil {
		return CredentialItemResponse{}, app_errors.ErrAuthorizationUnavailable
	}
	if _, err := s.prepareStoredSubscriptionCredentialWithForce(ctx, group, credential, true); err != nil {
		return CredentialItemResponse{}, err
	}
	return s.loadCredentialItem(ctx, groupID, credentialID)
}

// DownloadGroupCredential returns the provider canonical JSON for a
// subscription credential without exposing any server-side storage wrapper.
func (s *Service) DownloadGroupCredential(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (CredentialDownloadResult, error) {
	if groupID == 0 || credentialID == 0 {
		return CredentialDownloadResult{}, app_errors.ErrBadRequest
	}
	group, credential, err := s.loadSubscriptionCredentialTarget(ctx, groupID, credentialID)
	if err != nil {
		return CredentialDownloadResult{}, err
	}
	canonical, email, err := s.decodeCredential(group, credential)
	if err != nil {
		return CredentialDownloadResult{}, err
	}
	if len(canonical) == 0 {
		return CredentialDownloadResult{}, app_errors.ErrInternalServer
	}
	return CredentialDownloadResult{
		Filename:   subscriptionCredentialFilename(group.ChannelID, email, credential.ID),
		Credential: json.RawMessage(append([]byte(nil), canonical...)),
	}, nil
}

// DownloadAllGroupCredentials returns every subscription credential in the
// Group without applying collection pagination or filters.
func (s *Service) DownloadAllGroupCredentials(
	ctx context.Context,
	groupID uint,
) (CredentialDownloadAllResult, error) {
	if groupID == 0 {
		return CredentialDownloadAllResult{}, app_errors.ErrBadRequest
	}
	var group models.Group
	var rows []models.Credential
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Take(&group, groupID).Error; err != nil {
			return err
		}
		if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription {
			return app_errors.ErrForbidden
		}
		if group.ChannelID == "" {
			return app_errors.ErrValidation
		}
		return tx.Where("group_id = ?", groupID).Order("id ASC").Find(&rows).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CredentialDownloadAllResult{}, app_errors.ErrResourceNotFound
		}
		var apiErr *app_errors.APIError
		if errors.As(err, &apiErr) {
			return CredentialDownloadAllResult{}, err
		}
		return CredentialDownloadAllResult{}, app_errors.ParseDBError(err)
	}
	files := make([]CredentialDownloadResult, 0, len(rows))
	for _, row := range rows {
		canonical, email, err := s.decodeCredential(group, row)
		if err != nil {
			return CredentialDownloadAllResult{}, err
		}
		if len(canonical) == 0 {
			return CredentialDownloadAllResult{}, app_errors.ErrInternalServer
		}
		files = append(files, CredentialDownloadResult{
			Filename:   subscriptionCredentialFilename(group.ChannelID, email, row.ID),
			Credential: json.RawMessage(append([]byte(nil), canonical...)),
		})
	}
	return CredentialDownloadAllResult{Files: files}, nil
}

func (s *Service) loadSubscriptionCredentialTarget(
	ctx context.Context,
	groupID uint,
	credentialID uint,
) (models.Group, models.Credential, error) {
	var group models.Group
	var credential models.Credential
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		if err := tx.Take(&group, groupID).Error; err != nil {
			return err
		}
		if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeSubscription {
			return app_errors.ErrForbidden
		}
		if group.ChannelID == "" {
			return app_errors.ErrValidation
		}
		return tx.Where("id = ? AND group_id = ?", credentialID, groupID).Take(&credential).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return group, credential, app_errors.ErrResourceNotFound
		}
		var apiErr *app_errors.APIError
		if errors.As(err, &apiErr) {
			return group, credential, err
		}
		return group, credential, app_errors.ParseDBError(err)
	}
	return group, credential, nil
}

func subscriptionCredentialFilename(channelID, email string, credentialID uint) string {
	channelPart := sanitizeCredentialFilenamePart(channelID)
	if channelPart == "" {
		channelPart = "subscription"
	}
	accountPart := sanitizeCredentialFilenamePart(email)
	if accountPart == "" {
		accountPart = fmt.Sprintf("credential-%d", credentialID)
	}
	return channelPart + "-" + accountPart + ".json"
}

func sanitizeCredentialFilenamePart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	separator := false
	for _, character := range value {
		valid := character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '_' || character == '-'
		if valid {
			builder.WriteRune(character)
			separator = character == '-' || character == '_' || character == '.'
			continue
		}
		if !separator {
			builder.WriteByte('-')
			separator = true
		}
	}
	clean := strings.Trim(builder.String(), ".-_ ")
	if len(clean) > 80 {
		clean = strings.Trim(clean[:80], ".-_ ")
	}
	return clean
}
