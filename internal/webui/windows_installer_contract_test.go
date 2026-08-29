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

func TestWindowsInstallerPinsLanguageAndInstallationDirectory(t *testing.T) {
	installer := readRepositoryFile(t, "packaging/windows/gpt-load.iss")
	for _, required := range []string{
		`DefaultDirName={autopf}\GPT-Load`,
		"DisableDirPage=yes",
		"UsePreviousAppDir=no",
		`MessagesFile: "ChineseSimplified.isl"`,
	} {
		if !strings.Contains(installer, required) {
			t.Fatalf("Windows installer does not contain fixed installation contract %q", required)
		}
	}
	if strings.Contains(installer, `compiler:Languages\ChineseSimplified.isl`) {
		t.Fatal("Windows installer still depends on an optional compiler language file")
	}

	language := readRepositoryFile(t, "packaging/windows/ChineseSimplified.isl")
	for _, required := range []string{
		"6ef32198ef1f7b7b375cd4b6b90896c2a58eb4c2",
		"[LangOptions]",
		"LanguageName=简体中文",
		"LanguageID=$0804",
	} {
		if !strings.Contains(language, required) {
			t.Fatalf("vendored Simplified Chinese language file does not contain %q", required)
		}
	}
}

func TestWindowsInstallerFailsClosedAndRestoresInterruptedUpgrade(t *testing.T) {
	installer := readRepositoryFile(t, "packaging/windows/gpt-load.iss")
	for _, required := range []string{
		"GetCustomSetupExitCode",
		"InstallationFailed",
		"InstallCompleted",
		"PreviousBinaryPath",
		"PreviousServiceWasRunning",
		"FileCopy",
		"RestorePreviousInstallation",
		"DeinitializeSetup",
		"ssDone",
		"CanLaunchManagementPage",
	} {
		if !strings.Contains(installer, required) {
			t.Fatalf("Windows installer does not contain failure recovery contract %q", required)
		}
	}
	if strings.Contains(installer, "if CurStep = ssPostInstall then\n  begin\n    RunRequiredServiceCommand") {
		t.Fatal("Windows installer still raises a swallowed ssPostInstall service exception")
	}
}

func TestPullRequestWindowsCIBuildsAndSmokesInstaller(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/ci.yml")
	windowsJob := workflowJobBlock(t, workflow, "windows-encryption-acl")
	for _, required := range []string{
		"ISCC.exe",
		"packaging/windows/gpt-load.iss",
		"gpt-load-windows-setup.exe",
		"release-windows-installer-smoke.ps1",
	} {
		if !strings.Contains(windowsJob, required) {
			t.Fatalf("pull-request Windows CI does not contain %q:\n%s", required, windowsJob)
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
		"installer-smoke-owner",
		"refusing pre-existing Windows service",
		"refusing pre-existing ProgramData directory",
		"upgrade did not preserve persistent data",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("Windows installer smoke does not contain %q", required)
		}
	}
	if strings.Count(script, "Invoke-CheckedProcess -Path $setup") < 2 {
		t.Fatal("Windows installer smoke does not exercise an overwrite install")
	}
	installBody, _, found := strings.Cut(script, "Invoke-CheckedProcess -Path $uninstaller")
	if !found {
		t.Fatal("Windows installer smoke does not invoke the uninstaller")
	}
	if strings.Contains(installBody, "$installedBinary service stop") {
		t.Fatal("Windows installer smoke stops the service before testing the uninstaller")
	}
}

func TestWindowsInstallerSmokeWaitsForGUIProcesses(t *testing.T) {
	script := readRepositoryFile(t, ".github/scripts/release-windows-installer-smoke.ps1")
	for _, required := range []string{
		"Start-Process",
		"-FilePath $Path",
		"-ArgumentList $Arguments",
		"-Wait",
		"-PassThru",
		"$process.ExitCode",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("Windows installer smoke process helper does not contain %q", required)
		}
	}
	if strings.Contains(script, "& $Path @Arguments") {
		t.Fatal("Windows installer smoke launches GUI processes without waiting")
	}
}

func TestWindowsSmokesOwnTheirFixedInstallationDirectory(t *testing.T) {
	for _, path := range []string{
		".github/scripts/ci-windows-service-smoke.ps1",
		".github/scripts/release-windows-installer-smoke.ps1",
	} {
		script := readRepositoryFile(t, path)
		for _, required := range []string{
			`Join-Path $env:ProgramFiles "GPT-Load"`,
			"refusing pre-existing Windows service",
			"refusing pre-existing installation directory",
			"installOwnerMarker",
			"installOwnerToken",
		} {
			if !strings.Contains(script, required) {
				t.Fatalf("%s does not contain fixed install ownership contract %q", path, required)
			}
		}
		if strings.Contains(script, `"/DIR=`) {
			t.Fatalf("%s overrides the fixed installer directory", path)
		}
	}
}
