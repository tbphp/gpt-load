package catalog

import (
	"errors"
	"testing"
)

func TestWindowsCatalogReplaceUsesReplaceFileForExistingTarget(t *testing.T) {
	replaceCalls := 0
	moveCalls := 0
	err := replaceCatalogFileWindows(
		"data/catalog.new.tmp",
		"data/catalog.json",
		windowsCatalogReplacePrimitives{
			targetExists: func(targetPath string) (bool, error) {
				if targetPath != "data/catalog.json" {
					t.Fatalf("targetExists(%q)", targetPath)
				}
				return true, nil
			},
			replaceExisting: func(temporaryPath, finalPath string) error {
				replaceCalls++
				if temporaryPath != "data/catalog.new.tmp" || finalPath != "data/catalog.json" {
					t.Fatalf("replaceExisting(%q, %q)", temporaryPath, finalPath)
				}
				return nil
			},
			moveNew: func(string, string) error {
				moveCalls++
				return nil
			},
		},
	)
	if err != nil || replaceCalls != 1 || moveCalls != 0 {
		t.Fatalf("replace error/calls/moves = %v/%d/%d, want nil/1/0", err, replaceCalls, moveCalls)
	}
}

func TestWindowsCatalogReplaceUsesMoveFileOnlyForMissingTarget(t *testing.T) {
	replaceCalls := 0
	moveCalls := 0
	err := replaceCatalogFileWindows(
		"data/catalog.new.tmp",
		"data/catalog.json",
		windowsCatalogReplacePrimitives{
			targetExists: func(string) (bool, error) { return false, nil },
			replaceExisting: func(string, string) error {
				replaceCalls++
				return nil
			},
			moveNew: func(temporaryPath, finalPath string) error {
				moveCalls++
				if temporaryPath != "data/catalog.new.tmp" || finalPath != "data/catalog.json" {
					t.Fatalf("moveNew(%q, %q)", temporaryPath, finalPath)
				}
				return nil
			},
		},
	)
	if err != nil || replaceCalls != 0 || moveCalls != 1 {
		t.Fatalf("replace error/calls/moves = %v/%d/%d, want nil/0/1", err, replaceCalls, moveCalls)
	}
}

func TestWindowsCatalogReplaceFailsBeforePrimitiveWhenTargetStateIsUnknown(t *testing.T) {
	statErr := errors.New("injected target state failure")
	err := replaceCatalogFileWindows(
		"data/catalog.new.tmp",
		"data/catalog.json",
		windowsCatalogReplacePrimitives{
			targetExists: func(string) (bool, error) { return false, statErr },
			replaceExisting: func(string, string) error {
				t.Fatal("replaceExisting called after target state failure")
				return nil
			},
			moveNew: func(string, string) error {
				t.Fatal("moveNew called after target state failure")
				return nil
			},
		},
	)
	if !errors.Is(err, statErr) {
		t.Fatalf("replaceCatalogFileWindows() error = %v, want target state error", err)
	}
}
