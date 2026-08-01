package control

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

var errGroupSettingsUpstreamChangeConfirmationRequired = &app_errors.APIError{
	HTTPStatus: 409,
	Code:       "UPSTREAM_CHANGE_CONFIRMATION_REQUIRED",
	Message:    "Upstream URL change requires explicit confirmation",
}

type GroupSettingsResponse struct {
	Name            string                       `json:"name"`
	UpstreamURL     string                       `json:"upstream_url"`
	Protocols       []protocol.Protocol          `json:"protocols"`
	ValidationModel *string                      `json:"validation_model"`
	Enabled         bool                         `json:"enabled"`
	WeightManual    *int                         `json:"weight_manual"`
	Overrides       config.Settings              `json:"overrides"`
	Effective       GroupEffectiveConfigResponse `json:"effective"`
}

type GroupSettingsUpdateRequest struct {
	Name                  optionalField[string]              `json:"name"`
	UpstreamURL           optionalField[string]              `json:"upstream_url"`
	Protocols             optionalField[[]protocol.Protocol] `json:"protocols"`
	ValidationModel       optionalField[string]              `json:"validation_model"`
	Enabled               optionalField[bool]                `json:"enabled"`
	WeightManual          optionalField[int]                 `json:"weight_manual"`
	Overrides             optionalField[config.Settings]     `json:"overrides"`
	ConfirmUpstreamChange bool                               `json:"confirm_upstream_change"`
}

type normalizedGroupSettingsUpdate struct {
	name                  *string
	upstreamURL           *string
	protocols             []protocol.Protocol
	protocolsSet          bool
	validationModel       *string
	validationModelSet    bool
	enabled               *bool
	weightManual          *int
	weightManualSet       bool
	encodedOverrides      models.JSON
	overrides             config.Settings
	overridesSet          bool
	confirmUpstreamChange bool
}

func (s *Service) GetGroupSettings(ctx context.Context, groupID uint) (GroupSettingsResponse, error) {
	detail, err := s.GetGroup(ctx, groupID)
	if err != nil {
		return GroupSettingsResponse{}, err
	}
	return groupSettingsResponse(detail), nil
}

func groupSettingsResponse(detail GroupDetailResponse) GroupSettingsResponse {
	return GroupSettingsResponse{
		Name:            detail.Name,
		UpstreamURL:     detail.UpstreamURL,
		Protocols:       append([]protocol.Protocol(nil), detail.Protocols...),
		ValidationModel: cloneString(detail.ValidationModel),
		Enabled:         detail.Enabled,
		WeightManual:    cloneInt(detail.WeightManual),
		Overrides:       cloneGroupSettings(detail.Config),
		Effective:       detail.EffectiveConfig,
	}
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneGroupSettings(settings config.Settings) config.Settings {
	cloned := make(config.Settings, len(settings))
	for key, value := range settings {
		cloned[key] = value
	}
	return cloned
}

func normalizeGroupSettingsUpdate(
	request GroupSettingsUpdateRequest,
) (normalizedGroupSettingsUpdate, error) {
	for _, nullable := range []bool{
		request.Name.Set && request.Name.Null,
		request.UpstreamURL.Set && request.UpstreamURL.Null,
		request.Protocols.Set && request.Protocols.Null,
		request.Enabled.Set && request.Enabled.Null,
		request.Overrides.Set && request.Overrides.Null,
	} {
		if nullable {
			return normalizedGroupSettingsUpdate{}, app_errors.ErrValidation
		}
	}
	if !request.Name.Set && !request.UpstreamURL.Set && !request.Protocols.Set &&
		!request.ValidationModel.Set && !request.Enabled.Set && !request.WeightManual.Set &&
		!request.Overrides.Set {
		return normalizedGroupSettingsUpdate{}, app_errors.ErrBadRequest
	}

	result := normalizedGroupSettingsUpdate{confirmUpstreamChange: request.ConfirmUpstreamChange}
	if request.Name.Set {
		value, err := normalizeGroupName(&request.Name.Value)
		if err != nil {
			return normalizedGroupSettingsUpdate{}, err
		}
		result.name = value
	}
	if request.UpstreamURL.Set {
		value, _, err := normalizeUpstreamBaseURL(request.UpstreamURL.Value)
		if err != nil {
			return normalizedGroupSettingsUpdate{}, err
		}
		result.upstreamURL = &value
	}
	if request.Protocols.Set {
		values, err := normalizeGroupProtocols(request.Protocols.Value)
		if err != nil {
			return normalizedGroupSettingsUpdate{}, err
		}
		result.protocols = values
		result.protocolsSet = true
	}
	if request.ValidationModel.Set {
		result.validationModelSet = true
		if !request.ValidationModel.Null {
			value, err := normalizeValidationModel(request.ValidationModel.Value)
			if err != nil {
				return normalizedGroupSettingsUpdate{}, err
			}
			result.validationModel = &value
		}
	}
	if request.Enabled.Set {
		value := request.Enabled.Value
		result.enabled = &value
	}
	if request.WeightManual.Set {
		result.weightManualSet = true
		if !request.WeightManual.Null {
			if request.WeightManual.Value < 1 || request.WeightManual.Value > state.MaxWeight {
				return normalizedGroupSettingsUpdate{}, app_errors.ErrValidation
			}
			value := request.WeightManual.Value
			result.weightManual = &value
		}
	}
	if request.Overrides.Set {
		settings, encoded, err := normalizeGroupSettings(request.Overrides.Value)
		if err != nil {
			return normalizedGroupSettingsUpdate{}, err
		}
		result.encodedOverrides = encoded
		result.overrides = settings
		result.overridesSet = true
	}
	return result, nil
}

func (s *Service) UpdateGroupSettings(
	ctx context.Context,
	groupID uint,
	request GroupSettingsUpdateRequest,
) (GroupSettingsResponse, error) {
	if groupID == 0 {
		return GroupSettingsResponse{}, app_errors.ErrBadRequest
	}
	normalized, err := normalizeGroupSettingsUpdate(request)
	if err != nil {
		return GroupSettingsResponse{}, err
	}

	var result GroupSettingsResponse
	snapshot, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		_, group, err := loadGroupDetail(tx, groupID)
		if err != nil {
			return err
		}
		if err := validateGroupRowCandidate(ctx, tx, group); err != nil {
			return fmt.Errorf("validate existing group %d: %w", groupID, app_errors.ErrInternalServer)
		}

		updates := make(map[string]any, 7)
		if normalized.name != nil {
			group.Name = *normalized.name
			updates["name"] = group.Name
		}
		if normalized.upstreamURL != nil {
			currentURL, _, normalizeErr := normalizeUpstreamBaseURL(group.UpstreamURL)
			if normalizeErr != nil {
				return fmt.Errorf("normalize persisted group URL: %w", app_errors.ErrInternalServer)
			}
			if currentURL != *normalized.upstreamURL {
				conflicts, conflictErr := findOtherGroupsByUpstreamURL(tx, *normalized.upstreamURL, groupID)
				if conflictErr != nil {
					return conflictErr
				}
				if len(conflicts) > 0 {
					return app_errors.NewAPIErrorWithData(
						app_errors.ErrUpstreamURLConflict,
						UpstreamURLConflictData{Groups: conflicts},
					)
				}
				if !normalized.confirmUpstreamChange {
					return errGroupSettingsUpstreamChangeConfirmationRequired
				}
				group.UpstreamURL = *normalized.upstreamURL
				updates["upstream_url"] = group.UpstreamURL
			}
		}
		if normalized.protocolsSet {
			encoded, encodeErr := json.Marshal(normalized.protocols)
			if encodeErr != nil {
				return encodeErr
			}
			group.Protocols = models.JSON(encoded)
			updates["protocols"] = group.Protocols
		}
		if normalized.validationModelSet {
			group.ValidationModel = normalized.validationModel
			updates["validation_model"] = normalized.validationModel
		}
		if normalized.enabled != nil {
			group.Enabled = *normalized.enabled
			updates["enabled"] = group.Enabled
		}
		if normalized.weightManualSet {
			group.WeightManual = normalized.weightManual
			updates["weight_manual"] = normalized.weightManual
		}
		if normalized.overridesSet {
			group.Config = normalized.encodedOverrides
			updates["config"] = group.Config
		}
		if err := validateGroupInjectUsageOptionsConstraint(group); err != nil {
			return err
		}
		if err := validateGroupRowCandidate(ctx, tx, group); err != nil {
			return app_errors.ErrValidation
		}
		if err := tx.Model(&models.Group{}).Where("id = ?", groupID).Updates(updates).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		detail, _, err := loadGroupDetail(tx, groupID)
		if err != nil {
			return err
		}
		result = groupSettingsResponse(detail)
		return nil
	}, nil)
	if err != nil {
		return GroupSettingsResponse{}, withControlOperationContext(err, groupID, 0)
	}
	result.Effective, err = effectiveGroupConfig(snapshot.Settings, result.Overrides)
	if err != nil {
		return GroupSettingsResponse{}, fmt.Errorf(
			"resolve updated group %d effective config: %w",
			groupID,
			app_errors.ErrInternalServer,
		)
	}
	return result, nil
}

func validateGroupInjectUsageOptionsConstraint(group models.Group) error {
	settings := make(config.Settings)
	if len(group.Config) > 0 {
		if err := decodeGroupDiscoveryJSON(group.Config, &settings); err != nil {
			return app_errors.ErrValidation
		}
	}
	if _, injectUsageOptionsSet := settings[state.SettingInjectUsageOptions]; injectUsageOptionsSet &&
		!groupSupportsOpenAICompletions(group.Protocols) {
		return app_errors.ErrValidation
	}
	return nil
}

func groupSupportsOpenAICompletions(raw models.JSON) bool {
	var protocols []protocol.Protocol
	if err := decodeGroupDiscoveryJSON(raw, &protocols); err != nil {
		return false
	}
	for _, value := range protocols {
		if value == protocol.OpenAICompletions {
			return true
		}
	}
	return false
}
