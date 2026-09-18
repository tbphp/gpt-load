package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"gpt-load/internal/accessquota"
	"gpt-load/internal/automodel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/parameteroverride"
	platformhttp "gpt-load/internal/platform/httpclient"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/telemetry"
)

var reasonAutoModelUnavailable = reason{http.StatusServiceUnavailable, "auto_model_unavailable", "Automatic model selection is unavailable."}
var reasonAutoModelForbidden = reason{http.StatusForbidden, "auto_model_target_forbidden", "Automatic model entry or fallback target is not permitted for this request."}
var reasonAutoModelUnsupported = reason{http.StatusBadRequest, "auto_model_operation_unsupported", "Automatic model selection is unsupported for this operation."}

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

func (handler *Handler) autoDecisionClient(snapshot *state.ConfigSnapshot) (automodel.HTTPDoer, error) {
	if handler.decisionClient != nil {
		return handler.decisionClient, nil
	}
	handler.decisionMu.Lock()
	defer handler.decisionMu.Unlock()
	if handler.decisionHTTP != nil && handler.decisionProxy == snapshot.GlobalProxy {
		return handler.decisionHTTP, nil
	}
	client, err := handler.decisionClients.NewClientForOutboundProxy(&platformhttp.Config{ConnectTimeout: 2 * time.Second, TLSHandshakeTimeout: 2 * time.Second, DisableRedirects: true, MaxIdleConns: 10, MaxIdleConnsPerHost: 5, IdleConnTimeout: time.Minute, ForceAttemptHTTP2: true}, snapshot.GlobalProxy)
	if err != nil {
		return nil, err
	}
	if handler.decisionHTTP != nil {
		handler.decisionHTTP.CloseIdleConnections()
	}
	handler.decisionHTTP, handler.decisionProxy = client, snapshot.GlobalProxy
	return client, nil
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
		if len(scheduler.CandidateGroupIDsForQuery(snapshot, query)) == 0 {
			continue
		}
		presets = append(presets, preset)
		fallback = fallback || preset.ID == entry.Fallback
	}
	return presets, fallback
}

func (handler *Handler) prepareAutoModel(ctx context.Context, snapshot *state.ConfigSnapshot, key state.AccessKeyView, selectedDialect dialect.Dialect, parsed *dialect.ParsedRequest, metadata dialect.RequestMetadata, bound *automodel.Selection, admit func() *reason, websocketQueries ...scheduler.Query) (*dialect.ParsedRequest, dialect.RequestMetadata, *automodel.Decision, *reason) {
	entry, exists := snapshot.AutoModels.Lookup(optionalModelValue(metadata.Model))
	if !exists || !entry.Enabled || !snapshot.AutoModels.Enabled() {
		return parsed, metadata, nil, &reasonAutoModelUnavailable
	}
	if metadata.Operation != execution.OperationChatCompletion && metadata.Operation != execution.OperationResponsesCreate {
		return parsed, metadata, nil, &reasonAutoModelUnsupported
	}
	query := scheduler.Query{ClientProtocol: selectedDialect.Protocol()}
	if len(websocketQueries) > 0 {
		query = websocketQueries[0]
	}
	presets, fallbackAllowed := allowedAutoPresets(snapshot, key, entry, metadata, query)
	decision := &automodel.Decision{Source: "fallback", Status: "fallback", PromptVersion: automodel.PromptVersion,
		CostState: "not_applicable", PricingCompleteness: "not_applicable"}
	var chosen automodel.CompiledPreset
	if bound != nil {
		if bound.EntryID != entry.ID || bound.EntryName != entry.Name {
			return parsed, metadata, nil, &reasonResponseBindingNotFound
		}
		// 续接保留已保存的预设内容，不根据管理员的新预设重新映射。
		model := bound.TargetModel
		query.ExternalModel, query.AccessKey, query.Operation = &model, key, metadata.Operation
		query.RouteRequirement, query.ResponsesStorePreference = metadata.RouteRequirement, metadata.ResponsesStorePreference
		if len(key.Filters.Models) > 0 {
			if _, allowed := key.Filters.Models[entry.Name]; !allowed {
				return parsed, metadata, nil, &reasonAutoModelForbidden
			}
		}
		if len(scheduler.CandidateGroupIDsForQuery(snapshot, query)) == 0 {
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
		decision.Source, decision.Status, decision.Selection = "binding", "reused", *bound
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
	if bound == nil {
		view, reason := automodel.Extract(selectedDialect.Protocol(), parsed.Body)
		decision.Reason, decision.ContextTruncated = reason, view.ContextTruncated
		if reason == "" {
			client, err := handler.autoDecisionClient(snapshot)
			if err != nil {
				decision.Reason = "proxy_unavailable"
			}
			if client != nil {
				result := automodel.Decide(ctx, client, snapshot.AutoModels, presets, view, key.PriceMultiplier)
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
		decision.Selection = automodel.Selection{EntryID: entry.ID, EntryName: entry.Name, PresetID: chosen.ID, PresetName: chosen.Name, TargetModel: chosen.Model, ParameterOverrides: chosen.ParameterOverrides, ConfigRevision: snapshot.Revision}
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
	return rewritten, updated, decision, nil
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
	return &copy
}

// 保留原有分量的报价和舍入；只将两笔已知金额安全相加。
func addDecisionCost(answer int64, decision *automodel.Decision) int64 {
	return telemetry.TotalPricing(telemetry.PricingObservation{EstimatedCostNanoUSD: answer}, decision).EstimatedCostNanoUSD
}
