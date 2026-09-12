package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

type codexWSLogBuffer struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	updated chan struct{}
}

func (b *codexWSLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.buf.Write(p)
	select {
	case b.updated <- struct{}{}:
	default:
	}
	return n, err
}

func (b *codexWSLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestCodexWSSessionRedactsCloseReason(t *testing.T) {
	// 仅非并行测试捕获默认日志；生产封装不修改全局输出或日志级别。
	output := logrus.StandardLogger().Out
	defer logrus.SetOutput(output)
	for _, code := range []int{websocket.ClosePolicyViolation, 4001} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			logs := codexWSLogBuffer{updated: make(chan struct{}, 1)}
			logrus.SetOutput(&logs)
			const marker = "private-close-reason-marker"
			headersReady := make(chan struct{})
			releaseClose := sync.OnceFunc(func() { close(headersReady) })
			defer releaseClose()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{
					"X-Request-Id": {"close-reason-redaction-test"},
				})
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				<-headersReady
				if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, marker)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			session := wsTestSession(t, server.URL)
			headersObserved := false
			session.options.ObserveHeaders = func(http.Header, time.Time) {
				headersObserved = true
				releaseClose()
				// SDK 先交付错误再记录断连；若封装先清理连接，SDK 会跳过日志。
				// 在握手交接处控制这个合法时序，确保本用例实际经过日志脱敏路径。
				timeout := time.NewTimer(time.Second)
				defer timeout.Stop()
				for !strings.Contains(logs.String(), "upstream disconnected session="+session.id+" ") {
					select {
					case <-logs.updated:
					case <-timeout.C:
						t.Errorf("missing SDK close diagnostics before cleanup: %s", logs.String())
						return
					}
				}
			}
			_, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
			if err == nil {
				t.Fatal("upstream close must fail the turn")
			}
			if !headersObserved {
				t.Fatal("handshake callback did not exercise the controlled log ordering")
			}
			output := logs.String()
			if strings.Contains(output, marker) {
				t.Fatal("upstream close reason leaked into default logs")
			}
			if !strings.Contains(output, fmt.Sprintf("ws_close_code=%d", code)) || !strings.Contains(output, "error_class=websocket_closed") {
				t.Fatalf("missing safe close diagnostics: %s", output)
			}
		})
	}
}

func TestCodexWSLogHookScope(t *testing.T) {
	owned := "codex websockets: upstream disconnected session=" + codexWSSessionIDPrefix + "test auth=test url=ws://test reason=read_error"
	for _, test := range []struct {
		name    string
		message string
		want    string
	}{
		{"unknown error", owned + " err=private upstream error", owned},
		{"no error", owned, owned},
		{"other session", "codex websockets: upstream disconnected session=other err=retained", "codex websockets: upstream disconnected session=other err=retained"},
		{"application log", "application err=retained", "application err=retained"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := logrus.New()
			logger.SetOutput(&output)
			logger.SetFormatter(&logrus.JSONFormatter{})
			logger.AddHook(codexWSLogHook{})
			logger.Info(test.message)
			var entry struct {
				Message    string `json:"msg"`
				ErrorClass string `json:"error_class"`
			}
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if entry.Message != test.want || (entry.ErrorClass == "upstream_error") != (test.name == "unknown error") {
				t.Fatalf("unexpected log: %s", output.String())
			}
		})
	}
}
