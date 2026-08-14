// Package cpa adapts the execution-only CPA bridge to GPT-Load's neutral
// executor contract. GPT-Load remains the owner of selection, retry and health.
package cpa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/execution/responsealias"
	"gpt-load/internal/health"
	"gpt-load/internal/platform/encryption"
	platformredact "gpt-load/internal/platform/redact"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/state"
	stateloader "gpt-load/internal/state/loader"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

const (
	refreshLeadTime        = 5 * time.Minute
	refreshFinalizeTimeout = 5 * time.Second
	codexUpstreamAPI       = execution.UpstreamAPIOpenAIResponses
)

var convertedRepresentationHeaderNames = [...]string{
	"Content-Encoding",
	"Content-Length",
	"ETag",
	"Digest",
	"Content-MD5",
	"Content-Range",
	"Content-Digest",
	"Repr-Digest",
	"Signature",
	"Signature-Input",
}

type Adapter struct {
	db             *gorm.DB
	encryption     encryption.Service
	registry       *state.CredentialRegistry
	mutations      *health.MutationCoordinator
	executor       cpaembedded.HTTPExecutor
	refresh        func(context.Context, cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error)
	replaceSecret  func(uint, uint64, uint64, string, string) bool
	reconcileGroup func(uint, []state.CredentialEntry) (bool, error)
	now            func() time.Time
}

func NewAdapter(db *gorm.DB, encryptionService encryption.Service, registry *state.CredentialRegistry, mutations *health.MutationCoordinator) *Adapter {
	if mutations == nil {
		mutations = health.NewMutationCoordinator()
	}
	return &Adapter{
		db: db, encryption: encryptionService, registry: registry, mutations: mutations,
		executor: cpaembedded.NewCodexHTTPExecutor(), now: time.Now,
		replaceSecret:  registry.ReplaceCredentialSecretIfMatch,
		reconcileGroup: registry.ReconcileGroup,
		refresh: func(ctx context.Context, credential cpaembedded.CodexCredential) (cpaembedded.CodexCredential, error) {
			return cpaembedded.RefreshCodexCredentialOnce(ctx, credential, cpaembedded.Options{})
		},
	}
}

// PrepareCodexCredential applies the same durable refresh lifecycle used by
// data-plane requests before a control-plane Codex call.
func (a *Adapter) PrepareCodexCredential(
	ctx context.Context,
	snapshot execution.CredentialSnapshot,
) (cpaembedded.CodexCredential, *execution.ErrorEvidence) {
	return a.prepare(ctx, snapshot, false)
}

func (a *Adapter) Execute(ctx context.Context, spec execution.AttemptSpec) execution.AttemptResult {
	spec = execution.NewAttemptSpec(spec)
	if err := a.validateSpec(spec); err != nil {
		return unaryNotSent(execution.ErrorKindInvalidRequest, "unsupported subscription request", "", err)
	}
	credential, evidence := a.prepare(ctx, spec.Credential, spec.ForceCredentialRefresh)
	if evidence != nil {
		return execution.AttemptResult{DispatchState: execution.DispatchNotSent, Error: evidence}
	}
	execCtx, cancel := withRequestTimeout(ctx, spec.Timeouts.Request)
	defer cancel()
	response, err := a.executor.ExecuteCanonical(execCtx, strconv.FormatUint(uint64(spec.Credential.ID), 10), credential, bridgeRequest(spec))
	if err != nil {
		result := unaryExecutionError(execCtx, err, credential)
		result.UpstreamAPI = codexUpstreamAPI
		result.AppliedReasoning = appliedReasoning(response.AppliedReasoningEffort)
		return result
	}
	body := append([]byte(nil), response.Payload...)
	headers := convertedResponseHeaders(response.Headers, "application/json")
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: appliedReasoning(response.AppliedReasoningEffort), StatusCode: http.StatusOK,
		Header: headers, Body: body, Model: responseModel(body, spec.UpstreamModel),
		UpstreamRequestID: upstreamRequestID(headers), Usage: usageEvidence(spec.ClientProtocol, body),
	}
}

func (a *Adapter) ExecuteStream(ctx context.Context, spec execution.AttemptSpec, sink execution.StreamSink) execution.StreamResult {
	spec = execution.NewAttemptSpec(spec)
	if sink == nil {
		return streamNotSent(execution.ErrorKindInvalidRequest, "stream sink is required", "")
	}
	if err := a.validateSpec(spec); err != nil {
		return streamNotSent(execution.ErrorKindInvalidRequest, "unsupported subscription request", "")
	}
	credential, evidence := a.prepare(ctx, spec.Credential, spec.ForceCredentialRefresh)
	if evidence != nil {
		return execution.StreamResult{DispatchState: execution.DispatchNotSent, Error: evidence}
	}
	execCtx, cancel := withRequestTimeout(ctx, spec.Timeouts.Request)
	defer cancel()
	streamCtx, cancelStream := context.WithCancelCause(execCtx)
	defer cancelStream(context.Canceled)
	firstByte := startFirstByteGate(spec.Timeouts.FirstByte, cancelStream)
	defer firstByte.stop()
	response, err := a.executor.ExecuteStreamCanonical(streamCtx, strconv.FormatUint(uint64(spec.Credential.ID), 10), credential, bridgeRequest(spec))
	if err != nil {
		result := unaryExecutionError(streamCtx, err, credential)
		var applied *reasoning.Config
		if response != nil {
			applied = appliedReasoning(response.AppliedReasoningEffort)
		}
		return execution.StreamResult{
			DispatchState: result.DispatchState, ResponseStarted: result.ResponseStarted,
			UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied,
			StatusCode: result.StatusCode, Header: result.Header,
			UpstreamRequestID: result.UpstreamRequestID, Error: result.Error,
		}
	}
	applied := appliedReasoning(response.AppliedReasoningEffort)
	headers := convertedResponseHeaders(response.Headers, "text/event-stream")
	sequence := uint64(1)
	ready := false
	openAIDone := false
	geminiTerminal := false
	for {
		idleTimeout := spec.Timeouts.StreamIdle
		if !ready {
			idleTimeout = 0
		}
		chunk, ok, idleErr := nextChunk(streamCtx, response.Chunks, idleTimeout)
		if idleErr != nil {
			return streamExecutionError(headers, idleErr, credential, applied, ready)
		}
		if !ok {
			if err := context.Cause(streamCtx); err != nil {
				return streamExecutionError(headers, err, credential, applied, ready)
			}
			if !ready {
				return streamInternalError(headers, applied, "subscription upstream stream ended without data", false)
			}
			if spec.ClientProtocol == protocol.OpenAICompletions && !openAIDone {
				sequence++
				if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: []byte("data: [DONE]\n\n")}); err != nil {
					return streamConsumerStopped(headers, applied, ready)
				}
			}
			if spec.ClientProtocol == protocol.Gemini && !geminiTerminal {
				sequence++
				if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: []byte("data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")}); err != nil {
					return streamConsumerStopped(headers, applied, ready)
				}
			}
			return successfulStreamTerminal(spec, headers, applied)
		}
		if chunk.Err != nil {
			return streamExecutionError(headers, chunk.Err, credential, applied, ready)
		}
		if len(chunk.Payload) == 0 {
			continue
		}
		firstByte.stop()
		if err := context.Cause(streamCtx); err != nil {
			return streamExecutionError(headers, err, credential, applied, false)
		}
		payload := frameSSE(spec.ClientProtocol, chunk.Payload)
		payload, rewriteErr := rewriteStreamModelAlias(spec, payload)
		if rewriteErr != nil {
			return streamInternalError(headers, applied, "rewrite subscription response model", ready)
		}
		if spec.ClientProtocol == protocol.OpenAICompletions && isOpenAIDone(payload) {
			openAIDone = true
		}
		if spec.ClientProtocol == protocol.Gemini && isGeminiTerminal(payload) {
			geminiTerminal = true
		}
		if !ready {
			if err := sink(execution.StreamEvent{Kind: execution.StreamEventReady, Sequence: sequence, StatusCode: http.StatusOK, Header: headers}); err != nil {
				return streamConsumerStopped(headers, applied, false)
			}
			ready = true
		}
		sequence++
		if err := sink(execution.StreamEvent{Kind: execution.StreamEventData, Sequence: sequence, Data: payload}); err != nil {
			return streamConsumerStopped(headers, applied, ready)
		}
	}
}

func (a *Adapter) validateSpec(spec execution.AttemptSpec) error {
	if a == nil || a.db == nil || a.encryption == nil || a.registry == nil || a.executor == nil {
		return fmt.Errorf("subscription executor is unavailable")
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if spec.ConnectionType != "subscription" || spec.ChannelID != string(channel.Codex) {
		return fmt.Errorf("unsupported subscription target")
	}
	switch spec.ClientProtocol {
	case protocol.OpenAIResponses:
		if spec.Operation != execution.OperationResponsesCreate {
			return fmt.Errorf("unsupported subscription operation")
		}
	case protocol.OpenAICompletions, protocol.Anthropic, protocol.Gemini:
		if spec.Operation != execution.OperationChatCompletion {
			return fmt.Errorf("unsupported subscription operation")
		}
	default:
		return fmt.Errorf("unsupported subscription protocol")
	}
	if !json.Valid(spec.Body) {
		return fmt.Errorf("subscription request body must be JSON")
	}
	return nil
}

func (a *Adapter) prepare(ctx context.Context, snapshot execution.CredentialSnapshot, forceRefresh bool) (cpaembedded.CodexCredential, *execution.ErrorEvidence) {
	credential, err := cpaembedded.ParseCodexCredentialJSON(snapshot.Data())
	if err != nil {
		return cpaembedded.CodexCredential{}, localEvidence("credential_invalid", "subscription credential is invalid")
	}
	if !forceRefresh {
		if expiration, ok := cpaembedded.CodexCredentialExpiresAt(credential); !ok || expiration.After(a.now().Add(refreshLeadTime)) {
			return credential, nil
		}
	}
	var prepared cpaembedded.CodexCredential
	var prepareErr *execution.ErrorEvidence
	a.mutations.Do(snapshot.ID, func() {
		prepared, prepareErr = a.refreshCredentialLocked(ctx, snapshot.ID, snapshot.Version, forceRefresh)
	})
	return prepared, prepareErr
}

func (a *Adapter) refreshCredentialLocked(
	ctx context.Context,
	credentialID uint,
	expectedVersion uint64,
	forceRefresh bool,
) (cpaembedded.CodexCredential, *execution.ErrorEvidence) {
	var row models.Credential
	if err := a.db.WithContext(ctx).First(&row, credentialID).Error; err != nil {
		return cpaembedded.CodexCredential{}, localEvidence("credential_unavailable", "subscription credential is unavailable")
	}
	if row.AuthState != models.CredentialAuthStateReady {
		return cpaembedded.CodexCredential{}, authEvidence(string(row.AuthState))
	}
	plaintext, err := a.encryption.Decrypt(row.Data)
	if err != nil {
		return cpaembedded.CodexCredential{}, localEvidence("credential_decrypt_failed", "subscription credential is unavailable")
	}
	current, err := cpaembedded.ParseCodexCredentialJSON([]byte(plaintext))
	plaintext = ""
	if err != nil {
		return cpaembedded.CodexCredential{}, localEvidence("credential_invalid", "subscription credential is invalid")
	}
	if forceRefresh && row.SecretVersion > expectedVersion {
		return current, nil
	}
	if !forceRefresh {
		if expiration, ok := cpaembedded.CodexCredentialExpiresAt(current); !ok || expiration.After(a.now().Add(refreshLeadTime)) {
			return current, nil
		}
	}
	if err := a.transitionAuthState(ctx, row, row.SecretVersion, models.CredentialAuthStateRefreshing, ""); err != nil {
		finalizeContext, cancel := refreshFinalizeContext(ctx)
		restoreErr := a.setAuthState(finalizeContext, row.ID, row.SecretVersion, models.CredentialAuthStateReady, "")
		if restoreErr == nil {
			restoreErr = a.publishAuthState(finalizeContext, row, models.CredentialAuthStateReady)
		}
		cancel()
		if restoreErr != nil {
			a.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
			return cpaembedded.CodexCredential{}, authEvidence("refresh_registry_mismatch")
		}
		return cpaembedded.CodexCredential{}, localEvidence("refresh_start_failed", "subscription credential refresh could not start")
	}
	refreshed, refreshErr := a.refresh(ctx, current)
	if refreshErr != nil {
		stateValue, code := models.CredentialAuthStateOutcomeUnknown, "refresh_outcome_unknown"
		var tokenErr *cpaembedded.TokenEndpointError
		if errors.Is(refreshErr, cpaembedded.ErrCredentialIdentityChanged) {
			stateValue, code = models.CredentialAuthStateReauthorizationRequired, "refresh_identity_changed"
		} else if errors.As(refreshErr, &tokenErr) && cpaembedded.IsDefinitiveRefreshRejection(tokenErr.Code) {
			stateValue, code = models.CredentialAuthStateReauthorizationRequired, "refresh_rejected"
		}
		if err := a.transitionAuthState(ctx, row, row.SecretVersion, stateValue, code); err != nil {
			a.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
			return cpaembedded.CodexCredential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return cpaembedded.CodexCredential{}, authEvidence(code)
	}
	if refreshed.AccountID != current.AccountID {
		if err := a.transitionAuthState(ctx, row, row.SecretVersion, models.CredentialAuthStateReauthorizationRequired, "refresh_identity_changed"); err != nil {
			a.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
			return cpaembedded.CodexCredential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return cpaembedded.CodexCredential{}, authEvidence("refresh_identity_changed")
	}
	canonical, err := json.Marshal(refreshed)
	if err != nil {
		if markErr := a.markRefreshOutcomeUnknown(ctx, row, row.SecretVersion, "refresh_persist_failed"); markErr != nil {
			return cpaembedded.CodexCredential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return cpaembedded.CodexCredential{}, authEvidence("refresh_persist_failed")
	}
	ciphertext, err := a.encryption.Encrypt(string(canonical))
	fingerprint := a.encryption.Hash(string(canonical))
	clear(canonical)
	if err != nil {
		if markErr := a.markRefreshOutcomeUnknown(ctx, row, row.SecretVersion, "refresh_persist_failed"); markErr != nil {
			return cpaembedded.CodexCredential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return cpaembedded.CodexCredential{}, authEvidence("refresh_persist_failed")
	}
	nextVersion := row.SecretVersion + 1
	finalizeContext, cancelFinalize := refreshFinalizeContext(ctx)
	updated := a.db.WithContext(finalizeContext).Model(&models.Credential{}).
		Where("id = ? AND secret_version = ? AND auth_state = ?", row.ID, row.SecretVersion, models.CredentialAuthStateRefreshing).
		Updates(map[string]any{
			"data": ciphertext, "fingerprint": fingerprint, "secret_version": nextVersion,
			"auth_state": models.CredentialAuthStateReady, "auth_error_code": "", "updated_at_ms": a.now().UnixMilli(),
		})
	cancelFinalize()
	if updated.Error != nil || updated.RowsAffected != 1 {
		if markErr := a.markRefreshOutcomeUnknown(ctx, row, row.SecretVersion, "refresh_commit_failed"); markErr != nil {
			return cpaembedded.CodexCredential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
		}
		return cpaembedded.CodexCredential{}, authEvidence("refresh_commit_failed")
	}
	if a.replaceSecret == nil || !a.replaceSecret(row.ID, row.SecretVersion, nextVersion, fingerprint, ciphertext) {
		// The rotated token is durable. Reconcile this Group from DB truth so a
		// failed incremental publication cannot leave control and data planes at
		// different secret versions.
		reconcileContext, cancelReconcile := refreshFinalizeContext(ctx)
		entries, reconcileErr := stateloader.BuildGroupCredentialEntries(reconcileContext, a.db, row.GroupID)
		if reconcileErr != nil {
			cancelReconcile()
		} else if a.reconcileGroup == nil {
			reconcileErr = errors.New("credential registry reconciliation is unavailable")
			cancelReconcile()
		} else {
			_, reconcileErr = a.reconcileGroup(row.GroupID, entries)
			cancelReconcile()
		}
		if reconcileErr != nil {
			if markErr := a.markRefreshOutcomeUnknown(ctx, row, nextVersion, "refresh_registry_mismatch"); markErr != nil {
				a.registry.SetCredentialAuthState(row.ID, state.CredentialAuthStateOutcomeUnknown)
				return cpaembedded.CodexCredential{}, localEvidence("refresh_state_commit_failed", "subscription credential state could not be saved")
			}
			return cpaembedded.CodexCredential{}, authEvidence("refresh_registry_mismatch")
		}
	}
	return refreshed, nil
}

func (a *Adapter) markRefreshOutcomeUnknown(ctx context.Context, row models.Credential, version uint64, code string) error {
	return a.transitionAuthState(ctx, row, version, models.CredentialAuthStateOutcomeUnknown, code)
}

func (a *Adapter) setAuthState(ctx context.Context, credentialID uint, version uint64, authState models.CredentialAuthState, code string) error {
	result := a.db.WithContext(ctx).Model(&models.Credential{}).
		Where("id = ? AND secret_version = ?", credentialID, version).
		Updates(map[string]any{"auth_state": authState, "auth_error_code": code, "updated_at_ms": a.now().UnixMilli()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("credential auth state update affected %d rows", result.RowsAffected)
	}
	return nil
}

func (a *Adapter) transitionAuthState(
	ctx context.Context,
	row models.Credential,
	version uint64,
	authState models.CredentialAuthState,
	code string,
) error {
	finalizeContext, cancel := refreshFinalizeContext(ctx)
	defer cancel()
	if err := a.setAuthState(finalizeContext, row.ID, version, authState, code); err != nil {
		return err
	}
	return a.publishAuthState(finalizeContext, row, authState)
}

func (a *Adapter) publishAuthState(
	ctx context.Context,
	row models.Credential,
	authState models.CredentialAuthState,
) error {
	if a.registry.SetCredentialAuthState(row.ID, state.CredentialAuthState(authState)) {
		return nil
	}
	entries, err := stateloader.BuildGroupCredentialEntries(ctx, a.db, row.GroupID)
	if err != nil {
		return err
	}
	if a.reconcileGroup == nil {
		return fmt.Errorf("credential registry reconciliation is unavailable")
	}
	_, err = a.reconcileGroup(row.GroupID, entries)
	return err
}

func refreshFinalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), refreshFinalizeTimeout)
}

func bridgeRequest(spec execution.AttemptSpec) cpaembedded.ExecuteRequest {
	return cpaembedded.ExecuteRequest{
		Model: spec.UpstreamModel, Payload: append([]byte(nil), spec.Body...),
		Format: formatFor(spec.ClientProtocol), Headers: spec.Header.Clone(),
		OriginalRequest: append([]byte(nil), spec.Body...),
	}
}

func formatFor(clientProtocol protocol.Protocol) string {
	switch clientProtocol {
	case protocol.OpenAIResponses:
		return "openai-response"
	case protocol.Anthropic:
		return "claude"
	case protocol.Gemini:
		return "gemini"
	default:
		return "openai"
	}
}

func convertedResponseHeaders(source http.Header, contentType string) http.Header {
	headers := source.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	for _, name := range convertedRepresentationHeaderNames {
		for key := range headers {
			if strings.EqualFold(key, name) {
				delete(headers, key)
			}
		}
	}
	headers.Set("Content-Type", contentType)
	return headers
}

func unaryExecutionError(ctx context.Context, err error, credential cpaembedded.CodexCredential) execution.AttemptResult {
	status, evidence := executionErrorEvidence(ctx, err, credential)
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: status != 0,
		StatusCode: status, Error: evidence,
	}
}

func streamExecutionError(headers http.Header, err error, credential cpaembedded.CodexCredential, applied *reasoning.Config, responseStarted bool) execution.StreamResult {
	status, evidence := executionErrorEvidence(context.Background(), err, credential)
	responseStarted = responseStarted || status != 0
	if responseStarted && status == 0 {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied, StatusCode: status,
		Header: headers, UpstreamRequestID: upstreamRequestID(headers), Error: evidence,
	}
}

func streamInternalError(headers http.Header, applied *reasoning.Config, summary string, responseStarted bool) execution.StreamResult {
	status := 0
	if responseStarted {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied, StatusCode: status,
		Header: headers.Clone(), UpstreamRequestID: upstreamRequestID(headers),
		Error: &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Summary: summary},
	}
}

func streamConsumerStopped(headers http.Header, applied *reasoning.Config, responseStarted bool) execution.StreamResult {
	status := 0
	if responseStarted {
		status = http.StatusOK
	}
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: responseStarted,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied, StatusCode: status,
		Header: headers.Clone(), UpstreamRequestID: upstreamRequestID(headers),
		Error: &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Summary: "stream consumer stopped"},
	}
}

func executionErrorEvidence(ctx context.Context, err error, credential cpaembedded.CodexCredential) (int, *execution.ErrorEvidence) {
	if err == nil {
		return 0, nil
	}
	status := 0
	if value, ok := err.(interface{ StatusCode() int }); ok {
		status = value.StatusCode()
	}
	kind := execution.ErrorKindTransport
	if status != 0 {
		kind = execution.ErrorKindHTTP
	} else if errors.Is(err, context.DeadlineExceeded) || ctx != nil && errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		kind = execution.ErrorKindTimeout
	} else if errors.Is(err, context.Canceled) || ctx != nil && errors.Is(context.Cause(ctx), context.Canceled) {
		kind = execution.ErrorKindCanceled
	} else if _, ok := err.(net.Error); ok {
		kind = execution.ErrorKindTransport
	}
	typeValue, codeValue := errorTypeCode(err.Error())
	evidence := &execution.ErrorEvidence{
		Kind: kind, StatusCode: status, Type: typeValue, Code: codeValue,
		Summary: safeErrorSummary(err, credential),
	}
	if retry, ok := err.(interface{ RetryAfter() *time.Duration }); ok && retry.RetryAfter() != nil && *retry.RetryAfter() > 0 {
		evidence.RetryAfter = *retry.RetryAfter()
	}
	switch {
	case status == http.StatusUnauthorized:
		evidence.ReplaySafety = execution.ReplaySafetyUnknown
		if explicitAccessTokenExpiration(codeValue) {
			evidence.Hint = execution.FailureHintRefreshRequired
			evidence.ReplaySafety = execution.ReplaySafetyRejectedBeforeProcessing
		}
	case status == http.StatusTooManyRequests:
		evidence.Hint = execution.FailureHintRateLimited
	default:
		if status >= 500 {
			evidence.Hint = execution.FailureHintHostError
		}
	}
	return status, evidence
}

func explicitAccessTokenExpiration(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "token_expired", "access_token_expired", "expired_token":
		return true
	default:
		return false
	}
}

func safeErrorSummary(err error, credential cpaembedded.CodexCredential) string {
	fallback := "subscription upstream request failed"
	if err == nil {
		return fallback
	}
	summary := platformredact.ExtractErrorMessage([]byte(err.Error()))
	if summary == "" {
		summary = err.Error()
	}
	summary = platformredact.New().String(
		summary,
		credential.AccessToken,
		credential.RefreshToken,
		credential.IDToken,
		credential.AccountID,
		credential.Email,
	)
	summary = strings.Join(strings.Fields(strings.ToValidUTF8(summary, "\uFFFD")), " ")
	if summary == "" {
		summary = fallback
	}
	if utf8.RuneCountInString(summary) > execution.MaxErrorSummaryLength {
		summary = string([]rune(summary)[:execution.MaxErrorSummaryLength])
	}
	return summary
}

func errorTypeCode(value string) (string, string) {
	var payload struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(value), &payload) != nil {
		return "", ""
	}
	return safeScalar(payload.Error.Type), safeScalar(payload.Error.Code)
}

func safeScalar(value string) string {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n\x00") || len(value) > 128 {
		return ""
	}
	return value
}

func authEvidence(code string) *execution.ErrorEvidence {
	return &execution.ErrorEvidence{
		Kind: execution.ErrorKindProvider, Hint: execution.FailureHintReauthorizationRequired,
		Code: safeScalar(code), Summary: "subscription account requires reauthorization",
	}
}

func localEvidence(code, summary string) *execution.ErrorEvidence {
	return &execution.ErrorEvidence{Kind: execution.ErrorKindInternal, Code: safeScalar(code), Summary: summary}
}

func unaryNotSent(kind execution.ErrorKind, summary, code string, _ error) execution.AttemptResult {
	return execution.AttemptResult{DispatchState: execution.DispatchNotSent, Error: &execution.ErrorEvidence{Kind: kind, Code: code, Summary: summary}}
}

func streamNotSent(kind execution.ErrorKind, summary, code string) execution.StreamResult {
	return execution.StreamResult{DispatchState: execution.DispatchNotSent, Error: &execution.ErrorEvidence{Kind: kind, Code: code, Summary: summary}}
}

func successfulStreamTerminal(spec execution.AttemptSpec, headers http.Header, applied *reasoning.Config) execution.StreamResult {
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent, ResponseStarted: true,
		UpstreamAPI: codexUpstreamAPI, AppliedReasoning: applied, StatusCode: http.StatusOK,
		Header: headers, UpstreamRequestID: upstreamRequestID(headers), Model: spec.UpstreamModel,
	}
}

func appliedReasoning(effort string) *reasoning.Config {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" || len(effort) > 64 {
		return nil
	}
	for _, character := range effort {
		if (character < 'a' || character > 'z') && character != '-' && character != '_' {
			return nil
		}
	}
	return &reasoning.Config{Effort: effort}
}

func nextChunk(ctx context.Context, chunks <-chan cpaembedded.ExecuteStreamChunk, timeout time.Duration) (cpaembedded.ExecuteStreamChunk, bool, error) {
	if timeout <= 0 {
		select {
		case chunk, ok := <-chunks:
			return chunk, ok, nil
		case <-ctx.Done():
			return cpaembedded.ExecuteStreamChunk{}, false, context.Cause(ctx)
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case chunk, ok := <-chunks:
		return chunk, ok, nil
	case <-ctx.Done():
		return cpaembedded.ExecuteStreamChunk{}, false, context.Cause(ctx)
	case <-timer.C:
		return cpaembedded.ExecuteStreamChunk{}, false, context.DeadlineExceeded
	}
}

type firstByteGate struct {
	stopOnce sync.Once
	stopOne  chan struct{}
	done     chan struct{}
}

func startFirstByteGate(budget time.Duration, cancel context.CancelCauseFunc) *firstByteGate {
	gate := &firstByteGate{stopOne: make(chan struct{}), done: make(chan struct{})}
	if budget <= 0 {
		close(gate.done)
		return gate
	}
	go func() {
		defer close(gate.done)
		timer := time.NewTimer(budget)
		defer timer.Stop()
		select {
		case <-timer.C:
			cancel(context.DeadlineExceeded)
		case <-gate.stopOne:
		}
	}()
	return gate
}

func (g *firstByteGate) stop() {
	if g == nil {
		return
	}
	g.stopOnce.Do(func() { close(g.stopOne) })
	<-g.done
}

func frameSSE(clientProtocol protocol.Protocol, payload []byte) []byte {
	payload = bytes.TrimRight(payload, "\r\n")
	if (clientProtocol == protocol.OpenAICompletions || clientProtocol == protocol.Gemini) &&
		!bytes.HasPrefix(payload, []byte("data:")) && !bytes.HasPrefix(payload, []byte("event:")) {
		payload = append([]byte("data: "), payload...)
	}
	return append(append([]byte(nil), payload...), '\n', '\n')
}

func rewriteStreamModelAlias(spec execution.AttemptSpec, payload []byte) ([]byte, error) {
	if !responsealias.Needs(spec.ClientModel, spec.UpstreamModel) {
		return bytes.Clone(payload), nil
	}
	return responsealias.RewriteSSE(spec.ClientProtocol, payload, spec.ClientModel)
}

func isOpenAIDone(payload []byte) bool {
	payload = bytes.TrimSpace(payload)
	payload = bytes.TrimSpace(bytes.TrimPrefix(payload, []byte("data:")))
	return bytes.Equal(payload, []byte("[DONE]"))
}

func isGeminiTerminal(payload []byte) bool {
	payload = bytes.TrimSpace(payload)
	payload = bytes.TrimSpace(bytes.TrimPrefix(payload, []byte("data:")))
	var value struct {
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	if value.PromptFeedback.BlockReason != "" {
		return true
	}
	for _, candidate := range value.Candidates {
		if candidate.FinishReason != "" {
			return true
		}
	}
	return false
}

func responseModel(body []byte, fallback string) string {
	var value struct {
		Model        string `json:"model"`
		ModelVersion string `json:"modelVersion"`
		Response     struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &value) == nil {
		if strings.TrimSpace(value.Model) != "" {
			return strings.TrimSpace(value.Model)
		}
		if strings.TrimSpace(value.Response.Model) != "" {
			return strings.TrimSpace(value.Response.Model)
		}
		if strings.TrimSpace(value.ModelVersion) != "" {
			return strings.TrimSpace(value.ModelVersion)
		}
	}
	return fallback
}

func usageEvidence(clientProtocol protocol.Protocol, body []byte) *execution.UsageEvidence {
	var extractor dialect.UsageExtractor
	usageField := "usage"
	switch clientProtocol {
	case protocol.OpenAIResponses:
		extractor = dialect.NewOpenAIResponses()
	case protocol.Anthropic:
		extractor = dialect.NewAnthropic()
	case protocol.Gemini:
		extractor = dialect.NewGemini()
		usageField = "usageMetadata"
	default:
		extractor = dialect.NewOpenAI()
	}
	result, err := extractor.ExtractUsage(body)
	if err != nil || result.State == usage.StateMissing || result.State == usage.StateNotApplicable {
		return nil
	}
	var root map[string]json.RawMessage
	_ = json.Unmarshal(body, &root)
	raw := append([]byte(nil), root[usageField]...)
	return &execution.UsageEvidence{Normalized: result, Raw: raw}
}

func upstreamRequestID(headers http.Header) string {
	if value := headers.Get("X-Request-Id"); value != "" {
		return value
	}
	return headers.Get("Request-Id")
}

func withRequestTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

var _ execution.Executor = (*Adapter)(nil)
