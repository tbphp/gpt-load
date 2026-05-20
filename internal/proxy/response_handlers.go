package proxy

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func (ps *ProxyServer) handleStreamingResponse(c *gin.Context, resp *http.Response) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		logrus.Error("Streaming unsupported by the writer, falling back to normal response")
		ps.handleNormalResponse(c, resp)
		return
	}

	buf := make([]byte, 4*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
				logUpstreamError("writing stream to client", writeErr)
				return
			}
			flusher.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			logUpstreamError("reading from upstream", err)
			return
		}
	}
}

// handleNormalResponse buffers the upstream response body only when token usage
// extraction is needed (successful chat-completion responses). For all other
// non-stream responses it streams directly to the client via io.Copy to avoid
// buffering large payloads into memory.
// Note: response headers and status code are already set by the caller (HandleProxy).
func (ps *ProxyServer) handleNormalResponse(c *gin.Context, resp *http.Response) *TokenUsage {
	needsUsage := resp.StatusCode < 400 && isChatCompletionPath(c.Request.URL.Path)

	if !needsUsage {
		// Stream directly to client — no buffering.
		if _, err := io.Copy(c.Writer, resp.Body); err != nil {
			logUpstreamError("streaming response to client", err)
		}
		return nil
	}

	// Buffer the body for usage extraction.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logUpstreamError("reading response body", err)
		return nil
	}

	body = handleGzipCompression(resp, body)

	if _, writeErr := c.Writer.Write(body); writeErr != nil {
		logUpstreamError("writing buffered body to client", writeErr)
		return nil
	}

	return extractTokenUsage(body)
}
