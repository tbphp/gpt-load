package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/automodel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

var reasonAutoModelUnavailable = reason{http.StatusServiceUnavailable, "auto_model_unavailable", "Automatic model selection is unavailable."}
var reasonAutoModelForbidden = reason{http.StatusForbidden, "auto_model_target_forbidden", "Automatic model entry or fallback target is not permitted for this request."}
var reasonAutoModelUnsupported = reason{http.StatusBadRequest, "auto_model_operation_unsupported", "Automatic model selection is unsupported for this operation."}

type autoDecisionRunner interface {
	Decide(
		context.Context,
		*state.ConfigSnapshot,
		state.AccessKeyView,
		[]automodel.CompiledPreset,
		automodel.TaskState,
	) automodel.Decision
}

func (handler *Handler) admitAutoQuota(snapshot *state.ConfigSnapshot, admission *requestAccessQuotaAdmission) *reason {
	if admission == nil || admission.admitted || handler.accessQuota == nil {
		return nil
	}
	var decision accessquota.Decision
	var current bool
	admission.ticket, decision, current = handler.admitAccessQuotaForSnapshot(snapshot, admission.accessKeyID, handler.quotaNow())
	if !current {
		return &reasonConfigurationChanged
	}
	if !decision.Allowed {
		return &reasonAccessKeyCostLimitExceeded
	}
	admission.admitted = true
	return nil
}

func allowedAutoPresets(snapshot *state.ConfigSnapshot, key state.AccessKeyView, entry automodel.CompiledEntry, metadata dialect.RequestMetadata, query scheduler.Query) ([]automodel.CompiledPreset, bool) {
	if len(key.Filters.Models) > 0 {
		if _, allowed := key.Filters.Models[entry.Name]; !allowed {
			return nil, false
		}
	}
	var presets []automodel.CompiledPreset
	fallback := false
	for _, preset := range entry.Presets {
		model := preset.Model
		query.ExternalModel, query.AccessKey = &model, key
		query.Operation, query.RouteRequirement = metadata.Operation, metadata.RouteRequirement
		query.ResponsesStorePreference = metadata.ResponsesStorePreference
		if !hasAutoModelCandidates(snapshot, query) {
			continue
		}
		presets = append(presets, preset)
		fallback = fallback || preset.ID == entry.Fallback
	}
	return presets, fallback
}

// 续接仍限制原凭据所在的 Group，不能把其他 Group 的模型交给 Jev 选择。
func hasAutoModelCandidates(snapshot *state.ConfigSnapshot, query scheduler.Query) bool {
	for _, groupID := range scheduler.CandidateGroupIDsForQuery(snapshot, query) {
		if query.AllowedCredentialRefs == nil {
			return true
		}
		for _, ref := range query.AllowedCredentialRefs {
			if ref.GroupID == groupID {
				return true
			}
		}
	}
	return false
}

func (handler *Handler) prepareAutoModel(ctx context.Context, snapshot *state.ConfigSnapshot, key state.AccessKeyView, selectedDialect dialect.Dialect, parsed *dialect.ParsedRequest, metadata dialect.RequestMetadata, bound *automodel.Selection, admit func() *reason, executionQueries ...scheduler.Query) (*dialect.ParsedRequest, dialect.RequestMetadata, *automodel.Decision, *reason) {
	entry, exists := snapshot.AutoModels.Lookup(optionalModelValue(metadata.Model))
	if !exists || !entry.Enabled || !snapshot.AutoModels.Enabled() {
		return parsed, metadata, nil, &reasonAutoModelUnavailable
	}
	if metadata.Operation != execution.OperationChatCompletion && metadata.Operation != execution.OperationResponsesCreate {
		return parsed, metadata, nil, &reasonAutoModelUnsupported
	}
	query := scheduler.Query{ClientProtocol: selectedDialect.Protocol()}
	if len(executionQueries) > 0 {
		query = executionQueries[0]
	}
	query.ClientProtocol, query.AccessKey = selectedDialect.Protocol(), key
	query.Operation, query.RouteRequirement = metadata.Operation, metadata.RouteRequirement
	query.ResponsesStorePreference = metadata.ResponsesStorePreference
	view, extractReason := automodel.Extract(selectedDialect.Protocol(), parsed.Body)
	prewarm := false
	if metadata.Operation == execution.OperationResponsesCreate {
		var options struct {
			Generate *bool `json:"generate"`
		}
		if err := json.Unmarshal(parsed.Body, &options); err != nil {
			return parsed, metadata, nil, &reasonInvalidProtocolRequest
		}
		prewarm = options.Generate != nil && !*options.Generate
	}
	skipPrewarm := prewarm && extractReason != ""
	sameTask := bound != nil && view.ExecutionPhase == automodel.ExecutionPhaseToolContinuation &&
		view.TaskFingerprint != "" && bound.TaskFingerprint == view.TaskFingerprint &&
		bound.ConfigRevision == snapshot.Revision
	reuse := bound != nil && (skipPrewarm ||
		(extractReason == "task_missing" && view.ExecutionPhase != automodel.ExecutionPhaseUserTask) || sameTask)
	presets, fallbackAllowed := allowedAutoPresets(snapshot, key, entry, metadata, query)
	cacheKey := autoTaskKey{accessKeyID: key.ID, entryID: entry.ID, revision: snapshot.Revision, fingerprint: view.TaskFingerprint}
	var cachedPreset *automodel.CompiledPreset
	if !reuse && !skipPrewarm && extractReason == "" && fallbackAllowed {
		if value, found := handler.autoTasks.lookup(cacheKey, handler.now()); found &&
			(view.ExecutionPhase == automodel.ExecutionPhaseToolContinuation || value.prewarm) {
			for index := range presets {
				if presets[index].ID == value.presetID {
					cachedPreset = &presets[index]
					break
				}
			}
		}
	}
	decision := &automodel.Decision{Source: "fallback", Status: "fallback", PromptVersion: automodel.PromptVersion,
		ExecutionPhase: view.ExecutionPhase, CostState: "not_applicable", PricingCompleteness: "not_applicable"}
	var chosen automodel.CompiledPreset
	if bound != nil {
		if bound.EntryID != entry.ID || bound.EntryName != entry.Name {
			return parsed, metadata, nil, &reasonResponseBindingNotFound
		}
	}
	if bound != nil && (reuse || !fallbackAllowed) {
		// 只有无新任务的工具续接沿用上一档；新任务会重新判断。
		model := bound.TargetModel
		query.ExternalModel = &model
		if len(key.Filters.Models) > 0 {
			if _, allowed := key.Filters.Models[entry.Name]; !allowed {
				return parsed, metadata, nil, &reasonAutoModelForbidden
			}
		}
		if !hasAutoModelCandidates(snapshot, query) {
			return parsed, metadata, nil, &reasonAutoModelForbidden
		}
		var raw any
		decoder := json.NewDecoder(bytes.NewReader(bound.ParameterOverrides))
		decoder.UseNumber()
		if decoder.Decode(&raw) != nil {
			return parsed, metadata, nil, &reasonResponseBindingNotFound
		}
		rules, err := parameteroverride.Compile(raw)
		if err != nil || rules.ValidateResponsesContinuation() != nil {
			return parsed, metadata, nil, &reasonResponseBindingNotFound
		}
		chosen = automodel.CompiledPreset{Preset: automodel.Preset{ID: bound.PresetID, Name: bound.PresetName, Model: bound.TargetModel, ParameterOverrides: bound.ParameterOverrides}, Rules: rules}
	} else if cachedPreset != nil {
		chosen = *cachedPreset
	} else {
		if !fallbackAllowed {
			return parsed, metadata, nil, &reasonAutoModelForbidden
		}
		for _, preset := range presets {
			if preset.ID == entry.Fallback {
				chosen = preset
				break
			}
		}
	}
	if failure := admit(); failure != nil {
		return parsed, metadata, nil, failure
	}
	singlePreset := len(presets) == 1 && fallbackAllowed && !skipPrewarm && !reuse && extractReason == ""
	decision.Reason, decision.ContextTruncated = extractReason, view.ContextTruncated
	if skipPrewarm {
		decision.Source, decision.Status, decision.Reason = "prewarm", "skipped", "prewarm"
	} else if reuse {
		reason := "no_new_task"
		if sameTask {
			reason = "same_task"
		}
		decision.Source, decision.Status, decision.Reason, decision.Selection = "binding", "reused", reason, *bound
	} else if cachedPreset != nil {
		decision.Source, decision.Status, decision.Reason = "task_cache", "reused", "same_task"
	} else if singlePreset {
		chosen = presets[0]
		decision.Source, decision.Status, decision.Reason = "single_preset", "selected", ""
	} else {
		if extractReason == "" {
			var result automodel.Decision
			if handler.decisionClient != nil {
				result = handler.decisionClient.Decide(ctx, snapshot, key, presets, view)
			} else {
				result = handler.executeAutoDecision(ctx, snapshot, key, presets, view)
			}
			decision = &result
			if result.Status == "selected" {
				for _, preset := range presets {
					if preset.ID == result.Choice {
						chosen = preset
						break
					}
				}
			}
		}
	}
	if decision.Source != "binding" {
		decision.Selection = automodel.Selection{EntryID: entry.ID, EntryName: entry.Name, PresetID: chosen.ID, PresetName: chosen.Name, TargetModel: chosen.Model, ParameterOverrides: chosen.ParameterOverrides, ConfigRevision: snapshot.Revision, TaskFingerprint: view.TaskFingerprint}
	}
	if ctx.Err() != nil {
		return parsed, metadata, decision, nil
	}
	rewriter, supported := selectedDialect.(dialect.ModelRewriter)
	if !supported {
		return parsed, metadata, decision, &reasonAutoModelUnsupported
	}
	rewritten, err := rewriter.RewriteRequestModel(parsed, chosen.Model)
	if err != nil {
		return parsed, metadata, decision, &reasonInvalidProtocolRequest
	}
	body, _, err := chosen.Rules.Apply(selectedDialect.Protocol(), metadata.Operation, chosen.Model, rewritten.Body)
	if err != nil || int64(len(body)) > maxRequestBodyBytes {
		return parsed, metadata, decision, &reasonParameterOverrideUnavailable
	}
	rewritten.Body = body
	updated, err := selectedDialect.InspectRequest(rewritten)
	if err != nil {
		return parsed, metadata, decision, &reasonParameterOverrideUnavailable
	}
	decision.PresetReasoning = updated.Reasoning.Clone()
	if decision.Source == "jev" && decision.Status == "selected" {
		handler.autoTasks.record(cacheKey, autoTaskPreset{presetID: chosen.ID, prewarm: prewarm}, handler.now())
	}
	if !prewarm {
		// 正式请求到达后消费关联的预热选择，新任务不能重新捡起旧预热。
		for _, selection := range []*automodel.Selection{bound, &decision.Selection} {
			if selection == nil {
				continue
			}
			usedKey := autoTaskKey{accessKeyID: key.ID, entryID: selection.EntryID, revision: selection.ConfigRevision, fingerprint: selection.TaskFingerprint}
			handler.autoTasks.consumePrewarm(usedKey, selection.PresetID)
		}
	}
	return rewritten, updated, decision, nil
}

func (handler *Handler) executeAutoDecision(ctx context.Context, snapshot *state.ConfigSnapshot, key state.AccessKeyView, presets []automodel.CompiledPreset, view automodel.TaskState) automodel.Decision {
	payload, decision := automodel.BuildDecisionRequest(snapshot.AutoModels, presets, view)
	if len(payload) == 0 {
		return decision
	}
	observed, result := handler.executeJevDecision(ctx, snapshot, key, payload, false)
	decision.Provider = observed.Provider
	decision.GroupID = observed.GroupID
	decision.GroupName = observed.GroupName
	decision.ChannelID = observed.ChannelID
	decision.ChannelName = observed.ChannelName
	decision.CredentialID = observed.CredentialID
	decision.RequestedModel = observed.RequestedModel
	decision.UpstreamModel = observed.UpstreamModel
	decision.ReportedModel = observed.ReportedModel
	decision.RequestID = observed.RequestID
	decision.DurationMs = observed.DurationMs
	decision.Called = observed.Called
	decision.InputTokens = observed.InputTokens
	decision.OutputTokens = observed.OutputTokens
	decision.ProviderCostUSD = observed.ProviderCostUSD
	decision.CostState = observed.CostState
	decision.PricingCompleteness = observed.PricingCompleteness
	decision.EstimatedCostNanoUSD = observed.EstimatedCostNanoUSD
	decision.Receipt = observed.Receipt
	decision.Reason = observed.Reason
	if observed.Called && result.HasResponse() {
		decision = automodel.InterpretDecisionResponse(decision, presets, result.StatusCode, result.Header, result.Body, result.Usage)
	}
	// 答案解析不能覆盖执行层已规范化和脱敏的观测元数据。
	decision.ReportedModel = observed.ReportedModel
	decision.RequestID = observed.RequestID
	return decision
}

func (recorder *requestRecorder) autoSelection() *automodel.Selection {
	if recorder == nil || recorder.autoDecision == nil {
		return nil
	}
	selection := recorder.autoDecision.Selection
	return &selection
}

func (recorder *requestRecorder) autoLogDecision() *automodel.Decision {
	if recorder.autoDecision == nil {
		return nil
	}
	copy := *recorder.autoDecision
	copy.Selection.ParameterOverrides = nil
	copy.Selection.TaskFingerprint = ""
	return &copy
}

// 保留原有分量的报价和舍入；只将两笔已知金额安全相加。
func addDecisionCost(answer int64, decision *automodel.Decision) int64 {
	return telemetry.TotalPricing(telemetry.PricingObservation{EstimatedCostNanoUSD: answer}, decision).EstimatedCostNanoUSD
}
