package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebUIDoesNotCallBrowserSensitiveAPIsOutsideCompatibilityHelpers(t *testing.T) {
	root := filepath.Join("..", "..", "web", "src")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ts") && !strings.HasSuffix(entry.Name(), ".vue") {
			return nil
		}
		if path == filepath.Join(root, "lib", "clipboard.ts") ||
			path == filepath.Join(root, "lib", "uuid.ts") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "crypto.randomUUID()") {
			t.Errorf("%s calls crypto.randomUUID directly; use the shared UUID compatibility helper", path)
		}
		if strings.Contains(string(content), "navigator.clipboard") {
			t.Errorf("%s calls navigator.clipboard directly; use the shared clipboard compatibility helper", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk web source: %v", err)
	}
}

func TestBrowserCompatibilityHelpersPreferNativeAPIsAndStaySilent(t *testing.T) {
	uuid := readRepositoryFile(t, "web/src/lib/uuid.ts")
	assertOrderedSubstrings(t, uuid,
		"if (typeof webCrypto?.randomUUID === 'function')",
		"return webCrypto.randomUUID()",
		"if (typeof webCrypto?.getRandomValues !== 'function')",
		"webCrypto.getRandomValues(bytes)",
	)
	if strings.Contains(uuid, "console.") {
		t.Fatal("UUID compatibility helper emits a console message")
	}

	clipboard := readRepositoryFile(t, "web/src/lib/clipboard.ts")
	assertOrderedSubstrings(t, clipboard,
		"if (typeof writeText === 'function')",
		"await writeText.call(globalThis.navigator.clipboard, value)",
		"copyWithLegacyCommand(value)",
	)
	assertOrderedSubstrings(t, clipboard,
		"const previousFocus =",
		"document.activeElement instanceof HTMLElement ? document.activeElement : null",
		"textarea.focus()",
		"textarea.remove()",
		"if (previousFocus?.isConnected) previousFocus.focus()",
	)
	if strings.Contains(clipboard, "console.") {
		t.Fatal("clipboard compatibility helper emits a console message")
	}
}

func TestAsyncSecretCopyKeepsAnExplicitLegacyClipboardGesture(t *testing.T) {
	copyChip := readRepositoryFile(t, "web/src/components/ui/CopyChip.vue")
	assertOrderedSubstrings(t, copyChip,
		"const preparedValue = ref<string>()",
		"if (preparedValue.value !== undefined)",
		"await copyText(value)",
		"if (props.resolveValue && !canWriteToClipboardNatively())",
		"preparedValue.value = value",
		"state.value = 'ready'",
	)
	if !strings.Contains(copyChip, "t('common.copyReady')") {
		t.Fatal("CopyChip does not explain the second legacy-copy click")
	}

	gateway := readRepositoryFile(t, "web/src/features/home/GatewayConnection.vue")
	for _, required := range []string{
		"let preparedLegacyCopy: PreparedLegacyCopy | undefined",
		"function prepareLegacyCopy(",
		"async function copyPreparedLegacyValue(",
		"if (!canWriteToClipboardNatively() && !props.selfScoped)",
		"return prepareLegacyCopy(identity, target, value)",
	} {
		if !strings.Contains(gateway, required) {
			t.Fatalf("GatewayConnection does not contain %q", required)
		}
	}
}

func assertOrderedSubstrings(t *testing.T, content string, values ...string) {
	t.Helper()
	previous := -1
	for _, value := range values {
		index := strings.Index(content, value)
		if index < 0 {
			t.Fatalf("content does not contain %q", value)
		}
		if index <= previous {
			t.Fatalf("content does not keep %q after the previous contract marker", value)
		}
		previous = index
	}
}
