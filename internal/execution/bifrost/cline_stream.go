package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/maximhq/bifrost/core/schemas"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/httpheader"
	"gpt-load/internal/protocol"
)

// 复用原生传输的超时、取消和用量采集，仅在 Cline 边界处理错误及协议转换。
func (r *Runtime) executeClineStream(parent context.Context, spec execution.AttemptSpec, prepared preparedAttempt, sink execution.StreamSink) execution.StreamResult {
	ctx := schemas.NewBifrostContext(parent, schemas.NoDeadline)
	defer ctx.Cancel()
	stream := &clineStream{
		ctx: ctx, spec: spec, sink: sink, secrets: prepared.secrets,
		converted: prepared.mode == channel.RouteConverted,
	}
	if stream.converted {
		stream.encoder = newConvertedResponsesStreamEncoder(spec.ClientProtocol)
		stream.chatState = schemas.AcquireChatToResponsesStreamState()
		defer schemas.ReleaseChatToResponsesStreamState(stream.chatState)
	}
	wireSpec := spec
	wireSpec.ClientProtocol = protocol.OpenAICompletions
	wireSpec.Operation = execution.OperationChatCompletion
	result := r.executeNativeStream(parent, wireSpec, prepared, func(event execution.StreamEvent) error {
		err := stream.push(event)
		if err != nil && stream.sinkError == nil {
			stream.protocolError = err
		}
		return err
	})
	if result.Error == nil || result.Error.Kind == execution.ErrorKindHTTP {
		if err := stream.finish(); err != nil && stream.sinkError == nil {
			stream.protocolError = err
		}
	}
	if stream.sinkError != nil {
		result.Error = &execution.ErrorEvidence{Kind: execution.ErrorKindCanceled, Summary: "stream consumer stopped"}
	} else if stream.protocolError != nil {
		result.Error = invalidClineResponse(result.Header).Error
	} else if stream.upstreamError != nil {
		result.Error = stream.upstreamError
	}
	if stream.converted {
		result.AppliedReasoning = inspectWireAppliedReasoning(prepared.passthrough.Body)
	}
	if stream.headers != nil {
		result.Header = stream.headers
	}
	return result
}

type clineStream struct {
	ctx           *schemas.BifrostContext
	spec          execution.AttemptSpec
	sink          execution.StreamSink
	secrets       []string
	converted     bool
	encoder       *convertedResponsesStreamEncoder
	chatState     *schemas.ChatToResponsesStreamState
	pending       []byte
	errorBody     []byte
	status        int
	headers       http.Header
	sequence      uint64
	done          bool
	usage         *schemas.BifrostLLMUsage
	final         *schemas.BifrostResponsesStreamResponse
	sinkError     error
	protocolError error
	upstreamError *execution.ErrorEvidence
}

func (s *clineStream) emit(event execution.StreamEvent) error {
	s.sequence++
	event.Sequence = s.sequence
	if err := s.sink(event); err != nil {
		s.sinkError = err
		return err
	}
	return nil
}

func (s *clineStream) data(body []byte) error {
	return s.emit(execution.StreamEvent{Kind: execution.StreamEventData, Data: body})
}

func (s *clineStream) push(event execution.StreamEvent) error {
	switch event.Kind {
	case execution.StreamEventReady:
		s.status = event.StatusCode
		s.headers = event.Header.Clone()
		httpheader.StripRepresentationMetadata(s.headers)
		event.Header = s.headers
		return s.emit(event)
	case execution.StreamEventUsage:
		return s.emit(event)
	case execution.StreamEventData:
		if s.status < http.StatusOK || s.status >= http.StatusMultipleChoices {
			remaining := maxStreamErrorEvidenceBytes - len(s.errorBody)
			if remaining > 0 {
				s.errorBody = append(s.errorBody, event.Data[:min(len(event.Data), remaining)]...)
			}
			return nil
		}
		s.pending = append(s.pending, event.Data...)
		for len(s.pending) > 0 {
			end, complete := firstCompleteNativeSSEEvent(s.pending, 0)
			if !complete {
				if len(s.pending) > execution.DefaultSSEEventLimitBytes {
					return fmt.Errorf("Cline SSE event exceeds limit")
				}
				break
			}
			if end > execution.DefaultSSEEventLimitBytes {
				return fmt.Errorf("Cline SSE event exceeds limit")
			}
			if err := s.event(s.pending[:end]); err != nil {
				return err
			}
			s.pending = s.pending[end:]
		}
	}
	return nil
}

func (s *clineStream) event(frame []byte) error {
	var lines [][]byte
	for _, line := range splitNativeSSELines(frame) {
		if data, value := parseNativeSSEDataLine(line.content); data {
			lines = append(lines, value)
		}
	}
	if len(lines) == 0 {
		if !s.converted {
			return s.data(bytes.Clone(frame))
		}
		return nil
	}
	payload := bytes.TrimSpace(bytes.Join(lines, []byte("\n")))
	if s.done {
		return fmt.Errorf("Cline sent data after [DONE]")
	}
	if bytes.Equal(payload, []byte("[DONE]")) {
		s.done = true
		if !s.converted {
			return s.data(bytes.Clone(frame))
		}
		if s.upstreamError != nil {
			return nil
		}
		if s.final == nil || s.final.Response == nil {
			return fmt.Errorf("Cline stream ended without a completion")
		}
		if s.usage != nil {
			s.final.Response.Usage = s.usage.ToResponsesResponseUsage()
		}
		return s.encode(s.final)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return fmt.Errorf("invalid Cline SSE payload")
	}
	errorBody, err := clineStreamError(object)
	if err != nil {
		return err
	}
	if errorBody != nil {
		errorBody = redactSecrets(errorBody, s.secrets)
		s.upstreamError = passthroughHTTPError(http.StatusBadGateway, s.headers, errorBody, s.secrets)
		s.upstreamError.Kind = execution.ErrorKindProvider
		s.upstreamError.OriginHint = execution.ErrorOriginUpstream
		if !s.converted {
			return s.data(frameSSE(errorBody))
		}
		return s.encode(&schemas.BifrostResponsesStreamResponse{
			Type:    schemas.ResponsesStreamResponseTypeError,
			Message: schemas.Ptr(s.upstreamError.Summary), Code: schemas.Ptr(s.upstreamError.Code),
		})
	}
	if !s.converted {
		return s.data(bytes.Clone(frame))
	}
	var chunk schemas.BifrostChatResponse
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return fmt.Errorf("invalid Cline Chat chunk")
	}
	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}
	chunk.Model = s.spec.ClientModel
	for _, response := range clineChatStreamEvents(&chunk, s.chatState) {
		if response.Type == schemas.ResponsesStreamResponseTypeCompleted || response.Type == schemas.ResponsesStreamResponseTypeIncomplete {
			// finish_reason 后可能还有独立 usage 块，等 [DONE] 再发终态。
			s.final = response
			continue
		}
		if err := s.encode(response); err != nil {
			return err
		}
	}
	return nil
}

// 锁定版本的 SDK 每次只读取一个工具增量；逐个交给同一状态机，避免并行调用丢失。
func clineChatStreamEvents(chunk *schemas.BifrostChatResponse, state *schemas.ChatToResponsesStreamState) []*schemas.BifrostResponsesStreamResponse {
	if len(chunk.Choices) != 1 || chunk.Choices[0].ChatStreamResponseChoice == nil || chunk.Choices[0].Delta == nil || len(chunk.Choices[0].Delta.ToolCalls) < 2 {
		return chunk.ToBifrostResponsesStreamResponse(state)
	}
	choice := chunk.Choices[0]
	var events []*schemas.BifrostResponsesStreamResponse
	for index, call := range choice.Delta.ToolCalls {
		part := *chunk
		partChoice := choice
		delta := *choice.Delta
		if index > 0 {
			delta = schemas.ChatStreamResponseChoiceDelta{}
		}
		delta.ToolCalls = []schemas.ChatAssistantMessageToolCall{call}
		partChoice.ChatStreamResponseChoice = &schemas.ChatStreamResponseChoice{Delta: &delta}
		if index != len(choice.Delta.ToolCalls)-1 {
			partChoice.FinishReason = nil
			part.Usage = nil
		}
		part.Choices = []schemas.BifrostResponseChoice{partChoice}
		events = append(events, part.ToBifrostResponsesStreamResponse(state)...)
	}
	return events
}

func (s *clineStream) encode(response *schemas.BifrostResponsesStreamResponse) error {
	if response.Response != nil {
		response.Response.Model = s.spec.ClientModel
		response.Response.Store = schemas.Ptr(false)
		if response.Type == schemas.ResponsesStreamResponseTypeCreated || response.Type == schemas.ResponsesStreamResponseTypeInProgress {
			response.Response.Status = schemas.Ptr("in_progress")
		}
		if s.spec.ClientProtocol == protocol.OpenAIResponses {
			wire := response.WithDefaults()
			ensureResponsesReasoningSummary(wire)
			inner, err := marshalClientWire(protocol.OpenAIResponses, wire.Response)
			if err != nil {
				return err
			}
			body, err := marshalClientWire(protocol.OpenAIResponses, wire)
			if err != nil {
				return err
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(body, &object); err != nil {
				return err
			}
			object["response"] = inner
			body, err = json.Marshal(object)
			if err != nil {
				return err
			}
			return s.data(frameNamedSSE(string(wire.Type), body))
		}
	}
	frames, err := s.encoder.encode(s.ctx, response, nil)
	if err != nil {
		return err
	}
	for _, frame := range frames {
		if err := s.data(frame); err != nil {
			return err
		}
	}
	return nil
}

func (s *clineStream) finish() error {
	if s.status < http.StatusOK || s.status >= http.StatusMultipleChoices {
		_, body, err := normalizeClineResponse(s.status, s.errorBody)
		if err != nil {
			body, err = normalizeClineError(nil)
			if err != nil {
				return err
			}
		}
		s.upstreamError = passthroughHTTPError(s.status, s.headers, body, s.secrets)
		return s.data(body)
	}
	if len(bytes.TrimSpace(s.pending)) > 0 {
		if err := s.event(s.pending); err != nil {
			return err
		}
	}
	if !s.done && s.upstreamError == nil {
		return fmt.Errorf("Cline stream ended before [DONE]")
	}
	return nil
}

func clineStreamError(object map[string]json.RawMessage) ([]byte, error) {
	if clineErrorPresent(object["error"]) || bytes.Equal(bytes.TrimSpace(object["success"]), []byte("false")) {
		return normalizeClineError(object)
	}
	var choices []struct {
		FinishReason string          `json:"finish_reason"`
		Error        json.RawMessage `json:"error"`
	}
	if json.Unmarshal(object["choices"], &choices) != nil {
		return nil, nil
	}
	for _, choice := range choices {
		if choice.FinishReason == "error" || clineErrorPresent(choice.Error) {
			return normalizeClineError(map[string]json.RawMessage{"error": choice.Error})
		}
	}
	return nil, nil
}
