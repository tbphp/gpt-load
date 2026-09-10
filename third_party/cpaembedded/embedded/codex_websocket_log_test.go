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

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

type codexWSLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *codexWSLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
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
			var logs codexWSLogBuffer
			logrus.SetOutput(&logs)
			const marker = "private-close-reason-marker"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
				if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, marker)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			session := wsTestSession(t, server.URL)
			_, err := session.ExecuteTurn(context.Background(), json.RawMessage(`{"model":"gpt-5","input":"hello"}`), nil)
			if err == nil {
				t.Fatal("upstream close must fail the turn")
			}
			if strings.Contains(logs.String(), marker) {
				t.Fatal("upstream close reason leaked into default logs")
			}
			if !strings.Contains(logs.String(), fmt.Sprintf("ws_close_code=%d", code)) || !strings.Contains(logs.String(), "error_class=websocket_closed") {
				t.Fatalf("missing safe close diagnostics: %s", logs.String())
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
