// Package loader adapts persisted storage rows into runtime state.
package loader

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"

	"github.com/sirupsen/logrus"
)

type Loader struct {
	db              *gorm.DB
	manager         *state.Manager
	registry        *state.CredentialRegistry
	channelRegistry *channel.Registry
	encryption      encryption.Service
}

// NewWithCredentialValidation creates the production loader that verifies
// encrypted credentials before publishing any runtime state.
func NewWithCredentialValidation(
	db *gorm.DB,
	manager *state.Manager,
	registry *state.CredentialRegistry,
	channelRegistry *channel.Registry,
	encryptionService encryption.Service,
) *Loader {
	loader := New(db, manager, registry, channelRegistry)
	loader.encryption = encryptionService
	return loader
}

type compileRows struct {
	settings    []models.SystemSetting
	groups      []models.Group
	credentials []models.Credential
	accessKeys  []models.AccessKey
}

type modelDTO struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

type filterDTO struct {
	Groups    []uint              `json:"groups"`
	Protocols []protocol.Protocol `json:"protocols"`
	Models    []string            `json:"models"`
}

func (f filterDTO) toState() state.FilterSet {
	filters := state.FilterSet{}
	if len(f.Groups) > 0 {
		filters.Groups = make(map[uint]struct{}, len(f.Groups))
		for _, id := range f.Groups {
			filters.Groups[id] = struct{}{}
		}
	}
	if len(f.Protocols) > 0 {
		filters.Protocols = make(map[protocol.Protocol]struct{}, len(f.Protocols))
		for _, p := range f.Protocols {
			filters.Protocols[p] = struct{}{}
		}
	}
	if len(f.Models) > 0 {
		filters.Models = make(map[string]struct{}, len(f.Models))
		for _, model := range f.Models {
			filters.Models[model] = struct{}{}
		}
	}
	return filters
}

func New(
	db *gorm.DB,
	manager *state.Manager,
	registry *state.CredentialRegistry,
	registries ...*channel.Registry,
) *Loader {
	return &Loader{
		db: db, manager: manager, registry: registry,
		channelRegistry: selectChannelRegistry(registries),
	}
}

func (l *Loader) Load(ctx context.Context) error {
	input, entries, err := l.read(ctx)
	if err != nil {
		return fmt.Errorf("read runtime state: %w", err)
	}
	if err := state.ValidateCredentialEntries(entries); err != nil {
		return fmt.Errorf("validate credentials: %w", err)
	}
	if err := l.validatePersistedCredentials(input, entries); err != nil {
		return err
	}
	snapshot, err := l.manager.Publish(input)
	if err != nil {
		return fmt.Errorf("publish config snapshot: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"event":    "startup.config_snapshot_publish",
		"revision": snapshot.Revision,
	}).Info("config snapshot published")
	if err := l.registry.ReplaceCredentials(entries); err != nil {
		return fmt.Errorf("replace credential registry: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"event":       "startup.credential_registry_load",
		"credentials": len(entries),
	}).Info("credential registry loaded")
	return nil
}

func (l *Loader) validatePersistedCredentials(
	input state.CompileInput,
	entries []state.CredentialEntry,
) error {
	if l.encryption == nil {
		return nil
	}
	type validationTarget struct {
		channelID      channel.ID
		connectionType string
	}
	targets := make(map[uint]validationTarget, len(input.Groups))
	for _, group := range input.Groups {
		targets[group.ID] = validationTarget{channelID: group.ChannelID, connectionType: group.ConnectionType}
	}
	for _, entry := range entries {
		target, exists := targets[entry.GroupID]
		if !exists {
			return fmt.Errorf("validate credential %d for group %d: execution target is missing", entry.ID, entry.GroupID)
		}
		plaintext, err := l.encryption.Decrypt(entry.EncryptedValue)
		if err != nil {
			return fmt.Errorf("validate credential %d for group %d: decrypt failed", entry.ID, entry.GroupID)
		}
		raw := []byte(plaintext)
		plaintext = ""
		var canonical []byte
		if strings.TrimSpace(target.connectionType) == string(models.ConnectionTypeSubscription) {
			credential, parseErr := cpaembedded.ParseCodexCredentialJSON(raw)
			if parseErr == nil {
				canonical, parseErr = json.Marshal(credential)
			}
			clear(raw)
			if parseErr != nil {
				return fmt.Errorf("validate credential %d for group %d: stored shape is invalid", entry.ID, entry.GroupID)
			}
		} else {
			credential, validateErr := l.channelRegistry.ValidateCredential(target.channelID, raw)
			clear(raw)
			if validateErr != nil {
				return fmt.Errorf("validate credential %d for group %d: stored shape is invalid", entry.ID, entry.GroupID)
			}
			canonical = credential.CanonicalJSON()
		}
		fingerprint := l.encryption.Hash(string(canonical))
		clear(canonical)
		if subtle.ConstantTimeCompare([]byte(fingerprint), []byte(entry.Fingerprint)) != 1 {
			return fmt.Errorf("validate credential %d for group %d: fingerprint mismatch", entry.ID, entry.GroupID)
		}
	}
	return nil
}

func queryCompileRows(ctx context.Context, db *gorm.DB) (compileRows, error) {
	db = db.WithContext(ctx)
	var rows compileRows
	if err := db.
		Order(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "key"}}}}).
		Find(&rows.settings).Error; err != nil {
		return compileRows{}, fmt.Errorf("query system settings: %w", err)
	}
	if err := db.Order("id ASC").Find(&rows.groups).Error; err != nil {
		return compileRows{}, fmt.Errorf("query groups: %w", err)
	}
	if err := db.
		Select("id", "group_id", "fingerprint", "identity_fingerprint", "secret_version", "status", "weight_manual").
		Order("id ASC").
		Find(&rows.credentials).Error; err != nil {
		return compileRows{}, fmt.Errorf("query credential metadata: %w", err)
	}
	if err := db.
		Select("id", "name", "key_hash", "status", "filters", "rpm_limit").
		Order("id ASC").
		Find(&rows.accessKeys).Error; err != nil {
		return compileRows{}, fmt.Errorf("query access keys: %w", err)
	}
	return rows, nil
}

func queryCredentials(ctx context.Context, db *gorm.DB) ([]models.Credential, error) {
	var rows []models.Credential
	if err := db.WithContext(ctx).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query credentials: %w", err)
	}
	return rows, nil
}

// BuildCompileInput maps persisted configuration rows into a runtime compiler input.
func BuildCompileInput(
	ctx context.Context,
	db *gorm.DB,
	registries ...*channel.Registry,
) (state.CompileInput, error) {
	rows, err := queryCompileRows(ctx, db)
	if err != nil {
		return state.CompileInput{}, err
	}
	input, err := mapSystemAndGroups(rows)
	if err != nil {
		return state.CompileInput{}, err
	}
	input.ChannelRegistry = selectChannelRegistry(registries)
	input.Credentials = mapCredentialConfigs(rows.credentials, rows.groups)
	input.AccessKeys, err = mapAccessKeys(rows.accessKeys)
	if err != nil {
		return state.CompileInput{}, err
	}
	return input, nil
}

// BuildGroupCredentialEntries maps persisted credentials for one group.
// Runtime health fields are initialized to their durable baseline.
func BuildGroupCredentialEntries(
	ctx context.Context,
	db *gorm.DB,
	groupID uint,
) ([]state.CredentialEntry, error) {
	if groupID == 0 {
		return nil, fmt.Errorf("group id is required")
	}
	var group models.Group
	if err := db.WithContext(ctx).
		Model(&models.Group{}).
		Select("id", "channel_id", "connection_type", "params").
		Where("id = ?", groupID).
		Take(&group).Error; err != nil {
		return nil, fmt.Errorf("query group %d: %w", groupID, err)
	}
	var rows []models.Credential
	if err := db.WithContext(ctx).
		Where("group_id = ?", groupID).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query group %d credentials: %w", groupID, err)
	}
	entries := mapCredentials(rows, []models.Group{group})
	if err := state.ValidateCredentialEntries(entries); err != nil {
		return nil, fmt.Errorf("validate group %d credentials: %w", groupID, err)
	}
	return entries, nil
}

// BuildCredentialEntries maps all persisted credential rows into the exact
// runtime registry representation. It is used to converge runtime state after
// a committed control-plane write could not publish its incremental update.
func BuildCredentialEntries(ctx context.Context, db *gorm.DB) ([]state.CredentialEntry, error) {
	var groups []models.Group
	if err := db.WithContext(ctx).
		Model(&models.Group{}).
		Select("id", "channel_id", "connection_type", "params").
		Order("id ASC").
		Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("query credential targets: %w", err)
	}
	rows, err := queryCredentials(ctx, db)
	if err != nil {
		return nil, err
	}
	entries := mapCredentials(rows, groups)
	if err := state.ValidateCredentialEntries(entries); err != nil {
		return nil, fmt.Errorf("validate credentials: %w", err)
	}
	return entries, nil
}

func (l *Loader) read(ctx context.Context) (state.CompileInput, []state.CredentialEntry, error) {
	rows, err := queryCompileRows(ctx, l.db)
	if err != nil {
		return state.CompileInput{}, nil, err
	}
	input, err := mapSystemAndGroups(rows)
	if err != nil {
		return state.CompileInput{}, nil, err
	}
	input.ChannelRegistry = l.channelRegistry
	input.Credentials = mapCredentialConfigs(rows.credentials, rows.groups)
	input.AccessKeys, err = mapAccessKeys(rows.accessKeys)
	if err != nil {
		return state.CompileInput{}, nil, err
	}
	credentials, err := queryCredentials(ctx, l.db)
	if err != nil {
		return state.CompileInput{}, nil, err
	}
	return input, mapCredentials(credentials, rows.groups), nil
}

func selectChannelRegistry(registries []*channel.Registry) *channel.Registry {
	for _, registry := range registries {
		if registry != nil {
			return registry
		}
	}
	return channel.NewRegistry()
}

func decodeJSON(raw models.JSON, target any) error {
	return decodeJSONDocument(raw, target, false)
}

func decodeFilterJSON(raw models.JSON, target *filterDTO) error {
	return decodeJSONDocument(raw, target, true)
}

func decodeJSONDocument(raw models.JSON, target any, disallowUnknownFields bool) error {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
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

func decodeSettingValue(raw string) (any, error) {
	if !json.Valid([]byte(raw)) {
		return raw, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func isInternalSystemSetting(key string) bool {
	return strings.HasPrefix(key, models.InternalSystemSettingPrefix)
}

// LoadSystemSettings reads only the persisted system settings used to compile a draft Group.
func LoadSystemSettings(ctx context.Context, db *gorm.DB) (config.Settings, error) {
	var rows []models.SystemSetting
	if err := db.WithContext(ctx).
		Order(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "key"}}}}).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query system settings: %w", err)
	}
	return MapSystemSettings(rows)
}

// MapSystemSettings maps persisted system setting rows without accessing the database.
func MapSystemSettings(rows []models.SystemSetting) (config.Settings, error) {
	settings := make(config.Settings, len(rows))
	for _, row := range rows {
		if isInternalSystemSetting(row.Key) {
			continue
		}
		value, err := decodeSettingValue(row.Value)
		if err != nil {
			return nil, fmt.Errorf("decode system setting %q: %w", row.Key, err)
		}
		settings[row.Key] = value
	}
	return settings, nil
}

func mapSystemAndGroups(rows compileRows) (state.CompileInput, error) {
	input := state.CompileInput{
		SystemSettings: make(config.Settings, len(rows.settings)),
		Groups:         make([]state.GroupConfig, 0, len(rows.groups)),
	}
	for _, row := range rows.settings {
		if isInternalSystemSetting(row.Key) {
			continue
		}
		value, err := decodeSettingValue(row.Value)
		if err != nil {
			return state.CompileInput{}, fmt.Errorf("decode system setting %q: %w", row.Key, err)
		}
		input.SystemSettings[row.Key] = value
	}
	for _, row := range rows.groups {
		var storedModels []modelDTO
		if err := decodeJSON(row.Models, &storedModels); err != nil {
			return state.CompileInput{}, fmt.Errorf("decode group %d models: %w", row.ID, err)
		}
		settings := make(config.Settings)
		if err := decodeJSON(row.Overrides, &settings); err != nil {
			return state.CompileInput{}, fmt.Errorf("decode group %d overrides: %w", row.ID, err)
		}
		validationModel := ""
		if row.ValidationModel != nil {
			validationModel = strings.TrimSpace(*row.ValidationModel)
		}

		runtimeModels := make([]state.ModelConfig, 0, len(storedModels))
		for _, model := range storedModels {
			runtimeModels = append(runtimeModels, state.ModelConfig{ID: model.ID, Alias: model.Alias})
		}
		group := state.GroupConfig{
			ID:              row.ID,
			Name:            row.Name,
			ChannelID:       channel.ID(row.ChannelID),
			ConnectionType:  string(row.ConnectionType),
			Params:          append(json.RawMessage(nil), row.Params...),
			ValidationModel: validationModel,
			Models:          runtimeModels,
			Settings:        settings,
			WeightManual:    cloneWeight(row.WeightManual),
			Enabled:         row.Enabled,
		}
		input.Groups = append(input.Groups, group)
	}
	return input, nil
}

func mapAccessKeys(rows []models.AccessKey) ([]state.AccessKeyConfig, error) {
	result := make([]state.AccessKeyConfig, 0, len(rows))
	for _, row := range rows {
		var filters filterDTO
		if err := decodeFilterJSON(row.Filters, &filters); err != nil {
			return nil, fmt.Errorf("decode access key %d filters: %w", row.ID, err)
		}
		result = append(result, state.AccessKeyConfig{
			ID: row.ID, Name: row.Name, KeyHash: row.KeyHash,
			Status: state.AccessKeyStatus(row.Status), Filters: filters.toState(), RPMLimit: row.RPMLimit,
		})
	}
	return result, nil
}

func mapCredentialConfigs(
	rows []models.Credential,
	groups []models.Group,
) []state.CredentialConfig {
	targets := credentialTargets(groups)
	result := make([]state.CredentialConfig, 0, len(rows))
	for _, row := range rows {
		target := targets[row.GroupID]
		result = append(result, state.CredentialConfig{
			ID: row.ID, GroupID: row.GroupID, WeightManual: cloneWeight(row.WeightManual),
			Status:  state.CredentialStatus(row.Status),
			Version: credentialVersion(row.SecretVersion),
			IdentityGeneration: CredentialIdentityGeneration(
				row.IdentityFingerprint,
				target.channelID,
				target.connectionType,
				target.params,
			),
			Fingerprint: row.Fingerprint,
		})
	}
	return result
}

func mapCredentials(rows []models.Credential, groups []models.Group) []state.CredentialEntry {
	targets := credentialTargets(groups)
	result := make([]state.CredentialEntry, 0, len(rows))
	for _, row := range rows {
		target := targets[row.GroupID]
		result = append(result, state.CredentialEntry{
			ID: row.ID, GroupID: row.GroupID,
			Version: credentialVersion(row.SecretVersion),
			IdentityGeneration: CredentialIdentityGeneration(
				row.IdentityFingerprint,
				target.channelID,
				target.connectionType,
				target.params,
			),
			Fingerprint: row.Fingerprint, WeightManual: cloneWeight(row.WeightManual),
			WeightAuto: state.DefaultWeight,
			Status:     state.CredentialStatus(row.Status), AuthState: state.CredentialAuthState(row.AuthState), EncryptedValue: row.Data,
		})
	}
	return result
}

func credentialVersion(secretVersion uint64) uint64 {
	if secretVersion < 1 {
		return 1
	}
	return secretVersion
}

type credentialTarget struct {
	channelID      string
	connectionType string
	params         json.RawMessage
}

func credentialTargets(groups []models.Group) map[uint]credentialTarget {
	targets := make(map[uint]credentialTarget, len(groups))
	for _, group := range groups {
		targets[group.ID] = credentialTarget{
			channelID:      group.ChannelID,
			connectionType: string(group.ConnectionType),
			params:         append(json.RawMessage(nil), bytes.TrimSpace(group.Params)...),
		}
	}
	return targets
}

// CredentialIdentityGeneration binds durable credential data to its execution
// target so target changes cannot inherit stale runtime health or checkpoints.
func CredentialIdentityGeneration(
	identityFingerprint string,
	channelID string,
	connectionType string,
	params json.RawMessage,
) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(identityFingerprint))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(channelID))
	_, _ = hasher.Write([]byte{0})
	if strings.TrimSpace(connectionType) == "" {
		connectionType = string(models.ConnectionTypeAPIKey)
	}
	_, _ = hasher.Write([]byte(connectionType))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(bytes.TrimSpace(params))
	generation := hasher.Sum64()
	if generation == 0 {
		return 1
	}
	return generation
}

func cloneWeight(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
