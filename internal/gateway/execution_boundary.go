package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/codex"
	"gpt-load/internal/connection"
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
)

type normalizedCredential struct {
	apiKey  string
	payload []byte
	secrets []string
}

func normalizeChannelCredential(
	registry *channel.Registry,
	channelID channel.ID,
	connectionType string,
	decrypted string,
) (normalizedCredential, error) {
	if registry == nil || channelID == "" {
		return normalizedCredential{}, fmt.Errorf("credential channel is unavailable")
	}
	trimmed := strings.TrimSpace(decrypted)
	if trimmed == "" {
		return normalizedCredential{}, fmt.Errorf("credential is empty")
	}
	raw := []byte(trimmed)
	if connection.Normalize(connectionType) == connection.Subscription {
		credential, err := codex.ParseCredentialJSON(raw)
		if err != nil {
			return normalizedCredential{}, fmt.Errorf("validate subscription credential: %w", err)
		}
		payload, err := codex.MarshalCredential(credential)
		if err != nil {
			return normalizedCredential{}, fmt.Errorf("encode subscription credential: %w", err)
		}
		return normalizedCredential{
			payload: payload,
			secrets: credential.SecretValues(),
		}, nil
	}
	validated, err := registry.ValidateCredential(channelID, raw)
	if err != nil {
		return normalizedCredential{}, fmt.Errorf("validate credential: %w", err)
	}
	payload := bytes.Clone(validated.CanonicalJSON())
	apiKey, _ := validated.Value("api_key")
	return normalizedCredential{
		apiKey: apiKey, payload: payload, secrets: credentialSecretValues(payload),
	}, nil
}

func credentialSecretValues(payload []byte) []string {
	var values map[string]string
	if json.Unmarshal(payload, &values) != nil {
		return nil
	}
	fields := []string{
		"api_key", "client_id", "client_secret", "tenant_id", "access_key", "secret_key",
		"session_token", "role_arn", "external_id", "session_name", "service_account_json",
	}
	seen := make(map[string]struct{}, len(fields))
	result := make([]string, 0, len(fields))
	appendValue := func(value string) {
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	for _, field := range fields {
		value := values[field]
		appendValue(value)
		if field != "service_account_json" || value == "" {
			continue
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal([]byte(value), &nested) != nil {
			continue
		}
		for _, nestedField := range []string{"private_key", "private_key_id", "client_email"} {
			var nestedValue string
			if json.Unmarshal(nested[nestedField], &nestedValue) == nil {
				appendValue(nestedValue)
			}
		}
	}
	return result
}

func judgeUpstreamResult(
	result UpstreamResult,
	now time.Time,
) health.Result {
	result = normalizeUpstreamResultEvidence(result)
	downstreamErr := result.Err
	if result.ExecutionError != nil && !result.Committed {
		downstreamErr = nil
	}
	return health.JudgeExecution(health.ExecutionAttempt{
		DispatchState:       result.DispatchState,
		StatusCode:          result.StatusCode,
		Header:              result.Header,
		Evidence:            result.ExecutionError,
		DownstreamCommitted: result.Committed,
		DownstreamErr:       downstreamErr,
		Now:                 now,
	})
}

func normalizeUpstreamResultEvidence(result UpstreamResult) UpstreamResult {
	if result.DispatchState.Valid() {
		return result
	}
	result.DispatchState, result.ExecutionError = inferAttemptEvidence(result)
	result.ResponseStarted = result.ResponseStarted || result.StatusCode != 0
	return result
}

// inferAttemptEvidence keeps the AttemptForwarder boundary fail-safe when a
// custom implementation supplies HTTP or transport evidence without the
// normalized execution fields used by the built-in adapter.
func inferAttemptEvidence(result UpstreamResult) (execution.DispatchState, *execution.ErrorEvidence) {
	dispatchState := execution.DispatchNotSent
	if result.RequestWritten || result.StatusCode != 0 || result.ResponseStarted {
		dispatchState = execution.DispatchMaybeSent
	}
	if result.Err != nil {
		kind := execution.ErrorKindTransport
		switch {
		case errors.Is(result.Err, context.Canceled):
			kind = execution.ErrorKindCanceled
		case errors.Is(result.Err, context.DeadlineExceeded):
			kind = execution.ErrorKindTimeout
		}
		return dispatchState, &execution.ErrorEvidence{Kind: kind, Summary: result.Err.Error()}
	}
	if result.StatusCode >= 200 && result.StatusCode < 300 && !result.ProviderErrorBeforeCommit {
		return dispatchState, nil
	}
	summary := strings.TrimSpace(string(result.ClassificationBody))
	if summary == "" {
		summary = strings.TrimSpace(string(result.Body))
	}
	return dispatchState, &execution.ErrorEvidence{
		Kind:       execution.ErrorKindHTTP,
		StatusCode: result.StatusCode,
		Summary:    summary,
	}
}
