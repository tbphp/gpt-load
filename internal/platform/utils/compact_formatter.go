package utils

import (
	"bytes"
	"sort"

	"github.com/sirupsen/logrus"
)

const compactLogTimestampFormat = "2006-01-02 15:04:05"

// compactTextFormatter 保留 logrus 的级别着色和字段格式，只移除彩色文本模式的固定消息填充。
type compactTextFormatter struct {
	textFormatter logrus.TextFormatter
}

func newCompactTextFormatter() *compactTextFormatter {
	return &compactTextFormatter{
		textFormatter: logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: compactLogTimestampFormat,
			SortingFunc:     sortCompactLogFields,
		},
	}
}

var compactLogFieldPriority = map[string]int{
	// logrus 固定字段必须保持在日志头部。
	"time":  -600,
	"level": -500,
	"msg":   -400,
	"error": -300,
	"func":  -200,
	"file":  -100,

	// 请求完成日志按事件、结果、请求、性能、路由和诊断信息分组。
	"event":      0,
	"status":     10,
	"http":       20,
	"proto":      30,
	"model":      40,
	"up_model":   50,
	"duration":   60,
	"in_tokens":  70,
	"out_tokens": 80,
	"cost_usd":   90,
	"ak_id":      100,
	"group":      110,
	"kid":        120,
	"attempts":   130,
	"usage":      140,
	"cost_state": 150,
	"err":        160,
	"err_msg":    170,
}

func sortCompactLogFields(keys []string) {
	sort.SliceStable(keys, func(left, right int) bool {
		leftPriority, leftKnown := compactLogFieldPriority[keys[left]]
		rightPriority, rightKnown := compactLogFieldPriority[keys[right]]
		if leftKnown && rightKnown && leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		if leftKnown != rightKnown {
			return leftKnown
		}
		return keys[left] < keys[right]
	})
}

func (formatter *compactTextFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	formatted, err := formatter.textFormatter.Format(entry)
	if err != nil {
		return nil, err
	}
	return removeTextMessagePadding(formatted, entry.Message), nil
}

func removeTextMessagePadding(formatted []byte, message string) []byte {
	if message == "" {
		return formatted
	}

	messageStart := bytes.Index(formatted, []byte(message))
	if messageStart < 0 {
		return formatted
	}
	paddingStart := messageStart + len(message)
	paddingEnd := paddingStart
	for paddingEnd < len(formatted) && formatted[paddingEnd] == ' ' {
		paddingEnd++
	}
	if paddingEnd == paddingStart {
		return formatted
	}

	keepPadding := 0
	if paddingEnd < len(formatted) && bytes.HasPrefix(formatted[paddingEnd:], []byte("\x1b[")) {
		keepPadding = 1
	}
	compacted := make([]byte, 0, len(formatted)-(paddingEnd-paddingStart)+keepPadding)
	compacted = append(compacted, formatted[:paddingStart]...)
	if keepPadding == 1 {
		compacted = append(compacted, ' ')
	}
	compacted = append(compacted, formatted[paddingEnd:]...)
	return compacted
}
