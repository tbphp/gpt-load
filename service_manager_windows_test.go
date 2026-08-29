//go:build windows

package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestDesiredWindowsServiceConfigUsesLowPrivilegeIsolatedIdentity(t *testing.T) {
	arguments := []string{"service", "run"}
	config := desiredWindowsServiceConfig(`C:\Program Files\GPT-Load\gpt-load.exe`, arguments)
	if config.ServiceStartName != `NT AUTHORITY\LocalService` {
		t.Fatalf("service account = %q", config.ServiceStartName)
	}
	if config.SidType != windows.SERVICE_SID_TYPE_UNRESTRICTED {
		t.Fatalf("service SID type = %d", config.SidType)
	}
	if config.StartType != mgr.StartAutomatic || !config.DelayedAutoStart {
		t.Fatalf("service start policy = %d/%t", config.StartType, config.DelayedAutoStart)
	}
	for _, required := range arguments {
		if !strings.Contains(config.BinaryPathName, required) {
			t.Fatalf("binary path does not contain %q: %s", required, config.BinaryPathName)
		}
	}
	for _, forbidden := range []string{
		"--config-dir",
		"--data-dir",
		"AUTH_KEY",
		"ENCRYPTION_KEY",
		"DATABASE_DSN",
	} {
		if strings.Contains(config.BinaryPathName, forbidden) {
			t.Fatalf("binary path contains secret configuration name %q", forbidden)
		}
	}
}

func TestWindowsServiceRecoveryUsesBoundedBackoff(t *testing.T) {
	actions := windowsServiceRecoveryActions()
	want := []struct {
		actionType int
		delay      time.Duration
	}{
		{actionType: mgr.ServiceRestart, delay: 5 * time.Second},
		{actionType: mgr.ServiceRestart, delay: 30 * time.Second},
		{actionType: mgr.ServiceRestart, delay: time.Minute},
		{actionType: mgr.NoAction},
	}
	if len(actions) != len(want) {
		t.Fatalf("recovery action count = %d", len(actions))
	}
	for index, action := range actions {
		if action.Type != want[index].actionType || action.Delay != want[index].delay {
			t.Fatalf("recovery action %d = %#v", index, action)
		}
	}
}

func TestOwnedWindowsServiceConfigAcceptsOnlyOfficialCommandShape(t *testing.T) {
	official := desiredWindowsServiceConfig(
		`C:\Program Files\GPT-Load\gpt-load.exe`,
		[]string{"service", "run"},
	)
	if !isOwnedWindowsServiceConfig(official) {
		t.Fatal("official service configuration is not recognized")
	}

	portable := desiredWindowsServiceConfig(
		`C:\Downloads\gpt-load-windows-amd64.exe`,
		[]string{"service", "run"},
	)
	if !isOwnedWindowsServiceConfig(portable) {
		t.Fatal("portable-installed service configuration is not recognized")
	}

	for name, mutate := range map[string]func(*mgr.Config){
		"foreign display name": func(config *mgr.Config) {
			config.DisplayName = "Another Service"
		},
		"foreign description": func(config *mgr.Config) {
			config.Description = "Another service"
		},
		"foreign account": func(config *mgr.Config) {
			config.ServiceStartName = `LocalSystem`
		},
		"foreign SID policy": func(config *mgr.Config) {
			config.SidType = windows.SERVICE_SID_TYPE_NONE
		},
		"foreign command": func(config *mgr.Config) {
			config.BinaryPathName = windowsServiceCommandLine(
				`C:\Program Files\GPT-Load\gpt-load.exe`,
				"other",
				"run",
			)
		},
		"relative executable": func(config *mgr.Config) {
			config.BinaryPathName = windowsServiceCommandLine("gpt-load.exe", "service", "run")
		},
		"custom directories": func(config *mgr.Config) {
			config.BinaryPathName = windowsServiceCommandLine(
				`C:\Program Files\GPT-Load\gpt-load.exe`,
				"service", "run",
				"--config-dir", `C:\`,
				"--data-dir", `C:\data`,
			)
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := official
			mutate(&config)
			if isOwnedWindowsServiceConfig(config) {
				t.Fatal("foreign service configuration is recognized as owned")
			}
		})
	}
}

func TestApplyWindowsServiceUpdateCommitsConfigLast(t *testing.T) {
	desired := mgr.Config{DisplayName: "desired"}
	service := &fakeWindowsServiceUpdater{}

	if err := applyWindowsServiceUpdate(service, desired); err != nil {
		t.Fatalf("applyWindowsServiceUpdate() error = %v", err)
	}
	if service.config.DisplayName != desired.DisplayName {
		t.Fatalf("service config = %#v, want %#v", service.config, desired)
	}
	if got := service.calls[len(service.calls)-1]; got != "update_config" {
		t.Fatalf("last service call = %q, want update_config", got)
	}
}

func TestApplyWindowsServiceUpdateDoesNotCommitConfigAfterPreparationFailure(t *testing.T) {
	desired := mgr.Config{DisplayName: "desired"}
	service := &fakeWindowsServiceUpdater{recoveryErr: errors.New("recovery failed")}

	err := applyWindowsServiceUpdate(service, desired)
	if err == nil || !strings.Contains(err.Error(), "recovery failed") {
		t.Fatalf("applyWindowsServiceUpdate() error = %v", err)
	}
	for _, call := range service.calls {
		if call == "update_config" {
			t.Fatal("service config was updated after recovery preparation failed")
		}
	}
}

type fakeWindowsServiceUpdater struct {
	config      mgr.Config
	recoveryErr error
	updateErr   error
	calls       []string
}

func (service *fakeWindowsServiceUpdater) Config() (mgr.Config, error) {
	service.calls = append(service.calls, "config")
	return service.config, nil
}

func (service *fakeWindowsServiceUpdater) UpdateConfig(config mgr.Config) error {
	service.calls = append(service.calls, "update_config")
	if service.updateErr != nil {
		return service.updateErr
	}
	service.config = config
	return nil
}

func (service *fakeWindowsServiceUpdater) SetRecoveryActions(
	_ []mgr.RecoveryAction,
	_ uint32,
) error {
	service.calls = append(service.calls, "set_recovery")
	return service.recoveryErr
}

func (service *fakeWindowsServiceUpdater) SetRecoveryActionsOnNonCrashFailures(_ bool) error {
	service.calls = append(service.calls, "set_non_crash")
	return nil
}
