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
	"gpt-load/internal/execution"
	"gpt-load/internal/health"
	subscriptionruntime "gpt-load/internal/subscription/runtime"
)

type normalizedCredential struct {
	apiKey  string
	payload []byte
	secrets []string
}

func normalizeChannelCredential(
	registry *channel.Registry,
	subscriptions *subscriptionruntime.Runtime,
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
	expectedConnection, ok := registry.ConnectionType(channelID)
	if !ok || strings.TrimSpace(connectionType) != expectedConnection {
		return normalizedCredential{}, fmt.Errorf("credential connection does not match channel")
	}
	if driver, bound := subscriptions.Driver(channelID); bound {
		credential, err := driver.Parse(raw)
		if err != nil {
			return normalizedCredential{}, fmt.Errorf("validate subscription credential: %w", err)
		}
		return normalizedCredential{
			payload: credential.Canonical(),
			secrets: credential.SecretValues(),
		}, nil
	}
	if expectedConnection != "api_key" {
		return normalizedCredential{}, fmt.Errorf("subscription credential driver is unavailable")
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
	decisionContext health.DecisionContext,
) health.Decision {
	evidence, downstreamErr := decisionEvidence(result)
	if evidence != nil && !result.Committed &&
		!errors.Is(result.Err, context.Canceled) &&
		!errors.Is(result.Err, context.DeadlineExceeded) {
		downstreamErr = nil
	}
	return health.JudgeExecution(health.ExecutionAttempt{
		DispatchState:       result.DispatchState,
		StatusCode:          result.StatusCode,
		Header:              result.Header,
		Evidence:            evidence,
		DownstreamCommitted: result.Committed,
		DownstreamErr:       downstreamErr,
		Now:                 now,
	}, decisionContext)
}

func decisionEvidence(result UpstreamResult) (*execution.ErrorEvidence, error) {
	evidence := result.ExecutionError
	downstreamErr := result.Err
	summary := result.Stream.ErrorSummary
	if summary == "" {
		summary = fixedErrorSummary(streamErrorCode(result.Stream.EndReason))
	}
	switch result.Stream.EndReason {
	case StreamEndNone:
		return evidence, downstreamErr
	case StreamEndCleanEOF, StreamEndProviderIncomplete:
		return nil, nil
	case StreamEndSSEError:
		if evidence == nil {
			evidence = &execution.ErrorEvidence{
				Kind: execution.ErrorKindProvider, OriginHint: execution.ErrorOriginUpstream,
				StatusCode: result.StatusCode, Code: "upstream_sse_error", Summary: summary,
			}
		}
		return evidence, nil
	case StreamEndUpstreamTerminated:
		return &execution.ErrorEvidence{
			Kind: execution.ErrorKindTransport, OriginHint: execution.ErrorOriginUpstream,
			StatusCode: result.StatusCode, Code: "upstream_stream_terminated", Summary: summary,
		}, nil
	case StreamEndUpstreamProtocolError:
		return &execution.ErrorEvidence{
			Kind: execution.ErrorKindProvider, OriginHint: execution.ErrorOriginUpstream,
			StatusCode: result.StatusCode, Code: "upstream_protocol_error", Summary: summary,
		}, nil
	case StreamEndIdleTimeout:
		return &execution.ErrorEvidence{
			Kind: execution.ErrorKindTimeout, OriginHint: execution.ErrorOriginUpstream,
			StatusCode: result.StatusCode, Code: "upstream_stream_idle_timeout", Summary: summary,
		}, nil
	case StreamEndDownstreamWriteFailure:
		return evidence, errors.New("downstream write failed")
	case StreamEndClientCanceled, StreamEndServerShutdown:
		return evidence, context.Canceled
	default:
		return &execution.ErrorEvidence{
			Kind: execution.ErrorKindInternal, OriginHint: execution.ErrorOriginInternal,
			Code: "attempt_result_contract_invalid", Summary: "Attempt ended with an invalid terminal state.",
		}, nil
	}
}

func normalizeUpstreamResultContract(result UpstreamResult) UpstreamResult {
	if result.DispatchState.Valid() {
		return result
	}
	dispatchState := execution.DispatchNotSent
	if result.DispatchState == execution.DispatchLocal {
		dispatchState = execution.DispatchLocal
	} else if result.DispatchState == execution.DispatchMaybeSent || result.RequestWritten ||
		result.Committed || result.ResponseStarted || result.StatusCode != 0 ||
		len(result.Header) > 0 || len(result.Body) > 0 {
		dispatchState = execution.DispatchMaybeSent
	}
	evidence := execution.ErrorEvidence{
		Kind:         execution.ErrorKindInternal,
		OriginHint:   execution.ErrorOriginInternal,
		ScopeHint:    execution.ErrorScopeRequest,
		Code:         "attempt_result_contract_invalid",
		Summary:      "Attempt forwarder returned an invalid result.",
		ReplaySafety: execution.ReplaySafetyUnknown,
	}
	normalized := UpstreamResult{
		Err:            fmt.Errorf("%w: invalid attempt forwarder result", ErrUpstreamProtocol),
		RequestWritten: dispatchState == execution.DispatchMaybeSent,
		Committed:      result.Committed,
		DispatchState:  dispatchState,
		ExecutionError: &evidence,
		ErrorSummary:   evidence.Summary,
	}
	if result.Committed {
		normalized.Stream = streamTerminalObservation(StreamEndUpstreamProtocolError)
	}
	return normalized
}
