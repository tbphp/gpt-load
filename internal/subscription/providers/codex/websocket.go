package codex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

const (
	WSNotSent   = "not_sent"
	WSMaybeSent = "maybe_sent"
)

// WSSessionOptions 固定调用者已选定的凭据、API 代理根地址和出站代理。
// ProxyURL 复用现有代理策略选择的 direct 或代理 URL，环境代理须由调用者先解析。
type WSSessionOptions struct {
	CredentialID    string
	Credential      Credential
	BaseURL         string
	ProxyURL        string
	TurnTimeout     time.Duration
	MaxRequestBytes int
	MaxEventBytes   int
	Headers         http.Header
}

// WSTurnResult 的 Usage 保留上游 JSON，缺失时为 nil，不伪造零用量。
type WSTurnResult struct {
	ResponseID       string
	Status           string
	Usage            json.RawMessage
	Headers          http.Header
	HeaderObservedAt time.Time
	DispatchState    string
}

// WSError 暴露稳定分类和发送证据，错误文本不包含上游正文与凭据。
type WSError struct {
	Code          string
	UpstreamCode  string
	HTTPStatus    int
	DispatchState string
	cause         error
}

func (err *WSError) Error() string { return "codex websocket: " + err.Code }
func (err *WSError) Unwrap() error { return err.cause }

// WSSession 独立于既有 HTTP Executor，不注册到数据面或全局生命周期。
type WSSession struct{ bridge *cpaembedded.CodexWSSession }

// NewWSSession 创建独立句柄，首轮 ExecuteTurn 才建立上游连接。
func NewWSSession(options WSSessionOptions) (*WSSession, error) {
	bridge, err := cpaembedded.NewCodexWSSession(cpaembedded.CodexWSSessionOptions{
		CredentialID: options.CredentialID, Credential: credentialToBridge(options.Credential),
		BaseURL: options.BaseURL, ProxyURL: options.ProxyURL,
		Headers:     options.Headers.Clone(),
		TurnTimeout: options.TurnTimeout, MaxRequestBytes: options.MaxRequestBytes, MaxEventBytes: options.MaxEventBytes,
	})
	if err != nil {
		return nil, wsErrorFromBridge(err)
	}
	return &WSSession{bridge: bridge}, nil
}

// ExecuteTurn 同步执行原生 Responses Create，首轮不引用上一响应。
// emit 顺序接收原生 JSON 事件，须及时返回并响应 ctx；nil 表示忽略事件。
// 调用者负责保存响应 ID，并通过同一 Session 发起串行续接。
func (s *WSSession) ExecuteTurn(ctx context.Context, payload json.RawMessage, emit func(context.Context, json.RawMessage) error) (WSTurnResult, error) {
	var bridge *cpaembedded.CodexWSSession
	if s != nil {
		bridge = s.bridge
	}
	result, err := bridge.ExecuteTurn(ctx, payload, emit)
	return WSTurnResult{
		ResponseID: result.ResponseID, Status: result.Status, Usage: result.Usage,
		Headers: result.Headers, DispatchState: result.DispatchState,
		HeaderObservedAt: result.HeaderObservedAt,
	}, wsErrorFromBridge(err)
}

// Done 在本 Session 失效或关闭后通知调用者。
func (s *WSSession) Done() <-chan struct{} {
	var bridge *cpaembedded.CodexWSSession
	if s != nil {
		bridge = s.bridge
	}
	return bridge.Done()
}

// Close 幂等关闭本 Session，不影响其他 Session。
func (s *WSSession) Close() error {
	if s == nil {
		return nil
	}
	return wsErrorFromBridge(s.bridge.Close())
}

func wsErrorFromBridge(err error) error {
	if err == nil {
		return nil
	}
	var failure *cpaembedded.CodexWSError
	if errors.As(err, &failure) {
		return &WSError{
			Code: failure.Code, UpstreamCode: failure.UpstreamCode, HTTPStatus: failure.HTTPStatus,
			DispatchState: failure.DispatchState, cause: failure.Unwrap(),
		}
	}
	return &WSError{Code: "internal_error", DispatchState: WSMaybeSent}
}
