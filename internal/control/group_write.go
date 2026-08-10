package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"unicode"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/platform/epochms"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

const maxCredentialLines = 1000

type GroupModel struct {
	ID           string `json:"id"`
	Alias        string `json:"alias"`
	AliasEnabled bool   `json:"-"`
}

func (model *GroupModel) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID           string `json:"id"`
		Alias        string `json:"alias"`
		AliasEnabled bool   `json:"alias_enabled"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return fmt.Errorf("decode group model: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode group model: trailing JSON value")
		}
		return fmt.Errorf("decode group model trailing value: %w", err)
	}
	model.ID = wire.ID
	model.Alias = wire.Alias
	model.AliasEnabled = wire.AliasEnabled
	return nil
}

type credentialCandidate struct {
	canonical   json.RawMessage
	fingerprint string
}

type normalizedCredentials struct {
	candidates     []credentialCandidate
	duplicateLines int
}

type optionalGroupModels struct {
	Set    bool
	Values []GroupModel
}

type groupModelRequestWire struct {
	ID           string `json:"id"`
	Alias        string `json:"alias"`
	AliasEnabled *bool  `json:"alias_enabled"`
}

type optionalField[T any] struct {
	Set   bool
	Null  bool
	Value T
}

func (field *optionalField[T]) UnmarshalJSON(data []byte) error {
	if field == nil {
		return fmt.Errorf("optional field receiver is nil")
	}
	field.Set = true
	field.Null = false
	var zero T
	field.Value = zero
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		field.Null = true
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&field.Value); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("optional field contains multiple JSON values")
		}
		return err
	}
	return nil
}

func (value *optionalGroupModels) UnmarshalJSON(data []byte) error {
	if value == nil {
		return fmt.Errorf("models must be an array")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return app_errors.ErrValidation
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var encodedModels []json.RawMessage
	if err := decoder.Decode(&encodedModels); err != nil {
		return fmt.Errorf("decode models: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode models: trailing JSON value")
		}
		return fmt.Errorf("decode models trailing value: %w", err)
	}

	decoded := make([]GroupModel, 0, len(encodedModels))
	for _, encoded := range encodedModels {
		modelDecoder := json.NewDecoder(bytes.NewReader(encoded))
		modelDecoder.DisallowUnknownFields()
		var wire groupModelRequestWire
		if err := modelDecoder.Decode(&wire); err != nil {
			return fmt.Errorf("decode group model: %w", err)
		}
		if wire.AliasEnabled == nil {
			return app_errors.ErrValidation
		}
		decoded = append(decoded, GroupModel{
			ID:           wire.ID,
			Alias:        wire.Alias,
			AliasEnabled: *wire.AliasEnabled,
		})
	}

	value.Set = true
	value.Values = decoded
	return nil
}

func normalizeUpstreamBaseURL(raw string) (normalized, hostname string, err error) {
	parsed, parseErr := url.Parse(strings.TrimSpace(raw))
	if parseErr != nil || parsed.Opaque != "" || parsed.Host == "" {
		return "", "", app_errors.ErrValidation
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", app_errors.ErrValidation
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return "", "", app_errors.ErrValidation
	}

	hostname = strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", "", app_errors.ErrValidation
	}
	port := parsed.Port()
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		parsed.Host = "[" + hostname + "]"
	} else {
		parsed.Host = hostname
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	return parsed.String(), hostname, nil
}

func normalizeGroupModels(values []GroupModel) ([]GroupModel, error) {
	result := make([]GroupModel, 0, len(values))
	indexesByClientModel := make(map[string][]int, len(values))
	clientModelOrder := make([]string, 0, len(values))
	for index, value := range values {
		normalized := GroupModel{
			ID: strings.TrimSpace(value.ID),
		}
		if normalized.ID == "" {
			return nil, app_errors.ErrValidation
		}
		alias := strings.TrimSpace(value.Alias)
		aliasEnabled := value.AliasEnabled
		if aliasEnabled {
			if alias == "" {
				return nil, app_errors.ErrValidation
			}
			normalized.Alias = alias
		}
		clientModel := normalized.ID
		if normalized.Alias != "" {
			clientModel = normalized.Alias
		}
		if _, exists := indexesByClientModel[clientModel]; !exists {
			clientModelOrder = append(clientModelOrder, clientModel)
		}
		indexesByClientModel[clientModel] = append(indexesByClientModel[clientModel], index)
		result = append(result, normalized)
	}
	conflicts := make([]ModelNameConflict, 0)
	for _, clientModel := range clientModelOrder {
		indexes := indexesByClientModel[clientModel]
		if len(indexes) < 2 {
			continue
		}
		conflicts = append(conflicts, ModelNameConflict{
			ClientModel: clientModel,
			Indexes:     append([]int(nil), indexes...),
		})
	}
	if len(conflicts) > 0 {
		return nil, app_errors.NewAPIErrorWithData(
			app_errors.ErrModelNameConflict,
			ModelNameConflictData{Conflicts: conflicts},
		)
	}
	return result, nil
}

func normalizeGroupName(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" || len([]byte(normalized)) > 255 {
		return nil, app_errors.ErrValidation
	}
	for _, character := range normalized {
		if unicode.IsControl(character) {
			return nil, app_errors.ErrValidation
		}
	}
	return &normalized, nil
}

func (s *Service) normalizeCredentials(
	channelID channel.ID,
	raw string,
) (normalizedCredentials, error) {
	if s == nil || s.channelRegistry == nil || s.encryption == nil {
		return normalizedCredentials{}, app_errors.ErrInternalServer
	}
	result := normalizedCredentials{candidates: make([]credentialCandidate, 0)}
	seen := make(map[string]struct{})
	nonEmptyLines := 0
	for _, line := range credentialInputEntries(channelID, raw) {
		plaintext := strings.TrimSpace(line)
		if plaintext == "" {
			continue
		}
		nonEmptyLines++
		if nonEmptyLines > maxCredentialLines {
			return normalizedCredentials{}, app_errors.ErrValidation
		}
		var encoded json.RawMessage
		if strings.HasPrefix(plaintext, "{") {
			encoded = json.RawMessage(plaintext)
		} else {
			marshaled, err := json.Marshal(map[string]string{"api_key": plaintext})
			if err != nil {
				return normalizedCredentials{}, app_errors.ErrInternalServer
			}
			encoded = marshaled
		}
		credential, err := s.channelRegistry.ValidateCredential(channelID, encoded)
		if err != nil {
			return normalizedCredentials{}, app_errors.ErrValidation
		}
		canonical := credential.CanonicalJSON()
		fingerprint := s.encryption.Hash(string(canonical))
		if _, duplicate := seen[fingerprint]; duplicate {
			result.duplicateLines++
			continue
		}
		seen[fingerprint] = struct{}{}
		result.candidates = append(result.candidates, credentialCandidate{
			canonical: append(json.RawMessage(nil), canonical...), fingerprint: fingerprint,
		})
	}
	if len(result.candidates) == 0 {
		return normalizedCredentials{}, app_errors.ErrValidation
	}
	return result, nil
}

func credentialInputEntries(channelID channel.ID, raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") || !json.Valid([]byte(trimmed)) {
		return strings.Split(raw, "\n")
	}
	if channelID != channel.GoogleVertex {
		return []string{trimmed}
	}

	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(trimmed), &object) != nil {
		return []string{trimmed}
	}
	if _, wrapped := object["service_account_json"]; wrapped {
		return []string{trimmed}
	}
	var credentialType string
	if json.Unmarshal(object["type"], &credentialType) != nil || credentialType != "service_account" {
		return []string{trimmed}
	}
	encoded, err := json.Marshal(map[string]string{"service_account_json": trimmed})
	if err != nil {
		return []string{trimmed}
	}
	return []string{string(encoded)}
}

func (s *Service) persistCredentials(
	tx *gorm.DB,
	groupID uint,
	normalized normalizedCredentials,
) (int, int, error) {
	fingerprints := make([]string, 0, len(normalized.candidates))
	for _, candidate := range normalized.candidates {
		fingerprints = append(fingerprints, candidate.fingerprint)
	}
	var existingRows []models.Credential
	if err := tx.Where("group_id = ? AND fingerprint IN ?", groupID, fingerprints).
		Find(&existingRows).Error; err != nil {
		return 0, 0, app_errors.ParseDBError(err)
	}
	existingByFingerprint := make(map[string]struct{}, len(existingRows))
	for _, row := range existingRows {
		existingByFingerprint[row.Fingerprint] = struct{}{}
	}

	nowMS, err := epochms.FromTime(s.now())
	if err != nil {
		return 0, 0, app_errors.ErrInternalServer
	}
	if nowMS < 1 {
		nowMS = 1
	}
	added := 0
	duplicated := normalized.duplicateLines
	for _, candidate := range normalized.candidates {
		if _, exists := existingByFingerprint[candidate.fingerprint]; exists {
			duplicated++
			continue
		}
		ciphertext, err := s.encryption.Encrypt(string(candidate.canonical))
		if err != nil {
			return 0, 0, fmt.Errorf("encrypt credential: %w", err)
		}
		row := models.Credential{
			GroupID: groupID, Data: ciphertext, Fingerprint: candidate.fingerprint,
			Status: models.CredentialStatusActive, CreatedAtMS: nowMS, UpdatedAtMS: nowMS,
		}
		if err := tx.Create(&row).Error; err != nil {
			return 0, 0, app_errors.ParseDBError(err)
		}
		existingByFingerprint[candidate.fingerprint] = struct{}{}
		added++
	}
	return added, duplicated, nil
}

func isLiteralPrivateHost(hostname string) bool {
	normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if normalized == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(normalized, "[]"))
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
