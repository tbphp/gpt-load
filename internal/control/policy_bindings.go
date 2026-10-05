package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/policy"
	"gpt-load/internal/storage/models"
)

type PolicyBindingResponse struct {
	Scope        string `json:"scope"`
	ID           uint   `json:"id"`
	GroupID      uint   `json:"group_id"`
	CredentialID *uint  `json:"credential_id,omitempty"`
	RevisionText string `json:"revision_text"`
	ConfigText   string `json:"config_text"`
}

const defaultPolicyConfigJSON = `{"schema_version":1,"rules":[]}`

// policyBindingDTO 组装分组／凭据策略响应；ID 语义随 scope 变化。
func policyBindingDTO(scope models.PolicyScope, groupID, credentialID uint, revision uint64, config string) PolicyBindingResponse {
	resp := PolicyBindingResponse{
		Scope:        string(scope),
		GroupID:      groupID,
		RevisionText: strconv.FormatUint(revision, 10),
		ConfigText:   config,
	}
	if scope == models.PolicyScopeGroup {
		resp.ID = groupID
		return resp
	}
	resp.ID = credentialID
	resp.CredentialID = &credentialID
	return resp
}

// ensurePolicyScopeParent 校验父实体存在性与归属关系，避免孤儿绑定。
func ensurePolicyScopeParent(db *gorm.DB, scope models.PolicyScope, groupID, credentialID uint) error {
	switch scope {
	case models.PolicyScopeGroup:
		var group models.Group
		if err := db.Select("id").Where("id = ?", groupID).First(&group).Error; err != nil {
			return policyParentLookupError(err)
		}
		return nil
	case models.PolicyScopeCredential:
		var cred models.Credential
		if err := db.Select("id, group_id").Where("id = ? AND group_id = ?", credentialID, groupID).First(&cred).Error; err != nil {
			return policyParentLookupError(err)
		}
		return nil
	default:
		return app_errors.ErrBadRequest
	}
}

// policyValidationError 构造策略接口的校验失败错误，保持既有 ErrValidation 文案。
func policyValidationError(format string, args ...any) error {
	return app_errors.NewAPIErrorWithData(app_errors.ErrValidation, fmt.Sprintf(format, args...))
}

func policyParentLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return app_errors.ErrResourceNotFound
	}
	return app_errors.ParseDBError(err)
}

// loadPolicyBinding 读取绑定记录；未配置时返回 (nil, nil)。
func (s *Service) loadPolicyBinding(ctx context.Context, scope models.PolicyScope, groupID, credentialID uint) (*models.PolicyBinding, error) {
	var binding models.PolicyBinding
	err := s.db.WithContext(ctx).
		Where("scope = ? AND group_id = ? AND credential_id = ?", scope, groupID, credentialID).
		First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, app_errors.ParseDBError(err)
	}
	return &binding, nil
}

// PolicyUpdateRequest 是策略保存载荷。
// ExpectedRevision 仅为兼容旧客户端保留并忽略：保存语义是最新写入覆盖 (last-write-wins)。
type PolicyUpdateRequest struct {
	ExpectedRevision *string                        `json:"expected_revision"`
	Config           optionalField[json.RawMessage] `json:"config"`
}

// GetGroupPolicy 返回指定分组的策略绑定配置。
// 若未配置返回 revision=0, schema_version=1, rules=[]。
// 若数据库中存储的记录损坏或无法通过严格编译校验，必须返回错误，绝不伪装为默认合法配置。
func (s *Service) GetGroupPolicy(ctx context.Context, groupID uint) (PolicyBindingResponse, error) {
	if groupID == 0 {
		return PolicyBindingResponse{}, app_errors.ErrBadRequest
	}

	return s.getPolicyBinding(ctx, models.PolicyScopeGroup, groupID, 0)
}

// getPolicyBinding 读取并严格校验绑定：未配置返回默认空规则，
// 存储损坏或分组作用域非法 override 一律报错，绝不伪装为默认合法配置。
func (s *Service) getPolicyBinding(ctx context.Context, scope models.PolicyScope, groupID, credentialID uint) (PolicyBindingResponse, error) {
	if err := ensurePolicyScopeParent(s.db.WithContext(ctx), scope, groupID, credentialID); err != nil {
		return PolicyBindingResponse{}, err
	}
	binding, err := s.loadPolicyBinding(ctx, scope, groupID, credentialID)
	if err != nil {
		return PolicyBindingResponse{}, err
	}
	if binding == nil {
		return policyBindingDTO(scope, groupID, credentialID, 0, defaultPolicyConfigJSON), nil
	}
	compiled, cErr := policy.Compile(binding.Config)
	if cErr != nil {
		return PolicyBindingResponse{}, app_errors.ErrMalformedPolicyStorage
	}
	if scope == models.PolicyScopeGroup && compiled.GroupPolicyMode() == policy.GroupPolicyOverride {
		return PolicyBindingResponse{}, app_errors.ErrMalformedPolicyStorage
	}
	return policyBindingDTO(binding.Scope, groupID, credentialID, uint64(binding.Revision), string(binding.Config)), nil
}

// UpdateGroupPolicy 以最新写入覆盖保存 Group 范围策略。
func (s *Service) UpdateGroupPolicy(ctx context.Context, groupID uint, req PolicyUpdateRequest) (PolicyBindingResponse, error) {
	return s.savePolicyBinding(ctx, models.PolicyScopeGroup, groupID, 0, req)
}

// GetCredentialPolicy 返回指定分组下特定凭据的策略绑定配置。若凭据不从属于该分组，返回 404。
// 若数据库中存储的记录损坏或无法通过严格编译校验，必须返回错误，绝不伪装为默认合法配置。
func (s *Service) GetCredentialPolicy(ctx context.Context, groupID uint, credentialID uint) (PolicyBindingResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return PolicyBindingResponse{}, app_errors.ErrBadRequest
	}

	return s.getPolicyBinding(ctx, models.PolicyScopeCredential, groupID, credentialID)
}

// UpdateCredentialPolicy 以最新写入覆盖保存 Credential 范围策略。
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

	var toSaveRaw []byte
	if req.Config.Set {
		if req.Config.Null {
			return PolicyBindingResponse{}, app_errors.ErrBadRequest
		}
		trimmed := bytes.TrimSpace(req.Config.Value)
		if len(trimmed) == 0 {
			return PolicyBindingResponse{}, app_errors.ErrBadRequest
		}

		// 编译校验：必须使用 policy.Compile 成功通过
		compiled, compileErr := policy.Compile(trimmed)
		if compileErr != nil {
			return PolicyBindingResponse{}, app_errors.NewAPIErrorWithData(app_errors.ErrValidation, compileErr.Error())
		}
		// 分组作用域仅允许 inherit：group_policy=override 在保存时即拒绝
		if scope == models.PolicyScopeGroup && compiled.GroupPolicyMode() == policy.GroupPolicyOverride {
			return PolicyBindingResponse{}, policyValidationError("group scope policy cannot use group_policy 'override'")
		}
		toSaveRaw = trimmed
	}

	// 事务内 upsert：最新写入覆盖，revision 仅递增用于展示
	var finalRevision uint64
	var finalConfigRaw string

	_, err := s.writeConfig(ctx, func(tx *gorm.DB) error {
		// 在写事务内原子复核父实体存在性与归属关系，避免孤儿记录与并发删除/移动竞态
		if err := ensurePolicyScopeParent(tx, scope, groupID, credentialID); err != nil {
			return err
		}

		var current models.PolicyBinding
		qErr := tx.Where("scope = ? AND group_id = ? AND credential_id = ?", scope, groupID, credentialID).First(&current).Error

		if errors.Is(qErr, gorm.ErrRecordNotFound) {
			// 如果遗漏了 config，使用默认空规则
			createRaw := toSaveRaw
			if createRaw == nil {
				createRaw = []byte(defaultPolicyConfigJSON)
			}

			now := s.now().UnixMilli()
			newBinding := models.PolicyBinding{
				Scope:         scope,
				GroupID:       groupID,
				CredentialID:  credentialID,
				Revision:      1,
				SchemaVersion: 1,
				Config:        models.JSON(createRaw),
				CreatedAtMS:   now,
				UpdatedAtMS:   now,
			}
			if cErr := tx.Create(&newBinding).Error; cErr != nil {
				return app_errors.ParseDBError(cErr)
			}
			finalRevision = 1
			finalConfigRaw = string(createRaw)
			return nil
		}

		if qErr != nil {
			return app_errors.ParseDBError(qErr)
		}

		targetRaw := toSaveRaw
		if targetRaw == nil {
			// 遗漏 config 保持原有不变，但必须确保原存储配置通过编译校验
			if _, cErr := policy.Compile(current.Config); cErr != nil {
				return app_errors.ErrMalformedPolicyStorage
			}
			targetRaw = current.Config
		}

		nextRevision := current.Revision + 1
		result := tx.Model(&models.PolicyBinding{}).
			Where("id = ?", current.ID).
			Updates(map[string]any{
				"config":        models.JSON(targetRaw),
				"revision":      nextRevision,
				"updated_at_ms": s.now().UnixMilli(),
			})
		if result.Error != nil {
			return app_errors.ParseDBError(result.Error)
		}

		finalRevision = nextRevision
		finalConfigRaw = string(targetRaw)
		return nil
	}, nil)

	if err != nil {
		return PolicyBindingResponse{}, err
	}
	return policyBindingDTO(scope, groupID, credentialID, finalRevision, finalConfigRaw), nil
}

// HTTP Handlers

func (s *Server) handleGetGroupPolicy(c *gin.Context) {
	s.handlePolicyBinding(c, "get_group_policy", "group", false)
}

func (s *Server) handleUpdateGroupPolicy(c *gin.Context) {
	s.handlePolicyBinding(c, "update_group_policy", "group", true)
}

func (s *Server) handleGetCredentialPolicy(c *gin.Context) {
	s.handlePolicyBinding(c, "get_credential_policy", "credential", false)
}

func (s *Server) handleUpdateCredentialPolicy(c *gin.Context) {
	s.handlePolicyBinding(c, "update_credential_policy", "credential", true)
}

// handlePolicyBinding 共用四个策略路由的传输外壳：admin、ID 提取顺序、严格绑定与错误映射。
// 各服务方法仍各自校验作用域与 ID 归属，事务与覆盖保存语义不变。
func (s *Server) handlePolicyBinding(c *gin.Context, op, scope string, update bool) {
	if !s.requireAdminPrincipal(c, op) {
		return
	}
	grpID, ok := groupID(c, op)
	if !ok {
		return
	}
	crdID := uint(0)
	if scope == "credential" {
		crdID, ok = credentialID(c, op)
		if !ok {
			return
		}
	}
	var request PolicyUpdateRequest
	if update {
		if err := bindStrictPolicyJSON(c, &request); err != nil {
			writeServiceError(c, op, mapControlJSONError(err))
			return
		}
	}
	var result PolicyBindingResponse
	var err error
	switch {
	case update && scope == "credential":
		result, err = s.service.UpdateCredentialPolicy(c.Request.Context(), grpID, crdID, request)
	case update:
		result, err = s.service.UpdateGroupPolicy(c.Request.Context(), grpID, request)
	case scope == "credential":
		result, err = s.service.GetCredentialPolicy(c.Request.Context(), grpID, crdID)
	default:
		result, err = s.service.GetGroupPolicy(c.Request.Context(), grpID)
	}
	if err != nil {
		writeServiceError(c, op, err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func bindStrictPolicyJSON(c *gin.Context, target any) error {
	r := c.Request
	limited := io.LimitReader(r.Body, int64(maxStrictPolicyRequestBytes)+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(raw) > maxStrictPolicyRequestBytes {
		return &http.MaxBytesError{Limit: int64(maxStrictPolicyRequestBytes)}
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return decodeStrictControlJSONObject(raw, target)
}

func (s *Server) requireAdminPrincipal(c *gin.Context, op string) bool {
	principal, ok := currentControlPrincipal(c)
	if !ok || principal.Type != controlPrincipalAdmin {
		writeServiceError(c, op, app_errors.ErrForbidden)
		return false
	}
	return true
}
