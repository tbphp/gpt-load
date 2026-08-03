package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/contentcoding"
)

type plaintextResponseWriter struct {
	gin.ResponseWriter
	header      http.Header
	status      int
	size        int
	wroteHeader bool
	committed   bool
	streaming   bool
	body        bytes.Buffer
}

func newPlaintextResponseWriter(base gin.ResponseWriter) *plaintextResponseWriter {
	header := make(http.Header)
	if base != nil {
		header = base.Header().Clone()
		for name := range base.Header() {
			base.Header().Del(name)
		}
	}
	return &plaintextResponseWriter{ResponseWriter: base, header: header, status: http.StatusOK}
}

func (writer *plaintextResponseWriter) Header() http.Header {
	return writer.header
}

func (writer *plaintextResponseWriter) WriteHeader(statusCode int) {
	if writer.wroteHeader || writer.committed {
		return
	}
	writer.status = statusCode
	writer.wroteHeader = true
}

func (writer *plaintextResponseWriter) WriteHeaderNow() {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
}

func (writer *plaintextResponseWriter) Write(value []byte) (int, error) {
	if writer.committed {
		written, err := writer.ResponseWriter.Write(value)
		writer.size += written
		return written, err
	}
	writer.WriteHeaderNow()
	written, err := writer.body.Write(value)
	writer.size += written
	return written, err
}

func (writer *plaintextResponseWriter) WriteString(value string) (int, error) {
	return writer.Write([]byte(value))
}

func (writer *plaintextResponseWriter) Status() int {
	return writer.status
}

func (writer *plaintextResponseWriter) Size() int {
	return writer.size
}

func (writer *plaintextResponseWriter) Written() bool {
	return writer.wroteHeader || writer.committed || writer.body.Len() > 0
}

func (writer *plaintextResponseWriter) Flush() {
	_ = writer.FlushError()
}

func (writer *plaintextResponseWriter) FlushError() error {
	if writer.committed {
		return http.NewResponseController(writer.ResponseWriter).Flush()
	}
	writer.WriteHeaderNow()
	if isEventStreamContentType(writer.header.Get("Content-Type")) {
		encoding, err := contentcoding.ParseContentEncoding(writer.header.Values("Content-Encoding"))
		if err != nil || encoding != contentcoding.EncodingIdentity {
			if err == nil {
				err = fmt.Errorf("%w: compressed event stream", contentcoding.ErrUnsupportedEncoding)
			}
			return err
		}
		normalizePlainStreamingResponseHeaders(writer.header)
		writer.streaming = true
		return writer.commit(writer.body.Bytes())
	}

	encoding, err := contentcoding.ParseContentEncoding(writer.header.Values("Content-Encoding"))
	if err != nil {
		return err
	}
	plain, err := contentcoding.DecodeBytesLimited(
		encoding,
		writer.body.Bytes(),
		maxNonStreamingResponseBodyBytes,
	)
	if err != nil {
		return err
	}
	if encoding == contentcoding.EncodingIdentity {
		writer.header.Del("Content-Encoding")
		writer.header.Del("Content-Length")
		writer.header.Set("Content-Length", strconv.Itoa(len(plain)))
	} else {
		rebuildPlainBufferedResponseHeaders(writer.header, len(plain))
	}
	return writer.commit(plain)
}

func (writer *plaintextResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *plaintextResponseWriter) commit(body []byte) error {
	if writer.committed {
		return nil
	}
	if writer.ResponseWriter == nil {
		return fmt.Errorf("downstream response writer is required")
	}
	for name := range writer.ResponseWriter.Header() {
		writer.ResponseWriter.Header().Del(name)
	}
	for name, values := range writer.header {
		for _, value := range values {
			writer.ResponseWriter.Header().Add(name, value)
		}
	}
	writer.ResponseWriter.WriteHeader(writer.status)
	writer.ResponseWriter.WriteHeaderNow()
	if len(body) > 0 {
		written, err := writer.ResponseWriter.Write(body)
		if err != nil {
			return err
		}
		if written != len(body) {
			return io.ErrShortWrite
		}
	}
	writer.committed = true
	writer.body.Reset()
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

func isEventStreamContentType(value string) bool {
	mediaType := strings.TrimSpace(strings.SplitN(value, ";", 2)[0])
	return strings.EqualFold(mediaType, "text/event-stream")
}

func (handler *Handler) normalizeDownstreamContentCoding() gin.HandlerFunc {
	return func(context *gin.Context) {
		wrapped := newPlaintextResponseWriter(context.Writer)
		context.Writer = wrapped
		context.Next()
		if wrapped.Written() && !wrapped.committed {
			if err := wrapped.FlushError(); err != nil {
				_ = context.Error(err)
			}
		}
	}
}
