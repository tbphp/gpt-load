package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintHelpMarksKeyMigrationAsDeferred(t *testing.T) {
	var output bytes.Buffer
	printHelp(&output)

	help := output.String()
	if !strings.Contains(help, "migrate-keys") || !strings.Contains(help, "later release") {
		t.Fatalf("help does not mark migrate-keys as deferred:\n%s", help)
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
