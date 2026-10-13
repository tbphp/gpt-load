package responsemodel

import (
	"bytes"
	"strings"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// Observer 只记录响应中的模型，不改变响应字节或执行结果。
// 缺少模型时保持为空；同一尝试一旦观察到不一致，后续匹配值不能覆盖它。
type Observer struct {
	Model      string
	expected   string
	inspector  dialect.ResponseModelInspector
	classifier dialect.StreamEventClassifier
	limit      int
	line       []byte
	payload    []byte
	eventName  string
	eventBytes int
	afterCR    bool
	stopped    bool
}

// IsConversation 限定模型观测修复的操作范围，不改变计数、探测和资源接口的合同。
func IsConversation(p protocol.Protocol, operation execution.Operation) bool {
	switch operation {
	case execution.OperationChatCompletion:
		return p == protocol.OpenAICompletions || p == protocol.Anthropic || p == protocol.Gemini
	case execution.OperationResponsesCreate, execution.OperationResponsesCompact:
		return p == protocol.OpenAIResponses
	default:
		return false
	}
}

func New(p protocol.Protocol, expected string) *Observer {
	var selected dialect.Dialect
	switch p {
	case protocol.OpenAIResponses:
		selected = dialect.NewOpenAIResponses()
	case protocol.Anthropic:
		selected = dialect.NewAnthropic()
	case protocol.Gemini:
		selected = dialect.NewGemini()
	case protocol.OpenAICompletions:
		selected = dialect.NewOpenAI()
	}
	inspector, _ := selected.(dialect.ResponseModelInspector)
	classifier, _ := selected.(dialect.StreamEventClassifier)
	return &Observer{expected: expected, inspector: inspector, classifier: classifier, limit: execution.SSEEventLimit(p)}
}

func (o *Observer) ObserveModel(model string) {
	if strings.TrimSpace(model) != "" && (o.Model == "" || o.Model == o.expected || model != o.expected) {
		o.Model = model
	}
}

func (o *Observer) ObserveJSON(payload []byte) {
	if o.inspector == nil {
		return
	}
	for _, model := range o.inspector.InspectResponseModels(payload) {
		o.ObserveModel(model)
	}
}

// ObserveSSE 旁路读取任意分片，支持 CR/LF、跨片和多行 data。超限仅停止观测，
// 不向转发流程返回错误，也不缓存整个响应。
func (o *Observer) ObserveSSE(chunk []byte) {
	for o.inspector != nil && len(chunk) > 0 && !o.stopped {
		if o.afterCR {
			o.afterCR = false
			if chunk[0] == '\n' {
				chunk = chunk[1:]
				continue
			}
		}
		end := bytes.IndexAny(chunk, "\r\n")
		part := chunk
		if end >= 0 {
			part = chunk[:end]
		}
		o.eventBytes += len(part)
		if o.eventBytes > o.limit {
			o.stopped = true
			o.line, o.payload = nil, nil
			return
		}
		o.line = append(o.line, part...)
		if end < 0 {
			return
		}
		o.afterCR = chunk[end] == '\r'
		chunk = chunk[end+1:]
		o.observeLine()
	}
}

// FinishSSE 只在上游正常结束后处理未带空行分隔符的最后一个事件。
func (o *Observer) FinishSSE() {
	if !o.stopped {
		if len(o.line) > 0 {
			o.observeLine()
		}
		o.observeEvent()
	}
}

func (o *Observer) observeLine() {
	if len(o.line) == 0 {
		o.observeEvent()
		return
	}
	field, value, _ := bytes.Cut(o.line, []byte{':'})
	value = bytes.TrimPrefix(value, []byte{' '})
	switch string(field) {
	case "data":
		o.payload = append(o.payload, value...)
		o.payload = append(o.payload, '\n')
	case "event":
		o.eventName = string(value)
	}
	o.line = o.line[:0]
	// 每行至少占一个换行字节，避免空 data 行绕过事件大小上限。
	o.eventBytes++
}

func (o *Observer) observeEvent() {
	if len(o.payload) > 0 {
		payload := o.payload[:len(o.payload)-1]
		o.ObserveJSON(payload)
		if o.classifier != nil {
			classification, err := o.classifier.ClassifyStreamEvent(dialect.StreamEvent{Name: o.eventName, Payload: payload})
			if err == nil && classification.IsTerminal() {
				o.stopped = true
			}
		}
	}
	o.payload = o.payload[:0]
	o.eventName = ""
	o.eventBytes = 0
}
