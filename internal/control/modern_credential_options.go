package control

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/storage/models"
)

type ModernCredentialMembership struct {
	GroupID      uint `json:"group_id"`
	CredentialID uint `json:"credential_id"`
}

// 同一渠道的相同身份合并为一个选项；分组内的凭据 ID 仅用于精确筛选。
type ModernCredentialOption struct {
	ID          uint                         `json:"id"`
	ChannelID   string                       `json:"channel_id"`
	Label       string                       `json:"label"`
	Memberships []ModernCredentialMembership `json:"memberships"`
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
		key := row.Group.ChannelID + "\x00" + row.IdentityFingerprint
		index, exists := byIdentity[key]
		if !exists {
			canonical, identity, err := s.decodeCredential(*row.Group, row)
			if err != nil {
				return nil, err
			}
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
				ID: row.ID, ChannelID: row.Group.ChannelID, Label: label,
				Memberships: make([]ModernCredentialMembership, 0),
			})
		}
		items[index].Memberships = append(items[index].Memberships, ModernCredentialMembership{
			GroupID: row.GroupID, CredentialID: row.ID,
		})
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
