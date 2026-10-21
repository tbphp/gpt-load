package bifrost

import (
	"bytes"
	"strings"

	"gpt-load/internal/channel"
	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

// nativeWireUsage 在原生 Anthropic / Gemini 透传路径上直接读取上游原始 usage。
// SDK 的 PassthroughUsage 只携带 OpenAI 形状的 token 计数：Anthropic 的 usage.iterations 里
// advisor 子推理被丢掉，Gemini 的 toolUsePromptTokenCount 被丢掉。这里用 gpt-load 自己的方言提取器
// 读取原始响应；只有当原始响应报告了 SDK 折算结果之外的计费量时才替换，否则保持 SDK 结果不变。
type nativeWireUsage struct {
	kind      protocol.Protocol
	extractor dialect.UsageStreamExtractor
	line      []byte
	afterCR   bool
	seen      bool
}

func newNativeWireUsage(spec execution.AttemptSpec, prepared preparedAttempt) *nativeWireUsage {
	if prepared.mode != channel.RouteNative || spec.Operation != execution.OperationChatCompletion {
		return nil
	}
	switch prepared.upstreamProtocol {
	case protocol.Anthropic:
		return &nativeWireUsage{kind: protocol.Anthropic, extractor: dialect.NewAnthropic().NewUsageStreamSnapshotExtractor()}
	case protocol.Gemini:
		return &nativeWireUsage{kind: protocol.Gemini, extractor: dialect.NewGemini().NewUsageStreamExtractor()}
	}
	return nil
}

// observeSSE 接受任意分片，按行拆出 data 事件交给提取器；解析失败不影响转发。
func (w *nativeWireUsage) observeSSE(chunk []byte) {
	if w == nil {
		return
	}
	for len(chunk) > 0 {
		if w.afterCR {
			w.afterCR = false
			if chunk[0] == '\n' {
				chunk = chunk[1:]
				continue
			}
		}
		end := bytes.IndexAny(chunk, "\r\n")
		if end < 0 {
			if len(w.line) < maxStreamErrorEvidenceBytes*16 {
				w.line = append(w.line, chunk...)
			}
			return
		}
		w.line = append(w.line, chunk[:end]...)
		w.afterCR = chunk[end] == '\r'
		chunk = chunk[end+1:]
		w.flushLine()
	}
}

func (w *nativeWireUsage) finish() {
	if w != nil && len(w.line) > 0 {
		w.flushLine()
	}
}

func (w *nativeWireUsage) flushLine() {
	line := string(w.line)
	w.line = w.line[:0]
	if !strings.HasPrefix(line, "data:") {
		return
	}
	payload := bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))
	if len(payload) == 0 || payload[0] != '{' {
		return
	}
	if err := w.extractor.Observe(payload); err == nil {
		w.seen = true
	}
}

// result is the usage the stream has shown so far. Anthropic can be read at any point; Gemini's extractor
// finalizes once, so it is read when the stream has ended (final).
func (w *nativeWireUsage) result(final bool) (usage.Result, bool) {
	if w == nil || !w.seen {
		return usage.Result{}, false
	}
	if snapshot, ok := w.extractor.(dialect.UsageStreamSnapshotExtractor); ok {
		return snapshot.Snapshot(), true
	}
	if !final {
		return usage.Result{}, false
	}
	return w.extractor.Finalize()
}

// override replaces the SDK usage only when the wire shows billable usage the SDK did not carry.
func (w *nativeWireUsage) override(current *execution.UsageEvidence, final bool) *execution.UsageEvidence {
	wire, ok := w.result(final)
	if !ok {
		return current
	}
	return overrideWithWireUsage(current, wire)
}

func overrideWithWireUsage(current *execution.UsageEvidence, wire usage.Result) *execution.UsageEvidence {
	if wire.State != usage.StateComplete && wire.State != usage.StatePartial {
		return current
	}
	if !wireShowsMore(current, wire) {
		return current
	}
	evidence := &execution.UsageEvidence{Normalized: wire}
	if current != nil {
		evidence.Raw = append([]byte(nil), current.Raw...)
	}
	return evidence
}

// wireShowsMore: extra passes that ran on another model (Anthropic advisor), or more billable tokens in total
// than the SDK reported (Gemini tool-use prompt tokens).
func wireShowsMore(current *execution.UsageEvidence, wire usage.Result) bool {
	if wire.Extra != nil {
		return true
	}
	if current == nil {
		return false
	}
	wireTotal, wireOK := usage.CheckedTotal(wire.Tokens)
	currentTotal, currentOK := usage.CheckedTotal(current.Normalized.Tokens)
	return wireOK && currentOK && wireTotal > currentTotal
}

// unaryNativeWireUsage 处理非流式响应体。
func unaryNativeWireUsage(
	spec execution.AttemptSpec,
	prepared preparedAttempt,
	body []byte,
	current *execution.UsageEvidence,
) *execution.UsageEvidence {
	wire := newNativeWireUsage(spec, prepared)
	if wire == nil {
		return current
	}
	var (
		result usage.Result
		err    error
	)
	if wire.kind == protocol.Anthropic {
		result, err = dialect.NewAnthropic().ExtractUsage(body)
	} else {
		result, err = dialect.NewGemini().ExtractUsage(body)
	}
	if err != nil {
		return current
	}
	return overrideWithWireUsage(current, result)
}
