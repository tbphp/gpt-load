package control

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/catalog"
	"gpt-load/internal/platform/canonicaljson"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

type ClientModelProfileDTO struct {
	ClientModel  string                       `json:"client_model"`
	Automatic    catalog.ClientModelProfile   `json:"automatic"`
	Overrides    catalog.ClientModelOverrides `json:"overrides"`
	Effective    catalog.ClientModelProfile   `json:"effective"`
	HasOverrides bool                         `json:"has_overrides"`
}

type ClientModelProfileUpdateRequest struct {
	ClientModel string                        `json:"client_model"`
	Overrides   *catalog.ClientModelOverrides `json:"overrides"`
}

func (s *Service) GetClientModelProfile(ctx context.Context, model string) (ClientModelProfileDTO, error) {
	if err := validateClientModelName(model); err != nil {
		return ClientModelProfileDTO{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ClientModelProfileDTO{}, err
	}

	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	configured, err := clientModelIsConfigured(ctx, s.db, model)
	if err != nil {
		return ClientModelProfileDTO{}, err
	}
	if !configured {
		return ClientModelProfileDTO{}, app_errors.ErrResourceNotFound
	}
	var snapshot *state.ConfigSnapshot
	if s.manager != nil {
		snapshot = s.manager.Current()
	}
	overrides := catalog.ClientModelOverrides{}
	if snapshot != nil {
		overrides = snapshot.ClientModelOverrides[model].Clone()
	}
	automatic, effective, err := catalog.ResolveClientModelProfile(model, overrides)
	if err != nil {
		return ClientModelProfileDTO{}, err
	}
	return ClientModelProfileDTO{
		ClientModel:  model,
		Automatic:    automatic,
		Overrides:    overrides,
		Effective:    effective,
		HasOverrides: !overrides.IsEmpty(),
	}, nil
}

func (s *Service) UpdateClientModelProfile(
	ctx context.Context,
	request ClientModelProfileUpdateRequest,
) (ClientModelProfileDTO, error) {
	if err := validateClientModelName(request.ClientModel); err != nil {
		return ClientModelProfileDTO{}, err
	}
	if request.Overrides == nil {
		return ClientModelProfileDTO{}, app_errors.ErrValidation
	}
	overrides := request.Overrides.Clone()
	if err := overrides.Validate(); err != nil {
		return ClientModelProfileDTO{}, app_errors.ErrValidation
	}
	var encoded models.JSON
	if !overrides.IsEmpty() {
		value, err := canonicaljson.Marshal(overrides)
		if err != nil {
			return ClientModelProfileDTO{}, fmt.Errorf("encode client model overrides: %w", err)
		}
		encoded = models.JSON(value)
	}

	modelHash := models.ClientModelHash(request.ClientModel)
	_, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		configured, err := clientModelIsConfigured(ctx, tx, request.ClientModel)
		if err != nil {
			return err
		}
		if !configured {
			return app_errors.ErrResourceNotFound
		}
		if overrides.IsEmpty() {
			if err := tx.Where("model_hash = ?", modelHash).
				Delete(&models.ClientModelOverride{}).Error; err != nil {
				return app_errors.ParseDBError(err)
			}
			return nil
		}
		row := models.ClientModelOverride{
			ModelHash: modelHash, ClientModel: request.ClientModel, Overrides: encoded,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "model_hash"}},
			DoUpdates: clause.AssignmentColumns([]string{"client_model", "overrides"}),
		}).Create(&row).Error; err != nil {
			return app_errors.ParseDBError(err)
		}
		return nil
	}, nil)
	if err != nil {
		return ClientModelProfileDTO{}, err
	}
	return s.GetClientModelProfile(ctx, request.ClientModel)
}

func validateClientModelName(model string) error {
	if model == "" || !utf8.ValidString(model) || strings.TrimSpace(model) != model {
		return app_errors.ErrValidation
	}
	return nil
}

func clientModelIsConfigured(ctx context.Context, db *gorm.DB, model string) (bool, error) {
	var groups []models.Group
	if err := db.WithContext(ctx).Select("id", "models").Order("id ASC").Find(&groups).Error; err != nil {
		return false, fmt.Errorf("load configured client models: %w", app_errors.ParseDBError(err))
	}
	for _, group := range groups {
		var configured []GroupModel
		if err := decodeGroupDiscoveryJSON(group.Models, &configured); err != nil {
			return false, fmt.Errorf("decode group %d models: %w", group.ID, app_errors.ErrInternalServer)
		}
		for _, configuredModel := range configured {
			clientModel := strings.TrimSpace(configuredModel.ID)
			if alias := strings.TrimSpace(configuredModel.Alias); alias != "" {
				clientModel = alias
			}
			if clientModel == model {
				return true, nil
			}
		}
	}
	return false, nil
}
