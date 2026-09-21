package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/automodel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/parameteroverride"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
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

func (handler *Handler) executeAutoDecision(
	ctx context.Context,
	snapshot *state.ConfigSnapshot,
	key state.AccessKeyView,
	presets []automodel.CompiledPreset,
	view automodel.TaskState,
) (decision automodel.Decision) {
	payload, initial := automodel.BuildDecisionRequest(snapshot.AutoModels, presets, view)
	decision = initial
	if len(payload) == 0 {
		return decision
	}
	if ctx.Err() != nil {
		decision.Reason = "canceled"
		return decision
	}
	config := snapshot.AutoModels.Config()
	decisionCtx, cancel := context.WithTimeout(ctx, time.Duration(config.TimeoutSeconds)*time.Second)
	defer cancel()
	started := handler.now()
	defer func() {
		decision.DurationMs = handler.now().Sub(started).Milliseconds()
		if decision.DurationMs < 0 {
			decision.DurationMs = 0
		}
	}()

	model := config.Model
	query := scheduler.Query{
		ClientProtocol:   protocol.Decisions,
		Operation:        execution.OperationDecisionsCreate,
		RouteRequirement: execution.RouteRequirementNative,
		ExternalModel:    &model,
	}
	groupIDs := scheduler.CandidateGroupIDsForQuery(snapshot, query)
	refs := handler.registry.CaptureActiveCredentialRefs(groupIDs)
	allowedIDs := make(map[uint]struct{}, len(refs))
	allowedRefs := make(map[uint]state.CredentialRef, len(refs))
	for _, ref := range refs {
		allowedIDs[ref.ID] = struct{}{}
		allowedRefs[ref.ID] = ref
	}
	query.AllowedCredentialIDs = allowedIDs
	query.AllowedCredentialRefs = allowedRefs
	selection, err := scheduler.New(snapshot, handler.registry, query).Next()
	if err != nil {
		decision.Reason = "no_candidate"
		return decision
	}
	ref, ok := allowedRefs[selection.CredentialID]
	if !ok || ref.GroupID != selection.GroupID {
		decision.Reason = "no_candidate"
		return decision
	}
	decision.Provider = string(selection.ChannelID)
	decision.GroupID = selection.GroupID
	decision.GroupName = selection.Group.Name
	decision.ChannelID = string(selection.ChannelID)
	if descriptor, exists := handler.channels.Get(selection.ChannelID); exists {
		decision.ChannelName = descriptor.Name
	}
	decision.CredentialID = selection.CredentialID
	decision.UpstreamModel = optionalModelValue(selection.UpstreamModelID)

	encrypted, active := handler.registry.ActiveEncryptedCredentialDataIfMatch(ref)
	if !active {
		decision.Reason = "credential_unavailable"
		return decision
	}
	decrypted, err := handler.encryption.Decrypt(encrypted)
	if err != nil {
		decision.Reason = "credential_unavailable"
		return decision
	}
	credential, err := normalizeChannelCredential(
		handler.channels,
		handler.subscriptions,
		selection.ChannelID,
		selection.Group.ConnectionType,
		decrypted,
	)
	decrypted = ""
	if err != nil {
		decision.Reason = "credential_unavailable"
		return decision
	}
	effectiveProxy, proxyFingerprint, err := resolveAttemptProxy(
		handler.encryption,
		selection.Group.Proxy,
		ref,
	)
	if err != nil {
		decision.Reason = "proxy_unavailable"
		return decision
	}
	body, _, err := selection.Group.ParameterOverrides.Apply(
		protocol.Decisions,
		execution.OperationDecisionsCreate,
		model,
		payload,
	)
	if err != nil || int64(len(body)) > maxRequestBodyBytes {
		decision.Reason = "parameter_override_unavailable"
		return decision
	}
	request := &dialect.ParsedRequest{
		Method: http.MethodPost,
		Path:   decisionsPath,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   body,
	}
	decisionDialect := dialect.NewDecisions()
	metadata, err := decisionDialect.InspectRequest(request)
	if err != nil {
		decision.Reason = "invalid_request"
		return decision
	}
	requestID, err := handler.newRequestID()
	if err != nil {
		decision.Reason = "internal_error"
		return decision
	}
	frozenPricing := handler.freezeAttemptPricing(selection, metadata, true, key.PriceMultiplier)
	result := handler.forwarder.Forward(decisionCtx, ForwardInput{
		Dialect: decisionDialect, ObserveUsage: true,
		Group: selection.Group, APIKey: credential.apiKey,
		CredentialSecrets: credential.secrets, Request: request,
		ExternalModel: model, UpstreamModelID: optionalModelValue(selection.UpstreamModelID),
		RequestID: requestID, AttemptID: requestID + ":1", AttemptSequence: 1,
		ClientProtocol: protocol.Decisions, Operation: execution.OperationDecisionsCreate,
		RouteRequirement: execution.RouteRequirementNative,
		ChannelID:        string(selection.ChannelID), RouteMode: execution.RouteMode(selection.RouteMode),
		TargetConfig: selection.ResolvedTarget.TargetConfig,
		Credential: execution.NewCredentialSnapshot(
			selection.CredentialID,
			ref.Version,
			ref.IdentityGeneration,
			credential.payload,
		),
		Proxy: effectiveProxy, ProxyFingerprint: proxyFingerprint,
	})
	result = normalizeUpstreamResultContract(result)
	decision.Called = result.DispatchState == execution.DispatchMaybeSent
	if decision.Called {
		decision.CostState = string(pricing.CostStateUnpriced)
		decision.PricingCompleteness = string(pricing.CompletenessUnavailable)
	}
	attemptNow := handler.now()
	judgment := judgeUpstreamResult(result, attemptNow, health.DecisionContext{
		DefaultRateLimitCooldown: fixedCooldown,
		Method:                   http.MethodPost,
		Operation:                execution.OperationDecisionsCreate,
	})
	if result.DispatchState != execution.DispatchLocal && !result.ProviderErrorBeforeCommit &&
		result.HasResponse() && result.StatusCode >= http.StatusOK && result.StatusCode < http.StatusMultipleChoices {
		handler.recordCredentialSuccess(ref, attemptNow)
	}
	handler.applyGroupDecisionEffect(
		selection.Group,
		ref,
		refreshCooldownCredentialVersion(result, ref.Version),
		judgment,
		result.StatusCode,
		attemptNow,
		optionalModelValue(selection.UpstreamModelID),
	)

	if decision.Called && result.HasResponse() {
		decision = automodel.InterpretDecisionResponse(
			decision,
			presets,
			result.StatusCode,
			result.Header,
			result.Body,
			result.Usage,
		)
	} else {
		switch {
		case decisionCtx.Err() != nil && ctx.Err() == nil:
			decision.Reason = "timeout"
		case ctx.Err() != nil || errors.Is(result.Err, context.Canceled):
			decision.Reason = "canceled"
		case result.ExecutionError != nil && result.ExecutionError.Kind == execution.ErrorKindInvalidRequest:
			decision.Reason = "invalid_request"
		default:
			decision.Reason = "transport_error"
		}
	}
	if decision.Called && result.ResponseModelObserved {
		decision.ReportedModel = result.UpstreamReportedModel
	}
	if decision.Called && result.UpstreamRequestID != "" {
		decision.RequestID = result.UpstreamRequestID
	}
	if decision.Called {
		redactor := redact.New()
		decision.ReportedModel = redactor.String(decision.ReportedModel, credential.secrets...)
		decision.RequestID = redactor.String(decision.RequestID, credential.secrets...)
	}
	if !decision.Called {
		return decision
	}
	quote := quoteFrozenAttempt(
		frozenPricing,
		result.Usage,
		effectivePricingMode(metadata.PricingMode),
	)
	decision.CostState = quote.CostState
	decision.PricingCompleteness = quote.PricingCompleteness
	decision.EstimatedCostNanoUSD = quote.EstimatedCostNanoUSD
	if quote.ReceiptJSON != "" {
		decision.Receipt = json.RawMessage(quote.ReceiptJSON)
	}
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
