package embedded

import (
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
)

// Muse 的 SDK scanner 与终态观察必须读取同一份解码后的 SSE。
func normalizeMuseResponse(response *http.Response) error {
	body, err := helps.DecodeResponseBody(response.Body, response.Header)
	if err != nil {
		return err // SDK 解码器在初始化失败时已关闭原始 body。
	}
	response.Body = body
	contentType, _, _ := strings.Cut(response.Header.Get("Content-Type"), ";")
	if strings.EqualFold(strings.TrimSpace(contentType), "text/event-stream") {
		response.Body = &museSSEBody{ReadCloser: body}
	}
	encoding := strings.TrimSpace(strings.Join(response.Header.Values("Content-Encoding"), ","))
	response.Uncompressed = response.Uncompressed || encoding != "" && !strings.EqualFold(encoding, "identity")
	response.Header = response.Header.Clone()
	response.Header.Del("Content-Encoding")
	response.Header.Del("Content-Length")
	response.ContentLength = -1
	return nil
}

// 只规范行结束符，不缓存整个事件；CRLF 跨 Read 时仍只输出一个 LF。
type museSSEBody struct {
	io.ReadCloser
	afterCR bool
}

func (body *museSSEBody) Read(buffer []byte) (int, error) {
	for {
		n, err := body.ReadCloser.Read(buffer)
		written := 0
		for _, value := range buffer[:n] {
			if value == '\n' && body.afterCR {
				body.afterCR = false
				continue
			}
			body.afterCR = value == '\r'
			if body.afterCR {
				value = '\n'
			}
			buffer[written] = value
			written++
		}
		if written > 0 || err != nil || n == 0 {
			return written, err
		}
	}
}
