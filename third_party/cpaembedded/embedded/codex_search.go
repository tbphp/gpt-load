package embedded

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type codexSearchHTTPError struct {
	status     int
	body       string
	retryAfter time.Duration
	cause      error
}

func (err *codexSearchHTTPError) Error() string {
	if err.cause != nil {
		return fmt.Sprintf("read Codex search response: %v", err.cause)
	}
	return err.body
}

func (err *codexSearchHTTPError) Unwrap() error   { return err.cause }
func (err *codexSearchHTTPError) StatusCode() int { return err.status }
func (err *codexSearchHTTPError) RetryAfter() *time.Duration {
	return &err.retryAfter
}

// executeSearchCanonical 只执行独立搜索 HTTP 请求，不经过 Responses 转换器。
func (e *CodexHTTPExecutor) executeSearchCanonical(
	ctx context.Context,
	credentialID string,
	credential CodexCredential,
	request ExecuteRequest,
) (ExecuteResponse, error) {
	if err := validateCredential(credential); err != nil {
		return ExecuteResponse{}, err
	}
	endpoints, err := ResolveCodexAPIEndpoints(request.BaseURL)
	if err != nil {
		return ExecuteResponse{}, err
	}
	target := endpoints.ExecutionBase + "/alpha/search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(request.Payload))
	if err != nil {
		return ExecuteResponse{}, err
	}
	req.Header = normalizedCodexHeaders(request.Headers)
	applyCodexReadHeaders(req, credential)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	// 即使客户端带幂等头，也不允许 net/http 在一次 attempt 内隐式重放 POST。
	req.GetBody = nil
	auth := NewCodexAuth(credentialID, credential, endpoints.ExecutionBase)
	auth.ProxyURL = request.ProxyURL
	execCtx := e.executionContext(ctx, auth, nil, request.ProxyFromEnvironment, &request)
	resp, err := e.inner.HttpRequest(execCtx, authWithoutProxyURL(auth), req)
	if err != nil {
		return ExecuteResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	result := ExecuteResponse{StatusCode: resp.StatusCode, Headers: resp.Header.Clone(), UpstreamRequestPath: req.URL.Path}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxObservedBodyBytes+1))
	if err == nil && len(body) > maxObservedBodyBytes {
		err = fmt.Errorf("Codex search response exceeds size limit")
	}
	if err != nil {
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return result, &codexSearchHTTPError{status: resp.StatusCode, cause: err,
				retryAfter: boundedOAuthRetryAfter(resp.Header, time.Now())}
		}
		return result, err
	}
	result.Payload = body
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return result, &codexSearchHTTPError{status: resp.StatusCode, body: string(body),
			retryAfter: boundedOAuthRetryAfter(resp.Header, time.Now())}
	}
	return result, nil
}
