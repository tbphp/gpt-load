package utils

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestSetupLogger(t *testing.T) {
	originalLevel := logrus.GetLevel()
	originalFormatter := logrus.StandardLogger().Formatter
	originalOutput := logrus.StandardLogger().Out
	t.Cleanup(func() {
		logrus.SetLevel(originalLevel)
		logrus.SetFormatter(originalFormatter)
		logrus.SetOutput(originalOutput)
	})
	SetupLogger(LogConfig{Level: "debug", Format: "json"})

	if got := logrus.GetLevel(); got != logrus.DebugLevel {
		t.Fatalf("log level = %s, want %s", got, logrus.DebugLevel)
	}
	if _, ok := logrus.StandardLogger().Formatter.(*logrus.JSONFormatter); !ok {
		t.Fatalf("formatter = %T, want *logrus.JSONFormatter", logrus.StandardLogger().Formatter)
	}
	if got := logrus.StandardLogger().Out; got != os.Stdout {
		t.Fatalf("output = %T, want os.Stdout", got)
	}
}

func TestSetupLoggerFallsBackToInfoLevel(t *testing.T) {
	originalLevel := logrus.GetLevel()
	originalFormatter := logrus.StandardLogger().Formatter
	originalOutput := logrus.StandardLogger().Out
	t.Cleanup(func() {
		logrus.SetLevel(originalLevel)
		logrus.SetFormatter(originalFormatter)
		logrus.SetOutput(originalOutput)
	})
	SetupLogger(LogConfig{Level: "invalid", Format: "text"})

	if got := logrus.GetLevel(); got != logrus.InfoLevel {
		t.Fatalf("log level = %s, want %s", got, logrus.InfoLevel)
	}
	if _, ok := logrus.StandardLogger().Formatter.(*compactTextFormatter); !ok {
		t.Fatalf("formatter = %T, want *compactTextFormatter", logrus.StandardLogger().Formatter)
	}
	if got := logrus.StandardLogger().Out; got != os.Stdout {
		t.Fatalf("output = %T, want os.Stdout", got)
	}
}

func TestSetupLoggerTextFormatKeepsFieldsCompact(t *testing.T) {
	originalLevel := logrus.GetLevel()
	originalFormatter := logrus.StandardLogger().Formatter
	originalOutput := logrus.StandardLogger().Out
	t.Cleanup(func() {
		logrus.SetLevel(originalLevel)
		logrus.SetFormatter(originalFormatter)
		logrus.SetOutput(originalOutput)
	})

	SetupLogger(LogConfig{Level: "info", Format: "text"})
	formatter, ok := logrus.StandardLogger().Formatter.(*compactTextFormatter)
	if !ok {
		t.Fatalf("formatter = %T, want *compactTextFormatter", logrus.StandardLogger().Formatter)
	}
	formatter.textFormatter.ForceColors = true

	entry := logrus.NewEntry(logrus.StandardLogger())
	entry.Time = time.Date(2026, time.August, 5, 19, 18, 11, 0, time.FixedZone("CST", 8*60*60))
	entry.Level = logrus.InfoLevel
	entry.Message = "[DATA] Request completed"
	entry.Data["access_key_id"] = 2
	formatted, err := formatter.Format(entry)
	if err != nil {
		t.Fatalf("format info log entry: %v", err)
	}
	text := string(formatted)
	if !strings.Contains(text, "\x1b[36mINFO\x1b[0m") {
		t.Fatalf("formatted info log has no info color: %q", text)
	}
	if !strings.Contains(text, "[DATA] Request completed \x1b[36maccess_key_id\x1b[0m=2") {
		t.Fatalf("formatted log = %q, want adjacent message and field", text)
	}
	if strings.Contains(text, strings.Repeat(" ", 20)) {
		t.Fatalf("formatted log contains unexpected padding: %q", text)
	}

	for _, test := range []struct {
		name  string
		level logrus.Level
		color string
	}{
		{name: "warning", level: logrus.WarnLevel, color: "\x1b[33mWARN\x1b[0m"},
		{name: "error", level: logrus.ErrorLevel, color: "\x1b[31mERRO\x1b[0m"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry.Level = test.level
			formatted, err := formatter.Format(entry)
			if err != nil {
				t.Fatalf("format %s log entry: %v", test.name, err)
			}
			if !strings.Contains(string(formatted), test.color) {
				t.Fatalf("formatted %s log = %q, want color %q", test.name, formatted, test.color)
			}
		})
	}
}
