package requestlog

import (
	"gpt-load/internal/telemetry"
)

type queuedEvent struct {
	Event telemetry.RequestEvent
	// requestBodyReserved 是该事件占用的待写正文预算；requestBodySkipped 是预算不足时未保留的原始正文大小。
	requestBodyReserved int64
	requestBodySkipped  int64
}
