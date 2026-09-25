package gateway

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/encryption"
	"gpt-load/internal/platform/httpheader"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
)

var reasonRedactionFailed = reason{http.StatusBadRequest, "request_redaction_failed", "Request content could not be redacted safely."}

func redactionBusinessProtocol(value protocol.Protocol) bool {
	switch value {
	case protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini:
		return true
	default:
		return false
	}
}

// 每次从本次请求的原始内容构造外发副本，缓存副本可复用于同组重试。
func redactOutboundRequest(c *requestredact.Compiled, clientProtocol protocol.Protocol, request *dialect.ParsedRequest, cipher ...encryption.RedactionCipher) (*dialect.ParsedRequest, error) {
	// 没有规则时仍要拆掉网关包装过的签名，否则上游收到的不是它自己的签名。
	if request == nil || len(request.Body) == 0 || (c.Empty() && !requestredact.HasSignatureRecord(request.Body)) {
		return request, nil
	}
	var body []byte
	var err error
	mediaType, params, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		body, err = redactMultipart(c, request.Body, params["boundary"], cipher...)
	} else if clientProtocol == protocol.Decisions {
		if len(cipher) != 0 && cipher[0] != nil {
			body, err = c.ApplyDecisionsWithCipher(request.Body, cipher[0])
		} else {
			body, err = c.ApplyDecisions(request.Body)
		}
	} else {
		if len(cipher) != 0 && cipher[0] != nil {
			body, err = c.ApplyWithCipher(request.Body, cipher[0])
		} else {
			body, err = c.Apply(request.Body)
		}
	}
	if err != nil {
		return nil, requestredact.ErrContent
	}
	if int64(len(body)) > maxRequestBodyBytes {
		return nil, requestredact.ErrContent
	}
	if bytes.Equal(body, request.Body) {
		return request, nil
	}
	clone := *request
	clone.Body = body
	clone.Header = request.Header.Clone()
	httpheader.StripRepresentationMetadata(clone.Header)
	return &clone, nil
}

// 图片附件保持字节内容不变，只处理 multipart 的文本 prompt。
func redactMultipart(c *requestredact.Compiled, body []byte, boundary string, cipher ...encryption.RedactionCipher) ([]byte, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, requestredact.ErrContent
	}
	changed := false
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, requestredact.ErrContent
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return nil, requestredact.ErrContent
		}
		if part.FileName() == "" && part.FormName() == "prompt" {
			if part.Header.Get("Content-Transfer-Encoding") != "" {
				return nil, requestredact.ErrContent
			}
			var text string
			if len(cipher) != 0 && cipher[0] != nil {
				text, err = c.TextWithCipher(string(data), cipher[0])
			} else {
				text, err = c.Text(string(data))
			}
			if err != nil {
				return nil, err
			}
			if text != string(data) {
				changed = true
				data = []byte(text)
				part.Header.Del("Content-Length")
			}
		}
		target, err := writer.CreatePart(part.Header)
		if err != nil {
			return nil, requestredact.ErrContent
		}
		if _, err = target.Write(data); err != nil {
			return nil, requestredact.ErrContent
		}
		if int64(out.Len()) > maxRequestBodyBytes {
			return nil, requestredact.ErrContent
		}
	}
	if err := writer.Close(); err != nil {
		return nil, requestredact.ErrContent
	}
	if !changed {
		return body, nil
	}
	return out.Bytes(), nil
}

// logUnrestoredRedactionTokens 记录按原文放行的无法认证密文，便于评估模型抄写密文的可靠性。
func (handler *Handler) logUnrestoredRedactionTokens(cipher encryption.RedactionCipher, requestID string) {
	if cipher == nil {
		return
	}
	occurrences := cipher.UnrestoredTokens()
	if occurrences == 0 {
		return
	}
	utils.LogPlaneBestEffort(
		handler.logger,
		logrus.WarnLevel,
		utils.LogPlaneData,
		logrus.Fields{"request_id": requestID, "occurrences": occurrences},
		"Unrestorable redaction tokens were forwarded unchanged",
	)
}

// redactionTokenMarkers 出现在外发请求里时，上游看到了密文，响应可能原样带回。
var redactionTokenMarkers = [][]byte{[]byte("gld1_"), []byte(`\u0067ld1_`)}

// redactionContextMarkers 表示请求引用了上游保存、客户端看不到原文的上下文
// （推理密文、签名、previous_response_id、缓存内容、代码执行容器等），其中可能带着以前加密过的内容。
var redactionContextMarkers = [][]byte{
	[]byte("encrypted_content"), []byte("signature"), []byte("Signature"),
	[]byte("previous_response_id"), []byte(`"conversation"`),
	[]byte(`"cachedContent"`), []byte(`"cached_content"`), []byte(`"container"`), []byte(`"container_id"`),
}

// redactionMayRestore 判断是否需要还原上游响应。不需要时流式数据收到即转发，不做任何扣留。
func redactionMayRestore(request *dialect.ParsedRequest, reversible bool) bool {
	if request != nil && containsAny(request.Body, redactionTokenMarkers) {
		return true
	}
	// 没有可逆加密规则时，上游保存的上下文里不会有新的密文。
	if !reversible {
		return false
	}
	// 检索等没有请求体的请求读取的是上游保存的内容。
	return request == nil || len(request.Body) == 0 || containsAny(request.Body, redactionContextMarkers)
}

func containsAny(body []byte, markers [][]byte) bool {
	for _, marker := range markers {
		if bytes.Contains(body, marker) {
			return true
		}
	}
	return false
}
