package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

func (s *Service) DiscoverGroupModels(
	ctx context.Context,
	groupID uint,
) (ModelDiscoveryResult, error) {
	target, err := s.buildGroupDiscoveryTarget(ctx, groupID)
	if err != nil {
		return ModelDiscoveryResult{}, err
	}
	return s.executeModelDiscovery(ctx, target)
}

type groupDiscoverySnapshotRows struct {
	found    bool
	group    models.Group
	keys     []models.UpstreamKey
	settings []models.SystemSetting
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

		var keys []models.UpstreamKey
		if err := tx.
			Where("group_id = ? AND status = ?", groupID, models.UpstreamKeyStatusActive).
			Order("id ASC").
			Find(&keys).Error; err != nil {
			return err
		}
		rows.keys = cloneUpstreamKeyRows(keys)

		var settings []models.SystemSetting
		if err := tx.Order("key ASC").Find(&settings).Error; err != nil {
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

func cloneUpstreamKeyRows(rows []models.UpstreamKey) []models.UpstreamKey {
	cloned := make([]models.UpstreamKey, len(rows))
	for index := range rows {
		cloned[index] = rows[index]
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
	if len(rows.keys) == 0 {
		return discoveryTarget{}, app_errors.ErrNoActiveUpstreamKey
	}

	var protocols []protocol.Protocol
	if err := decodeGroupDiscoveryJSON(rows.group.Protocols, &protocols); err != nil {
		return discoveryTarget{}, fmt.Errorf("decode persisted discovery protocols: %w", app_errors.ErrInternalServer)
	}
	groupConfig := rows.group.Config
	if len(bytes.TrimSpace(groupConfig)) == 0 {
		groupConfig = models.JSON(`{}`)
	}
	settings := make(config.Settings)
	if err := decodeGroupDiscoveryJSON(groupConfig, &settings); err != nil {
		return discoveryTarget{}, fmt.Errorf("decode persisted discovery config: %w", app_errors.ErrInternalServer)
	}

	systemSettings, err := stateloader.MapSystemSettings(rows.settings)
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("load persisted discovery settings: %w", app_errors.ErrInternalServer)
	}
	snapshot, err := state.Compile(state.CompileInput{
		SystemSettings: systemSettings,
		Groups: []state.GroupConfig{{
			ID: rows.group.ID, Name: rows.group.Name, UpstreamURL: rows.group.UpstreamURL,
			ProviderID: cloneString(rows.group.ProviderID),
			Protocols:  protocols, Settings: settings, Enabled: true,
		}},
	})
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("compile persisted discovery target: %w", app_errors.ErrInternalServer)
	}
	compiledGroup, ok := snapshot.Groups[rows.group.ID]
	if !ok {
		return discoveryTarget{}, fmt.Errorf("compiled persisted discovery Group is missing: %w", app_errors.ErrInternalServer)
	}

	plaintextKeys := make([]string, 0, len(rows.keys))
	for _, keyRow := range rows.keys {
		plaintext, err := s.encryption.Decrypt(keyRow.KeyValue)
		if err != nil {
			return discoveryTarget{}, fmt.Errorf("decrypt persisted discovery key: %w", app_errors.ErrInternalServer)
		}
		plaintextKeys = append(plaintextKeys, plaintext)
	}
	priceScopeKey, err := PriceScopeKeyForGroup(rows.group)
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("resolve persisted discovery price scope: %w", app_errors.ErrInternalServer)
	}

	return discoveryTarget{
		baseURL: rows.group.UpstreamURL, protocols: protocols,
		keys: plaintextKeys, headerRules: compiledGroup.HeaderRules,
		providerID: cloneString(rows.group.ProviderID), priceScopeKey: priceScopeKey,
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
