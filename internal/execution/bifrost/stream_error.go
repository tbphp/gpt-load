package bifrost

import (
	"encoding/json"
	"sync/atomic"

	providerUtils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"

	"gpt-load/internal/protocol"
)

type streamErrorStatusKey struct{}

// 0 表示没有错误事件，-1 表示事件未携带状态，正数是上游显式状态。
// 只保存状态来源，不保存原始响应内容。
type streamErrorStatus struct{ value atomic.Int64 }

func captureStreamErrorStatus(ctx *schemas.BifrostContext, upstream protocol.Protocol) {
	if upstream == protocol.OpenAICompletions || upstream == protocol.OpenAIResponses {
		ctx.SetValue(streamErrorStatusKey{}, &streamErrorStatus{})
	}
}

type streamErrorDataReader struct {
	providerUtils.SSEDataReader
	status *streamErrorStatus
}

func (reader *streamErrorDataReader) ReadDataLine() ([]byte, error) {
	reader.status.value.Store(0)
	data, err := reader.SSEDataReader.ReadDataLine()
	if err != nil {
		return data, err
	}
	eventType := gjson.GetBytes(data, "type").String()
	if eventType != "error" && eventType != "response.failed" && !gjson.GetBytes(data, "error").IsObject() {
		return data, nil
	}
	var event struct {
		StatusCode *int `json:"status_code"`
	}
	if json.Unmarshal(data, &event) == nil {
		status := int64(-1)
		if event.StatusCode != nil && *event.StatusCode >= 100 && *event.StatusCode <= 599 {
			status = int64(*event.StatusCode)
		}
		reader.status.value.Store(status)
	}
	return data, nil
}

// 保留 SDK 对 [DONE]、截断及 finish_reason 后心跳的判断。
func (reader *streamErrorDataReader) SawDoneMarker() bool {
	return providerUtils.SSEStreamEndedOnMarker(reader.SSEDataReader)
}

func (reader *streamErrorDataReader) EndOnCommentAfterFinish() {
	providerUtils.SSEEndOnCommentAfterFinish(reader.SSEDataReader)
}

func (reader *streamErrorDataReader) EndedOnComment() bool {
	return providerUtils.SSEEndedOnComment(reader.SSEDataReader)
}

type streamErrorStatusHook struct{}

func (streamErrorStatusHook) GetName() string { return "gpt-load-stream-error-status" }
func (streamErrorStatusHook) Cleanup() error  { return nil }
func (streamErrorStatusHook) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	return nil
}
func (streamErrorStatusHook) PreLLMHook(_ *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	return req, nil, nil
}
func (streamErrorStatusHook) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, err *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	capture, _ := ctx.Value(streamErrorStatusKey{}).(*streamErrorStatus)
	if capture == nil || err == nil {
		return resp, err, nil
	}
	status := capture.value.Swap(0)
	if status == 0 {
		return resp, err, nil
	}
	// SDK 同步处理刚读取的错误事件时恢复源状态；真实 HTTP 拒绝没有此记录。
	// 在这里归一化也避免首帧错误被 SDK 提升返回后丢失来源。
	copyError := *err
	copyError.StatusCode = nil
	if status > 0 {
		copyError.StatusCode = schemas.Ptr(int(status))
	}
	return resp, &copyError, nil
}
