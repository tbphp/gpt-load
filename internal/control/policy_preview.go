package control

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/policy"
	"gpt-load/internal/pricing"
	"gpt-load/internal/state"
)

const (
	maxPreviewCandidates = 50
	maxPreviewTargets    = 10
	maxPreviewTotalNodes = 10000

	caveatCodeNoSessionAffinity      = "policy.preview.caveat.no_session_affinity"
	caveatCodeMixedSimulationContext = "policy.preview.caveat.mixed_simulation_context"
)

type PolicyPreviewRequest struct {
	RequestModel  string               `json:"request_model"`
	SimulatedTime *string              `json:"simulated_time"`
	Config        optionalPolicyConfig `json:"config"`
}

type PolicyPreviewRuleActionResponse struct {
	Type       string `json:"type"`
	Factor     string `json:"factor,omitempty"`
	Multiplier string `json:"multiplier,omitempty"`
}

type PolicyPreviewSchedulingReasonResponse struct {
	RuleID       string `json:"rule_id"`
	NameSnapshot string `json:"name_snapshot"`
	Domain       string `json:"domain"`
}

type PolicyPreviewRuleResponse struct {
	RuleID       string                          `json:"rule_id"`
	NameSnapshot string                          `json:"name_snapshot"`
	Domain       policy.Domain                   `json:"domain"`
	Enabled      bool                            `json:"enabled"`
	Status       policy.RuleStatus               `json:"status"`
	BindingScope string                          `json:"binding_scope"`
	Provenance   string                          `json:"provenance"`
	RevisionText string                          `json:"revision_text"`
	Condition    policy.NodeInspectResult        `json:"condition"`
	Action       PolicyPreviewRuleActionResponse `json:"action"`
}

type PolicyPreviewPricingMatchResponse struct {
	RuleID       string `json:"rule_id"`
	NameSnapshot string `json:"name_snapshot"`
	Domain       string `json:"domain"`
	Factor       string `json:"factor"`
	Multiplier   string `json:"multiplier"`
	BindingScope string `json:"binding_scope"`
	Provenance   string `json:"provenance"`
	RevisionText string `json:"revision_text"`
}

type PolicyPreviewPricingResultResponse struct {
	Matches              []PolicyPreviewPricingMatchResponse `json:"matches"`
	Factors              []string                            `json:"factors"`
	CumulativeMultiplier string                              `json:"cumulative_multiplier"`
}

type PolicyPreviewSchedulingResultResponse struct {
	Excluded bool                                   `json:"excluded"`
	Reason   *PolicyPreviewSchedulingReasonResponse `json:"reason,omitempty"`
}

type PolicyPreviewTargetResponse struct {
	UpstreamModel   string                                `json:"upstream_model"`
	Available       bool                                  `json:"available"`
	HardFilterState string                                `json:"hard_filter_state,omitempty"`
	GroupRules      []PolicyPreviewRuleResponse           `json:"group_rules"`
	CredentialRules []PolicyPreviewRuleResponse           `json:"credential_rules"`
	Scheduling      PolicyPreviewSchedulingResultResponse `json:"scheduling"`
	Pricing         PolicyPreviewPricingResultResponse    `json:"pricing"`
}

type PolicyPreviewCandidateResponse struct {
	CredentialID       uint                          `json:"credential_id"`
	CredentialName     string                        `json:"credential_name"`
	CredentialVersion  string                        `json:"credential_version,omitempty"`
	IdentityGeneration string                        `json:"identity_generation,omitempty"`
	Targets            []PolicyPreviewTargetResponse `json:"targets"`
}

type PolicyPreviewResponse struct {
	SnapshotRevisionText string                           `json:"snapshot_revision_text,omitempty"`
	ServerTime           string                           `json:"server_time"`
	ServerTimeZoneOffset string                           `json:"server_time_zone_offset"`
	SimulatedTime        *string                          `json:"simulated_time,omitempty"`
	CaveatCodes          []string                         `json:"caveat_codes"`
	Candidates           []PolicyPreviewCandidateResponse `json:"candidates"`
}

func externalModelName(m state.ModelConfig) string {
	if alias := strings.TrimSpace(m.Alias); alias != "" {
		return alias
	}
	return strings.TrimSpace(m.ID)
}

// PreviewGroupPolicy 执行分组级纯策略草稿预览
func (s *Service) PreviewGroupPolicy(
	ctx context.Context,
	groupID uint,
	req PolicyPreviewRequest,
) (PolicyPreviewResponse, error) {
	if groupID == 0 {
		return PolicyPreviewResponse{}, app_errors.ErrBadRequest
	}
	return s.previewPolicyInternal(ctx, "group", groupID, 0, req)
}

// PreviewCredentialPolicy 执行凭据级纯策略草稿预览
func (s *Service) PreviewCredentialPolicy(
	ctx context.Context,
	groupID uint,
	credentialID uint,
	req PolicyPreviewRequest,
) (PolicyPreviewResponse, error) {
	if groupID == 0 || credentialID == 0 {
		return PolicyPreviewResponse{}, app_errors.ErrBadRequest
	}
	return s.previewPolicyInternal(ctx, "credential", groupID, credentialID, req)
}

func (s *Service) previewPolicyInternal(
	ctx context.Context,
	scope string,
	groupID uint,
	credentialID uint,
	req PolicyPreviewRequest,
) (PolicyPreviewResponse, error) {
	requestModel := strings.TrimSpace(req.RequestModel)
	if requestModel == "" {
		return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
			app_errors.ErrValidation,
			"request_model is required for runnable preview",
		)
	}

	observation, err := s.captureRuntimeObservation()
	if err != nil {
		return PolicyPreviewResponse{}, err
	}
	snapshot := observation.snapshot
	keys := observation.keys
	realNow := observation.observedAt

	group, exists := snapshot.Groups[groupID]
	if !exists {
		return PolicyPreviewResponse{}, app_errors.ErrResourceNotFound
	}

	// 筛选评估候选凭据并严格验证归属所有权（防止跨组凭据窥探）
	var candidates []state.CredentialRuntimeView
	if scope == "group" {
		for _, k := range keys {
			if k.GroupID == groupID {
				candidates = append(candidates, k)
			}
		}
	} else {
		var found bool
		for _, k := range keys {
			if k.ID == credentialID {
				if k.GroupID != groupID {
					return PolicyPreviewResponse{}, app_errors.ErrResourceNotFound
				}
				candidates = append(candidates, k)
				found = true
				break
			}
		}
		if !found {
			return PolicyPreviewResponse{}, app_errors.ErrResourceNotFound
		}
	}

	if len(candidates) > maxPreviewCandidates {
		return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
			app_errors.ErrValidation,
			fmt.Sprintf("candidate count %d exceeds maximum preview budget of %d", len(candidates), maxPreviewCandidates),
		)
	}

	// 模拟时间解析与程序日历/时区转换
	var simTimeText *string
	conditionNow := realNow
	caveatCodes := []string{caveatCodeNoSessionAffinity}

	if req.SimulatedTime != nil && strings.TrimSpace(*req.SimulatedTime) != "" {
		trimmedSim := strings.TrimSpace(*req.SimulatedTime)
		parsed, pErr := time.Parse(time.RFC3339, trimmedSim)
		if pErr != nil {
			return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
				app_errors.ErrValidation,
				"invalid simulated_time, expected RFC3339 format",
			)
		}
		inLoc := parsed.In(realNow.Location())
		conditionNow = inLoc
		formattedSim := inLoc.Format(time.RFC3339)
		simTimeText = &formattedSim
		caveatCodes = append(caveatCodes, caveatCodeMixedSimulationContext)
	}

	// 解析或编译 Draft 配置
	var draftCompiled *policy.CompiledConfig
	var isDraftProvided bool

	if req.Config.Specified {
		if req.Config.IsNull {
			return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
				app_errors.ErrValidation,
				"config cannot be null",
			)
		}
		raw := bytes.TrimSpace(req.Config.Raw)
		if len(raw) == 0 {
			return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
				app_errors.ErrValidation,
				"config cannot be empty",
			)
		}
		compiled, cErr := policy.Compile(raw)
		if cErr != nil {
			return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
				app_errors.ErrValidation,
				cErr.Error(),
			)
		}
		draftCompiled = compiled
		isDraftProvided = true
	}

	// 分组草稿仅允许 inherit：与保存、运行时编译保持一致
	if scope == "group" && draftCompiled.GroupPolicyMode() == policy.GroupPolicyOverride {
		return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
			app_errors.ErrValidation,
			"group scope policy cannot use group_policy 'override'",
		)
	}

	// 解析请求模型对应的有效上游目标
	seenTargets := make(map[string]struct{})
	var targets []string
	for _, m := range group.Models {
		clientName := externalModelName(m)
		if clientName == requestModel {
			if _, seen := seenTargets[m.ID]; !seen {
				seenTargets[m.ID] = struct{}{}
				targets = append(targets, m.ID)
			}
		}
	}
	if len(targets) == 0 {
		return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
			app_errors.ErrValidation,
			fmt.Sprintf("request_model %q has no matching targets in group", requestModel),
		)
	}
	if len(targets) > maxPreviewTargets {
		return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
			app_errors.ErrValidation,
			fmt.Sprintf("target count %d exceeds maximum preview budget of %d", len(targets), maxPreviewTargets),
		)
	}

	// 预算与求值使用同一来源选择，不计入被覆盖模式忽略的分组规则。
	selectPolicies := func(id uint) (groupConfig, credConfig *policy.CompiledConfig, groupDraft, credDraft bool) {
		var savedGroup, savedCred *policy.CompiledConfig
		if snapshot.Policies != nil {
			savedGroup = snapshot.Policies.GroupPolicy(groupID)
			savedCred = snapshot.Policies.CredentialPolicy(id)
		}
		currentCred := savedCred
		if scope == "credential" && isDraftProvided {
			currentCred = draftCompiled
		}
		if currentCred.GroupPolicyMode() == policy.GroupPolicyOverride {
			return nil, currentCred, false, scope == "credential" && isDraftProvided
		}
		if scope == "group" && isDraftProvided {
			return draftCompiled, currentCred, true, false
		}
		return savedGroup, currentCred, false, scope == "credential" && isDraftProvided
	}

	var estimatedTotalNodes int
	for _, cred := range candidates {
		gp, cp, _, _ := selectPolicies(cred.ID)
		estimatedTotalNodes += len(targets) * (gp.NodeCount() + cp.NodeCount())
	}
	if estimatedTotalNodes > maxPreviewTotalNodes {
		return PolicyPreviewResponse{}, app_errors.NewAPIErrorWithData(
			app_errors.ErrValidation,
			fmt.Sprintf("estimated condition nodes %d exceeds maximum preview budget of %d", estimatedTotalNodes, maxPreviewTotalNodes),
		)
	}

	// 纯求值各候选与目标
	candidateResponses := make([]PolicyPreviewCandidateResponse, 0, len(candidates))
	for _, cred := range candidates {
		groupCompiledToUse, credCompiledToUse, isGroupDraft, isCredDraft := selectPolicies(cred.ID)
		var groupSavedRev, credSavedRev uint64
		if snapshot.Policies != nil {
			groupSavedRev = snapshot.Policies.GroupRevision(groupID)
			credSavedRev = snapshot.Policies.CredentialRevision(cred.ID)
		}
		targetResponses := make([]PolicyPreviewTargetResponse, 0, len(targets))
		for _, targetUpstream := range targets {
			evalCtx := &policy.EvalContext{
				Now:           conditionNow,
				QuotaNow:      realNow,
				RequestModel:  policy.StringFact{Value: requestModel, State: policy.FactStateMeasured},
				UpstreamModel: policy.StringFact{Value: targetUpstream, State: policy.FactStateMeasured},
				QuotaWindows:  cred.QuotaWindows,
			}

			schedulingRes := PolicyPreviewSchedulingResultResponse{}
			pricingRes := PolicyPreviewPricingResultResponse{
				Matches: []PolicyPreviewPricingMatchResponse{},
				Factors: []string{},
			}
			var multipliers []pricing.PriceMultiplier
			// 在解释规则的同一次遍历中生成响应，分组在前、账号在后。
			evalPolicyBinding := func(scopeName string, compiled *policy.CompiledConfig, isDraft bool, savedRev uint64) []PolicyPreviewRuleResponse {
				ruleResponses := make([]PolicyPreviewRuleResponse, 0)
				if compiled == nil {
					return ruleResponses
				}
				inspectRes := compiled.Inspect(evalCtx)
				prov := "saved"
				revText := strconv.FormatUint(savedRev, 10)
				if isDraft {
					prov = "draft"
					revText = "draft"
				}
				for _, r := range inspectRes.Rules {
					actDTO := PolicyPreviewRuleActionResponse{
						Type:   string(r.Action.Type),
						Factor: r.Action.Factor,
					}
					if r.Action.Multiplier > 0 {
						actDTO.Multiplier = pricing.FormatPriceMultiplier(r.Action.Multiplier)
					}
					ruleResponses = append(ruleResponses, PolicyPreviewRuleResponse{
						RuleID:       r.RuleID,
						NameSnapshot: r.NameSnapshot,
						Domain:       r.Domain,
						Enabled:      r.Enabled,
						Status:       r.Status,
						BindingScope: scopeName,
						Provenance:   prov,
						RevisionText: revText,
						Condition:    r.Condition,
						Action:       actDTO,
					})
					if r.Status != policy.RuleStatusHit {
						continue
					}
					if r.Domain == policy.DomainScheduling && r.Action.Type == policy.ActionExcludeCandidate {
						if !schedulingRes.Excluded {
							schedulingRes.Excluded = true
							schedulingRes.Reason = &PolicyPreviewSchedulingReasonResponse{
								RuleID:       r.RuleID,
								NameSnapshot: r.NameSnapshot,
								Domain:       string(r.Domain),
							}
						}
					} else if r.Domain == policy.DomainPricing && r.Action.Type == policy.ActionMultiplyPrice {
						pricingRes.Matches = append(pricingRes.Matches, PolicyPreviewPricingMatchResponse{
							RuleID:       r.RuleID,
							NameSnapshot: r.NameSnapshot,
							Domain:       string(r.Domain),
							Factor:       r.Action.Factor,
							Multiplier:   pricing.FormatPriceMultiplier(r.Action.Multiplier),
							BindingScope: scopeName,
							Provenance:   prov,
							RevisionText: revText,
						})
						pricingRes.Factors = append(pricingRes.Factors, r.Action.Factor)
						multipliers = append(multipliers, r.Action.Multiplier)
					}
				}
				return ruleResponses
			}
			groupRuleResponses := evalPolicyBinding("group", groupCompiledToUse, isGroupDraft, groupSavedRev)
			credRuleResponses := evalPolicyBinding("credential", credCompiledToUse, isCredDraft, credSavedRev)
			pricingRes.CumulativeMultiplier = computeCumulativeMultiplierString(multipliers)

			// 硬性可用性状态与模型冷却
			available := true
			hardFilterState := ""

			if group.WeightManual != nil && *group.WeightManual <= 0 {
				available = false
				hardFilterState = "group_weight"
			} else if cred.WeightManual != nil && *cred.WeightManual <= 0 {
				available = false
				hardFilterState = "credential_weight"
			} else if !cred.AuthReady() {
				available = false
				hardFilterState = "auth"
			} else {
				switch cred.RuntimeState(realNow) {
				case state.CredentialRuntimeBlacklisted:
					available = false
					hardFilterState = "blacklisted"
				case state.CredentialRuntimeCooldown:
					available = false
					hardFilterState = "cooldown"
				case state.CredentialRuntimeDisabled:
					available = false
					hardFilterState = "disabled"
				default:
					if until, ok := cred.ModelCooldowns[targetUpstream]; ok && until.After(realNow) {
						available = false
						hardFilterState = "model_cooldown"
					}
				}
			}

			targetResponses = append(targetResponses, PolicyPreviewTargetResponse{
				UpstreamModel:   targetUpstream,
				Available:       available,
				HardFilterState: hardFilterState,
				GroupRules:      groupRuleResponses,
				CredentialRules: credRuleResponses,
				Scheduling:      schedulingRes,
				Pricing:         pricingRes,
			})
		}

		alias := observation.credentialNames[cred.ID]
		if alias == "" {
			alias = fmt.Sprintf("Credential %d", cred.ID)
		}

		candidateResponses = append(candidateResponses, PolicyPreviewCandidateResponse{
			CredentialID:       cred.ID,
			CredentialName:     alias,
			CredentialVersion:  strconv.FormatUint(cred.Version, 10),
			IdentityGeneration: strconv.FormatUint(cred.IdentityGeneration, 10),
			Targets:            targetResponses,
		})
	}

	snapshotRevText := ""
	if snapshot != nil {
		snapshotRevText = strconv.FormatUint(snapshot.Revision, 10)
	}

	return PolicyPreviewResponse{
		SnapshotRevisionText: snapshotRevText,
		ServerTime:           realNow.Format(time.RFC3339),
		ServerTimeZoneOffset: realNow.Format("-07:00"),
		SimulatedTime:        simTimeText,
		CaveatCodes:          caveatCodes,
		Candidates:           candidateResponses,
	}, nil
}

func computeCumulativeMultiplierString(multipliers []pricing.PriceMultiplier) string {
	n := len(multipliers)
	if n == 0 {
		return "1"
	}
	for _, m := range multipliers {
		if m == 0 {
			return "0"
		}
	}
	product := big.NewInt(1)
	for _, m := range multipliers {
		product.Mul(product, big.NewInt(int64(m)))
	}

	scale := 6 * n
	str := product.String()
	var formatted string
	if len(str) <= scale {
		formatted = "0." + strings.Repeat("0", scale-len(str)) + str
	} else {
		integerPart := str[:len(str)-scale]
		fractionalPart := str[len(str)-scale:]
		formatted = integerPart + "." + fractionalPart
	}
	trimmed := strings.TrimRight(formatted, "0")
	trimmed = strings.TrimRight(trimmed, ".")
	if trimmed == "" {
		return "0"
	}
	return trimmed
}

// HTTP Handlers

func (s *Server) handlePreviewGroupPolicy(c *gin.Context) {
	if !s.requireAdminPrincipal(c, "preview_group_policy") {
		return
	}
	id, ok := groupID(c, "preview_group_policy")
	if !ok {
		return
	}
	var req PolicyPreviewRequest
	if err := bindStrictPolicyJSON(c, &req); err != nil {
		writeServiceError(c, "preview_group_policy", mapControlJSONError(err))
		return
	}
	result, err := s.service.PreviewGroupPolicy(c.Request.Context(), id, req)
	if err != nil {
		writeServiceError(c, "preview_group_policy", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handlePreviewCredentialPolicy(c *gin.Context) {
	if !s.requireAdminPrincipal(c, "preview_credential_policy") {
		return
	}
	grpID, ok := groupID(c, "preview_credential_policy")
	if !ok {
		return
	}
	crdID, ok := credentialID(c, "preview_credential_policy")
	if !ok {
		return
	}
	var req PolicyPreviewRequest
	if err := bindStrictPolicyJSON(c, &req); err != nil {
		writeServiceError(c, "preview_credential_policy", mapControlJSONError(err))
		return
	}
	result, err := s.service.PreviewCredentialPolicy(c.Request.Context(), grpID, crdID, req)
	if err != nil {
		writeServiceError(c, "preview_credential_policy", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}
