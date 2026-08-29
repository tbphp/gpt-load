package webui

import (
	"strings"
	"testing"
)

func TestWindowsInstallerKeepsPortableBinaryAndAddsSetupAsset(t *testing.T) {
	manifest := readRepositoryFile(t, ".github/release-assets.txt")
	for _, asset := range []string{
		"gpt-load-windows-amd64.exe",
		"gpt-load-windows-setup.exe",
	} {
		if !strings.Contains(manifest, asset) {
			t.Fatalf("release manifest does not contain %q", asset)
		}
	}

	workflow := readRepositoryFile(t, ".github/workflows/release.yml")
	setupJob := workflowJobBlock(t, workflow, "build-windows-setup")
	for _, required := range []string{
		"windows-2025",
		"binary-gpt-load-windows-amd64.exe",
		"packaging/windows/gpt-load.iss",
		"ISCC.exe",
		"gpt-load-windows-setup.exe",
		"binary-gpt-load-windows-setup.exe",
	} {
		if !strings.Contains(setupJob, required) {
			t.Fatalf("Windows setup build does not contain %q:\n%s", required, setupJob)
		}
	}

	checksums := workflowJobBlock(t, workflow, "package-checksums")
	if !strings.Contains(checksums, "build-windows-setup") {
		t.Fatalf("package checksums do not wait for Windows setup:\n%s", checksums)
	}
	preflight := workflowJobBlock(t, workflow, "publication-preflight")
	for _, gate := range []string{"build-windows-setup", "windows-installer-smoke"} {
		if !strings.Contains(preflight, "- "+gate) {
			t.Fatalf("publication preflight does not require %s:\n%s", gate, preflight)
		}
	}
}

func TestWindowsInstallerInstallsServiceWithoutAResidentWrapper(t *testing.T) {
	installer := readRepositoryFile(t, "packaging/windows/gpt-load.iss")
	for _, required := range []string{
		"PrivilegesRequired=admin",
		"gpt-load.exe",
		"service install",
		"service start",
		"service stop",
		"service uninstall",
		"http://127.0.0.1:3001",
		"{commondesktop}",
		"{group}",
		"auth.key",
		"TOutputMsgMemoWizardPage",
	} {
		if !strings.Contains(installer, required) {
			t.Fatalf("Windows installer does not contain %q", required)
		}
	}
	for _, forbidden := range []string{
		"WinSW",
		"NSSM",
		"gpt-load-tray",
		"SetClipboardText",
		"deleteafterinstall",
	} {
		if strings.Contains(installer, forbidden) {
			t.Fatalf("Windows installer contains forbidden behavior %q", forbidden)
		}
	}
}

func TestWindowsInstallerSmokeCoversInstallHealthStopAndUninstall(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/release.yml")
	job := workflowJobBlock(t, workflow, "windows-installer-smoke")
	if !strings.Contains(job, ".github/scripts/release-windows-installer-smoke.ps1") {
		t.Fatalf("Windows installer smoke job does not invoke its script:\n%s", job)
	}
	if !strings.Contains(job, "binary-gpt-load-windows-amd64.exe") {
		t.Fatalf("Windows installer smoke does not download the portable binary:\n%s", job)
	}
	script := readRepositoryFile(t, ".github/scripts/release-windows-installer-smoke.ps1")
	for _, required := range []string{
		"RELEASE_WINDOWS_BINARY",
		"/VERYSILENT",
		"Get-Service",
		"http://127.0.0.1:3001/health",
		"auth.key",
		"Authorization",
		"service stop",
		"unins000.exe",
		"/VERYSILENT",
		"Get-Acl",
		"NT SERVICE\\gpt-load",
		"installed binary differs from the portable release binary",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("Windows installer smoke does not contain %q", required)
		}
	}
}
