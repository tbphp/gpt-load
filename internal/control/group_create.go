package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
)

type GroupCreateRequest struct {
	Name                *string               `json:"name"`
	ChannelID           channel.ID            `json:"channel_id"`
	ConnectionType      models.ConnectionType `json:"connection_type"`
	Params              json.RawMessage       `json:"params"`
	Models              optionalGroupModels   `json:"models"`
	Credentials         string                `json:"credentials"`
	StagedCredentialIDs []string              `json:"staged_credential_ids"`
	ConfirmSameTarget   bool                  `json:"confirm_same_target"`
}

type GroupCreateResult struct {
	GroupID               uint   `json:"group_id"`
	GroupName             string `json:"group_name"`
	CredentialsAdded      int    `json:"credentials_added"`
	CredentialsDuplicated int    `json:"credentials_duplicated"`
}

type ExistingGroupSummary struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type SameTargetConflictData struct {
	Groups []ExistingGroupSummary `json:"groups"`
}

type normalizedGroupCreate struct {
	channelID           channel.ID
	connectionType      models.ConnectionType
	params              models.JSON
	defaultName         string
	hostname            string
	explicitName        *string
	models              []GroupModel
	encodedOverrides    models.JSON
	credentials         normalizedCredentials
	stagedCredentialIDs []string
	confirmSameTarget   bool
}

func (s *Service) CreateGroup(ctx context.Context, request GroupCreateRequest) (GroupCreateResult, error) {
	normalized, err := s.normalizeGroupCreate(ctx, request)
	if err != nil {
		return GroupCreateResult{}, err
	}
	if isLiteralPrivateHost(normalized.hostname) {
		utils.LogPlaneBestEffort(
			logrus.StandardLogger(),
			logrus.WarnLevel,
			utils.LogPlaneControl,
			logrus.Fields{"host": normalized.hostname},
			"Creating channel group with a private or local host",
		)
	}

	result := GroupCreateResult{}
	requestedEntries := make([]state.CredentialEntry, 0, len(normalized.credentials.candidates)+len(normalized.stagedCredentialIDs))
	_, err = s.writeGroupConfig(ctx, func(tx *gorm.DB) error {
		if !normalized.confirmSameTarget {
			conflicts, err := findGroupsByTarget(tx, normalized.channelID, normalized.connectionType, normalized.params)
			if err != nil {
				return err
			}
			if len(conflicts) > 0 {
				return app_errors.NewAPIErrorWithData(
					app_errors.ErrChannelTargetConflict,
					SameTargetConflictData{Groups: conflicts},
				)
			}
		}

		name, err := resolveGroupCreateName(tx, normalized.explicitName, normalized.defaultName)
		if err != nil {
			return err
		}
		encodedModels, err := json.Marshal(normalized.models)
		if err != nil {
			return fmt.Errorf("encode group models: %w", err)
		}
		group := models.Group{
			Name:           name,
			ChannelID:      string(normalized.channelID),
			ConnectionType: normalized.connectionType,
			Params:         append(models.JSON(nil), normalized.params...),
			Models:         models.JSON(encodedModels),
			Overrides:      normalized.encodedOverrides,
			Enabled:        true,
		}
		if err := tx.Create(&group).Error; err != nil {
			return app_errors.ParseDBError(err)
		}

		result.GroupID = group.ID
		result.GroupName = group.Name
		if normalized.connectionType == models.ConnectionTypeSubscription {
			result.CredentialsAdded, err = s.consumeCredentialStages(
				tx,
				group.ID,
				normalized.channelID,
				normalized.connectionType,
				normalized.stagedCredentialIDs,
			)
		} else {
			result.CredentialsAdded, result.CredentialsDuplicated, err =
				s.persistCredentials(tx, group.ID, normalized.credentials)
		}
		if err != nil {
			return err
		}
		requestedEntries, err = stateloader.BuildGroupCredentialEntries(ctx, tx, group.ID)
		return err
	}, func() error {
		_, reconcileErr := s.reconcileRegistryGroup(result.GroupID, requestedEntries)
		return reconcileErr
	})
	if err != nil {
		return GroupCreateResult{}, err
	}
	if len(normalized.models) > 0 && s.catalogSync != nil {
		s.catalogSync.RequestGroupSync()
	}
	return result, nil
}

func (s *Service) normalizeGroupCreate(
	ctx context.Context,
	request GroupCreateRequest,
) (normalizedGroupCreate, error) {
	if s == nil || s.channelRegistry == nil || request.ChannelID == "" {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}
	connectionType := request.ConnectionType
	if connectionType == "" {
		connectionType = models.ConnectionTypeAPIKey
	}
	if !s.channelRegistry.SupportsConnectionType(request.ChannelID, string(connectionType)) {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}
	if connectionType == models.ConnectionTypeAPIKey {
		if len(request.StagedCredentialIDs) != 0 {
			return normalizedGroupCreate{}, app_errors.ErrValidation
		}
	} else if strings.TrimSpace(request.Credentials) != "" || len(request.StagedCredentialIDs) == 0 {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}
	params, err := s.channelRegistry.ValidateParams(request.ChannelID, request.Params)
	if err != nil {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}
	if connectionType == models.ConnectionTypeSubscription && string(params.CanonicalJSON()) != "{}" {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}
	descriptor, ok := s.channelRegistry.Get(request.ChannelID)
	if !ok {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}
	explicitName, err := normalizeGroupName(request.Name)
	if err != nil {
		return normalizedGroupCreate{}, err
	}
	if !request.Models.Set {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}
	groupModels, err := normalizeGroupModels(request.Models.Values)
	if err != nil {
		return normalizedGroupCreate{}, err
	}
	credentials := normalizedCredentials{}
	stagedCredentialIDs := []string(nil)
	if connectionType == models.ConnectionTypeAPIKey {
		credentials, err = s.normalizeCredentials(request.ChannelID, request.Credentials)
		if err != nil {
			return normalizedGroupCreate{}, err
		}
	} else {
		stagedCredentialIDs, err = normalizeCredentialStageIDs(request.StagedCredentialIDs)
		if err != nil {
			return normalizedGroupCreate{}, err
		}
	}

	runtimeModels := make([]state.ModelConfig, 0, len(groupModels))
	for _, model := range groupModels {
		runtimeModels = append(runtimeModels, state.ModelConfig{ID: model.ID, Alias: model.Alias})
	}
	systemSettings, err := stateloader.LoadSystemSettings(ctx, s.db)
	if parentErr := ctx.Err(); parentErr != nil {
		return normalizedGroupCreate{}, parentErr
	}
	if err != nil {
		return normalizedGroupCreate{}, app_errors.ParseDBError(err)
	}
	canonicalParams := params.CanonicalJSON()
	_, err = state.Compile(state.CompileInput{
		SystemSettings:  systemSettings,
		ChannelRegistry: s.channelRegistry,
		Groups: []state.GroupConfig{{
			ID: 1, Name: "candidate", ChannelID: request.ChannelID,
			ConnectionType: string(connectionType), Params: canonicalParams,
			Models: runtimeModels, Settings: config.Settings{}, Enabled: true,
		}},
	})
	if err != nil {
		return normalizedGroupCreate{}, app_errors.ErrValidation
	}

	defaultName := descriptor.Name
	hostname := ""
	if baseURL, exists := params.Value("base_url"); exists {
		_, hostname, err = normalizeUpstreamBaseURL(baseURL)
		if err != nil {
			return normalizedGroupCreate{}, app_errors.ErrValidation
		}
		defaultName = hostname
	}
	return normalizedGroupCreate{
		channelID:           request.ChannelID,
		connectionType:      connectionType,
		params:              models.JSON(canonicalParams),
		defaultName:         defaultName,
		hostname:            hostname,
		explicitName:        explicitName,
		models:              groupModels,
		encodedOverrides:    models.JSON(`{}`),
		credentials:         credentials,
		stagedCredentialIDs: stagedCredentialIDs,
		confirmSameTarget:   request.ConfirmSameTarget,
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
	for key, value := range normalized {
		normalized[key] = canonicalizeGroupSettingNumbers(value)
	}
	encoded, err = json.Marshal(normalized)
	if err != nil {
		return nil, nil, app_errors.ErrValidation
	}
	return normalized, models.JSON(encoded), nil
}

func canonicalizeGroupSettingNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		parsed, ok := new(big.Rat).SetString(typed.String())
		if ok && parsed.IsInt() && parsed.Num().IsInt64() {
			return parsed.Num().Int64()
		}
		return typed
	case map[string]any:
		for key, nested := range typed {
			typed[key] = canonicalizeGroupSettingNumbers(nested)
		}
		return typed
	case []any:
		for index, nested := range typed {
			typed[index] = canonicalizeGroupSettingNumbers(nested)
		}
		return typed
	default:
		return value
	}
}

func findGroupsByTarget(
	tx *gorm.DB,
	channelID channel.ID,
	connectionType models.ConnectionType,
	params models.JSON,
) ([]ExistingGroupSummary, error) {
	type targetRow struct {
		ID     uint
		Name   string
		Params models.JSON
	}
	var rows []targetRow
	if err := tx.Model(&models.Group{}).
		Select("id", "name", "params").
		Where("channel_id = ? AND connection_type = ?", string(channelID), string(connectionType)).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	groups := make([]ExistingGroupSummary, 0, len(rows))
	for _, row := range rows {
		if !bytes.Equal(bytes.TrimSpace(row.Params), bytes.TrimSpace(params)) {
			continue
		}
		groups = append(groups, ExistingGroupSummary{ID: row.ID, Name: row.Name})
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
