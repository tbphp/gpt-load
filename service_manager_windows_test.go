//go:build windows

package main

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestDesiredWindowsServiceConfigUsesLowPrivilegeIsolatedIdentity(t *testing.T) {
	arguments := []string{
		"service", "run",
		"--config-dir", `C:\ProgramData\GPT-Load`,
		"--data-dir", `C:\ProgramData\GPT-Load\data`,
	}
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
	for _, forbidden := range []string{"AUTH_KEY", "ENCRYPTION_KEY", "DATABASE_DSN"} {
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

func TestNormalizeWindowsServiceDirectoriesRejectsRelativeAndSharedPaths(t *testing.T) {
	for _, test := range []struct {
		config string
		data   string
	}{
		{config: `relative`, data: `C:\ProgramData\GPT-Load\data`},
		{config: `C:\ProgramData\GPT-Load`, data: `relative`},
		{config: `C:\ProgramData\GPT-Load`, data: `c:\programdata\gpt-load`},
	} {
		if _, _, err := normalizeWindowsServiceDirectories(test.config, test.data); err == nil {
			t.Fatalf("normalizeWindowsServiceDirectories(%q, %q) error = nil", test.config, test.data)
		}
	}
}
