package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type GroupUpdateRequest struct {
	Name                     optionalField[string]              `json:"name"`
	Enabled                  optionalField[bool]                `json:"enabled"`
	UpstreamURL              optionalField[string]              `json:"upstream_url"`
	Protocols                optionalField[[]protocol.Protocol] `json:"protocols"`
	ValidationModel          optionalField[string]              `json:"validation_model"`
	WeightManual             optionalField[int]                 `json:"weight_manual"`
	Config                   optionalField[config.Settings]     `json:"config"`
	ConfirmUpstreamURLChange optionalField[bool]                `json:"confirm_upstream_url_change"`
}

type GroupUpdateResult struct {
	Group                       GroupDetailResponse `json:"group"`
	ModelRediscoveryRecommended bool                `json:"model_rediscovery_recommended"`
}

type normalizedGroupUpdate struct {
	name                     *string
	enabled                  *bool
	upstreamURL              *string
	upstreamHostname         string
	protocols                []protocol.Protocol
	protocolsSet             bool
	validationModel          *string
	validationModelSet       bool
	weightManual             *int
	weightManualSet          bool
	encodedConfig            models.JSON
	configSet                bool
	confirmUpstreamURLChange bool
}

func normalizeGroupUpdate(request GroupUpdateRequest) (normalizedGroupUpdate, error) {
	for _, nullable := range []bool{
		request.Name.Set && request.Name.Null,
		request.Enabled.Set && request.Enabled.Null,
		request.UpstreamURL.Set && request.UpstreamURL.Null,
		request.Protocols.Set && request.Protocols.Null,
		request.Config.Set && request.Config.Null,
		request.ConfirmUpstreamURLChange.Set && request.ConfirmUpstreamURLChange.Null,
	} {
		if nullable {
			return normalizedGroupUpdate{}, app_errors.ErrValidation
		}
	}
	if !request.Name.Set && !request.Enabled.Set && !request.UpstreamURL.Set &&
		!request.Protocols.Set && !request.ValidationModel.Set &&
		!request.WeightManual.Set && !request.Config.Set {
		return normalizedGroupUpdate{}, app_errors.ErrBadRequest
	}

	result := normalizedGroupUpdate{
		confirmUpstreamURLChange: request.ConfirmUpstreamURLChange.Set &&
			request.ConfirmUpstreamURLChange.Value,
	}
	if request.Name.Set {
		value, err := normalizeGroupName(&request.Name.Value)
		if err != nil {
			return normalizedGroupUpdate{}, err
		}
		result.name = value
	}
	if request.Enabled.Set {
		value := request.Enabled.Value
		result.enabled = &value
	}
	if request.UpstreamURL.Set {
		value, hostname, err := normalizeUpstreamBaseURL(request.UpstreamURL.Value)
		if err != nil {
			return normalizedGroupUpdate{}, err
		}
		result.upstreamURL = &value
		result.upstreamHostname = hostname
	}
	if request.Protocols.Set {
		values, err := normalizeGroupProtocols(request.Protocols.Value)
		if err != nil {
			return normalizedGroupUpdate{}, err
		}
		result.protocols = values
		result.protocolsSet = true
	}
	if request.ValidationModel.Set {
		result.validationModelSet = true
		if !request.ValidationModel.Null {
			value, err := normalizeValidationModel(request.ValidationModel.Value)
			if err != nil {
				return normalizedGroupUpdate{}, err
			}
			result.validationModel = &value
		}
	}
	if request.WeightManual.Set {
		result.weightManualSet = true
		if !request.WeightManual.Null {
			if request.WeightManual.Value < 0 || request.WeightManual.Value > state.MaxWeight {
				return normalizedGroupUpdate{}, app_errors.ErrValidation
			}
			value := request.WeightManual.Value
			result.weightManual = &value
		}
	}
	if request.Config.Set {
		_, encoded, err := normalizeGroupSettings(request.Config.Value)
		if err != nil {
			return normalizedGroupUpdate{}, err
		}
		result.encodedConfig = encoded
		result.configSet = true
	}
	return result, nil
}

func normalizeValidationModel(raw string) (string, error) {
	normalized := strings.TrimSpace(raw)
	if normalized == "" || len([]byte(normalized)) > 255 {
		return "", app_errors.ErrValidation
	}
	for _, character := range normalized {
		if unicode.IsControl(character) {
			return "", app_errors.ErrValidation
		}
	}
	return normalized, nil
}

func (s *Service) UpdateGroup(
	ctx context.Context,
	groupID uint,
	request GroupUpdateRequest,
) (GroupUpdateResult, error) {
	if groupID == 0 {
		return GroupUpdateResult{}, app_errors.ErrBadRequest
	}
	normalized, err := normalizeGroupUpdate(request)
	if err != nil {
		return GroupUpdateResult{}, err
	}

	var result GroupUpdateResult
	var changedHostname string
	_, err = s.writeConfig(ctx, func(tx *gorm.DB) error {
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
		if normalized.enabled != nil {
			group.Enabled = *normalized.enabled
			updates["enabled"] = group.Enabled
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
				if !normalized.confirmUpstreamURLChange {
					return app_errors.ErrUpstreamURLChangeConfirmationRequired
				}
				group.UpstreamURL = *normalized.upstreamURL
				updates["upstream_url"] = group.UpstreamURL
				result.ModelRediscoveryRecommended = true
				changedHostname = normalized.upstreamHostname
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
		if normalized.weightManualSet {
			group.WeightManual = normalized.weightManual
			updates["weight_manual"] = normalized.weightManual
		}
		if normalized.configSet {
			group.Config = normalized.encodedConfig
			updates["config"] = group.Config
		}
		if err := validateGroupRowCandidate(ctx, tx, group); err != nil {
			return app_errors.ErrValidation
		}
		if err := tx.Model(&models.Group{}).
			Where("id = ?", groupID).
			Updates(updates).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		result.Group, _, err = loadGroupDetail(tx, groupID)
		return err
	}, nil)
	if err != nil {
		return GroupUpdateResult{}, withControlOperationContext(err, groupID, 0)
	}
	if changedHostname != "" && isLiteralPrivateHost(changedHostname) {
		logrus.WithField("host", changedHostname).
			Warn("Updating upstream group to a private or local host")
	}
	return result, nil
}

func mapGroupRowToState(group models.Group) (state.GroupConfig, error) {
	var protocols []protocol.Protocol
	if err := decodeGroupDiscoveryJSON(group.Protocols, &protocols); err != nil {
		return state.GroupConfig{}, err
	}
	var storedModels []GroupModel
	if err := decodeGroupDiscoveryJSON(group.Models, &storedModels); err != nil {
		return state.GroupConfig{}, err
	}
	settings := make(config.Settings)
	if len(group.Config) > 0 {
		if err := decodeGroupDiscoveryJSON(group.Config, &settings); err != nil {
			return state.GroupConfig{}, err
		}
	}
	runtimeModels := make([]state.ModelConfig, 0, len(storedModels))
	for _, model := range storedModels {
		runtimeModels = append(runtimeModels, state.ModelConfig{ID: model.ID, Alias: model.Alias})
	}
	validationModel := ""
	if group.ValidationModel != nil {
		validationModel = *group.ValidationModel
	}
	return state.GroupConfig{
		ID: group.ID, Name: group.Name, UpstreamURL: group.UpstreamURL,
		ValidationModel: validationModel,
		Protocols:       protocols, Models: runtimeModels, Settings: settings,
		WeightManual: cloneInt(group.WeightManual), Enabled: group.Enabled,
	}, nil
}

func validateGroupRowCandidate(ctx context.Context, tx *gorm.DB, group models.Group) error {
	candidate, err := mapGroupRowToState(group)
	if err != nil {
		return err
	}
	systemSettings, err := stateloader.LoadSystemSettings(ctx, tx)
	if err != nil {
		return err
	}
	_, err = state.Compile(state.CompileInput{
		SystemSettings: systemSettings,
		Groups:         []state.GroupConfig{candidate},
	})
	return err
}

func findOtherGroupsByUpstreamURL(
	tx *gorm.DB,
	upstreamURL string,
	excludedID uint,
) ([]ExistingGroupSummary, error) {
	groups := make([]ExistingGroupSummary, 0)
	if err := tx.Model(&models.Group{}).
		Select("id", "name").
		Where("upstream_url = ? AND id <> ?", upstreamURL, excludedID).
		Order("id ASC").
		Scan(&groups).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	return groups, nil
}
