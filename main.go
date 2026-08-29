// Package main provides the GPT-Load 2.0 process entry point.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

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
	case "service":
		return dispatchServiceCommand(args[1:], stdout, stderr)
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
	fmt.Fprintln(output, "  gpt-load                    Start the gateway")
	fmt.Fprintln(output, "  gpt-load help               Display this help message")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Windows Service Commands:")
	fmt.Fprintln(output, "  gpt-load service start      Start the Windows service")
	fmt.Fprintln(output, "  gpt-load service stop       Stop the Windows service")
	fmt.Fprintln(output, "  gpt-load service restart    Restart the Windows service")
	fmt.Fprintln(output, "  gpt-load service status     Display the Windows service status")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Deferred Commands:")
	fmt.Fprintln(output, "  migrate-keys      Key rotation support will be available in a later release")
}

func runServer() error {
	runtime, err := buildManagedRuntime()
	if err != nil {
		return err
	}
	quit := make(chan os.Signal, 2)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	if err := runtime.start(); err != nil {
		return err
	}

	var serveErr error
	var firstSignal os.Signal
	select {
	case firstSignal = <-quit:
		logrus.WithFields(logrus.Fields{
			"event":  "shutdown.requested",
			"signal": firstSignal.String(),
		}).Info("shutdown requested by signal")
	case serveErr = <-runtime.serveErrors():
	}

	force := make(chan struct{})
	stopResult := make(chan error, 1)
	go func() { stopResult <- runtime.stop(force) }()
	forceReason := "signal_during_shutdown"
	if firstSignal != nil {
		forceReason = "second_signal"
	}
	select {
	case secondSignal := <-quit:
		logrus.WithFields(logrus.Fields{
			"event":  "shutdown.force",
			"signal": secondSignal.String(),
			"reason": forceReason,
		}).Warn("additional shutdown signal received; forcing shutdown")
		close(force)
		if err := <-stopResult; err != nil {
			return errors.Join(serveErr, fmt.Errorf("stop application: %w", err))
		}
		return serveErr
	case err := <-stopResult:
		if err != nil {
			return errors.Join(serveErr, fmt.Errorf("stop application: %w", err))
		}
		return serveErr
	}
}
