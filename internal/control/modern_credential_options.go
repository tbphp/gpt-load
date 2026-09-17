package control

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/storage/models"
)

// 按当前解析的真实身份去重，不使用记录 ID 或历史持久化身份指纹。
type ModernCredentialOption struct {
	Key       string `json:"key"`
	ChannelID string `json:"channel_id"`
	Label     string `json:"label"`
	GroupIDs  []uint `json:"group_ids"`
}

func (s *Service) credentialFilterKey(group models.Group, row models.Credential, canonical json.RawMessage) (string, error) {
	connectionType := normalizeGroupConnectionType(group.ConnectionType)
	identity := row.Fingerprint
	if connectionType == models.ConnectionTypeSubscription {
		driver, err := s.subscriptionDriver(channel.ID(group.ChannelID))
		if err != nil {
			return "", err
		}
		credential, err := driver.Parse(canonical)
		if err != nil {
			return "", err
		}
		identity = credential.Identity()
	}
	if strings.TrimSpace(identity) == "" {
		return "", app_errors.ErrInternalServer
	}
	return s.encryption.Hash("credential-filter/v1|" + group.ChannelID + "|" + string(connectionType) + "|" + identity), nil
}

func (s *Service) ListModernCredentialOptions(ctx context.Context) ([]ModernCredentialOption, error) {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	var rows []models.Credential
	if err := s.withReadSnapshot(ctx, func(tx *gorm.DB) error {
		return tx.Preload("Group").Order("id ASC").Find(&rows).Error
	}); err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	items := make([]ModernCredentialOption, 0)
	byIdentity := make(map[string]int)
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if row.Group == nil || row.IdentityFingerprint == "" {
			return nil, app_errors.ErrInternalServer
		}
		canonical, identity, err := s.decodeCredential(*row.Group, row)
		if err != nil {
			return nil, err
		}
		key, err := s.credentialFilterKey(*row.Group, row, canonical)
		if err != nil {
			return nil, err
		}
		index, exists := byIdentity[key]
		if !exists {
			mask, account, err := s.credentialPresentation(*row.Group, row, canonical, identity)
			if err != nil {
				return nil, err
			}
			label := mask
			if normalizeGroupConnectionType(row.Group.ConnectionType) == models.ConnectionTypeSubscription {
				label = strings.TrimSpace(account.Email)
				if label == "" {
					label = account.EmailMask
				}
				if label == "" {
					label = row.Group.Name
				}
			}
			index = len(items)
			byIdentity[key] = index
			items = append(items, ModernCredentialOption{
				Key: key, ChannelID: row.Group.ChannelID, Label: label,
				GroupIDs: make([]uint, 0),
			})
		}
		if !slices.Contains(items[index].GroupIDs, row.GroupID) {
			items[index].GroupIDs = append(items[index].GroupIDs, row.GroupID)
		}
	}
	return items, nil
}

func (s *Server) handleModernCredentialOptions(c *gin.Context) {
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		writeServiceError(c, "modern_credential_options", app_errors.ErrBadRequest)
		return
	}
	items, err := s.service.ListModernCredentialOptions(c.Request.Context())
	if err != nil {
		writeServiceError(c, "modern_credential_options", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.SuccessI18n(c, "common.success", struct {
		Items []ModernCredentialOption `json:"items"`
	}{Items: items})
}
