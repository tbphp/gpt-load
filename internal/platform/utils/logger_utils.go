package utils

import (
	"io"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// LogConfig contains the platform logger settings without depending on application config types.
type LogConfig struct {
	Level      string
	Format     string
	EnableFile bool
	FilePath   string
}

// SetupLogger configures the process-wide logrus logger.
func SetupLogger(config LogConfig) {
	level, err := logrus.ParseLevel(config.Level)
	if err != nil {
		logrus.Warn("Invalid log level, using info")
		level = logrus.InfoLevel
	}
	logrus.SetLevel(level)

	if config.Format == "json" {
		logrus.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: "2006-01-02T15:04:05.000Z07:00",
		})
	} else {
		logrus.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		})
	}

	if !config.EnableFile || config.FilePath == "" {
		return
	}

	logDir := filepath.Dir(config.FilePath)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		logrus.Warnf("Failed to create log directory: %v", err)
		return
	}

	logFile, err := os.OpenFile(config.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		logrus.Warnf("Failed to open log file: %v", err)
		return
	}
	logrus.SetOutput(io.MultiWriter(os.Stdout, logFile))
}
