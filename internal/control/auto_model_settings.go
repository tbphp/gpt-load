package control

import (
	"bytes"
	"encoding/json"

	"gorm.io/gorm"

	"gpt-load/internal/automodel"
	app_errors "gpt-load/internal/platform/errors"
)

type AutoModelSettingsView struct {
	automodel.Config
}

func newAutoModelSettingsView(compiled *automodel.Compiled) AutoModelSettingsView {
	return AutoModelSettingsView{Config: compiled.Config()}
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
	encoded, _ := json.Marshal(object)
	config, err := automodel.Decode(encoded)
	if err != nil {
		return update, app_errors.ErrValidation
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
