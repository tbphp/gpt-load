package gateway

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
)

// redactionSigner 为响应里的上游签名生成还原记录：签名内容里每个字符串的客户端整串哈希，
// 以及网关还原过的片段和上游原文。下一轮请求据此把签名内容逐字节还给上游。
type redactionSigner struct {
	cipher encryption.RedactionCipher
}

func newRedactionSigner(cipher encryption.RedactionCipher) *redactionSigner {
	if cipher == nil {
		return nil
	}
	return &redactionSigner{cipher: cipher}
}

// spans 找出 restored 相对 original 被还原的片段；逐个密文推算不出 restored 时整串记为一段。
func (signer *redactionSigner) spans(original, restored string) []requestredact.RestoreSpan {
	if original == restored {
		return nil
	}
	var spans []requestredact.RestoreSpan
	var built strings.Builder
	last := 0
	for scan := 0; scan < len(original); {
		relative := strings.Index(original[scan:], redactionStreamPrefix)
		if relative < 0 {
			break
		}
		start := scan + relative
		end, valid := signer.cipher.ValidTokenAt(original, start)
		if !valid {
			scan = start + len(redactionStreamPrefix)
			continue
		}
		token := original[start:end]
		plaintext, err := signer.cipher.RestoreText(token)
		if err != nil || plaintext == token {
			scan = end
			continue
		}
		built.WriteString(original[last:start])
		spans = append(spans, requestredact.RestoreSpan{Offset: built.Len(), Length: len(plaintext), Original: token})
		built.WriteString(plaintext)
		last, scan = end, end
	}
	built.WriteString(original[last:])
	if built.String() != restored {
		return []requestredact.RestoreSpan{{Offset: 0, Length: len(restored), Original: original}}
	}
	return spans
}

// redactionSignedBlock 是一个协议签名位置。block 是签名所属对象在本条响应里的路径（空串表示根对象），
// signature 是签名在对象里的路径。正文在本条响应里时，记录覆盖签名块里需要核对的全部字符串
// （requestredact.SignedContent）；正文跨事件下发时（streamed），记录由各通道累计的正文生成。
type redactionSignedBlock struct {
	block     string
	signature []any
	streamed  bool
	values    []requestredact.SignedString
}

// signPayload 把还原记录写进 restored 里各签名位置；original 是同一结构的上游原文。
func (signer *redactionSigner) signPayload(original, restored []byte, blocks []redactionSignedBlock) ([]byte, error) {
	if signer == nil || len(blocks) == 0 {
		return restored, nil
	}
	originalRoot, restoredRoot := gjson.ParseBytes(original), gjson.ParseBytes(restored)
	var patches []unaryRestorePatch
	for _, block := range blocks {
		restoredBlock, originalBlock := restoredRoot, originalRoot
		if block.block != "" {
			restoredBlock, originalBlock = restoredRoot.Get(block.block), originalRoot.Get(block.block)
		}
		signature := requestredact.PathValue(restoredBlock, block.signature)
		if signature.Type != gjson.String || signature.Str == "" {
			continue
		}
		values := block.values
		if !block.streamed {
			var ok bool
			if values, ok = requestredact.SignedContent(restoredBlock, block.signature); !ok {
				return nil, errUnaryRestore
			}
			for i := range values {
				upstream := requestredact.PathValue(originalBlock, values[i].Path)
				if upstream.Type != gjson.String {
					return nil, errUnaryRestore
				}
				values[i].Spans = signer.spans(upstream.Str, values[i].Value)
			}
		}
		wrapped := requestredact.WrapSignature(signature.Str, values)
		if wrapped == signature.Str {
			continue
		}
		encoded, err := json.Marshal(wrapped)
		if err != nil {
			return nil, errUnaryRestore
		}
		patches = append(patches, unaryRestorePatch{signature.Index, signature.Index + len(signature.Raw), encoded})
	}
	if len(patches) == 0 {
		return restored, nil
	}
	ctx := unaryRestoreContext{body: restored, patches: patches}
	return ctx.apply()
}

// unarySignedBlocks 列出响应里的协议签名位置：Claude 思考块、Gemini 片段、Chat 的
// reasoning_details 与 Gemini 兼容接口的工具调用、Responses 推理项的推理文本。
// 工具参数等业务数据里的同名字段不是签名。
func unarySignedBlocks(clientProtocol protocol.Protocol, root gjson.Result) []redactionSignedBlock {
	var blocks []redactionSignedBlock
	switch clientProtocol {
	case protocol.Anthropic:
		redactionEach(root.Get("content"), func(i int, block gjson.Result) {
			if block.Get("type").Str == "thinking" {
				blocks = append(blocks, redactionSignedBlock{block: "content." + strconv.Itoa(i), signature: []any{"signature"}})
			}
		})
	case protocol.Gemini:
		redactionEach(root.Get("candidates"), func(c int, candidate gjson.Result) {
			redactionEach(candidate.Get("content.parts"), func(p int, part gjson.Result) {
				blocks = append(blocks, geminiSignedBlocks("candidates."+strconv.Itoa(c)+".content.parts."+strconv.Itoa(p), part)...)
			})
		})
	case protocol.OpenAICompletions:
		redactionEach(root.Get("choices"), func(c int, choice gjson.Result) {
			message := "choices." + strconv.Itoa(c) + ".message"
			redactionEach(choice.Get("message.reasoning_details"), func(i int, detail gjson.Result) {
				blocks = append(blocks, redactionSignedBlock{block: message + ".reasoning_details." + strconv.Itoa(i), signature: []any{"signature"}})
			})
			redactionEach(choice.Get("message.tool_calls"), func(i int, _ gjson.Result) {
				blocks = append(blocks, chatToolCallSignedBlock(message+".tool_calls."+strconv.Itoa(i)))
			})
		})
	case protocol.OpenAIResponses:
		for _, list := range []string{"output", "response.output", "data"} {
			redactionEach(root.Get(list), func(i int, item gjson.Result) {
				blocks = append(blocks, responsesReasoningSignedBlocks(list+"."+strconv.Itoa(i), item)...)
			})
		}
	}
	return blocks
}

func geminiSignedBlocks(path string, part gjson.Result) []redactionSignedBlock {
	var blocks []redactionSignedBlock
	for _, name := range []string{"thoughtSignature", "thought_signature"} {
		if part.Get(name).Exists() {
			blocks = append(blocks, redactionSignedBlock{block: path, signature: []any{name}})
		}
	}
	return blocks
}

func chatToolCallSignedBlock(path string) redactionSignedBlock {
	return redactionSignedBlock{block: path, signature: []any{"extra_content", "google", "thought_signature"}}
}

func responsesReasoningSignedBlocks(path string, item gjson.Result) []redactionSignedBlock {
	if item.Get("type").Str != "reasoning" {
		return nil
	}
	var blocks []redactionSignedBlock
	redactionEach(item.Get("content"), func(i int, part gjson.Result) {
		if part.Get("type").Str == "reasoning_text" {
			blocks = append(blocks, redactionSignedBlock{block: path + ".content." + strconv.Itoa(i), signature: []any{"signature"}})
		}
	})
	return blocks
}

func redactionEach(array gjson.Result, visit func(int, gjson.Result)) {
	if !array.IsArray() {
		return
	}
	position := 0
	array.ForEach(func(_, value gjson.Result) bool {
		visit(position, value)
		position++
		return true
	})
}

// redactionSignedChannel 累计一个正文通道在当前签名块里的上游原文与客户端看到的文字。
// 超出累计上限时不再保存正文，记录改为必然核对失败，下一轮按普通上游内容加密。
type redactionSignedChannel struct {
	original strings.Builder
	restored strings.Builder
	overflow bool
}

// redactionSignedChannelKey 报告通道的正文是否可能跨事件属于一个签名块。
func redactionSignedChannelKey(key string) bool {
	return strings.HasPrefix(key, "anthropic/") && strings.HasSuffix(key, "/thinking") ||
		strings.HasPrefix(key, "chat/") && (strings.Contains(key, "/reasoning_details/") || strings.Contains(key, "/tool/")) ||
		strings.HasPrefix(key, "response/reasoning/")
}

type redactionChannelField struct {
	path []any
	key  string
}

// channelValues 由各通道累计的正文生成签名记录；没有收到过正文的通道不记录。
func (stream *redactionRestoreSSE) channelValues(fields ...redactionChannelField) []requestredact.SignedString {
	if stream.signer == nil {
		return nil
	}
	var values []requestredact.SignedString
	for _, field := range fields {
		channel := stream.signedChannels[field.key]
		if channel == nil {
			continue
		}
		if channel.overflow {
			values = append(values, requestredact.SignedString{Path: field.path})
			continue
		}
		original, restored := channel.original.String(), channel.restored.String()
		values = append(values, requestredact.SignedString{
			Path: field.path, Value: restored, Spans: stream.signer.spans(original, restored),
		})
	}
	return values
}

func (stream *redactionRestoreSSE) recordChannel(key, original, restored string) {
	if stream.signer == nil || !redactionSignedChannelKey(key) {
		return
	}
	channel := stream.signedChannels[key]
	if channel == nil {
		channel = &redactionSignedChannel{}
		stream.signedChannels[key] = channel
	}
	if channel.overflow {
		return
	}
	if stream.signedBytes > maxRedactionStreamPendingBytes-len(original)-len(restored) {
		channel.overflow = true
		stream.signedBytes -= channel.original.Len() + channel.restored.Len()
		channel.original.Reset()
		channel.restored.Reset()
		return
	}
	stream.signedBytes += len(original) + len(restored)
	channel.original.WriteString(original)
	channel.restored.WriteString(restored)
}

// dropSignedChannels 在签名块结束后释放累计的正文。
func (stream *redactionRestoreSSE) dropSignedChannels(prefix string) {
	for key, channel := range stream.signedChannels {
		if prefix == "" || strings.HasPrefix(key, prefix) {
			stream.signedBytes -= channel.original.Len() + channel.restored.Len()
			delete(stream.signedChannels, key)
		}
	}
}

// redactionSignaturePresent 报告签名字段是否带着真实签名；分片里的 null 或空串只是占位。
func redactionSignaturePresent(value gjson.Result) bool {
	return value.Type == gjson.String && value.Str != ""
}

func (stream *redactionRestoreSSE) signAt(event *redactionStreamEvent, block redactionSignedBlock) {
	if stream.signer != nil {
		event.signatures = append(event.signatures, block)
	}
}
