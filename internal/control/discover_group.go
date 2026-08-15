package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func (s *Service) DiscoverGroupModels(
	ctx context.Context,
	groupID uint,
) (ModelDiscoveryResult, error) {
	if groupID == 0 {
		return ModelDiscoveryResult{}, app_errors.ErrValidation
	}
	rows, err := s.readGroupDiscoverySnapshot(ctx, groupID)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	if rows.found {
		bindings, ok := s.channelRegistry.CapabilityBindings(channel.ID(rows.group.ChannelID))
		if !ok {
			return ModelDiscoveryResult{}, app_errors.ErrInternalServer
		}
		if bindings.ModelDiscovery != "" {
			return s.discoverSubscriptionGroupModels(ctx, rows)
		}
	}
	target, err := s.mapGroupDiscoveryTarget(rows)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	return s.executeModelDiscovery(ctx, target)
}

type groupDiscoverySnapshotRows struct {
	found       bool
	group       models.Group
	credentials []models.Credential
	settings    []models.SystemSetting
}

func (s *Service) buildGroupDiscoveryTarget(
	ctx context.Context,
	groupID uint,
) (discoveryTarget, error) {
	if err := ctx.Err(); err != nil {
		return discoveryTarget{}, err
	}
	if groupID == 0 {
		return discoveryTarget{}, app_errors.ErrValidation
	}

	rows, err := s.readGroupDiscoverySnapshot(ctx, groupID)
	if parentErr := ctx.Err(); parentErr != nil {
		return discoveryTarget{}, parentErr
	}
	if err != nil {
		return discoveryTarget{}, err
	}
	target, err := s.mapGroupDiscoveryTarget(rows)
	if parentErr := ctx.Err(); parentErr != nil {
		return discoveryTarget{}, parentErr
	}
	return target, err
}

func (s *Service) readGroupDiscoverySnapshot(
	ctx context.Context,
	groupID uint,
) (groupDiscoverySnapshotRows, error) {
	var rows groupDiscoverySnapshotRows
	err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		var groups []models.Group
		if err := tx.Where("id = ?", groupID).Limit(1).Find(&groups).Error; err != nil {
			return err
		}
		if len(groups) == 0 {
			return nil
		}
		rows.found = true
		rows.group = cloneGroupRows(groups)[0]

		var credentials []models.Credential
		if err := tx.
			Where("group_id = ? AND status = ?", groupID, models.CredentialStatusActive).
			Order("id ASC").
			Find(&credentials).Error; err != nil {
			return err
		}
		rows.credentials = cloneDiscoveryCredentialRows(credentials)

		var settings []models.SystemSetting
		if err := tx.
			Order(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "key"}}}}).
			Find(&settings).Error; err != nil {
			return err
		}
		rows.settings = append([]models.SystemSetting(nil), settings...)
		return nil
	})
	if parentErr := ctx.Err(); parentErr != nil {
		return groupDiscoverySnapshotRows{}, parentErr
	}
	if err != nil {
		return groupDiscoverySnapshotRows{}, fmt.Errorf(
			"load persisted discovery snapshot: %w",
			app_errors.ErrInternalServer,
		)
	}
	return rows, nil
}

func cloneDiscoveryCredentialRows(rows []models.Credential) []models.Credential {
	cloned := make([]models.Credential, len(rows))
	for index := range rows {
		cloned[index] = rows[index]
		cloned[index].Group = nil
		if rows[index].WeightManual != nil {
			value := *rows[index].WeightManual
			cloned[index].WeightManual = &value
		}
	}
	return cloned
}

func (s *Service) mapGroupDiscoveryTarget(
	rows groupDiscoverySnapshotRows,
) (discoveryTarget, error) {
	if !rows.found {
		return discoveryTarget{}, app_errors.ErrResourceNotFound
	}
	if len(rows.credentials) == 0 {
		return discoveryTarget{}, app_errors.ErrNoActiveCredential
	}

	if s == nil || s.channelRegistry == nil {
		return discoveryTarget{}, app_errors.ErrInternalServer
	}
	resolvedTarget, err := s.channelRegistry.Resolve(
		channel.ID(rows.group.ChannelID),
		json.RawMessage(rows.group.Params),
	)
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("resolve persisted discovery channel: %w", app_errors.ErrInternalServer)
	}
	overrides := rows.group.Overrides
	if len(bytes.TrimSpace(overrides)) == 0 {
		overrides = models.JSON(`{}`)
	}
	settings := make(config.Settings)
	if err := decodeGroupDiscoveryJSON(overrides, &settings); err != nil {
		return discoveryTarget{}, fmt.Errorf("decode persisted discovery overrides: %w", app_errors.ErrInternalServer)
	}

	systemSettings, err := stateloader.MapSystemSettings(rows.settings)
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("load persisted discovery settings: %w", app_errors.ErrInternalServer)
	}
	snapshot, err := state.Compile(state.CompileInput{
		SystemSettings: systemSettings, ChannelRegistry: s.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: rows.group.ID, Name: rows.group.Name,
			ChannelID: channel.ID(rows.group.ChannelID), ConnectionType: string(rows.group.ConnectionType),
			Params:   append(json.RawMessage(nil), rows.group.Params...),
			Settings: settings, Enabled: true,
		}},
	})
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("compile persisted discovery target: %w", app_errors.ErrInternalServer)
	}
	compiledGroup, ok := snapshot.Groups[rows.group.ID]
	if !ok {
		return discoveryTarget{}, fmt.Errorf("compiled persisted discovery Group is missing: %w", app_errors.ErrInternalServer)
	}

	discoveryCredentials := make([]discoveryCredential, 0, len(rows.credentials))
	for _, credentialRow := range rows.credentials {
		canonical, apiKey, err := s.decodeCredential(rows.group, credentialRow)
		if err != nil {
			return discoveryTarget{}, err
		}
		if normalizeGroupConnectionType(rows.group.ConnectionType) == models.ConnectionTypeSubscription {
			apiKey = ""
		}
		discoveryCredentials = append(discoveryCredentials, discoveryCredential{
			snapshot: execution.NewCredentialSnapshot(
				credentialRow.ID,
				groupCollectionCredentialVersion(credentialRow.SecretVersion),
				groupCollectionCredentialIdentity(credentialRow.IdentityFingerprint, rows.group),
				canonical,
			),
			apiKey: apiKey,
		})
	}
	return discoveryTarget{
		channelID:      channel.ID(rows.group.ChannelID),
		resolvedTarget: resolvedTarget, credentials: discoveryCredentials,
		headerRules: compiledGroup.HeaderRules, timeouts: compiledGroup.Timeouts,
		catalogProviderID: resolvedTarget.CatalogProviderID,
	}, nil
}

func decodeGroupDiscoveryJSON(raw models.JSON, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
