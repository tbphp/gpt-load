//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"gpt-load/internal/platform/securefile"
)

const (
	windowsServiceName        = "gpt-load"
	windowsServiceDisplayName = "GPT-Load"
	windowsServiceDescription = "GPT-Load self-hosted AI API gateway"
	windowsServiceAccount     = `NT AUTHORITY\LocalService`
	windowsServicePollDelay   = 200 * time.Millisecond
	windowsServiceWaitTimeout = 2 * time.Minute
)

var errWindowsServiceNotInstalled = errors.New("Windows service is not installed")

func defaultWindowsServiceDirectories() (string, string, error) {
	programData, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", "", fmt.Errorf("resolve ProgramData: %w", err)
	}
	configDir := filepath.Join(programData, "GPT-Load")
	return configDir, filepath.Join(configDir, "data"), nil
}

func normalizeWindowsServiceDirectories(
	configDir string,
	dataDir string,
) (string, string, error) {
	if configDir == "" || dataDir == "" {
		defaultConfigDir, _, err := defaultWindowsServiceDirectories()
		if err != nil {
			return "", "", err
		}
		if configDir == "" {
			configDir = defaultConfigDir
		}
		if dataDir == "" {
			dataDir = filepath.Join(configDir, "data")
		}
	}
	configDir = filepath.Clean(configDir)
	dataDir = filepath.Clean(dataDir)
	if !filepath.IsAbs(configDir) || !filepath.IsAbs(dataDir) {
		return "", "", fmt.Errorf("Windows service directories must be absolute")
	}
	if strings.EqualFold(configDir, dataDir) {
		return "", "", fmt.Errorf("Windows service DATA_DIR must differ from its configuration directory")
	}
	return configDir, dataDir, nil
}

func installWindowsService(configDir string, dataDir string) error {
	configDir, dataDir, err := normalizeWindowsServiceDirectories(configDir, dataDir)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve service executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return fmt.Errorf("resolve absolute service executable: %w", err)
	}
	arguments := []string{
		"service", "run",
		"--config-dir", configDir,
		"--data-dir", dataDir,
	}
	serviceConfig := desiredWindowsServiceConfig(executable, arguments)

	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Windows service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(windowsServiceName)
	created := false
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		service, err = manager.CreateService(
			windowsServiceName,
			executable,
			serviceConfig,
			arguments...,
		)
		created = err == nil
	} else if err == nil {
		err = service.UpdateConfig(serviceConfig)
	}
	if service != nil {
		defer service.Close()
	}
	if err != nil {
		return fmt.Errorf("install Windows service: %w", err)
	}
	rollback := func() {
		if created {
			_ = service.Delete()
		}
	}

	if err := securefile.PrepareWindowsServiceDirectories(
		windowsServiceName,
		configDir,
		dataDir,
	); err != nil {
		rollback()
		return fmt.Errorf("prepare Windows service directories: %w", err)
	}
	if err := service.SetRecoveryActions(windowsServiceRecoveryActions(), 24*60*60); err != nil {
		rollback()
		return fmt.Errorf("configure Windows service recovery: %w", err)
	}
	if err := service.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		rollback()
		return fmt.Errorf("configure Windows non-crash recovery: %w", err)
	}
	if err := installWindowsEventLogSource(); err != nil {
		rollback()
		return err
	}
	return nil
}

func desiredWindowsServiceConfig(executable string, arguments []string) mgr.Config {
	return mgr.Config{
		ServiceType:      windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:        mgr.StartAutomatic,
		ErrorControl:     mgr.ErrorNormal,
		BinaryPathName:   windowsServiceCommandLine(executable, arguments...),
		ServiceStartName: windowsServiceAccount,
		DisplayName:      windowsServiceDisplayName,
		Description:      windowsServiceDescription,
		SidType:          windows.SERVICE_SID_TYPE_UNRESTRICTED,
		DelayedAutoStart: true,
	}
}

func windowsServiceRecoveryActions() []mgr.RecoveryAction {
	return []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.NoAction},
	}
}

func windowsServiceCommandLine(executable string, arguments ...string) string {
	commandLine := syscall.EscapeArg(executable)
	for _, argument := range arguments {
		commandLine += " " + syscall.EscapeArg(argument)
	}
	return commandLine
}

func installWindowsEventLogSource() error {
	err := eventlog.InstallAsEventCreate(
		windowsServiceName,
		eventlog.Error|eventlog.Warning|eventlog.Info,
	)
	if err != nil && !strings.Contains(err.Error(), "registry key already exists") {
		return fmt.Errorf("install Windows Event Log source: %w", err)
	}
	return nil
}

func startWindowsService() error {
	service, closeService, err := openWindowsService()
	if err != nil {
		return err
	}
	defer closeService()
	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query Windows service before start: %w", err)
	}
	switch status.State {
	case svc.Running:
		return nil
	case svc.StartPending:
		return waitWindowsServiceState(service, svc.Running)
	case svc.StopPending:
		if err := waitWindowsServiceState(service, svc.Stopped); err != nil {
			return err
		}
	case svc.Stopped:
	default:
		return fmt.Errorf("Windows service cannot start from state %d", status.State)
	}
	if err := service.Start(); err != nil {
		return fmt.Errorf("start Windows service: %w", err)
	}
	return waitWindowsServiceState(service, svc.Running)
}

func stopWindowsService() error {
	service, closeService, err := openWindowsService()
	if errors.Is(err, errWindowsServiceNotInstalled) {
		return nil
	}
	if err != nil {
		return err
	}
	defer closeService()
	return stopOpenedWindowsService(service)
}

func stopOpenedWindowsService(service *mgr.Service) error {
	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query Windows service before stop: %w", err)
	}
	switch status.State {
	case svc.Stopped:
		return nil
	case svc.StopPending:
		return waitWindowsServiceState(service, svc.Stopped)
	case svc.StartPending:
		if err := waitWindowsServiceState(service, svc.Running); err != nil {
			return err
		}
	case svc.Running, svc.Paused:
	default:
		return fmt.Errorf("Windows service cannot stop from state %d", status.State)
	}
	if _, err := service.Control(svc.Stop); err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return nil
		}
		return fmt.Errorf("stop Windows service: %w", err)
	}
	return waitWindowsServiceState(service, svc.Stopped)
}

func restartWindowsService() error {
	if err := stopWindowsService(); err != nil {
		return err
	}
	return startWindowsService()
}

func uninstallWindowsService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to Windows service manager: %w", err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(windowsServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return removeWindowsEventLogSource()
	}
	if err != nil {
		return fmt.Errorf("open Windows service: %w", err)
	}
	defer service.Close()
	if err := stopOpenedWindowsService(service); err != nil {
		return err
	}
	if err := service.Delete(); err != nil {
		return fmt.Errorf("delete Windows service: %w", err)
	}
	return removeWindowsEventLogSource()
}

func removeWindowsEventLogSource() error {
	if err := eventlog.Remove(windowsServiceName); err != nil &&
		!errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return fmt.Errorf("remove Windows Event Log source: %w", err)
	}
	return nil
}

func windowsServiceStatus() (string, error) {
	service, closeService, err := openWindowsService()
	if err != nil {
		return "", err
	}
	defer closeService()
	status, err := service.Query()
	if err != nil {
		return "", fmt.Errorf("query Windows service: %w", err)
	}
	switch status.State {
	case svc.Stopped:
		return "stopped", nil
	case svc.StartPending:
		return "start_pending", nil
	case svc.StopPending:
		return "stop_pending", nil
	case svc.Running:
		return "running", nil
	case svc.ContinuePending:
		return "continue_pending", nil
	case svc.PausePending:
		return "pause_pending", nil
	case svc.Paused:
		return "paused", nil
	default:
		return "", fmt.Errorf("unknown Windows service state %d", status.State)
	}
}

func openWindowsService() (*mgr.Service, func(), error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, func() {}, fmt.Errorf("connect to Windows service manager: %w", err)
	}
	service, err := manager.OpenService(windowsServiceName)
	if err != nil {
		manager.Disconnect()
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, func() {}, errWindowsServiceNotInstalled
		}
		return nil, func() {}, fmt.Errorf("open Windows service: %w", err)
	}
	return service, func() {
		service.Close()
		manager.Disconnect()
	}, nil
}

func waitWindowsServiceState(service *mgr.Service, wanted svc.State) error {
	deadline := time.Now().Add(windowsServiceWaitTimeout)
	for {
		status, err := service.Query()
		if err != nil {
			return fmt.Errorf("query Windows service state: %w", err)
		}
		if status.State == wanted {
			return nil
		}
		if wanted == svc.Running && status.State == svc.Stopped {
			return fmt.Errorf(
				"Windows service stopped during startup with exit code %d/%d",
				status.Win32ExitCode,
				status.ServiceSpecificExitCode,
			)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for Windows service state %d", wanted)
		}
		time.Sleep(windowsServicePollDelay)
	}
}
