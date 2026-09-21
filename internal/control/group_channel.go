package control

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

// GroupChannelUpdateRequest switches one API key group to another API key
// channel. Params are migrated from the stored group, so clients never resend
// them.
type GroupChannelUpdateRequest struct {
	ChannelID         channel.ID `json:"channel_id"`
	ConfirmSameTarget bool       `json:"confirm_same_target"`
}

// UpdateGroupChannel repoints one group at another API key channel and
// reconciles the runtime state the previous execution target owned. Subscription
// groups are excluded because their credentials are bound to one OAuth driver.
func (s *Service) UpdateGroupChannel(
	ctx context.Context,
	groupID uint,
	request GroupChannelUpdateRequest,
) (GroupSettingsResponse, error) {
	if groupID == 0 {
		return GroupSettingsResponse{}, app_errors.ErrBadRequest
	}
	if s == nil || s.channelRegistry == nil || s.encryption == nil {
		return GroupSettingsResponse{}, app_errors.ErrInternalServer
	}
	targetChannel := request.ChannelID
	if !s.channelRegistry.SupportsConnectionType(
		targetChannel, string(models.ConnectionTypeAPIKey),
	) {
		return GroupSettingsResponse{}, app_errors.ErrValidation
	}

	var committed models.Group
	var targetEntries []state.CredentialEntry
	snapshot, err := s.writeGroupConfig(ctx, func(tx *gorm.DB) error {
		group, err := loadGroupRow(tx, groupID)
		if err != nil {
			return err
		}
		if normalizeGroupConnectionType(group.ConnectionType) != models.ConnectionTypeAPIKey {
			return app_errors.ErrValidation
		}
		previousChannel := channel.ID(group.ChannelID)
		if previousChannel == targetChannel {
			return app_errors.ErrValidation
		}
		if group.ProxyConfig != nil && !s.channelRegistry.SupportsOutboundProxy(targetChannel) {
			return app_errors.ErrValidation
		}
		params, err := migrateGroupParams(s.channelRegistry, targetChannel, group.Params)
		if err != nil {
			return err
		}
		if err := s.validateGroupCredentialsForChannel(tx, groupID, targetChannel); err != nil {
			return err
		}
		if !request.ConfirmSameTarget {
			conflicts, err := findGroupsByTarget(
				tx, targetChannel, models.ConnectionTypeAPIKey, models.JSON(params),
			)
			if err != nil {
				return err
			}
			conflicts = slices.DeleteFunc(conflicts, func(summary ExistingGroupSummary) bool {
				return summary.ID == groupID
			})
			if len(conflicts) > 0 {
				return app_errors.NewAPIErrorWithData(
					app_errors.ErrChannelTargetConflict,
					SameTargetConflictData{Groups: conflicts},
				)
			}
		}

		group.ChannelID = string(targetChannel)
		group.Params = models.JSON(params)
		// The previous protocol belongs to the previous channel; re-derive it so
		// the snapshot never carries a probe the target channel cannot serve.
		group.ValidationProtocol = nil
		if err := s.initializeValidationProtocol(&group); err != nil {
			return err
		}
		if err := validateGroupRowCandidate(ctx, tx, group, s.channelRegistry); err != nil {
			return app_errors.ErrValidation
		}
		if err := tx.Model(&models.Group{}).Where("id = ?", groupID).Updates(map[string]any{
			"channel_id":          group.ChannelID,
			"params":              append(models.JSON(nil), group.Params...),
			"validation_protocol": group.ValidationProtocol,
		}).Error; err != nil {
			return app_errors.ParseDBError(err)
		}

		targetEntries, err = stateloader.BuildGroupCredentialEntriesWithProxy(
			ctx, tx, groupID, s.encryption,
		)
		if err != nil {
			return err
		}
		committed = group
		return nil
	}, func() error {
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
	if s.catalogSync != nil {
		s.catalogSync.RequestGroupSync()
	}
	response, err := groupSettingsResponse(committed, snapshot.Settings, s.channelRegistry)
	if err != nil {
		return GroupSettingsResponse{}, err
	}
	response.Proxy, err = s.groupProxyView(ctx, s.db, committed)
	return response, err
}

// validateGroupCredentialsForChannel refuses a switch the startup loader would
// reject later: every stored credential must already satisfy the target channel
// schema. Credential rows are never rewritten here.
func (s *Service) validateGroupCredentialsForChannel(
	tx *gorm.DB,
	groupID uint,
	channelID channel.ID,
) error {
	var rows []models.Credential
	if err := tx.Where("group_id = ?", groupID).Order("id ASC").Find(&rows).Error; err != nil {
		return app_errors.ParseDBError(err)
	}
	for index, row := range rows {
		plaintext, err := s.encryption.Decrypt(row.Data)
		if err != nil {
			return fmt.Errorf("decrypt credential %d: %w", row.ID, app_errors.ErrInternalServer)
		}
		_, err = normalizeStoredCredential(s.channelRegistry, channelID, plaintext)
		plaintext = ""
		if err != nil {
			return s.credentialValidationError(channelID, index+1, err)
		}
	}
	return nil
}

// migrateGroupParams reshapes stored parameters for the target channel. Keys the
// target does not declare are dropped; every value it does declare survives the
// switch untouched. A stored address is always treated as the operator's, since
// nothing distinguishes a value the create form prefilled from a typed one, and
// dropping it would both discard operator data and break a switch to a channel
// whose base URL is mandatory.
func migrateGroupParams(
	registry *channel.Registry,
	target channel.ID,
	current models.JSON,
) (json.RawMessage, error) {
	if registry == nil {
		return nil, app_errors.ErrInternalServer
	}
	descriptor, ok := registry.Get(target)
	if !ok {
		return nil, app_errors.ErrValidation
	}
	stored := map[string]string{}
	if len(current) > 0 {
		if err := json.Unmarshal(current, &stored); err != nil {
			return nil, app_errors.ErrValidation
		}
	}
	migrated := make(map[string]string, len(descriptor.ParamFields))
	for _, field := range descriptor.ParamFields {
		value, exists := stored[field.Key]
		if !exists || value == "" {
			continue
		}
		migrated[field.Key] = value
	}
	encoded, err := json.Marshal(migrated)
	if err != nil {
		return nil, app_errors.ErrInternalServer
	}
	params, err := registry.ValidateParams(target, encoded)
	if err != nil {
		return nil, app_errors.ErrValidation
	}
	return params.CanonicalJSON(), nil
}
