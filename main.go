// Package main provides the entry point for the GPT-Load proxy server
package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gpt-load/internal/app"
	"gpt-load/internal/commands"
	"gpt-load/internal/container"
	"gpt-load/internal/types"
	"gpt-load/internal/utils"

	"github.com/sirupsen/logrus"
)

//go:embed web/dist
var buildFS embed.FS

//go:embed web/dist/index.html
var indexPage []byte

func main() {
	if len(os.Args) > 1 {
		runCommand()
	} else {
		runServer()
	}
}

// runCommand dispatches to the appropriate command handler
func runCommand() {
	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "migrate-keys":
		commands.RunMigrateKeys(args)
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		fmt.Println("Run 'gpt-load help' for usage.")
		os.Exit(1)
	}
}

// printHelp displays the general help information
func printHelp() {
	fmt.Println("GPT-Load - Multi-channel AI proxy with intelligent key rotation.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  gpt-load                    Start the proxy server")
	fmt.Println("  gpt-load <command> [args]   Execute a command")
	fmt.Println()
	fmt.Println("Available Commands:")
	fmt.Println("  migrate-keys    Migrate encryption keys")
	fmt.Println("  help            Display this help message")
	fmt.Println()
	fmt.Println("Use 'gpt-load <command> --help' for more information about a command.")
}

// runServer run App Server
func runServer() {
	// Build the dependency injection container
	container, err := container.BuildContainer()
	if err != nil {
		logrus.Fatalf("Failed to build container: %v", err)
	}

	// Provide UI assets to the container
	if err := container.Provide(func() embed.FS { return buildFS }); err != nil {
		logrus.Fatalf("Failed to provide buildFS: %v", err)
	}
	if err := container.Provide(func() []byte { return indexPage }); err != nil {
		logrus.Fatalf("Failed to provide indexPage: %v", err)
	}

	// Initialize global logger
	if err := container.Invoke(func(configManager types.ConfigManager) {
		utils.SetupLogger(configManager)
	}); err != nil {
		logrus.Fatalf("Failed to setup logger: %v", err)
	}

	// Create and run the application
	if err := container.Invoke(func(application *app.App, configManager types.ConfigManager) {
		if err := application.Start(); err != nil {
			logrus.Fatalf("Failed to start application: %v", err)
		}

		// Wait for interrupt signal for graceful shutdown
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit

		// Create a context with timeout for shutdown
		serverConfig := configManager.GetEffectiveServerConfig()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(serverConfig.GracefulShutdownTimeout)*time.Second)
		defer cancel()

		// Perform graceful shutdown
		application.Stop(shutdownCtx)

	}); err != nil {
		logrus.Fatalf("Failed to run application: %v", err)
	}
}
