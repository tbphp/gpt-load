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

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

const maxUpstreamKeyLines = 1000

type upstreamKeyCandidate struct {
	plaintext string
	hash      string
}

type normalizedUpstreamKeys struct {
	candidates     []upstreamKeyCandidate
	duplicateLines int
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

func normalizeGroupProtocols(values []protocol.Protocol) ([]protocol.Protocol, error) {
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

func normalizeGroupModels(values []GroupModel) ([]GroupModel, error) {
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

func (s *Service) normalizeUpstreamKeys(raw string) (normalizedUpstreamKeys, error) {
	result := normalizedUpstreamKeys{
		candidates: make([]upstreamKeyCandidate, 0),
	}
	seen := make(map[string]struct{})
	nonEmptyLines := 0
	for _, line := range strings.Split(raw, "\n") {
		plaintext := strings.TrimSpace(line)
		if plaintext == "" {
			continue
		}
		nonEmptyLines++
		if nonEmptyLines > maxUpstreamKeyLines {
			return normalizedUpstreamKeys{}, app_errors.ErrValidation
		}
		hash := s.encryption.Hash(plaintext)
		if _, duplicate := seen[hash]; duplicate {
			result.duplicateLines++
			continue
		}
		seen[hash] = struct{}{}
		result.candidates = append(result.candidates, upstreamKeyCandidate{
			plaintext: plaintext,
			hash:      hash,
		})
	}
	if len(result.candidates) == 0 {
		return normalizedUpstreamKeys{}, app_errors.ErrValidation
	}
	return result, nil
}

func (s *Service) persistUpstreamKeys(
	tx *gorm.DB,
	groupID uint,
	normalized normalizedUpstreamKeys,
) ([]state.KeyEntry, int, int, error) {
	hashes := make([]string, 0, len(normalized.candidates))
	for _, candidate := range normalized.candidates {
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

	entries := make([]state.KeyEntry, 0, len(normalized.candidates))
	added := 0
	duplicated := normalized.duplicateLines
	for _, candidate := range normalized.candidates {
		row, exists := existingByHash[candidate.hash]
		if exists {
			duplicated++
		} else {
			ciphertext, err := s.encryption.Encrypt(candidate.plaintext)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("encrypt upstream key: %w", err)
			}
			row = models.UpstreamKey{
				GroupID:  groupID,
				KeyValue: ciphertext,
				KeyHash:  candidate.hash,
				Status:   models.UpstreamKeyStatusActive,
			}
			if err := tx.Create(&row).Error; err != nil {
				return nil, 0, 0, app_errors.ParseDBError(err)
			}
			added++
		}
		entries = append(entries, state.KeyEntry{
			ID:             row.ID,
			GroupID:        row.GroupID,
			WeightManual:   row.WeightManual,
			WeightAuto:     state.DefaultWeight,
			Status:         state.KeyStatus(row.Status),
			EncryptedValue: row.KeyValue,
		})
	}
	return entries, added, duplicated, nil
}

func (s *Service) applyMissingRegistryKeys(groupID uint, entries []state.KeyEntry) error {
	missing := make([]state.KeyEntry, 0, len(entries))
	for _, entry := range entries {
		if _, exists := s.registry.EncryptedValue(entry.ID); !exists {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return s.registry.ApplyImport(groupID, missing)
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
