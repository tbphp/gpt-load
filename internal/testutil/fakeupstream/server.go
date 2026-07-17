// Package fakeupstream 提供可脚本化的三方言 AI HTTP 上游，供网关测试使用。
package fakeupstream

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed testdata/*/*
var fixtureFS embed.FS

// Step 描述按请求顺序消费的一次响应。
type Step struct {
	Status   int
	Fixture  string
	Stream   bool
	Delay    time.Duration
	DropConn bool
	Headers  http.Header
}

// Request 是收到的上游请求快照。
type Request struct {
	Method   string
	Path     string
	RawQuery string
	Headers  http.Header
	Body     []byte
}

// Server 封装 httptest.Server，并记录请求与脚本消费位置。
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	steps    []Step
	nextStep int
	requests []Request
}

// New 启动一个 fake upstream。有效请求按传入顺序各消费一个 Step。
func New(steps ...Step) *Server {
	server := &Server{steps: cloneSteps(steps)}
	server.Server = httptest.NewServer(server)
	return server
}

// Requests 返回所有已收到请求的独立快照。
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()

	requests := make([]Request, len(s.requests))
	for i, request := range s.requests {
		requests[i] = cloneRequest(request)
	}
	return requests
}

// ServeHTTP 实现三方言路由，并执行下一步脚本。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	dialect, allowedMethod, ok := routeForPath(r.URL.Path)
	if !ok {
		s.recordRequest(r, body)
		http.NotFound(w, r)
		return
	}
	if r.Method != allowedMethod {
		s.recordRequest(r, body)
		w.Header().Set("Allow", allowedMethod)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	step, ok := s.recordRequestAndConsumeStep(r, body)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "fake upstream script exhausted")
		return
	}

	if step.Delay > 0 && !waitForDelay(r, step.Delay) {
		return
	}
	if step.DropConn {
		dropConnection(w)
		return
	}

	fixture, err := readEmbeddedFixture(dialect, step.Fixture)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	copyHeaders(w.Header(), step.Headers)
	if step.Stream {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		}
		if w.Header().Get("Cache-Control") == "" {
			w.Header().Set("Cache-Control", "no-cache")
		}
	} else if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}

	status := step.Status
	if status == 0 {
		status = http.StatusOK
	}
	if status < 100 || status > 999 {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("invalid scripted status: %d", status))
		return
	}
	w.WriteHeader(status)
	if step.Stream {
		writeSSE(w, fixture)
		return
	}
	_, _ = w.Write(fixture)
}

func (s *Server) recordRequest(r *http.Request, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, requestSnapshot(r, body))
}

func (s *Server) recordRequestAndConsumeStep(r *http.Request, body []byte) (Step, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, requestSnapshot(r, body))
	if s.nextStep >= len(s.steps) {
		return Step{}, false
	}
	step := cloneStep(s.steps[s.nextStep])
	s.nextStep++
	return step, true
}

func requestSnapshot(r *http.Request, body []byte) Request {
	return Request{
		Method:   r.Method,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
		Headers:  r.Header.Clone(),
		Body:     bytes.Clone(body),
	}
}

func routeForPath(requestPath string) (dialect, method string, ok bool) {
	if requestPath == "/v1/models" {
		return "openai", http.MethodGet, true
	}
	dialect, ok = dialectForPath(requestPath)
	if !ok {
		return "", "", false
	}
	return dialect, http.MethodPost, true
}

func dialectForPath(requestPath string) (string, bool) {
	switch requestPath {
	case "/v1/chat/completions":
		return "openai", true
	case "/v1/messages":
		return "anthropic", true
	}

	const geminiPrefix = "/v1beta/models/"
	if !strings.HasPrefix(requestPath, geminiPrefix) {
		return "", false
	}
	modelAndMethod := strings.TrimPrefix(requestPath, geminiPrefix)
	for _, suffix := range []string{":generateContent", ":streamGenerateContent"} {
		if model := strings.TrimSuffix(modelAndMethod, suffix); model != modelAndMethod && model != "" {
			return "gemini", true
		}
	}
	return "", false
}

func readEmbeddedFixture(dialect, name string) ([]byte, error) {
	name = filepath.ToSlash(name)
	name = strings.ReplaceAll(name, `\`, "/")
	fixturePrefix := path.Join("testdata", dialect) + "/"
	for strings.HasPrefix(name, fixturePrefix) {
		name = strings.TrimPrefix(name, fixturePrefix)
	}
	if !strings.Contains(name, "/") {
		name = path.Join(dialect, name)
	}
	name = path.Clean(name)
	if name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
		return nil, fmt.Errorf("invalid fixture path: %q", name)
	}

	fixture, err := fixtureFS.ReadFile(path.Join("testdata", name))
	if err != nil {
		return nil, fmt.Errorf("read fixture %q: %w", name, err)
	}
	return fixture, nil
}

func waitForDelay(r *http.Request, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

func dropConnection(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		panic(http.ErrAbortHandler)
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	_ = conn.Close()
}

func writeSSE(w http.ResponseWriter, fixture []byte) {
	flusher, canFlush := w.(http.Flusher)
	for _, event := range splitSSEEvents(fixture) {
		if _, err := w.Write(event); err != nil {
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}
}

func splitSSEEvents(data []byte) [][]byte {
	var events [][]byte
	for len(data) > 0 {
		separatorIndex, separatorLength := nextSSESeparator(data)
		if separatorIndex < 0 {
			if len(bytes.TrimSpace(data)) > 0 {
				events = append(events, bytes.Clone(data))
			}
			break
		}

		end := separatorIndex + separatorLength
		event := data[:end]
		if len(bytes.TrimSpace(event)) > 0 {
			events = append(events, bytes.Clone(event))
		}
		data = data[end:]
	}
	return events
}

func nextSSESeparator(data []byte) (int, int) {
	lineFeedIndex := bytes.Index(data, []byte("\n\n"))
	carriageReturnIndex := bytes.Index(data, []byte("\r\n\r\n"))
	switch {
	case lineFeedIndex < 0:
		return carriageReturnIndex, len("\r\n\r\n")
	case carriageReturnIndex < 0:
		return lineFeedIndex, len("\n\n")
	case carriageReturnIndex < lineFeedIndex:
		return carriageReturnIndex, len("\r\n\r\n")
	default:
		return lineFeedIndex, len("\n\n")
	}
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, message)
}

func cloneSteps(steps []Step) []Step {
	cloned := make([]Step, len(steps))
	for i, step := range steps {
		cloned[i] = cloneStep(step)
	}
	return cloned
}

func cloneStep(step Step) Step {
	step.Headers = step.Headers.Clone()
	return step
}

func cloneRequest(request Request) Request {
	request.Headers = request.Headers.Clone()
	request.Body = bytes.Clone(request.Body)
	return request
}
