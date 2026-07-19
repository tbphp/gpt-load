package utils

import (
	"os"
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
	if _, ok := logrus.StandardLogger().Formatter.(*logrus.TextFormatter); !ok {
		t.Fatalf("formatter = %T, want *logrus.TextFormatter", logrus.StandardLogger().Formatter)
	}
	if got := logrus.StandardLogger().Out; got != os.Stdout {
		t.Fatalf("output = %T, want os.Stdout", got)
	}
}
