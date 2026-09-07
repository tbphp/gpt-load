package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/outboundproxy"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type GroupSettingsResponse struct {
	PriceMultiplier string                       `json:"price_multiplier"`
	ChannelID       channel.ID                   `json:"channel_id"`
	ConnectionType  models.ConnectionType        `json:"connection_type"`
	Params          json.RawMessage              `json:"params"`
	Name            string                       `json:"name"`
	ValidationModel *string                      `json:"validation_model"`
	Enabled         bool                         `json:"enabled"`
	WeightManual    *int                         `json:"weight_manual"`
	Overrides       config.Settings              `json:"overrides"`
	Effective       GroupEffectiveConfigResponse `json:"effective"`
	Proxy           outboundproxy.View           `json:"proxy"`
}

type GroupSettingsUpdateRequest struct {
	PriceMultiplier optionalField[string]               `json:"price_multiplier"`
	Name            optionalField[string]               `json:"name"`
	Params          optionalField[json.RawMessage]      `json:"params"`
	ValidationModel optionalField[string]               `json:"validation_model"`
	Enabled         optionalField[bool]                 `json:"enabled"`
	WeightManual    optionalField[int]                  `json:"weight_manual"`
	Overrides       optionalField[config.Settings]      `json:"overrides"`
	Proxy           optionalField[outboundproxy.Config] `json:"proxy"`
}

type normalizedGroupSettingsUpdate struct {
	priceMultiplierMicros *int64
	name                  *string
	params                json.RawMessage
	paramsSet             bool
	validationModel       *string
	validationModelSet    bool
	enabled               *bool
	weightManual          *int
	weightManualSet       bool
	encodedOverrides      models.JSON
	overridesSet          bool
	proxyConfig           *string
	proxySet              bool
}

func (s *Service) GetGroupSettings(ctx context.Context, groupID uint) (GroupSettingsResponse, error) {
	if groupID == 0 {
		return GroupSettingsResponse{}, app_errors.ErrBadRequest
	}

	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	group, err := loadGroupRow(s.db.WithContext(ctx), groupID)
	if err != nil {
		return GroupSettingsResponse{}, err
	}
	snapshot := s.manager.Current()
	if snapshot == nil {
		return GroupSettingsResponse{}, fmt.Errorf(
			"runtime snapshot unavailable: %w", app_errors.ErrInternalServer,
		)
	}
	response, err := groupSettingsResponse(group, snapshot.Settings, s.channelRegistry)
	if err != nil {
		return GroupSettingsResponse{}, err
	}
	response.Proxy, err = s.groupProxyView(ctx, s.db, group)
	return response, err
}

func groupSettingsResponse(
	group models.Group,
	system state.RuntimeSettings,
	registry *channel.Registry,
) (GroupSettingsResponse, error) {
	if registry == nil || group.ChannelID == "" {
		return GroupSettingsResponse{}, fmt.Errorf(
			"resolve group %d channel: %w", group.ID, app_errors.ErrInternalServer,
		)
	}
	channelID := channel.ID(group.ChannelID)
	validated, err := registry.ValidateParams(channelID, json.RawMessage(group.Params))
	if err != nil {
		return GroupSettingsResponse{}, fmt.Errorf(
			"validate group %d params: %w", group.ID, app_errors.ErrInternalServer,
		)
	}
	overrides := make(config.Settings)
	if len(group.Overrides) > 0 {
		if err := decodeGroupDiscoveryJSON(group.Overrides, &overrides); err != nil {
			return GroupSettingsResponse{}, fmt.Errorf("decode group %d config: %w", group.ID, err)
		}
	}
	if overrides == nil {
		overrides = make(config.Settings)
	}
	effective, err := effectiveGroupConfig(system, overrides)
	if err != nil {
		return GroupSettingsResponse{}, fmt.Errorf(
			"resolve group %d effective config: %w", group.ID, app_errors.ErrInternalServer,
		)
	}
	return GroupSettingsResponse{
		PriceMultiplier: priceMultiplierResponse(group.PriceMultiplierMicros),
		ChannelID:       channelID,
		ConnectionType:  normalizeGroupConnectionType(group.ConnectionType),
		Params:          validated.CanonicalJSON(),
		Name:            group.Name,
		ValidationModel: cloneString(group.ValidationModel),
		Enabled:         group.Enabled,
		WeightManual:    cloneInt(group.WeightManual),
		Overrides:       overrides,
		Effective:       effective,
	}, nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func normalizeGroupSettingsUpdate(
	request GroupSettingsUpdateRequest,
	encryptionService encryption.Service,
) (normalizedGroupSettingsUpdate, error) {
	for _, nullable := range []bool{
		request.Name.Set && request.Name.Null,
		request.Params.Set && request.Params.Null,
		request.Enabled.Set && request.Enabled.Null,
		request.Overrides.Set && request.Overrides.Null,
	} {
		if nullable {
			return normalizedGroupSettingsUpdate{}, app_errors.ErrValidation
		}
	}
	if !request.Name.Set && !request.Params.Set && !request.ValidationModel.Set &&
		!request.Enabled.Set && !request.WeightManual.Set && !request.Overrides.Set && !request.Proxy.Set && !request.PriceMultiplier.Set {
		return normalizedGroupSettingsUpdate{}, app_errors.ErrBadRequest
	}

	result := normalizedGroupSettingsUpdate{}
	if request.PriceMultiplier.Set {
		value, err := normalizePriceMultiplier(request.PriceMultiplier)
		if err != nil {
			return normalizedGroupSettingsUpdate{}, err
		}
		result.priceMultiplierMicros = priceMultiplierStorage(value)
	}
	if request.Name.Set {
		value, err := normalizeGroupName(&request.Name.Value)
		if err != nil {
			return normalizedGroupSettingsUpdate{}, err
		}
		result.name = value
	}
	if request.Params.Set {
		result.paramsSet = true
		result.params = append(json.RawMessage(nil), request.Params.Value...)
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
		_, encoded, err := normalizeGroupSettings(request.Overrides.Value)
		if err != nil {
			return normalizedGroupSettingsUpdate{}, err
		}
		result.encodedOverrides = encoded
		result.overridesSet = true
	}
	proxyConfig, proxySet, err := normalizeProxyOverride(request.Proxy, encryptionService)
	if err != nil {
		return normalizedGroupSettingsUpdate{}, err
	}
	result.proxyConfig = proxyConfig
	result.proxySet = proxySet
	return result, nil
}

// UpdateGroupSettings atomically applies validated group settings and
// reconciles the resulting runtime credential entries.
func (s *Service) UpdateGroupSettings(
	ctx context.Context,
	groupID uint,
	request GroupSettingsUpdateRequest,
) (GroupSettingsResponse, error) {
	if groupID == 0 {
		return GroupSettingsResponse{}, app_errors.ErrBadRequest
	}
	normalized, err := normalizeGroupSettingsUpdate(request, s.encryption)
	if err != nil {
		return GroupSettingsResponse{}, err
	}

	var committed models.Group
	var targetEntries []state.CredentialEntry
	targetChanged := false
	snapshot, err := s.writeGroupConfig(ctx, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if err := validateGroupRowCandidate(ctx, tx, group, s.channelRegistry); err != nil {
			return fmt.Errorf("validate existing group %d: %w", groupID, app_errors.ErrInternalServer)
		}
		if request.Proxy.Set && !request.Proxy.Null &&
			!s.channelRegistry.SupportsOutboundProxy(channel.ID(group.ChannelID)) {
			return app_errors.ErrValidation
		}

		updates := make(map[string]any, 8)
		if normalized.priceMultiplierMicros != nil {
			group.PriceMultiplierMicros = normalized.priceMultiplierMicros
			updates["price_multiplier_micros"] = *normalized.priceMultiplierMicros
		}
		if normalized.name != nil {
			group.Name = *normalized.name
			updates["name"] = group.Name
		}
		if normalized.paramsSet {
			previousParams := append([]byte(nil), group.Params...)
			params, validateErr := s.channelRegistry.ValidateParams(
				channel.ID(group.ChannelID), normalized.params,
			)
			if validateErr != nil {
				return app_errors.ErrValidation
			}
			group.Params = models.JSON(params.CanonicalJSON())
			targetChanged = !bytes.Equal(bytes.TrimSpace(previousParams), bytes.TrimSpace(group.Params))
			updates["params"] = append(models.JSON(nil), group.Params...)
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
			group.Overrides = normalized.encodedOverrides
			updates["overrides"] = group.Overrides
		}
		if normalized.proxySet {
			group.ProxyConfig = normalized.proxyConfig
			updates["proxy_config"] = normalized.proxyConfig
		}
		if err := validateGroupRowCandidate(ctx, tx, group, s.channelRegistry); err != nil {
			return app_errors.ErrValidation
		}
		if len(updates) > 0 {
			if err := tx.Model(&models.Group{}).Where("id = ?", groupID).Updates(updates).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
		}
		if targetChanged {
			targetEntries, err = stateloader.BuildGroupCredentialEntriesWithProxy(ctx, tx, groupID, s.encryption)
			if err != nil {
				return err
			}
			if group.ConnectionType == models.ConnectionTypeSubscription {
				credentialIDs := tx.Model(&models.Credential{}).Select("id").Where("group_id = ?", groupID)
				if err := tx.Model(&models.CredentialObservation{}).Where("credential_id IN (?)", credentialIDs).
					Updates(map[string]any{
						"state": models.CredentialObservationUnavailable, "snapshot_json": models.JSON(`{}`),
						"observation_version": gorm.Expr("observation_version + 1"),
						"observed_at_ms":      nil, "last_attempt_at_ms": nil, "next_allowed_at_ms": nil,
						"last_error_code": "", "last_auth_refresh_secret_version": nil,
						"updated_at_ms": s.now().UTC().UnixMilli(),
					}).Error; err != nil {
					return app_errors.ParseDBError(err)
				}
			}
		}
		committed = group
		return nil
	}, func() error {
		if !targetChanged {
			return nil
		}
		s.invalidateGroupObservationFlights(groupID)
		if s.stats != nil {
			for _, entry := range targetEntries {
				s.stats.Reset(entry.ID)
			}
		}
		_, err := s.reconcileRegistryGroup(groupID, targetEntries)
		return err
	})
	if err != nil {
		return GroupSettingsResponse{}, withControlOperationContext(err, groupID, 0)
	}
	response, err := groupSettingsResponse(committed, snapshot.Settings, s.channelRegistry)
	if err != nil {
		return GroupSettingsResponse{}, err
	}
	response.Proxy, err = s.groupProxyView(ctx, s.db, committed)
	return response, err
}
