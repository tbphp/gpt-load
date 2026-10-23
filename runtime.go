package main

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/app"
	"gpt-load/internal/container"
	"gpt-load/internal/gateway"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/memlimit"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
)

type managedApplication interface {
	Start() error
	Stop(context.Context) error
	ServeErrors() <-chan error
}

type managedRuntime struct {
	application     managedApplication
	shutdownTimeout time.Duration
}

func buildManagedRuntime() (*managedRuntime, error) {
	dependencyContainer, err := container.BuildContainer()
	if err != nil {
		return nil, fmt.Errorf("build dependency container: %w", err)
	}

	if err := dependencyContainer.Invoke(func(cfg *config.Config, runtimeRedactor *redact.Redactor) {
		utils.SetupLogger(utils.LogConfig{
			Level:  cfg.Log.Level,
			Format: cfg.Log.Format,
		})
		logrus.AddHook(redact.NewHook(runtimeRedactor))
	}); err != nil {
		return nil, fmt.Errorf("configure logger: %w", err)
	}

	if err := dependencyContainer.Invoke(func(memory memlimit.Memory, gatewayHandler *gateway.Handler) {
		goMemoryLimit, goMemoryLimitSource := memlimit.ApplyGoMemoryLimit(memory)
		fields := logrus.Fields{
			"event":                    "startup.memory_limits",
			"detected_memory_bytes":    memory.Available(),
			"go_memory_limit_source":   goMemoryLimitSource,
			"request_body_limit_bytes": gatewayHandler.RequestBodyLimit(),
		}
		if goMemoryLimit < math.MaxInt64 {
			fields["go_memory_limit_bytes"] = goMemoryLimit
		}
		logrus.WithFields(fields).Info("memory limits configured")
	}); err != nil {
		return nil, fmt.Errorf("configure memory limits: %w", err)
	}

	var application *app.App
	var cfg *config.Config
	if err := dependencyContainer.Invoke(func(resolvedApp *app.App, resolvedConfig *config.Config) {
		application = resolvedApp
		cfg = resolvedConfig
	}); err != nil {
		return nil, fmt.Errorf("resolve application: %w", err)
	}

	return &managedRuntime{
		application:     application,
		shutdownTimeout: time.Duration(cfg.Server.GracefulShutdownTimeout) * time.Second,
	}, nil
}

func (runtime *managedRuntime) start() error {
	if err := runtime.application.Start(); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.application.Stop(cleanupCtx)
		return fmt.Errorf("start application: %w", err)
	}
	return nil
}

func (runtime *managedRuntime) serveErrors() <-chan error {
	return runtime.application.ServeErrors()
}

func (runtime *managedRuntime) stop(force <-chan struct{}) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), runtime.shutdownTimeout)
	defer cancel()

	stopResult := make(chan error, 1)
	go func() { stopResult <- runtime.application.Stop(shutdownCtx) }()
	if force == nil {
		return <-stopResult
	}

	select {
	case err := <-stopResult:
		return err
	case <-force:
		cancel()
		return <-stopResult
	}
}
