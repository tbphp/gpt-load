package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"unicode"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

const maxImportKeyLines = 1000

type ImportRequest struct {
	UpstreamURL string              `json:"upstream_url"`
	Protocols   []protocol.Protocol `json:"protocols"`
	Name        *string             `json:"name"`
	Keys        string              `json:"keys"`
	Models      optionalGroupModels `json:"models"`
}

type ImportResult struct {
	GroupID        uint         `json:"group_id"`
	GroupName      string       `json:"group_name"`
	Created        bool         `json:"created"`
	KeysAdded      int          `json:"keys_added"`
	KeysDuplicated int          `json:"keys_duplicated"`
	Models         []GroupModel `json:"models"`
}

type importKeyCandidate struct {
	plaintext string
	hash      string
}

type optionalGroupModels struct {
	Set    bool
	Values []GroupModel
}

func (value *optionalGroupModels) UnmarshalJSON(data []byte) error {
	if value == nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("models must be an array")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded []GroupModel
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode models: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode models: trailing JSON value")
		}
		return fmt.Errorf("decode models trailing value: %w", err)
	}

	value.Set = true
	value.Values = decoded
	return nil
}

type normalizedImport struct {
	upstreamURL    string
	signature      string
	hostname       string
	protocols      []protocol.Protocol
	explicitName   *string
	keys           []importKeyCandidate
	duplicateLines int
	models         optionalGroupModels
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

func normalizeUpstreamURL(raw string) (normalized, signature, hostname string, err error) {
	normalized, hostname, err = normalizeUpstreamBaseURL(raw)
	if err != nil {
		return "", "", "", err
	}
	sum := sha256.Sum256([]byte(normalized))
	return normalized, hex.EncodeToString(sum[:]), hostname, nil
}

func (s *Service) normalizeImportInput(request ImportRequest) (normalizedImport, error) {
	upstreamURL, signature, hostname, err := normalizeUpstreamURL(request.UpstreamURL)
	if err != nil {
		return normalizedImport{}, err
	}

	protocols, err := normalizeImportProtocols(request.Protocols)
	if err != nil {
		return normalizedImport{}, err
	}
	explicitName, err := normalizeImportName(request.Name)
	if err != nil {
		return normalizedImport{}, err
	}
	keys, duplicateLines, err := s.normalizeImportKeys(request.Keys)
	if err != nil {
		return normalizedImport{}, err
	}
	importModels := optionalGroupModels{Set: request.Models.Set}
	if request.Models.Set {
		importModels.Values, err = normalizeImportModels(request.Models.Values)
		if err != nil {
			return normalizedImport{}, err
		}
	}

	return normalizedImport{
		upstreamURL: upstreamURL, signature: signature, hostname: hostname,
		protocols: protocols, explicitName: explicitName, keys: keys,
		duplicateLines: duplicateLines,
		models:         importModels,
	}, nil
}

func normalizeImportModels(values []GroupModel) ([]GroupModel, error) {
	type pair struct {
		id    string
		alias string
	}

	result := make([]GroupModel, 0, len(values))
	seen := make(map[pair]struct{}, len(values))
	for _, value := range values {
		normalized := GroupModel{
			ID:    strings.TrimSpace(value.ID),
			Alias: strings.TrimSpace(value.Alias),
		}
		if normalized.ID == "" {
			return nil, app_errors.ErrValidation
		}
		identity := pair{id: normalized.ID, alias: normalized.Alias}
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func normalizeImportProtocols(values []protocol.Protocol) ([]protocol.Protocol, error) {
	result := make([]protocol.Protocol, 0, len(values))
	seen := make(map[protocol.Protocol]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() || value == protocol.OpenAIResponse {
			return nil, app_errors.ErrValidation
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, app_errors.ErrValidation
	}
	return result, nil
}

func normalizeImportName(value *string) (*string, error) {
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

func (s *Service) normalizeImportKeys(raw string) ([]importKeyCandidate, int, error) {
	result := make([]importKeyCandidate, 0)
	seen := make(map[string]struct{})
	duplicateLines := 0
	nonEmptyLines := 0
	for _, line := range strings.Split(raw, "\n") {
		plaintext := strings.TrimSpace(line)
		if plaintext == "" {
			continue
		}
		nonEmptyLines++
		if nonEmptyLines > maxImportKeyLines {
			return nil, 0, app_errors.ErrValidation
		}
		hash := s.encryption.Hash(plaintext)
		if _, duplicate := seen[hash]; duplicate {
			duplicateLines++
			continue
		}
		seen[hash] = struct{}{}
		result = append(result, importKeyCandidate{plaintext: plaintext, hash: hash})
	}
	if len(result) == 0 {
		return nil, 0, app_errors.ErrValidation
	}
	return result, duplicateLines, nil
}

func (s *Service) Import(ctx context.Context, request ImportRequest) (ImportResult, error) {
	normalized, err := s.normalizeImportInput(request)
	if err != nil {
		return ImportResult{}, err
	}
	if isLiteralPrivateHost(normalized.hostname) {
		logrus.WithFields(logrus.Fields{
			"host":      normalized.hostname,
			"signature": normalized.signature,
		}).Warn("Importing upstream with a private or local host")
	}
	result := ImportResult{Models: make([]GroupModel, 0)}
	var requestedEntries []state.KeyEntry
	_, err = s.writeConfig(ctx, func(tx *gorm.DB) error {
		group, created, err := s.persistImportGroup(tx, normalized)
		if err != nil {
			return err
		}
		result.GroupID = group.ID
		result.GroupName = group.Name
		result.Created = created
		result.Models, err = decodeImportModels(group.Models)
		if err != nil {
			return err
		}

		requestedEntries, result.KeysAdded, result.KeysDuplicated, err =
			s.persistImportKeys(tx, group.ID, normalized)
		if err != nil {
			return err
		}
		if err := state.ValidateKeyEntries(requestedEntries); err != nil {
			return fmt.Errorf("validate imported keys: %w", err)
		}
		return nil
	}, func() error {
		batch := make([]state.KeyEntry, 0, len(requestedEntries))
		for _, entry := range requestedEntries {
			if _, exists := s.registry.EncryptedValue(entry.ID); !exists {
				batch = append(batch, entry)
			}
		}
		if len(batch) == 0 {
			return nil
		}
		return s.registry.ApplyImport(result.GroupID, batch)
	})
	if err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func (s *Service) persistImportGroup(
	tx *gorm.DB,
	normalized normalizedImport,
) (models.Group, bool, error) {
	var group models.Group
	query := tx.Where("signature = ?", normalized.signature).Limit(1).Find(&group)
	if query.Error != nil {
		return models.Group{}, false, app_errors.ParseDBError(query.Error)
	}
	if query.RowsAffected == 1 {
		var existing []protocol.Protocol
		if err := json.Unmarshal(group.Protocols, &existing); err != nil {
			return models.Group{}, false, fmt.Errorf("decode group %d protocols: %w", group.ID, err)
		}
		merged := stableProtocolUnion(existing, normalized.protocols)
		encoded, err := json.Marshal(merged)
		if err != nil {
			return models.Group{}, false, fmt.Errorf("encode group protocols: %w", err)
		}
		updates := map[string]any{"protocols": models.JSON(encoded)}
		if normalized.models.Set {
			encodedModels, err := json.Marshal(normalized.models.Values)
			if err != nil {
				return models.Group{}, false, fmt.Errorf("encode group models: %w", err)
			}
			updates["models"] = models.JSON(encodedModels)
			group.Models = models.JSON(encodedModels)
		}
		if err := tx.Model(&group).Updates(updates).Error; err != nil {
			return models.Group{}, false, app_errors.ParseDBError(err)
		}
		group.Protocols = models.JSON(encoded)
		return group, false, nil
	}

	name, err := resolveImportGroupName(tx, normalized)
	if err != nil {
		return models.Group{}, false, err
	}
	protocols, err := json.Marshal(normalized.protocols)
	if err != nil {
		return models.Group{}, false, fmt.Errorf("encode group protocols: %w", err)
	}
	storedModels := make([]GroupModel, 0)
	if normalized.models.Set {
		storedModels = append(storedModels, normalized.models.Values...)
	}
	encodedModels, err := json.Marshal(storedModels)
	if err != nil {
		return models.Group{}, false, fmt.Errorf("encode group models: %w", err)
	}
	group = models.Group{
		Name: name, UpstreamURL: normalized.upstreamURL, Signature: normalized.signature,
		Protocols: models.JSON(protocols), Models: models.JSON(encodedModels), Config: models.JSON(`{}`),
		Enabled: true,
	}
	if err := tx.Create(&group).Error; err != nil {
		return models.Group{}, false, app_errors.ParseDBError(err)
	}
	return group, true, nil
}

func resolveImportGroupName(tx *gorm.DB, normalized normalizedImport) (string, error) {
	if normalized.explicitName != nil {
		available, err := importGroupNameAvailable(tx, *normalized.explicitName)
		if err != nil {
			return "", err
		}
		if !available {
			return "", app_errors.ErrDuplicateResource
		}
		return *normalized.explicitName, nil
	}

	for suffix := 1; ; suffix++ {
		candidate := normalized.hostname
		if suffix > 1 {
			candidate = fmt.Sprintf("%s-%d", normalized.hostname, suffix)
		}
		available, err := importGroupNameAvailable(tx, candidate)
		if err != nil {
			return "", err
		}
		if available {
			return candidate, nil
		}
	}
}

func importGroupNameAvailable(tx *gorm.DB, name string) (bool, error) {
	var count int64
	if err := tx.Model(&models.Group{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return false, app_errors.ParseDBError(err)
	}
	return count == 0, nil
}

func stableProtocolUnion(existing, requested []protocol.Protocol) []protocol.Protocol {
	result := append([]protocol.Protocol(nil), existing...)
	seen := make(map[protocol.Protocol]struct{}, len(existing)+len(requested))
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range requested {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *Service) persistImportKeys(
	tx *gorm.DB,
	groupID uint,
	normalized normalizedImport,
) ([]state.KeyEntry, int, int, error) {
	hashes := make([]string, 0, len(normalized.keys))
	for _, candidate := range normalized.keys {
		hashes = append(hashes, candidate.hash)
	}
	var existingRows []models.UpstreamKey
	if err := tx.Where("group_id = ? AND key_hash IN ?", groupID, hashes).Find(&existingRows).Error; err != nil {
		return nil, 0, 0, app_errors.ParseDBError(err)
	}
	existingByHash := make(map[string]models.UpstreamKey, len(existingRows))
	for _, row := range existingRows {
		existingByHash[row.KeyHash] = row
	}

	entries := make([]state.KeyEntry, 0, len(normalized.keys))
	added := 0
	duplicated := normalized.duplicateLines
	for _, candidate := range normalized.keys {
		row, exists := existingByHash[candidate.hash]
		if exists {
			duplicated++
		} else {
			ciphertext, err := s.encryption.Encrypt(candidate.plaintext)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("encrypt imported key: %w", err)
			}
			row = models.UpstreamKey{
				GroupID: groupID, KeyValue: ciphertext, KeyHash: candidate.hash,
				Status: models.UpstreamKeyStatusActive,
			}
			if err := tx.Create(&row).Error; err != nil {
				return nil, 0, 0, app_errors.ParseDBError(err)
			}
			added++
		}
		entries = append(entries, state.KeyEntry{
			ID: row.ID, GroupID: row.GroupID, WeightManual: row.WeightManual,
			Status: state.KeyStatus(row.Status), EncryptedValue: row.KeyValue,
		})
	}
	return entries, added, duplicated, nil
}

func decodeImportModels(raw models.JSON) ([]GroupModel, error) {
	var result []GroupModel
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode imported group models: %w", err)
	}
	if result == nil {
		result = make([]GroupModel, 0)
	}
	return result, nil
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
