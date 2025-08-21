package proxy

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func (ps *ProxyServer) handleStreamingResponse(c *gin.Context, resp *http.Response, needsLogging bool) []byte {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		logrus.Error("Streaming unsupported by the writer, falling back to normal response")
		return ps.handleNormalResponse(c, resp, needsLogging)
	}

	var responseBytes []byte
	buf := make([]byte, 4*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// Write to client
			if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
				logUpstreamError("writing stream to client", writeErr)
				return responseBytes
			}
			// Also capture for logging
			if needsLogging {
				responseBytes = append(responseBytes, buf[:n]...)
			}
			flusher.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			logUpstreamError("reading from upstream", err)
			return responseBytes
		}
	}
	return responseBytes
}

func (ps *ProxyServer) handleNormalResponse(c *gin.Context, resp *http.Response, needsLogging bool) []byte {
	if !needsLogging {
		if _, err := io.Copy(c.Writer, resp.Body); err != nil {
			logUpstreamError("copying response body", err)
		}
		return nil
	}

	// Read the response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logUpstreamError("reading response body", err)
		return nil
	}

	// Write to client
	if _, err := c.Writer.Write(responseBody); err != nil {
		logUpstreamError("copying response body", err)
	}

	return responseBody
}
