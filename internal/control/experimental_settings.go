package control

import (
	"encoding/json"
	"errors"
	"sort"

	"gorm.io/gorm"

	"gpt-load/internal/automodel"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/state"
	"gpt-load/internal/storage/models"
)

func decisionRouteOptions(snapshot *state.ConfigSnapshot) []DecisionRouteOption {
	byGroup := map[uint]map[string]bool{}
	for model, targets := range snapshot.ExecutionCandidates[protocol.Decisions][execution.OperationDecisionsCreate] {
		for _, target := range targets {
			if byGroup[target.GroupID] == nil {
				byGroup[target.GroupID] = map[string]bool{}
			}
			byGroup[target.GroupID][model] = true
		}
	}
	options := []DecisionRouteOption{}
	for id, models := range byGroup {
		names := []string{}
		for name := range models {
			names = append(names, name)
		}
		sort.Strings(names)
		options = append(options, DecisionRouteOption{GroupID: id, GroupName: snapshot.Groups[id].Name, Models: names})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].GroupName < options[j].GroupName })
	return options
}

// 用户首次保存公共配置后清除旧嵌套连接，恢复默认时不会重新启用旧连接。
func (s *Service) clearNestedJevConfig(tx *gorm.DB) error {
	var row models.SystemSetting
	if err := tx.Where(&models.SystemSetting{Key: automodel.SettingKey}).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	plaintext, err := s.encryption.Decrypt(row.Value)
	if err != nil {
		return err
	}
	config, err := automodel.Decode([]byte(plaintext))
	if err != nil {
		return err
	}
	config.Model = ""
	config.TimeoutSeconds = automodel.DefaultConfig().TimeoutSeconds
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	ciphertext, err := s.encryption.Encrypt(string(raw))
	if err != nil {
		return err
	}
	return tx.Model(&models.SystemSetting{}).Where(&models.SystemSetting{Key: automodel.SettingKey}).Update("value", ciphertext).Error
}

func auditAccessKeyOptions(snapshot *state.ConfigSnapshot) []AuditAccessKeyOption {
	options := []AuditAccessKeyOption{}
	for _, key := range snapshot.AccessKeysByID {
		options = append(options, AuditAccessKeyOption{ID: key.ID, Name: key.Name})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	return options
}
