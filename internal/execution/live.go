package execution

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gorilla/websocket"
)

// LiveCall is the result of one Codex Live session bootstrap. The selected
// credential remains owned by Session for the complete call.
type LiveCall struct {
	// DispatchState 在失败时也必须区分是否已发送建连请求。
	DispatchState DispatchState
	CallID        string
	SDP           string
	Header        http.Header
	Session       LiveSession
}

// LiveSession is bound to one upstream call and never selects another account.
type LiveSession interface {
	DialSideband(context.Context, string, []string) (*websocket.Conn, int, error)
	ReleaseSideband(*websocket.Conn)
	Hangup(context.Context) error
	Close() error
}

// LiveOpener only performs the selected native call; scheduling is gateway-owned.
type LiveOpener interface {
	OpenLive(context.Context, AttemptSpec, string, json.RawMessage) (LiveCall, *ErrorEvidence)
}
