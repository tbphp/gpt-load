package control

import (
	"context"
	"strings"
	"unicode"

	"gorm.io/gorm"

	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

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
