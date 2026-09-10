package embedded

import (
	"strconv"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

const codexWSSessionIDPrefix = "gptload-codex-ws-"

var codexWSLogHookOnce sync.Once

func installCodexWSLogHook() {
	codexWSLogHookOnce.Do(func() { logrus.AddHook(codexWSLogHook{}) })
}

// CPA v7.2.151 在通知 lifecycle 前将 CloseError 正文写入默认 logger。
// 仅处理本封装会话的该条日志，不修改全局级别、输出或其他执行器的日志。
type codexWSLogHook struct{}

func (codexWSLogHook) Levels() []logrus.Level { return []logrus.Level{logrus.InfoLevel} }

func (codexWSLogHook) Fire(entry *logrus.Entry) error {
	if !strings.HasPrefix(entry.Message, "codex websockets: upstream disconnected session="+codexWSSessionIDPrefix) {
		return nil
	}
	message, rawError, found := strings.Cut(entry.Message, " err=")
	if !found {
		return nil
	}
	entry.Message = message
	entry.Data["error_class"] = "upstream_error"
	// 只提取 Gorilla CloseError 的数值关闭码；未知格式也不保留错误正文。
	if detail, ok := strings.CutPrefix(rawError, "websocket: close "); ok {
		if end := strings.IndexAny(detail, " :"); end >= 0 {
			detail = detail[:end]
		}
		if code, err := strconv.Atoi(detail); err == nil && code >= 1000 && code < 5000 {
			entry.Data["ws_close_code"] = code
			entry.Data["error_class"] = "websocket_closed"
		}
	}
	return nil
}
