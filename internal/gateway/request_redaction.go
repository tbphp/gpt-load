package gateway

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/httpheader"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestredact"
)

var reasonRedactionFailed = reason{http.StatusBadRequest, "request_redaction_failed", "Request content could not be redacted safely."}

// 每次从本次请求的原始内容构造外发副本，缓存副本可复用于同组重试。
func redactOutboundRequest(c *requestredact.Compiled, clientProtocol protocol.Protocol, request *dialect.ParsedRequest) (*dialect.ParsedRequest, error) {
	if c.Empty() || request == nil || len(request.Body) == 0 {
		return request, nil
	}
	var body []byte
	var err error
	mediaType, params, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if mediaType == "multipart/form-data" {
		body, err = redactMultipart(c, request.Body, params["boundary"])
	} else if clientProtocol == protocol.Decisions {
		body, err = c.ApplyDecisions(request.Body)
	} else {
		body, err = c.Apply(request.Body)
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
func redactMultipart(c *requestredact.Compiled, body []byte, boundary string) ([]byte, error) {
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
			text, err := c.Text(string(data))
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
