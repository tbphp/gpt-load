// Package main provides the GPT-Load 2.0 process entry point.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gpt-load/internal/app"
	"gpt-load/internal/container"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/utils"

	"github.com/sirupsen/logrus"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(dispatchCommand(os.Args[1:], os.Stdout, os.Stderr))
	}
	if err := runServer(); err != nil {
		logrus.WithError(err).Error("GPT-Load stopped with an error")
		os.Exit(1)
	}
}

func dispatchCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHelp(stdout)
		return 0
	case "migrate-keys":
		fmt.Fprintln(stderr, "migrate-keys will be available in a later release")
		return 1
	default:
		fmt.Fprintf(stderr, "Unknown command: %s\n", args[0])
		fmt.Fprintln(stderr, "Run 'gpt-load help' for usage.")
		return 1
	}
}

func printHelp(output io.Writer) {
	fmt.Fprintln(output, "GPT-Load - self-hosted AI API key gateway")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  gpt-load          Start the gateway")
	fmt.Fprintln(output, "  gpt-load help     Display this help message")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Deferred Commands:")
	fmt.Fprintln(output, "  migrate-keys      Key rotation support will be available in a later release")
}

func runServer() error {
	dependencyContainer, err := container.BuildContainer()
	if err != nil {
		return fmt.Errorf("build dependency container: %w", err)
	}

	if err := dependencyContainer.Invoke(func(cfg *config.Config) {
		utils.SetupLogger(utils.LogConfig{
			Level:  cfg.Log.Level,
			Format: cfg.Log.Format,
		})
	}); err != nil {
		return fmt.Errorf("configure logger: %w", err)
	}

	var application *app.App
	var cfg *config.Config
	if err := dependencyContainer.Invoke(func(resolvedApp *app.App, resolvedConfig *config.Config) {
		application = resolvedApp
		cfg = resolvedConfig
	}); err != nil {
		return fmt.Errorf("resolve application: %w", err)
	}
	if err := application.Start(); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = application.Stop(cleanupCtx)
		return fmt.Errorf("start application: %w", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	var serveErr error
	select {
	case <-quit:
	case serveErr = <-application.ServeErrors():
	}

	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		time.Duration(cfg.Server.GracefulShutdownTimeout)*time.Second,
	)
	defer cancel()
	if err := application.Stop(shutdownCtx); err != nil {
		return errors.Join(serveErr, fmt.Errorf("stop application: %w", err))
	}
	return serveErr
}
