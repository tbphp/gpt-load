package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type GroupCreateRequest struct {
	Name                   *string             `json:"name"`
	UpstreamURL            string              `json:"upstream_url"`
	Protocols              []protocol.Protocol `json:"protocols"`
	Models                 optionalGroupModels `json:"models"`
	Config                 config.Settings     `json:"config"`
	Keys                   string              `json:"keys"`
	ConfirmSameUpstreamURL bool                `json:"confirm_same_upstream_url"`
}

type GroupCreateResult struct {
	GroupID        uint         `json:"group_id"`
	GroupName      string       `json:"group_name"`
	KeysAdded      int          `json:"keys_added"`
	KeysDuplicated int          `json:"keys_duplicated"`
	Models         []GroupModel `json:"models"`
}

type ExistingGroupSummary struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type UpstreamURLConflictData struct {
	Groups []ExistingGroupSummary `json:"groups"`
}

type normalizedGroupCreate struct {
	upstreamURL            string
	hostname               string
	protocols              []protocol.Protocol
	explicitName           *string
	models                 []GroupModel
	encodedConfig          models.JSON
	keys                   normalizedUpstreamKeys
	confirmSameUpstreamURL bool
}

func (s *Service) CreateGroup(ctx context.Context, request GroupCreateRequest) (GroupCreateResult, error) {
	normalized, err := s.normalizeGroupCreate(request)
	if err != nil {
		return GroupCreateResult{}, err
	}
	if isLiteralPrivateHost(normalized.hostname) {
		logrus.WithField("host", normalized.hostname).
			Warn("Creating upstream group with a private or local host")
	}

	result := GroupCreateResult{
		Models: append(make([]GroupModel, 0, len(normalized.models)), normalized.models...),
	}
	requestedEntries := make([]state.KeyEntry, 0, len(normalized.keys.candidates))
	_, err = s.writeConfig(ctx, func(tx *gorm.DB) error {
		if !normalized.confirmSameUpstreamURL {
			conflicts, err := findGroupsByUpstreamURL(tx, normalized.upstreamURL)
			if err != nil {
				return err
			}
			if len(conflicts) > 0 {
				return app_errors.NewAPIErrorWithData(
					app_errors.ErrUpstreamURLConflict,
					UpstreamURLConflictData{Groups: conflicts},
				)
			}
		}

		name, err := resolveGroupCreateName(tx, normalized.explicitName, normalized.hostname)
		if err != nil {
			return err
		}
		encodedProtocols, err := json.Marshal(normalized.protocols)
		if err != nil {
			return fmt.Errorf("encode group protocols: %w", err)
		}
		encodedModels, err := json.Marshal(normalized.models)
		if err != nil {
			return fmt.Errorf("encode group models: %w", err)
		}
		group := models.Group{
			Name:        name,
			UpstreamURL: normalized.upstreamURL,
			Protocols:   models.JSON(encodedProtocols),
			Models:      models.JSON(encodedModels),
			Config:      normalized.encodedConfig,
			Enabled:     true,
		}
		if err := tx.Create(&group).Error; err != nil {
			return app_errors.ParseDBError(err)
		}

		result.GroupID = group.ID
		result.GroupName = group.Name
		requestedEntries, result.KeysAdded, result.KeysDuplicated, err =
			s.persistUpstreamKeys(tx, group.ID, normalized.keys)
		if err != nil {
			return err
		}
		if err := state.ValidateKeyEntries(requestedEntries); err != nil {
			return fmt.Errorf("validate created group keys: %w", err)
		}
		return nil
	}, func() error {
		return s.applyMissingRegistryKeys(result.GroupID, requestedEntries)
	})
	if err != nil {
		return GroupCreateResult{}, err
	}
	return result, nil
}

func (s *Service) normalizeGroupCreate(request GroupCreateRequest) (normalizedGroupCreate, error) {
	upstreamURL, hostname, err := normalizeUpstreamBaseURL(request.UpstreamURL)
	if err != nil {
		return normalizedGroupCreate{}, err
	}
	protocols, err := normalizeGroupProtocols(request.Protocols)
	if err != nil {
		return normalizedGroupCreate{}, err
	}
	explicitName, err := normalizeGroupName(request.Name)
	if err != nil {
		return normalizedGroupCreate{}, err
	}
	groupModels := make([]GroupModel, 0)
	if request.Models.Set {
		groupModels, err = normalizeGroupModels(request.Models.Values)
		if err != nil {
			return normalizedGroupCreate{}, err
		}
	}
	settings, encodedConfig, err := normalizeGroupSettings(request.Config)
	if err != nil {
		return normalizedGroupCreate{}, err
	}
	keys, err := s.normalizeUpstreamKeys(request.Keys)
	if err != nil {
		return normalizedGroupCreate{}, err
	}

	runtimeModels := make([]state.ModelConfig, 0, len(groupModels))
	for _, model := range groupModels {
		runtimeModels = append(runtimeModels, state.ModelConfig{ID: model.ID, Alias: model.Alias})
	}
	_, err = state.Compile(state.CompileInput{Groups: []state.GroupConfig{{
		ID: 1, Name: "candidate", UpstreamURL: upstreamURL,
		Protocols: protocols, Models: runtimeModels, Settings: settings, Enabled: true,
	}}})
	if err != nil {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}

	return normalizedGroupCreate{
		upstreamURL: upstreamURL, hostname: hostname, protocols: protocols,
		explicitName: explicitName, models: groupModels,
		encodedConfig: encodedConfig, keys: keys,
		confirmSameUpstreamURL: request.ConfirmSameUpstreamURL,
	}, nil
}

func normalizeGroupSettings(settings config.Settings) (config.Settings, models.JSON, error) {
	if settings == nil {
		settings = make(config.Settings)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, nil, app_errors.ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	normalized := make(config.Settings)
	if err := decoder.Decode(&normalized); err != nil {
		return nil, nil, app_errors.ErrValidation
	}
	return normalized, models.JSON(encoded), nil
}

func findGroupsByUpstreamURL(tx *gorm.DB, upstreamURL string) ([]ExistingGroupSummary, error) {
	groups := make([]ExistingGroupSummary, 0)
	if err := tx.Model(&models.Group{}).
		Select("id", "name").
		Where("upstream_url = ?", upstreamURL).
		Order("id ASC").
		Scan(&groups).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	return groups, nil
}

func resolveGroupCreateName(tx *gorm.DB, explicit *string, hostname string) (string, error) {
	if explicit != nil {
		return *explicit, nil
	}
	for suffix := 1; ; suffix++ {
		candidate := hostname
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", hostname, suffix)
		}
		var count int64
		if err := tx.Model(&models.Group{}).Where("name = ?", candidate).Count(&count).Error; err != nil {
			return "", app_errors.ParseDBError(err)
		}
		if count == 0 {
			return candidate, nil
		}
	}
}
