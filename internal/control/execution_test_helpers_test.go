package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"gpt-load/internal/execution"
)

// controlHTTPExecutor is a narrow test double for control/data-plane publication
// tests. Provider protocol behavior is covered by the production Bifrost adapter
// tests; this double only proves that a frozen channel target and credential are
// visible atomically to the gateway.
type controlHTTPExecutor struct {
	client *http.Client
}

type recordingCredentialRuntimeExecutor struct {
	controlHTTPExecutor

	mu      sync.Mutex
	retired []uint
}

func (executor *recordingCredentialRuntimeExecutor) RetireCredential(credentialID uint) {
	executor.mu.Lock()
	executor.retired = append(executor.retired, credentialID)
	executor.mu.Unlock()
}

func (executor *recordingCredentialRuntimeExecutor) retiredCredentialIDs() []uint {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	return append([]uint(nil), executor.retired...)
}

func (executor controlHTTPExecutor) Execute(
	ctx context.Context,
	spec execution.AttemptSpec,
) execution.AttemptResult {
	response, err := executor.do(ctx, spec)
	if err != nil {
		return controlExecutionFailure(execution.ErrorKindTransport)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return controlExecutionFailure(execution.ErrorKindTransport)
	}
	result := execution.AttemptResult{
		DispatchState:   execution.DispatchMaybeSent,
		ResponseStarted: true,
		StatusCode:      response.StatusCode,
		Header:          response.Header.Clone(),
		Body:            body,
		Model:           spec.UpstreamModel,
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		result.Error = &execution.ErrorEvidence{
			Kind: execution.ErrorKindHTTP, StatusCode: response.StatusCode,
			Summary: "test upstream rejected request",
		}
	}
	return result
}

func (executor controlHTTPExecutor) ExecuteStream(
	ctx context.Context,
	spec execution.AttemptSpec,
	sink execution.StreamSink,
) execution.StreamResult {
	response, err := executor.do(ctx, spec)
	if err != nil {
		return controlStreamExecutionFailure(execution.ErrorKindTransport)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return controlStreamExecutionFailure(execution.ErrorKindTransport)
	}
	if err := sink(execution.StreamEvent{
		Sequence: 1, Kind: execution.StreamEventReady,
		StatusCode: response.StatusCode, Header: response.Header.Clone(),
	}); err != nil {
		return controlStreamExecutionFailure(execution.ErrorKindCanceled)
	}
	if len(body) > 0 {
		if err := sink(execution.StreamEvent{
			Sequence: 2, Kind: execution.StreamEventData, Data: body,
		}); err != nil {
			return controlStreamExecutionFailure(execution.ErrorKindCanceled)
		}
	}
	result := execution.StreamResult{
		DispatchState:   execution.DispatchMaybeSent,
		ResponseStarted: true,
		StatusCode:      response.StatusCode,
		Header:          response.Header.Clone(),
		Model:           spec.UpstreamModel,
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		result.Error = &execution.ErrorEvidence{
			Kind: execution.ErrorKindHTTP, StatusCode: response.StatusCode,
			Summary: "test upstream rejected request",
		}
	}
	return result
}

func (executor controlHTTPExecutor) do(
	ctx context.Context,
	spec execution.AttemptSpec,
) (*http.Response, error) {
	var target struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.Unmarshal(spec.TargetConfig, &target); err != nil {
		return nil, err
	}
	var credential struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(spec.Credential.Data(), &credential); err != nil {
		return nil, err
	}
	requestURL := strings.TrimSuffix(target.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, spec.Method, requestURL, bytes.NewReader(spec.Body))
	if err != nil {
		return nil, err
	}
	request.Header = spec.Header.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("Authorization", "Bearer "+credential.APIKey)
	client := executor.client
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(request)
}

func controlExecutionFailure(kind execution.ErrorKind) execution.AttemptResult {
	return execution.AttemptResult{
		DispatchState: execution.DispatchMaybeSent,
		Error:         &execution.ErrorEvidence{Kind: kind, Summary: "test upstream execution failed"},
	}
}

func controlStreamExecutionFailure(kind execution.ErrorKind) execution.StreamResult {
	return execution.StreamResult{
		DispatchState: execution.DispatchMaybeSent,
		Error:         &execution.ErrorEvidence{Kind: kind, Summary: "test upstream execution failed"},
	}
}
