package control

import (
	"bytes"
	"encoding/json"

	"gorm.io/gorm"

	"gpt-load/internal/automodel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/storage/models"
)

type AutoModelSettingsView struct {
	automodel.Config
	APIKeyConfigured bool `json:"api_key_configured"`
}

func newAutoModelSettingsView(compiled *automodel.Compiled) AutoModelSettingsView {
	config := compiled.Config()
	configured := config.APIKey != ""
	config.APIKey = ""
	return AutoModelSettingsView{Config: config, APIKeyConfigured: configured}
}

func (s *Service) normalizeAutoModelUpdate(tx *gorm.DB, raw json.RawMessage) (persistedSettingUpdate, error) {
	update := persistedSettingUpdate{key: automodel.SettingKey}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return update, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil || rejectDuplicateJSONFields(raw) != nil || s.encryption == nil {
		return update, app_errors.ErrValidation
	}
	// 读取响应中的脱敏状态可原样回传，它不参与服务端密钥状态判定。
	delete(object, "api_key_configured")
	if _, present := object["input_price"]; !present {
		return update, app_errors.ErrValidation
	}
	if _, present := object["output_price"]; !present {
		return update, app_errors.ErrValidation
	}
	encoded, _ := json.Marshal(object)
	config, err := automodel.Decode(encoded)
	if err != nil {
		return update, app_errors.ErrValidation
	}
	if config.APIKey == "" {
		var row models.SystemSetting
		if err := tx.Where("key = ?", automodel.SettingKey).Find(&row).Error; err != nil {
			return update, app_errors.ParseDBError(err)
		}
		if row.Key != "" {
			plaintext, err := s.encryption.Decrypt(row.Value)
			if err != nil {
				return update, app_errors.ErrInternalServer
			}
			before, err := automodel.Decode([]byte(plaintext))
			if err != nil {
				return update, app_errors.ErrInternalServer
			}
			if before.Provider != config.Provider {
				return update, app_errors.ErrValidation
			}
			config.APIKey = before.APIKey
		}
	}
	encoded, err = json.Marshal(config)
	if err != nil {
		return update, app_errors.ErrValidation
	}
	ciphertext, err := s.encryption.Encrypt(string(encoded))
	if err != nil {
		return update, app_errors.ErrInternalServer
	}
	update.value = &ciphertext
	return update, nil
}
