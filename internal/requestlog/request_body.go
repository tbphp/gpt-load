package requestlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"

	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

// RequestBodyState 描述一次请求的客户端原始请求体是如何被记录的。
type RequestBodyState string

const (
	RequestBodyOriginal  RequestBodyState = "original"
	RequestBodyTruncated RequestBodyState = "truncated"
	RequestBodyNonJSON   RequestBodyState = "non_json"
	RequestBodyTooLarge  RequestBodyState = "too_large"
	RequestBodyQueueFull RequestBodyState = "queue_full"
)

const (
	maxRecordedRequestBodyBytes   = 256 << 10
	initialRequestBodyStringBytes = 2 << 10
	minRequestBodyStringBytes     = 64
	// maxPendingRequestBodyBytes 限制等待写入的原始请求体总量，超出时只写日志、不记正文。
	maxPendingRequestBodyBytes = 64 << 20
	// requestBodyInsertBatchSize 让单条插入语句保持在 1MB 左右，避开 MySQL 的包大小限制。
	requestBodyInsertBatchSize = 4

	requestBodyMarkerPrefix = "…[truncated "
	requestBodyMarkerSuffix = " bytes]"
)

// RequestBody 是一条请求日志记录下来的客户端原始请求体。
type RequestBody struct {
	State         RequestBodyState
	OriginalBytes int64
	Body          *string
}

// GetRequestBody 读取请求日志的原始请求体记录；该请求没有记录时返回 nil。
func (service *Service) GetRequestBody(ctx context.Context, requestID string) (*RequestBody, error) {
	var row models.RequestLogBody
	if err := service.db.WithContext(ctx).Where("request_id = ?", requestID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("query request log body: %w", err)
	}
	return &RequestBody{State: RequestBodyState(row.State), OriginalBytes: row.OriginalBytes, Body: row.Body}, nil
}

// reserveRequestBody 在入队前占用待写正文预算；超出预算时丢掉正文，只保留原始大小用于标注。
func (service *Service) reserveRequestBody(event telemetry.RequestEvent) queuedEvent {
	queued := queuedEvent{Event: event}
	size := int64(len(event.RequestBody))
	if size == 0 {
		queued.Event.RequestBody = nil
		return queued
	}
	if service.pendingRequestBodyBytes.Load()+size > service.requestBodyBudget {
		queued.Event.RequestBody = nil
		queued.requestBodySkipped = size
		return queued
	}
	service.pendingRequestBodyBytes.Add(size)
	queued.requestBodyReserved = size
	return queued
}

func (service *Service) releaseRequestBodies(events []queuedEvent) {
	for _, event := range events {
		if event.requestBodyReserved > 0 {
			service.pendingRequestBodyBytes.Add(-event.requestBodyReserved)
		}
	}
}

func mapRequestBody(requestID string, event queuedEvent) *models.RequestLogBody {
	if event.requestBodySkipped > 0 {
		return &models.RequestLogBody{
			RequestID: requestID, State: string(RequestBodyQueueFull), OriginalBytes: event.requestBodySkipped,
		}
	}
	raw := event.Event.RequestBody
	if len(raw) == 0 {
		return nil
	}
	state, body := recordRequestBody(raw)
	row := &models.RequestLogBody{RequestID: requestID, State: string(state), OriginalBytes: int64(len(raw))}
	if body != nil {
		text := string(body)
		row.Body = &text
	}
	return row
}

func writeRequestBodies(tx *gorm.DB, rows []models.RequestLog) error {
	bodies := make([]models.RequestLogBody, 0)
	for _, row := range rows {
		if row.RequestBodyRow != nil {
			bodies = append(bodies, *row.RequestBodyRow)
		}
	}
	if len(bodies) == 0 {
		return nil
	}
	if err := tx.CreateInBatches(bodies, requestBodyInsertBatchSize).Error; err != nil {
		return fmt.Errorf("insert request log bodies: %w", err)
	}
	return nil
}

type requestBodySpan struct {
	start int
	end   int
}

// recordRequestBody 生成要保存的请求体：合法 JSON 且不超过上限时原样保存；超过上限时
// 只截断字符串值，逐级降低保留长度直到放得下，键、顺序、数组元素和非字符串值保持不变。
func recordRequestBody(raw []byte) (RequestBodyState, []byte) {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return RequestBodyNonJSON, nil
	}
	if len(raw) <= maxRecordedRequestBodyBytes {
		return RequestBodyOriginal, raw
	}
	spans := requestBodyStringValues(raw)
	for limit := initialRequestBodyStringBytes; limit >= minRequestBodyStringBytes; limit /= 2 {
		if truncatedRequestBodySize(raw, spans, limit) <= maxRecordedRequestBodyBytes {
			return RequestBodyTruncated, truncateRequestBodyStrings(raw, spans, limit)
		}
	}
	return RequestBodyTooLarge, nil
}

// requestBodyStringValues 返回可能被截断的字符串值内容区间（不含引号）。
// 输入已确认是合法 JSON，后面紧跟冒号的字符串是键，键永远不截断。
func requestBodyStringValues(raw []byte) []requestBodySpan {
	var spans []requestBodySpan
	for index := 0; index < len(raw); index++ {
		if raw[index] != '"' {
			continue
		}
		start, end := index+1, index+1
		for raw[end] != '"' {
			if raw[end] == '\\' {
				end++
			}
			end++
		}
		index = end
		next := end + 1
		for next < len(raw) && (raw[next] == ' ' || raw[next] == '\t' || raw[next] == '\n' || raw[next] == '\r') {
			next++
		}
		if (next < len(raw) && raw[next] == ':') || end-start <= minRequestBodyStringBytes {
			continue
		}
		spans = append(spans, requestBodySpan{start: start, end: end})
	}
	return spans
}

func truncatedRequestBodySize(raw []byte, spans []requestBodySpan, limit int) int {
	size := len(raw)
	for _, span := range spans {
		length := span.end - span.start
		if length <= limit {
			continue
		}
		cut := requestBodyCut(raw[span.start:span.end], limit)
		omitted := length - cut
		size += len(requestBodyMarkerPrefix) + len(strconv.Itoa(omitted)) + len(requestBodyMarkerSuffix) - omitted
	}
	return size
}

func truncateRequestBodyStrings(raw []byte, spans []requestBodySpan, limit int) []byte {
	recorded := make([]byte, 0, maxRecordedRequestBodyBytes)
	last := 0
	for _, span := range spans {
		length := span.end - span.start
		if length <= limit {
			continue
		}
		cut := requestBodyCut(raw[span.start:span.end], limit)
		recorded = append(recorded, raw[last:span.start+cut]...)
		recorded = append(recorded, requestBodyMarkerPrefix...)
		recorded = strconv.AppendInt(recorded, int64(length-cut), 10)
		recorded = append(recorded, requestBodyMarkerSuffix...)
		last = span.end
	}
	return append(recorded, raw[last:]...)
}

// requestBodyCut 返回不超过 limit 的最大切点，不拆开转义序列、代理对和多字节字符。
func requestBodyCut(content []byte, limit int) int {
	cut := 0
	for cut < len(content) {
		width := 1
		switch {
		case content[cut] == '\\' && content[cut+1] == 'u':
			width = 6
			if isHighSurrogateEscape(content[cut:]) && cut+12 <= len(content) &&
				content[cut+6] == '\\' && content[cut+7] == 'u' {
				width = 12
			}
		case content[cut] == '\\':
			width = 2
		case content[cut] >= utf8.RuneSelf:
			_, width = utf8.DecodeRune(content[cut:])
		}
		if cut+width > limit {
			return cut
		}
		cut += width
	}
	return cut
}

func isHighSurrogateEscape(escape []byte) bool {
	value, err := strconv.ParseUint(string(escape[2:6]), 16, 16)
	return err == nil && value >= 0xD800 && value <= 0xDBFF
}
