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

func TestParseServiceCommandAcceptsHiddenRunWithoutOptions(t *testing.T) {
	command, err := parseServiceCommand([]string{"run"})
	if err != nil {
		t.Fatalf("parseServiceCommand(run) error = %v", err)
	}
	if command.action != "run" {
		t.Fatalf("action = %q, want run", command.action)
	}
}

func TestParseServiceCommandRejectsCustomServiceDirectories(t *testing.T) {
	for _, action := range []string{"install", "run"} {
		t.Run(action, func(t *testing.T) {
			if _, err := parseServiceCommand([]string{
				action,
				"--config-dir", `C:\ProgramData\GPT-Load`,
				"--data-dir", `C:\ProgramData\GPT-Load\data`,
			}); err == nil {
				t.Fatal("parseServiceCommand() error = nil, want fixed-directory enforcement")
			}
		})
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
