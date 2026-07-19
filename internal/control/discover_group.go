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

	var group models.Group
	err := s.db.WithContext(ctx).Where("id = ?", groupID).Take(&group).Error
	if parentErr := ctx.Err(); parentErr != nil {
		return discoveryTarget{}, parentErr
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return discoveryTarget{}, app_errors.ErrResourceNotFound
	}
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("load persisted discovery Group: %w", app_errors.ErrInternalServer)
	}

	var keyRows []models.UpstreamKey
	err = s.db.WithContext(ctx).
		Where("group_id = ? AND status = ?", groupID, models.UpstreamKeyStatusActive).
		Order("id ASC").
		Find(&keyRows).Error
	if parentErr := ctx.Err(); parentErr != nil {
		return discoveryTarget{}, parentErr
	}
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("load persisted discovery keys: %w", app_errors.ErrInternalServer)
	}
	if len(keyRows) == 0 {
		return discoveryTarget{}, app_errors.ErrNoActiveUpstreamKey
	}

	var protocols []protocol.Protocol
	if err := decodeGroupDiscoveryJSON(group.Protocols, &protocols); err != nil {
		return discoveryTarget{}, fmt.Errorf("decode persisted discovery protocols: %w", app_errors.ErrInternalServer)
	}
	groupConfig := group.Config
	if len(bytes.TrimSpace(groupConfig)) == 0 {
		groupConfig = models.JSON(`{}`)
	}
	settings := make(config.Settings)
	if err := decodeGroupDiscoveryJSON(groupConfig, &settings); err != nil {
		return discoveryTarget{}, fmt.Errorf("decode persisted discovery config: %w", app_errors.ErrInternalServer)
	}

	systemSettings, err := stateloader.LoadSystemSettings(ctx, s.db)
	if parentErr := ctx.Err(); parentErr != nil {
		return discoveryTarget{}, parentErr
	}
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("load persisted discovery settings: %w", app_errors.ErrInternalServer)
	}
	snapshot, err := state.Compile(state.CompileInput{
		SystemSettings: systemSettings,
		Groups: []state.GroupConfig{{
			ID: group.ID, Name: group.Name, UpstreamURL: group.UpstreamURL,
			Protocols: protocols, Settings: settings, Enabled: true,
		}},
	})
	if err != nil {
		return discoveryTarget{}, fmt.Errorf("compile persisted discovery target: %w", app_errors.ErrInternalServer)
	}
	compiledGroup, ok := snapshot.Groups[group.ID]
	if !ok {
		return discoveryTarget{}, fmt.Errorf("compiled persisted discovery Group is missing: %w", app_errors.ErrInternalServer)
	}

	plaintextKeys := make([]string, 0, len(keyRows))
	for _, keyRow := range keyRows {
		plaintext, err := s.encryption.Decrypt(keyRow.KeyValue)
		if err != nil {
			return discoveryTarget{}, fmt.Errorf("decrypt persisted discovery key: %w", app_errors.ErrInternalServer)
		}
		plaintextKeys = append(plaintextKeys, plaintext)
	}

	return discoveryTarget{
		baseURL: group.UpstreamURL, protocols: protocols,
		keys: plaintextKeys, headerRules: compiledGroup.HeaderRules,
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
