package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/policy"
	"gpt-load/internal/storage/models"
)

// optionalPolicyConfig 用于在严格 JSON 反序列化中精确区分：
// 1. 遗漏 config (Specified == false)
// 2. 显式传 null (Specified == true && IsNull == true)
// 3. 显式传合法 JSON (Specified == true && IsNull == false)
type optionalPolicyConfig struct {
	Specified bool
	IsNull    bool
	Raw       json.RawMessage
}

func (o *optionalPolicyConfig) UnmarshalJSON(data []byte) error {
	o.Specified = true
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		o.IsNull = true
		return nil
	}
	o.Raw = append(json.RawMessage(nil), data...)
	return nil
}

type PolicyConfigDTO struct {
	SchemaVersion int               `json:"schema_version"`
	Rules         []json.RawMessage `json:"rules"`
}

type PolicyBindingResponse struct {
	Scope         string            `json:"scope"`
	ID            uint              `json:"id"`
	GroupID       uint              `json:"group_id"`
	CredentialID  *uint             `json:"credential_id,omitempty"`
	Revision      uint64            `json:"revision"`
	SchemaVersion int               `json:"schema_version"`
	Rules         []json.RawMessage `json:"rules"`
	Config        PolicyConfigDTO   `json:"config"`
}

type PolicyUpdateRequest struct {
	ExpectedRevision *uint64              `json:"expected_revision"`
	Config           optionalPolicyConfig `json:"config"`
}

func parsePolicyConfigDTO(raw []byte) (PolicyConfigDTO, error) {
	var dto PolicyConfigDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		return PolicyConfigDTO{}, err
	}
	if dto.Rules == nil {
		dto.Rules = []json.RawMessage{}
	}
	return dto, nil
}

// GetGroupPolicy 返回指定分组的策略绑定配置。
// 若未配置返回 revision=0, schema_version=1, rules=[]。
// 若数据库中存储的记录损坏或无法通过严格编译校验，必须返回错误，绝不伪装为默认合法配置。
func (s *Service) GetGroupPolicy(ctx context.Context, groupID uint) (PolicyBindingResponse, error) {
	if groupID == 0 {
		return PolicyBindingResponse{}, app_errors.ErrBadRequest
	}

	var group models.Group
	if err := s.db.WithContext(ctx).Select("id").First(&group, groupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PolicyBindingResponse{}, app_errors.ErrResourceNotFound
		}
		return PolicyBindingResponse{}, app_errors.ParseDBError(err)
	}

	var binding models.PolicyBinding
	err := s.db.WithContext(ctx).
		Where("scope = ? AND group_id = ? AND credential_id = 0", models.PolicyScopeGroup, groupID).
		First(&binding).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PolicyBindingResponse{
			Scope:         string(models.PolicyScopeGroup),
			ID:            groupID,
			GroupID:       groupID,
			Revision:      0,
			SchemaVersion: 1,
			Rules:         []json.RawMessage{},
			Config: PolicyConfigDTO{
				SchemaVersion: 1,
				Rules:         []json.RawMessage{},
			},
		}, nil
	}
	if err != nil {
		return PolicyBindingResponse{}, app_errors.ParseDBError(err)
	}

	if _, cErr := policy.Compile(binding.Config); cErr != nil {
		return PolicyBindingResponse{}, app_errors.ErrMalformedPolicyStorage
	}

	cfgDTO, err := parsePolicyConfigDTO(binding.Config)
	if err != nil {
		return PolicyBindingResponse{}, app_errors.ErrMalformedPolicyStorage
	}

	return PolicyBindingResponse{
		Scope:         string(binding.Scope),
		ID:            groupID,
		GroupID:       groupID,
		Revision:      uint64(binding.Revision),
		SchemaVersion: cfgDTO.SchemaVersion,
		Rules:         cfgDTO.Rules,
		Config:        cfgDTO,
	}, nil
}

// UpdateGroupPolicy 乐观并发更新 Group 范围策略。
func (s *Service) UpdateGroupPolicy(ctx context.Context, groupID uint, req PolicyUpdateRequest) (PolicyBindingResponse, error) {
	return s.savePolicyBinding(ctx, models.PolicyScopeGroup, groupID, 0, req)
}

// GetCredentialPolicy 返回指定分组下特定凭据的策略绑定配置。若凭据不从属于该分组，返回 404。
// 若数据库中存储的记录损坏或无法通过严格编译校验，必须返回错误，绝不伪装为默认合法配置。
func (s *Service) GetCredentialPolicy(ctx context.Context, groupID uint, credentialID uint) (PolicyBindingResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return PolicyBindingResponse{}, app_errors.ErrBadRequest
	}

	var cred models.Credential
	if err := s.db.WithContext(ctx).Select("id, group_id").Where("id = ? AND group_id = ?", credentialID, groupID).First(&cred).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PolicyBindingResponse{}, app_errors.ErrResourceNotFound
		}
		return PolicyBindingResponse{}, app_errors.ParseDBError(err)
	}

	var binding models.PolicyBinding
	err := s.db.WithContext(ctx).
		Where("scope = ? AND group_id = ? AND credential_id = ?", models.PolicyScopeCredential, groupID, credentialID).
		First(&binding).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return PolicyBindingResponse{
			Scope:         string(models.PolicyScopeCredential),
			ID:            credentialID,
			GroupID:       groupID,
			CredentialID:  &credentialID,
			Revision:      0,
			SchemaVersion: 1,
			Rules:         []json.RawMessage{},
			Config: PolicyConfigDTO{
				SchemaVersion: 1,
				Rules:         []json.RawMessage{},
			},
		}, nil
	}
	if err != nil {
		return PolicyBindingResponse{}, app_errors.ParseDBError(err)
	}

	if _, cErr := policy.Compile(binding.Config); cErr != nil {
		return PolicyBindingResponse{}, app_errors.ErrMalformedPolicyStorage
	}

	cfgDTO, err := parsePolicyConfigDTO(binding.Config)
	if err != nil {
		return PolicyBindingResponse{}, app_errors.ErrMalformedPolicyStorage
	}

	return PolicyBindingResponse{
		Scope:         string(binding.Scope),
		ID:            credentialID,
		GroupID:       groupID,
		CredentialID:  &credentialID,
		Revision:      uint64(binding.Revision),
		SchemaVersion: cfgDTO.SchemaVersion,
		Rules:         cfgDTO.Rules,
		Config:        cfgDTO,
	}, nil
}

// UpdateCredentialPolicy 乐观并发更新 Credential 范围策略。
func (s *Service) UpdateCredentialPolicy(ctx context.Context, groupID uint, credentialID uint, req PolicyUpdateRequest) (PolicyBindingResponse, error) {
	return s.savePolicyBinding(ctx, models.PolicyScopeCredential, groupID, credentialID, req)
}

func (s *Service) savePolicyBinding(
	ctx context.Context,
	scope models.PolicyScope,
	groupID uint,
	credentialID uint,
	req PolicyUpdateRequest,
) (PolicyBindingResponse, error) {
	if groupID == 0 || (scope == models.PolicyScopeCredential && credentialID == 0) {
		return PolicyBindingResponse{}, app_errors.ErrBadRequest
	}
	if req.ExpectedRevision == nil {
		return PolicyBindingResponse{}, app_errors.ErrBadRequest
	}
	expectedRev := *req.ExpectedRevision

	// 1. 处理 config payload 验证
	var hasNewConfig bool
	var toSaveRaw []byte
	var toSaveDTO PolicyConfigDTO

	if req.Config.Specified {
		if req.Config.IsNull {
			return PolicyBindingResponse{}, app_errors.ErrBadRequest
		}
		trimmed := bytes.TrimSpace(req.Config.Raw)
		if len(trimmed) == 0 {
			return PolicyBindingResponse{}, app_errors.ErrBadRequest
		}

		// 编译校验：必须使用 policy.Compile(DefaultRegistry) 成功通过
		if _, compileErr := policy.Compile(trimmed); compileErr != nil {
			return PolicyBindingResponse{}, app_errors.NewAPIErrorWithData(app_errors.ErrValidation, compileErr.Error())
		}

		dto, err := parsePolicyConfigDTO(trimmed)
		if err != nil {
			return PolicyBindingResponse{}, app_errors.ErrBadRequest
		}
		hasNewConfig = true
		toSaveRaw = trimmed
		toSaveDTO = dto
	}

	// 2. 事务执行原子检查与 CAS 保存
	var finalRevision uint64
	var finalConfigDTO PolicyConfigDTO

	var response PolicyBindingResponse
	_, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		// 2.1 在写事务内原子复核父实体存在性与归属关系，避免孤儿记录与并发删除/移动竞态
		if scope == models.PolicyScopeGroup {
			var grp models.Group
			if err := tx.Select("id").Where("id = ?", groupID).First(&grp).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return app_errors.ErrResourceNotFound
				}
				return app_errors.ParseDBError(err)
			}
		} else if scope == models.PolicyScopeCredential {
			var cred models.Credential
			if err := tx.Select("id, group_id").Where("id = ? AND group_id = ?", credentialID, groupID).First(&cred).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return app_errors.ErrResourceNotFound
				}
				return app_errors.ParseDBError(err)
			}
		} else {
			return app_errors.ErrBadRequest
		}

		// 2.2 查询当前策略绑定记录
		var current models.PolicyBinding
		qErr := tx.Where("scope = ? AND group_id = ? AND credential_id = ?", scope, groupID, credentialID).First(&current).Error

		if errors.Is(qErr, gorm.ErrRecordNotFound) {
			// 未配置状态，预期版本必须为 0
			if expectedRev != 0 {
				return app_errors.ErrPolicyRevisionConflict
			}

			// 如果遗漏了 config，使用默认空规则
			if !hasNewConfig {
				toSaveRaw = []byte(`{"schema_version":1,"rules":[]}`)
				toSaveDTO = PolicyConfigDTO{SchemaVersion: 1, Rules: []json.RawMessage{}}
			}

			now := s.now().UnixMilli()
			newBinding := models.PolicyBinding{
				Scope:         scope,
				GroupID:       groupID,
				CredentialID:  credentialID,
				Revision:      1,
				SchemaVersion: toSaveDTO.SchemaVersion,
				Config:        models.JSON(toSaveRaw),
				CreatedAtMS:   now,
				UpdatedAtMS:   now,
			}
			if cErr := tx.Create(&newBinding).Error; cErr != nil {
				if errors.Is(cErr, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(cErr.Error()), "unique") {
					return app_errors.ErrPolicyRevisionConflict
				}
				return app_errors.ParseDBError(cErr)
			}
			finalRevision = 1
			finalConfigDTO = toSaveDTO
			return nil
		}

		if qErr != nil {
			return app_errors.ParseDBError(qErr)
		}

		// 2.3 检查 expectedRevision 是否匹配
		if expectedRev != uint64(current.Revision) {
			return app_errors.ErrPolicyRevisionConflict
		}

		// 2.4 版本号溢出防御：MaxUint64 + 1 会回绕为 0，必须拒绝更新且不写库
		if uint64(current.Revision) == math.MaxUint64 {
			return app_errors.ErrPolicyRevisionOverflow
		}
		nextRevision := current.Revision + 1

		targetRaw := toSaveRaw
		targetDTO := toSaveDTO
		if !hasNewConfig {
			// 遗漏 config 保持原有不变，但必须确保原存储配置通过编译校验
			if _, cErr := policy.Compile(current.Config); cErr != nil {
				return app_errors.ErrMalformedPolicyStorage
			}
			targetRaw = current.Config
			existingDTO, pErr := parsePolicyConfigDTO(current.Config)
			if pErr != nil {
				return app_errors.ErrMalformedPolicyStorage
			}
			targetDTO = existingDTO
		}

		now := s.now().UnixMilli()
		result := tx.Model(&models.PolicyBinding{}).
			Where("id = ? AND revision = ?", current.ID, current.Revision).
			Updates(map[string]any{
				"config":         models.JSON(targetRaw),
				"revision":       nextRevision,
				"schema_version": targetDTO.SchemaVersion,
				"updated_at_ms":  now,
			})

		if result.Error != nil {
			return app_errors.ParseDBError(result.Error)
		}
		if result.RowsAffected == 0 {
			return app_errors.ErrPolicyRevisionConflict
		}

		finalRevision = uint64(nextRevision)
		finalConfigDTO = targetDTO
		return nil
	}, nil)

	if err != nil {
		return PolicyBindingResponse{}, err
	}
	response = PolicyBindingResponse{
		Scope:         string(scope),
		GroupID:       groupID,
		Revision:      finalRevision,
		SchemaVersion: finalConfigDTO.SchemaVersion,
		Rules:         finalConfigDTO.Rules,
		Config:        finalConfigDTO,
	}
	if scope == models.PolicyScopeGroup {
		response.ID = groupID
	} else {
		response.ID = credentialID
		response.CredentialID = &credentialID
	}
	return response, nil
}

// HTTP Handlers

func (s *Server) handleGetGroupPolicy(c *gin.Context) {
	if !s.requireAdminPrincipal(c, "get_group_policy") {
		return
	}
	id, ok := groupID(c, "get_group_policy")
	if !ok {
		return
	}
	result, err := s.service.GetGroupPolicy(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, "get_group_policy", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateGroupPolicy(c *gin.Context) {
	if !s.requireAdminPrincipal(c, "update_group_policy") {
		return
	}
	id, ok := groupID(c, "update_group_policy")
	if !ok {
		return
	}
	var request PolicyUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_group_policy", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateGroupPolicy(c.Request.Context(), id, request)
	if err != nil {
		writeServiceError(c, "update_group_policy", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleGetCredentialPolicy(c *gin.Context) {
	if !s.requireAdminPrincipal(c, "get_credential_policy") {
		return
	}
	grpID, ok := groupID(c, "get_credential_policy")
	if !ok {
		return
	}
	crdID, ok := credentialID(c, "get_credential_policy")
	if !ok {
		return
	}
	result, err := s.service.GetCredentialPolicy(c.Request.Context(), grpID, crdID)
	if err != nil {
		writeServiceError(c, "get_credential_policy", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleUpdateCredentialPolicy(c *gin.Context) {
	if !s.requireAdminPrincipal(c, "update_credential_policy") {
		return
	}
	grpID, ok := groupID(c, "update_credential_policy")
	if !ok {
		return
	}
	crdID, ok := credentialID(c, "update_credential_policy")
	if !ok {
		return
	}
	var request PolicyUpdateRequest
	if err := bindStrictJSON(c, &request); err != nil {
		writeServiceError(c, "update_credential_policy", mapControlJSONError(err))
		return
	}
	result, err := s.service.UpdateCredentialPolicy(c.Request.Context(), grpID, crdID, request)
	if err != nil {
		writeServiceError(c, "update_credential_policy", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) requireAdminPrincipal(c *gin.Context, op string) bool {
	principal, ok := currentControlPrincipal(c)
	if !ok || principal.Type != controlPrincipalAdmin {
		writeServiceError(c, op, app_errors.ErrForbidden)
		return false
	}
	return true
}
