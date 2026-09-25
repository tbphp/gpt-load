package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	xproxy "golang.org/x/net/proxy"
)

const (
	codexLiveCallURL     = "https://chatgpt.com/backend-api/codex/realtime/calls?intent=quicksilver&architecture=avas"
	codexLiveSidebandURL = "https://api.openai.com/v1"
	codexLiveMaxBody     = 16 << 20
)

// CodexLiveRequest already belongs to one selected GPT-Load credential.
type CodexLiveRequest struct {
	CredentialID         string
	Credential           CodexCredential
	BaseURL              string
	ProxyURL             string
	ProxyFromEnvironment bool
	Headers              http.Header
	Body                 []byte
	// doHTTP is used by local contract tests; production uses CPA's HTTP transport.
	doHTTP func(context.Context, *http.Request) (*http.Response, error)
}

type CodexLiveCall struct {
	CallID  string
	SDP     string
	Header  http.Header
	Session *CodexLiveSession
}

type CodexLiveHTTPError struct{ Status int }

func (err *CodexLiveHTTPError) Error() string {
	return fmt.Sprintf("codex live upstream returned %d", err.Status)
}

// CodexLiveSession retains only the selected access token, account and network
// target. It does not own an account pool, refresh token or business retry.
type CodexLiveSession struct {
	credential CodexCredential
	authID     string
	baseURL    string
	proxyURL   string
	sideband   string
	callID     string
	headers    http.Header
	doHTTP     func(context.Context, *http.Request) (*http.Response, error)
	mu         sync.Mutex
	connection *websocket.Conn
	closed     bool
}

func StartCodexLive(ctx context.Context, input CodexLiveRequest) (CodexLiveCall, error) {
	if err := validateCredential(input.Credential); err != nil {
		return CodexLiveCall{}, err
	}
	if !json.Valid(input.Body) || len(input.Body) > codexLiveMaxBody {
		return CodexLiveCall{}, errors.New("invalid codex live call body")
	}
	callURL, err := ResolveAPIEndpoint(input.BaseURL, codexLiveCallURL)
	if err != nil {
		return CodexLiveCall{}, err
	}
	sidebandBase, err := ResolveAPIEndpoint(input.BaseURL, codexLiveSidebandURL)
	if err != nil {
		return CodexLiveCall{}, err
	}
	proxyURL := input.ProxyURL
	if proxyURL == "" && !input.ProxyFromEnvironment {
		proxyURL = "direct"
	}
	auth := NewCodexAuth(input.CredentialID, input.Credential, callURL)
	delete(auth.Metadata, "refresh_token")
	auth.ProxyURL = proxyURL
	executor := NewCodexHTTPExecutor()
	original := ExecuteRequest{Headers: input.Headers.Clone(), ProxyURL: proxyURL, ProxyFromEnvironment: input.ProxyFromEnvironment}
	doHTTP := input.doHTTP
	if doHTTP == nil {
		doHTTP = func(ctx context.Context, request *http.Request) (*http.Response, error) {
			execCtx := executor.executionContext(ctx, auth, nil, input.ProxyFromEnvironment, &original)
			return executor.inner.HttpRequest(execCtx, authWithoutProxyURL(auth), request)
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, callURL, bytes.NewReader(input.Body))
	if err != nil {
		return CodexLiveCall{}, err
	}
	request.GetBody = nil
	request.Header = liveCodexHeaders(input.Headers, input.Credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	response, err := doHTTP(ctx, request)
	if err != nil {
		return CodexLiveCall{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return CodexLiveCall{}, &CodexLiveHTTPError{Status: response.StatusCode}
	}
	answer, err := io.ReadAll(io.LimitReader(response.Body, codexLiveMaxBody+1))
	if err != nil || len(answer) > codexLiveMaxBody {
		return CodexLiveCall{}, errors.New("invalid codex live SDP response")
	}
	callID, err := codexLiveCallID(response.Header.Get("Location"))
	if err != nil {
		return CodexLiveCall{}, errors.New("codex live call response is incomplete")
	}
	responseSDP, err := codexLiveResponseSDP(answer, response.Header.Get("Content-Type"))
	if err != nil {
		return CodexLiveCall{}, err
	}
	session := &CodexLiveSession{
		credential: CodexCredential{AccessToken: input.Credential.AccessToken, AccountID: input.Credential.AccountID},
		authID:     input.CredentialID, baseURL: input.BaseURL, proxyURL: proxyURL,
		sideband: sidebandBase, callID: callID, headers: input.Headers.Clone(), doHTTP: doHTTP,
	}
	return CodexLiveCall{CallID: callID, SDP: responseSDP, Header: http.Header{
		"Location":          {response.Header.Get("Location")},
		"X-Request-Id":      response.Header.Values("X-Request-Id"),
		"Openai-Request-Id": response.Header.Values("Openai-Request-Id"),
	}, Session: session}, nil
}

func codexLiveResponseSDP(body []byte, contentType string) (string, error) {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "application/json" {
		var payload struct {
			SDP string `json:"sdp"`
		}
		if json.Unmarshal(body, &payload) != nil || strings.TrimSpace(payload.SDP) == "" {
			return "", errors.New("invalid codex live SDP response")
		}
		return payload.SDP, nil
	}
	if strings.TrimSpace(string(body)) == "" {
		return "", errors.New("invalid codex live SDP response")
	}
	return string(body), nil
}

func liveCodexHeaders(source http.Header, credential CodexCredential) http.Header {
	headers := make(http.Header)
	for _, name := range []string{"OpenAI-Alpha", "X-Session-Id", "Session-Id", "Thread-Id", "Originator", "OpenAI-Safety-Identifier", "OpenAI-Organization", "OpenAI-Project", "X-Oai-Attestation"} {
		for _, value := range source.Values(name) {
			headers.Add(name, value)
		}
	}
	request := &http.Request{Header: headers}
	applyCodexReadHeaders(request, credential)
	if originator := source.Get("Originator"); originator != "" {
		headers.Set("Originator", originator)
	}
	return headers
}

func codexLiveCallID(location string) (string, error) {
	location = strings.TrimSpace(location)
	if validCodexLiveCallID(location) {
		return location, nil
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed == nil || parsed.Fragment != "" {
		return "", errors.New("invalid codex live call location")
	}
	if id := parsed.Query().Get("call_id"); validCodexLiveCallID(id) {
		return id, nil
	}
	path := strings.Trim(parsed.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || (parts[len(parts)-2] != "calls" && parts[len(parts)-2] != "live") {
		return "", errors.New("invalid codex live call location")
	}
	id := parts[len(parts)-1]
	if !validCodexLiveCallID(id) {
		return "", errors.New("invalid codex live call id")
	}
	return id, nil
}

func validCodexLiveCallID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, ch := range id {
		if !('a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || '0' <= ch && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}

func (session *CodexLiveSession) DialSideband(ctx context.Context, style string, protocols []string) (*websocket.Conn, int, error) {
	if session == nil {
		return nil, 0, errors.New("codex live session unavailable")
	}
	session.mu.Lock()
	if session.closed || session.connection != nil {
		session.mu.Unlock()
		return nil, 0, errors.New("codex live sideband is already attached")
	}
	session.mu.Unlock()
	base := strings.TrimRight(session.sideband, "/")
	endpoint := base + "/live/" + session.callID
	switch style {
	case "realtime-query":
		endpoint = base + "/realtime?intent=quicksilver&call_id=" + url.QueryEscape(session.callID)
	case "realtime-calls":
		endpoint = base + "/realtime/calls/" + session.callID
	case "live":
	default:
		return nil, 0, errors.New("invalid codex live sideband style")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, 0, err
	}
	if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	} else if parsed.Scheme == "http" {
		parsed.Scheme = "ws"
	}
	dialer, err := liveCodexDialer(session.proxyURL, parsed, protocols, http.ProxyFromEnvironment)
	if err != nil {
		return nil, 0, err
	}
	connection, response, err := dialer.DialContext(ctx, parsed.String(), liveCodexHeaders(session.headers, session.credential))
	status := 0
	if response != nil {
		status = response.StatusCode
		if response.Body != nil {
			_ = response.Body.Close()
		}
	}
	if err != nil {
		return nil, status, err
	}
	session.mu.Lock()
	if session.closed || session.connection != nil {
		session.mu.Unlock()
		_ = connection.Close()
		return nil, 0, errors.New("codex live sideband closed")
	}
	session.connection = connection
	session.mu.Unlock()
	return connection, status, nil
}

func (session *CodexLiveSession) ReleaseSideband(connection *websocket.Conn) {
	if session == nil {
		return
	}
	session.mu.Lock()
	if session.connection == connection {
		session.connection = nil
	}
	session.mu.Unlock()
}

func liveCodexDialer(proxyURL string, target *url.URL, protocols []string, proxyFromEnvironment func(*http.Request) (*url.URL, error)) (*websocket.Dialer, error) {
	dialer := &websocket.Dialer{HandshakeTimeout: 10 * time.Second, Subprotocols: append([]string(nil), protocols...)}
	if proxyURL == "" {
		if target == nil || proxyFromEnvironment == nil {
			return nil, errors.New("codex live proxy target unavailable")
		}
		proxyTarget := *target
		switch proxyTarget.Scheme {
		case "ws":
			proxyTarget.Scheme = "http"
		case "wss":
			proxyTarget.Scheme = "https"
		default:
			return nil, errors.New("invalid codex live sideband target")
		}
		selected, err := proxyFromEnvironment(&http.Request{Method: http.MethodGet, URL: &proxyTarget})
		if err != nil {
			return nil, err
		}
		if selected == nil {
			return dialer, nil
		}
		proxyURL = selected.String()
	}
	setting, err := proxyutil.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	if setting.Mode == proxyutil.ModeDirect {
		return dialer, nil
	}
	if setting.Mode != proxyutil.ModeProxy {
		return nil, errors.New("invalid codex live proxy")
	}
	if setting.URL.Scheme == "http" {
		dialer.Proxy = http.ProxyURL(setting.URL)
		return dialer, nil
	}
	proxyDialer, _, err := proxyutil.BuildDialer(proxyURL)
	if err != nil {
		return nil, err
	}
	contextDialer, ok := proxyDialer.(xproxy.ContextDialer)
	if !ok {
		return nil, errors.New("codex live proxy does not support cancellation")
	}
	dialer.NetDialContext = contextDialer.DialContext
	return dialer, nil
}

func (session *CodexLiveSession) Hangup(ctx context.Context) error {
	if session == nil {
		return nil
	}
	endpoint, err := ResolveAPIEndpoint(session.baseURL, codexLiveSidebandURL+"/realtime/calls/"+session.callID+"/hangup")
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header = liveCodexHeaders(session.headers, session.credential)
	response, err := session.doHTTP(ctx, request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &CodexLiveHTTPError{Status: response.StatusCode}
	}
	return nil
}

func (session *CodexLiveSession) Close() error {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	session.closed = true
	connection := session.connection
	session.connection = nil
	session.mu.Unlock()
	if connection != nil {
		return connection.Close()
	}
	return nil
}
