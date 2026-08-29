package main

import "testing"

func TestParseServiceCommandAcceptsPublicActions(t *testing.T) {
	for _, action := range []string{"install", "start", "stop", "restart", "status", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			command, err := parseServiceCommand([]string{action})
			if err != nil {
				t.Fatalf("parseServiceCommand(%q) error = %v", action, err)
			}
			if command.action != action {
				t.Fatalf("action = %q, want %q", command.action, action)
			}
		})
	}
}

func TestParseServiceCommandAcceptsExplicitInstallDirectories(t *testing.T) {
	command, err := parseServiceCommand([]string{
		"install",
		"--config-dir", `C:\ProgramData\GPT-Load`,
		"--data-dir", `C:\ProgramData\GPT-Load\data`,
	})
	if err != nil {
		t.Fatalf("parseServiceCommand() error = %v", err)
	}
	if command.configDir != `C:\ProgramData\GPT-Load` ||
		command.dataDir != `C:\ProgramData\GPT-Load\data` {
		t.Fatalf("directories = %#v", command)
	}
}

func TestParseServiceCommandRejectsHiddenRunWithoutDirectories(t *testing.T) {
	if _, err := parseServiceCommand([]string{"run"}); err == nil {
		t.Fatal("parseServiceCommand(run) error = nil, want explicit directory requirement")
	}
}

func TestParseServiceCommandRejectsUnknownActionAndOptions(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"pause"},
		{"status", "--data-dir", `C:\data`},
		{"install", "--unknown", "value"},
	} {
		if _, err := parseServiceCommand(args); err == nil {
			t.Fatalf("parseServiceCommand(%q) error = nil", args)
		}
	}
}
