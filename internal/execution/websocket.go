package execution

import (
	"context"
	"net/http"
	"time"
)

// WebsocketCapabilities 是执行路径承诺的原生 Responses WS 能力，不由 HTTP 存储声明推导。
type WebsocketCapabilities struct {
	Native          bool
	Continuation    bool
	StoredResponses bool
	Prewarm         bool
	Multiplex       bool
}

func (c WebsocketCapabilities) Supports(required WebsocketCapabilities) bool {
	return c.Native && (!required.Continuation || c.Continuation) &&
		(!required.StoredResponses || c.StoredResponses) &&
		(!required.Prewarm || c.Prewarm) && (!required.Multiplex || c.Multiplex)
}

// WebsocketResult 只报告本轮发送证据和错误；usage/响应 ID 由原生事件提供。
type WebsocketResult struct {
	DispatchState    DispatchState
	Header           http.Header
	HeaderObservedAt time.Time
	Error            *ErrorEvidence
}

// WebsocketSession 固定已选目标；同流顺序由网关保证，支持的不同流可并发调用。
// payload 为不含 type 的 Responses Create 参数；原生 stream_id 保留。
// emit 同步消费一个完整原生 JSON 事件，必须响应 ctx，返回错误将终止连接。
type WebsocketSession interface {
	ExecuteTurn(ctx context.Context, payload []byte, emit func(context.Context, []byte) error) WebsocketResult
	Done() <-chan struct{}
	Close() error
}

// WebsocketOpener 不进行选号、业务重放或 HTTP 回退。
type WebsocketOpener interface {
	OpenWebsocket(context.Context, AttemptSpec) (WebsocketSession, WebsocketResult)
}
