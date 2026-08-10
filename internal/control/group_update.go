package control

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
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
	var storedModels []GroupModel
	if err := decodeGroupDiscoveryJSON(group.Models, &storedModels); err != nil {
		return state.GroupConfig{}, err
	}
	settings := make(config.Settings)
	if len(group.Overrides) > 0 {
		if err := decodeGroupDiscoveryJSON(group.Overrides, &settings); err != nil {
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
	result := state.GroupConfig{
		ID:              group.ID,
		Name:            group.Name,
		ChannelID:       channel.ID(group.ChannelID),
		Params:          append(json.RawMessage(nil), group.Params...),
		ValidationModel: validationModel,
		Models:          runtimeModels,
		Settings:        settings,
		WeightManual:    cloneInt(group.WeightManual), Enabled: group.Enabled,
	}
	return result, nil
}

func validateGroupRowCandidate(
	ctx context.Context,
	tx *gorm.DB,
	group models.Group,
	registry *channel.Registry,
) error {
	candidate, err := mapGroupRowToState(group)
	if err != nil {
		return err
	}
	systemSettings, err := stateloader.LoadSystemSettings(ctx, tx)
	if err != nil {
		return err
	}
	_, err = state.Compile(state.CompileInput{
		SystemSettings:  systemSettings,
		ChannelRegistry: registry,
		Groups:          []state.GroupConfig{candidate},
	})
	return err
}
