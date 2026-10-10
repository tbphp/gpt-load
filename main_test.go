package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"gpt-load/internal/platform/version"
)

func TestDispatchCommandPrintsVersion(t *testing.T) {
	want := "GPT-Load " + version.Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")\n"
	for _, command := range []string{"version", "--version"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := dispatchCommand([]string{command}, &stdout, &stderr)

		if exitCode != 0 || stdout.String() != want || stderr.Len() != 0 {
			t.Fatalf(
				"dispatchCommand(%s) = %d, stdout %q, stderr %q; want 0, %q, empty",
				command,
				exitCode,
				stdout.String(),
				stderr.String(),
				want,
			)
		}
	}
}

func TestDispatchCommandRejectsArgumentsForVersionAndUpdate(t *testing.T) {
	for _, args := range [][]string{{"version", "extra"}, {"update", "--yes"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := dispatchCommand(args, &stdout, &stderr)

		if exitCode == 0 || !strings.Contains(stderr.String(), "does not accept arguments") {
			t.Fatalf("dispatchCommand(%v) = %d, stderr %q; want argument error", args, exitCode, stderr.String())
		}
	}
}

func TestPrintHelpDocumentsVersionAndUpdate(t *testing.T) {
	var output bytes.Buffer
	printHelp(&output)

	help := output.String()
	for _, required := range []string{"gpt-load version", "gpt-load update"} {
		if !strings.Contains(help, required) {
			t.Fatalf("help does not document %q:\n%s", required, help)
		}
	}
}

func TestPrintHelpMarksKeyMigrationAsDeferred(t *testing.T) {
	var output bytes.Buffer
	printHelp(&output)

	help := output.String()
	if !strings.Contains(help, "migrate-keys") || !strings.Contains(help, "later release") {
		t.Fatalf("help does not mark migrate-keys as deferred:\n%s", help)
	}
}

func TestPrintHelpDocumentsOnlyPublicWindowsServiceManagement(t *testing.T) {
	var output bytes.Buffer
	printHelp(&output)

	help := output.String()
	for _, required := range []string{
		"service start",
		"service stop",
		"service restart",
		"service status",
		"Windows",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("help does not document %q:\n%s", required, help)
		}
	}
	for _, internal := range []string{"service install", "service uninstall"} {
		if strings.Contains(help, internal) {
			t.Fatalf("help exposes internal command %q:\n%s", internal, help)
		}
	}
}

func TestDispatchCommandDoesNotRunLegacyKeyMigration(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := dispatchCommand([]string{"migrate-keys"}, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("dispatchCommand(migrate-keys) exit code = 0, want non-zero")
	}
	if !strings.Contains(stderr.String(), "later release") {
		t.Fatalf("stderr does not explain deferred command: %s", stderr.String())
	}
}

func TestDispatchCommandRecognizesWindowsServiceNamespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows boundary test")
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := dispatchCommand([]string{"service", "status"}, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("dispatchCommand(service status) exit code = 0 on a non-Windows host")
	}
	if strings.Contains(stderr.String(), "Unknown command") ||
		!strings.Contains(stderr.String(), "Windows") {
		t.Fatalf("stderr does not identify the Windows service boundary: %s", stderr.String())
	}
}
