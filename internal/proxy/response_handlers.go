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

// handleNormalResponse buffers the upstream response body, attempts to extract
// token usage metadata, writes the original body to the client, and returns
// the parsed usage (or nil when the body is not a JSON chat-completion response).
func (ps *ProxyServer) handleNormalResponse(c *gin.Context, resp *http.Response) *TokenUsage {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logUpstreamError("reading response body", err)
		if _, writeErr := c.Writer.Write(body); writeErr != nil {
			logUpstreamError("writing buffered body to client", writeErr)
		}
		return nil
	}

	// Check for gzip encoding
	body = handleGzipCompression(resp, body)

	if _, writeErr := c.Writer.Write(body); writeErr != nil {
		logUpstreamError("writing buffered body to client", writeErr)
		return nil
	}

	// Only parse usage for successful chat-completion-like responses.
	if resp.StatusCode < 400 && isChatCompletionPath(c.Request.URL.Path) {
		if usage := extractTokenUsage(body); usage != nil {
			return usage
		}
	}

	return nil
}
