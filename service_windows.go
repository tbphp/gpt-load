//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/securefile"
)

const (
	windowsServicePendingInterval = 2 * time.Second
	windowsServiceStartWaitHint   = 30 * time.Second
	windowsServiceStopWaitBuffer  = 5 * time.Second
	windowsServiceMinStopWaitHint = 10 * time.Second
)

type windowsServiceHandler struct {
	newRuntime      func() (*managedRuntime, error)
	pendingInterval time.Duration
	reportFailure   func(error)

	mu     sync.Mutex
	runErr error
}

type windowsRuntimeStartResult struct {
	runtime *managedRuntime
	err     error
}

func dispatchServiceCommand(args []string, stdout, stderr io.Writer) int {
	command, err := parseServiceCommand(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	switch command.action {
	case "run":
		err = runWindowsService(command.configDir, command.dataDir)
	case "install":
		err = installWindowsService(command.configDir, command.dataDir)
	case "start":
		err = startWindowsService()
	case "stop":
		err = stopWindowsService()
	case "restart":
		err = restartWindowsService()
	case "status":
		var status string
		status, err = windowsServiceStatus()
		if err == nil {
			fmt.Fprintln(stdout, status)
		}
	case "uninstall":
		err = uninstallWindowsService()
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if command.action != "run" && command.action != "status" {
		fmt.Fprintf(stdout, "Windows service %s completed.\n", command.action)
	}
	return 0
}

func runWindowsService(configDir string, dataDir string) error {
	configDir, dataDir, err := normalizeWindowsServiceDirectories(configDir, dataDir)
	if err != nil {
		return err
	}
	handler := &windowsServiceHandler{
		pendingInterval: windowsServicePendingInterval,
		reportFailure:   reportWindowsServiceFailure,
		newRuntime: func() (*managedRuntime, error) {
			if err := securefile.UseWindowsServiceACL(windowsServiceName); err != nil {
				return nil, fmt.Errorf("configure Windows service ACL: %w", err)
			}
			if err := os.Chdir(configDir); err != nil {
				return nil, fmt.Errorf("change Windows service working directory: %w", err)
			}
			if err := os.Setenv("DATA_DIR", dataDir); err != nil {
				return nil, fmt.Errorf("set Windows service DATA_DIR: %w", err)
			}
			runtime, err := buildManagedRuntime()
			if err != nil {
				return nil, err
			}
			if err := configureWindowsServiceLogging(); err != nil {
				return nil, err
			}
			return runtime, nil
		},
	}
	if err := svc.Run(windowsServiceName, handler); err != nil {
		return fmt.Errorf("run Windows service dispatcher: %w", err)
	}
	return handler.result()
}

func (handler *windowsServiceHandler) Execute(
	_ []string,
	requests <-chan svc.ChangeRequest,
	changes chan<- svc.Status,
) (bool, uint32) {
	interval := handler.pendingInterval
	if interval <= 0 {
		interval = windowsServicePendingInterval
	}
	current := svc.Status{
		State:      svc.StartPending,
		CheckPoint: 1,
		WaitHint:   durationMilliseconds(windowsServiceStartWaitHint),
	}
	changes <- current

	started := make(chan windowsRuntimeStartResult, 1)
	go func() {
		runtime, err := handler.newRuntime()
		if err == nil {
			err = runtime.start()
		}
		started <- windowsRuntimeStartResult{runtime: runtime, err: err}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	stopAfterStart := false
	var runtime *managedRuntime
	for runtime == nil {
		select {
		case result := <-started:
			if result.err != nil {
				handler.fail(result.err)
				return true, 1
			}
			runtime = result.runtime
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- current
			case svc.Stop, svc.Shutdown, svc.PreShutdown:
				stopAfterStart = true
			}
		case <-ticker.C:
			current.CheckPoint++
			changes <- current
		}
	}

	current = svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown,
	}
	changes <- current

	var terminalErr error
	serveErrors := runtime.serveErrors()
	if !stopAfterStart {
		for terminalErr == nil && !stopAfterStart {
			select {
			case request := <-requests:
				switch request.Cmd {
				case svc.Interrogate:
					changes <- current
				case svc.Stop, svc.Shutdown, svc.PreShutdown:
					stopAfterStart = true
				}
			case serveErr, ok := <-serveErrors:
				if !ok {
					serveErrors = nil
					continue
				}
				if serveErr != nil {
					terminalErr = serveErr
				}
			}
		}
	}

	current = svc.Status{
		State:      svc.StopPending,
		CheckPoint: 1,
		WaitHint:   windowsServiceStopWaitHint(runtime.shutdownTimeout),
	}
	changes <- current
	stopped := make(chan error, 1)
	go func() { stopped <- runtime.stop(nil) }()
	for {
		select {
		case stopErr := <-stopped:
			terminalErr = errors.Join(terminalErr, stopErr)
			if terminalErr != nil {
				handler.fail(terminalErr)
				return true, 1
			}
			return false, 0
		case request := <-requests:
			if request.Cmd == svc.Interrogate {
				changes <- current
			}
		case <-ticker.C:
			current.CheckPoint++
			changes <- current
		}
	}
}

func (handler *windowsServiceHandler) fail(err error) {
	handler.mu.Lock()
	handler.runErr = err
	handler.mu.Unlock()
	if handler.reportFailure != nil {
		handler.reportFailure(err)
	}
}

func reportWindowsServiceFailure(err error) {
	message := redact.New().String(err.Error())
	if log, openErr := eventlog.Open(windowsServiceName); openErr == nil {
		_ = log.Error(3, "GPT-Load Windows service failed: "+message)
		_ = log.Close()
	}
}

func (handler *windowsServiceHandler) result() error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.runErr
}

func windowsServiceStopWaitHint(shutdownTimeout time.Duration) uint32 {
	wait := shutdownTimeout + windowsServiceStopWaitBuffer
	if wait < windowsServiceMinStopWaitHint {
		wait = windowsServiceMinStopWaitHint
	}
	return durationMilliseconds(wait)
}

func durationMilliseconds(duration time.Duration) uint32 {
	milliseconds := duration / time.Millisecond
	if milliseconds <= 0 {
		return 1
	}
	if milliseconds > time.Duration(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(milliseconds)
}

type windowsEventLogHook struct {
	log *eventlog.Log
}

func configureWindowsServiceLogging() error {
	log, err := eventlog.Open(windowsServiceName)
	if err != nil {
		return fmt.Errorf("open Windows Event Log: %w", err)
	}
	logrus.SetOutput(io.Discard)
	logrus.AddHook(&windowsEventLogHook{log: log})
	return nil
}

func (hook *windowsEventLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (hook *windowsEventLogHook) Fire(entry *logrus.Entry) error {
	formatted, err := entry.Logger.Formatter.Format(entry)
	if err != nil {
		return err
	}
	message := strings.TrimSpace(string(formatted))
	const maxEventLogMessageBytes = 30_000
	if len(message) > maxEventLogMessageBytes {
		message = message[:maxEventLogMessageBytes]
	}
	switch entry.Level {
	case logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel:
		return hook.log.Error(3, message)
	case logrus.WarnLevel:
		return hook.log.Warning(2, message)
	default:
		return hook.log.Info(1, message)
	}
}
