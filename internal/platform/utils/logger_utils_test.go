package utils

import (
	"io"
	"testing"

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
	logrus.SetOutput(io.Discard)

	SetupLogger(LogConfig{Level: "debug", Format: "json"})

	if got := logrus.GetLevel(); got != logrus.DebugLevel {
		t.Fatalf("log level = %s, want %s", got, logrus.DebugLevel)
	}
	if _, ok := logrus.StandardLogger().Formatter.(*logrus.JSONFormatter); !ok {
		t.Fatalf("formatter = %T, want *logrus.JSONFormatter", logrus.StandardLogger().Formatter)
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
	logrus.SetOutput(io.Discard)

	SetupLogger(LogConfig{Level: "invalid", Format: "text"})

	if got := logrus.GetLevel(); got != logrus.InfoLevel {
		t.Fatalf("log level = %s, want %s", got, logrus.InfoLevel)
	}
	if _, ok := logrus.StandardLogger().Formatter.(*logrus.TextFormatter); !ok {
		t.Fatalf("formatter = %T, want *logrus.TextFormatter", logrus.StandardLogger().Formatter)
	}
}
