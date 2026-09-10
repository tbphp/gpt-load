package main

import (
	"bytes"
	"maps"
	"strings"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/subscription/providers/codex"
)

type runtimeLogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (capture *runtimeLogCapture) Write(body []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.buf.Write(body)
}

func (capture *runtimeLogCapture) String() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.buf.String()
}

func (*runtimeLogCapture) Levels() []logrus.Level { return logrus.AllLevels }

func (capture *runtimeLogCapture) Fire(entry *logrus.Entry) error {
	// 与 Windows Event Log 一样，在 Hook 内格式化并写入，而非等待最终输出。
	formatted, err := entry.Logger.Formatter.Format(entry)
	if err != nil {
		return err
	}
	_, err = capture.Write(formatted)
	return err
}

func TestCodexWSRedactionPrecedesRuntimeLogHooks(t *testing.T) {
	// 非并行测试保留包初始化安装的 Hook，只临时追加运行时日志链。
	logger := logrus.StandardLogger()
	originalOutput, originalHooks := logger.Out, maps.Clone(logger.Hooks)
	t.Cleanup(func() {
		logger.SetOutput(originalOutput)
		logger.ReplaceHooks(originalHooks)
	})
	var output, sink runtimeLogCapture
	logger.SetOutput(&output)
	logger.AddHook(redact.NewHook(redact.New()))
	logger.AddHook(&sink)

	// 复现服务启动后才首次创建 Session 的顺序；创建句柄不会建立连接。
	session, err := codex.NewWSSession(codex.WSSessionOptions{
		CredentialID: "test", Credential: codex.Credential{
			Type: codex.Provider, AccessToken: "test-access", RefreshToken: "test-refresh", AccountID: "test-account",
		}, ProxyURL: "direct",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	const marker = "private-close-reason-marker"
	// 使用固定 SDK 的断连日志格式，验证桥接封装与项目日志链的组合。
	logger.Infof("codex websockets: upstream disconnected session=gptload-codex-ws-log-order-test auth=test url=wss://example.test/responses reason=read_error err=websocket: close 1008 (policy violation): %s", marker)
	for name, capture := range map[string]*runtimeLogCapture{"output": &output, "sink": &sink} {
		message := capture.String()
		if strings.Contains(message, marker) {
			t.Errorf("close reason leaked into %s", name)
		}
		if !strings.Contains(message, "session=[REDACTED]") ||
			!strings.Contains(message, "ws_close_code=1008") ||
			!strings.Contains(message, "error_class=websocket_closed") {
			t.Errorf("missing redacted session or safe close diagnostics in %s", name)
		}
	}
}
