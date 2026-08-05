package utils

import (
	"bytes"

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
		},
	}
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
