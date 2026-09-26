package codex

import (
	"context"
	"net/http"

	"github.com/gorilla/websocket"
	cpaembedded "github.com/router-for-me/CLIProxyAPI/v7/gptload-embedded/embedded"
)

type LiveRequest struct {
	CredentialID         string
	Credential           Credential
	BaseURL              string
	ProxyURL             string
	ProxyFromEnvironment bool
	Headers              http.Header
	Body                 []byte
}

type LiveCall struct {
	CallID  string
	SDP     string
	Header  http.Header
	Session *LiveSession
}

type LiveSession struct{ inner *cpaembedded.CodexLiveSession }

type LiveHTTPError = cpaembedded.CodexLiveHTTPError

func StartLive(ctx context.Context, request LiveRequest) (LiveCall, error) {
	result, err := cpaembedded.StartCodexLive(ctx, cpaembedded.CodexLiveRequest{
		CredentialID: request.CredentialID,
		Credential:   credentialToBridge(request.Credential),
		BaseURL:      request.BaseURL, ProxyURL: request.ProxyURL,
		ProxyFromEnvironment: request.ProxyFromEnvironment,
		Headers:              request.Headers, Body: request.Body,
	})
	call := LiveCall{CallID: result.CallID, SDP: result.SDP, Header: result.Header}
	if result.Session != nil {
		call.Session = &LiveSession{inner: result.Session}
	}
	return call, err
}

func (session *LiveSession) DialSideband(ctx context.Context, style string, protocols []string) (*websocket.Conn, int, error) {
	return session.inner.DialSideband(ctx, style, protocols)
}

func (session *LiveSession) ReleaseSideband(connection *websocket.Conn) {
	session.inner.ReleaseSideband(connection)
}

func (session *LiveSession) Hangup(ctx context.Context) error { return session.inner.Hangup(ctx) }
func (session *LiveSession) Close() error                     { return session.inner.Close() }
