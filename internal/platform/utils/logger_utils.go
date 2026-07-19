package utils

import (
	"os"

	"github.com/sirupsen/logrus"
)

// LogConfig contains the platform logger settings without depending on application config types.
type LogConfig struct {
	Level  string
	Format string
}

// SetupLogger configures the process-wide logrus logger.
func SetupLogger(config LogConfig) {
	logrus.SetOutput(os.Stdout)

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
}
