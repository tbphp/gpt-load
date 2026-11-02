package embedded

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
)

func TestMuseExecutorPreservesEncodedSSEAndTerminalState(t *testing.T) {
	fixture := httptest.NewRecorder()
	writeMuseTestStream(fixture)
	for _, representation := range []string{"gzip", "cr", "gzip_cr"} {
		for _, terminal := range []string{"completed", "incomplete", "missing"} {
			for _, format := range []string{"openai-response", "openai", "claude", "gemini"} {
				t.Run(representation+"/"+terminal+"/"+format, func(t *testing.T) {
					wire := fixture.Body.String()
					switch terminal {
					case "incomplete":
						wire = strings.ReplaceAll(wire, "response.completed", "response.incomplete")
					case "missing":
						wire = strings.ReplaceAll(wire, "event: response.completed\ndata: "+museCompleted+"\n\n", "")
					}
					if strings.Contains(representation, "cr") {
						wire = strings.ReplaceAll(wire, "\n", "\r")
					}
					compressed := strings.HasPrefix(representation, "gzip")
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "text/event-stream")
						if compressed {
							if r.Header.Get("Accept-Encoding") != "gzip" {
								t.Error("missing configured gzip negotiation")
							}
							w.Header().Set("Content-Encoding", "gzip")
							writer := gzip.NewWriter(w)
							if _, err := writer.Write([]byte(wire)); err != nil {
								t.Error(err)
							}
							if err := writer.Close(); err != nil {
								t.Error(err)
							}
							return
						}
						if _, err := w.Write([]byte(wire)); err != nil {
							t.Error(err)
						}
					}))
					defer server.Close()
					executor := NewMuseHTTPExecutor().(*museHTTPExecutor)
					executor.baseURL = server.URL
					payload := map[string]string{
						"openai-response": `{"model":"muse-spark-1.3","input":"hi"}`,
						"openai":          `{"model":"muse-spark-1.3","messages":[{"role":"user","content":"hi"}]}`,
						"claude":          `{"model":"muse-spark-1.3","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`,
						"gemini":          `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
					}[format]
					request := ExecuteRequest{Model: "muse-spark-1.3", Format: format, Payload: []byte(payload), OriginalRequest: []byte(payload)}
					if compressed {
						request.Headers = http.Header{"Accept-Encoding": {"gzip"}}
						request.ConfiguredHeaders = []string{"Accept-Encoding"}
					}
					observed := &strings.Builder{}
					ctx := WithStreamBodyObserver(t.Context(), func(body io.ReadCloser, header http.Header) io.ReadCloser {
						if header.Get("Content-Encoding") != "" || header.Get("Content-Length") != "" {
							t.Error("decoded body retained wire encoding or length")
						}
						return &museObservedTestBody{ReadCloser: body, output: observed}
					})
					response, err := executor.ExecuteStreamCanonical(ctx, "account", testMuseExecutionCredential(), request)
					if err != nil {
						t.Fatal(err)
					}
					var output strings.Builder
					incomplete, failed := false, false
					for chunk := range response.Chunks {
						output.Write(chunk.Payload)
						incomplete = incomplete || chunk.Incomplete
						failed = failed || chunk.Err != nil
					}
					if failed != (terminal == "missing") || incomplete != (terminal == "incomplete") {
						t.Errorf("terminal=%s incomplete=%t failed=%t", terminal, incomplete, failed)
					}
					if !strings.Contains(output.String(), `"ok"`) || strings.Contains(output.String(), "private-tier") {
						t.Errorf("answer lost or subscription event leaked: %q", output.String())
					}
					if !strings.Contains(observed.String(), "response.output_text.delta") || strings.Contains(observed.String(), "\r") {
						t.Error("parent observer did not receive the decoded, normalized SSE")
					}
				})
			}
		}
	}
}

type museObservedTestBody struct {
	io.ReadCloser
	output *strings.Builder
}

func (body *museObservedTestBody) Read(buffer []byte) (int, error) {
	n, err := body.ReadCloser.Read(buffer)
	body.output.Write(buffer[:n])
	return n, err
}

func TestMuseResponseNormalizationPreservesSplitLinesErrorsAndClose(t *testing.T) {
	readErr, closeErr := errors.New("stream reset"), errors.New("close failed")
	for _, test := range []struct{ name, contentType, input, want string }{
		{"mixed SSE lines", "text/event-stream; charset=utf-8", "event: message\r\ndata: {\"text\":\"中文\\r\\n\"}\r\rdata: next\n\ndata: last\r\n", "event: message\ndata: {\"text\":\"中文\\r\\n\"}\n\ndata: next\n\ndata: last\n"},
		{"JSON is unchanged", "application/json", "{\r\n\"ok\":true\r\n}\r", "{\r\n\"ok\":true\r\n}\r"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &museResponseTestBody{
				Reader:   iotest.DataErrReader(io.MultiReader(iotest.OneByteReader(strings.NewReader(test.input)), iotest.ErrReader(readErr))),
				closeErr: closeErr,
			}
			response := &http.Response{Body: body, Header: http.Header{"Content-Type": {test.contentType}}}
			if err := normalizeMuseResponse(response); err != nil {
				t.Fatal(err)
			}
			output, err := io.ReadAll(response.Body)
			if string(output) != test.want || !errors.Is(err, readErr) {
				t.Fatalf("normalized output=%q error=%v", output, err)
			}
			if err := response.Body.Close(); !errors.Is(err, closeErr) || body.closed != 1 {
				t.Fatalf("close error=%v count=%d", err, body.closed)
			}
		})
	}
}

func TestMuseResponseDecoderClosesBodyOnMalformedEncoding(t *testing.T) {
	body := &museResponseTestBody{Reader: strings.NewReader("invalid gzip")}
	response := &http.Response{Body: body, Header: http.Header{"Content-Type": {"text/event-stream"}, "Content-Encoding": {"gzip"}}}
	if err := normalizeMuseResponse(response); err == nil || body.closed != 1 {
		t.Fatalf("decode error=%v close count=%d", err, body.closed)
	}
}

type museResponseTestBody struct {
	io.Reader
	closed   int
	closeErr error
}

func (body *museResponseTestBody) Close() error {
	body.closed++
	return body.closeErr
}
