package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	"gpt-load/internal/jev"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/scheduler"
	"gpt-load/internal/state"
	"gpt-load/internal/usage"
)

func (handler *Handler) executeJevDecision(ctx context.Context, snapshot *state.ConfigSnapshot, key state.AccessKeyView, payload []byte, protected bool) (decision jev.Observation, result UpstreamResult) {
	decision = jev.Observation{RequestedModel: snapshot.Jev.Model, CostState: "not_applicable", PricingCompleteness: "not_applicable"}
	if ctx.Err() != nil {
		decision.Reason = "canceled"
		return decision, result
	}
	config := snapshot.Jev
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
	if config.GroupID != 0 {
		query.AccessKey.Filters.Groups = map[uint]struct{}{config.GroupID: {}}
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
		return decision, result
	}
	ref, ok := allowedRefs[selection.CredentialID]
	if !ok || ref.GroupID != selection.GroupID {
		decision.Reason = "no_candidate"
		return decision, result
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
		return decision, result
	}
	decrypted, err := handler.encryption.Decrypt(encrypted)
	if err != nil {
		decision.Reason = "credential_unavailable"
		return decision, result
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
		return decision, result
	}
	effectiveProxy, proxyFingerprint, err := resolveAttemptProxy(
		handler.encryption,
		selection.Group.Proxy,
		ref,
	)
	if err != nil {
		decision.Reason = "proxy_unavailable"
		return decision, result
	}
	body, _, err := selection.Group.ParameterOverrides.Apply(
		protocol.Decisions,
		execution.OperationDecisionsCreate,
		model,
		payload,
	)
	if err != nil || int64(len(body)) > maxRequestBodyBytes {
		decision.Reason = "parameter_override_unavailable"
		return decision, result
	}
	if protected && !sameDecisionContent(payload, body) {
		decision.Reason = "parameter_override_unavailable"
		return decision, result
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
		return decision, result
	}
	requestID, err := handler.newRequestID()
	if err != nil {
		decision.Reason = "internal_error"
		return decision, result
	}
	frozenPricing := handler.freezeAttemptPricing(selection, metadata, true, key.PriceMultiplier)
	result = handler.forwarder.Forward(decisionCtx, ForwardInput{
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
		var metadata struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(result.Body, &metadata) == nil && len(metadata.Model) <= 255 {
			decision.ReportedModel = metadata.Model
		}
		if requestID := result.Header.Get("X-Request-Id"); len(requestID) <= 255 {
			decision.RequestID = requestID
		}
		if result.StatusCode < 200 || result.StatusCode >= 300 {
			decision.Reason = "upstream_error"
		}
		if result.Usage.State == usage.StateComplete || result.Usage.State == usage.StatePartial {
			input := result.Usage.Tokens.UncachedInput
			decision.InputTokens = &input
			if result.Usage.State == usage.StateComplete {
				output := result.Usage.Tokens.Output
				decision.OutputTokens = &output
			}
		}
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
		return decision, result
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
	return decision, result
}

func sameDecisionContent(before, after []byte) bool {
	var a, b map[string]json.RawMessage
	if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil {
		return false
	}
	for _, field := range []string{"state", "questions"} {
		var av, bv any
		beforeDecoder := json.NewDecoder(bytes.NewReader(a[field]))
		afterDecoder := json.NewDecoder(bytes.NewReader(b[field]))
		beforeDecoder.UseNumber()
		afterDecoder.UseNumber()
		if beforeDecoder.Decode(&av) != nil || afterDecoder.Decode(&bv) != nil {
			return false
		}
		ar, _ := json.Marshal(av)
		br, _ := json.Marshal(bv)
		if !bytes.Equal(ar, br) {
			return false
		}
	}
	return true
}
