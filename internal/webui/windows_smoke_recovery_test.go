package webui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsSmokeRecovery(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell recovery tests run in the Windows CI job")
	}
	script := `
$ErrorActionPreference = 'Stop'
. $env:RECOVERY_SCRIPT
$script:record = $null
$script:stops = 0
$script:deletes = 0
function Get-CimInstance { [CmdletBinding()] param($ClassName, $Filter); return $script:record }
function Get-Service {
  [CmdletBinding()] param($Name)
  $controller = [pscustomobject]@{ Status = 'Running' }
  $controller | Add-Member ScriptMethod Stop { $script:stops++ }
  $controller | Add-Member ScriptMethod WaitForStatus { param($Status, $Timeout) }
  $controller | Add-Member ScriptMethod Dispose { }
  return $controller
}
function sc.exe {
  param($Action, $Name)
  if ($Action -ne 'delete' -or $Name -ne 'gpt-load') { throw 'unexpected service operation' }
  $script:deletes++
  $global:LASTEXITCODE = 0
}
# Windows 系统目录与注册表只在真实 smoke 中使用；此测试只操作临时目录。
function Test-Path {
  [CmdletBinding()] param([string]$LiteralPath)
  if (-not $LiteralPath.StartsWith($env:TEST_ROOT)) { return $false }
  return Microsoft.PowerShell.Management\Test-Path -LiteralPath $LiteralPath
}

foreach ($case in @('unmarked', 'invalid', 'mismatch', 'foreign-service', 'service', 'partial', 'installer', 'linked')) {
  $root = Join-Path $env:TEST_ROOT $case
  $install = Join-Path $root 'install'
  $config = Join-Path $root 'config'
  New-Item -ItemType Directory -Path $install, $config | Out-Null
  $token = [guid]::NewGuid().ToString('N')
  $marker = if ($case -eq 'installer') { '.installer-smoke-owner' } else { '.service-smoke-owner' }
  if ($case -ne 'unmarked') {
    $value = if ($case -eq 'invalid') { 'not-a-test-owner' } else { $token }
    [IO.File]::WriteAllText((Join-Path $config $marker), $value)
    if ($case -ne 'installer') {
      $value = if ($case -eq 'mismatch') { [guid]::NewGuid().ToString('N') } else { $value }
      [IO.File]::WriteAllText((Join-Path $install $marker), $value)
    } else {
      New-Item -ItemType Directory -Path (Join-Path $config 'data') | Out-Null
      [IO.File]::WriteAllText((Join-Path $config 'data/installer-smoke-failure.txt'), $token)
    }
  }
  if ($case -eq 'partial') { Microsoft.PowerShell.Management\Remove-Item -LiteralPath $config -Recurse -Force }
  if ($case -eq 'linked') {
    Microsoft.PowerShell.Management\Remove-Item -LiteralPath $install -Recurse -Force
    New-Item -ItemType SymbolicLink -Path $install -Target $config | Out-Null
  }
  $script:record = if ($case -in @('service', 'foreign-service')) {
    $binary = if ($case -eq 'service') { Join-Path $install 'gpt-load.exe' } else { Join-Path $root 'real-app.exe' }
    [pscustomobject]@{ PathName = '"' + $binary + '" service run'; StartName = 'NT AUTHORITY\LocalService' }
  } else { $null }
  $lock = $null
  $failed = $false
  try { $lock = Enter-WindowsSmoke -InstallDir $install -ConfigDir $config }
  catch { $failed = $true }
  finally { if ($null -ne $lock) { $lock.ReleaseMutex(); $lock.Dispose() } }
  $wantFailure = $case -in @('invalid', 'mismatch', 'foreign-service', 'linked')
  if ($failed -ne $wantFailure) { throw "$case failure = $failed, want $wantFailure" }
  $wantPreserved = $wantFailure -or $case -eq 'unmarked'
  if ((Test-Path $install) -ne $wantPreserved) { throw "$case installation preservation failed" }
  if ((Test-Path $config) -ne $wantPreserved) { throw "$case data preservation failed" }
}
if ($script:stops -ne 1 -or $script:deletes -ne 1) { throw 'stopped or deleted an unowned service' }

# 第二个进程不能清理第一个进程仍在使用的安装目录。
$script:record = $null
$root = Join-Path $env:TEST_ROOT 'busy'
$lock = Enter-WindowsSmoke -InstallDir $root -ConfigDir $root
try {
  & ([Environment]::ProcessPath) -NoLogo -NoProfile -Command '
    $ErrorActionPreference = "Stop"
    . $env:RECOVERY_SCRIPT
    try {
      $lock = Enter-WindowsSmoke -InstallDir $env:TEST_ROOT -ConfigDir $env:TEST_ROOT
      $lock.ReleaseMutex(); $lock.Dispose()
      exit 2
    } catch {
      if ($_.Exception.Message -ne "another Windows smoke is running") { throw }
      exit 0
    }
  '
  if ($LASTEXITCODE -ne 0) { throw 'active smoke was not protected' }
} finally { $lock.ReleaseMutex(); $lock.Dispose() }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "recovery-test.ps1")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	recovery, err := filepath.Abs("../../.github/scripts/windows-smoke-recovery.ps1")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-File", path)
	command.Env = append(os.Environ(), "RECOVERY_SCRIPT="+recovery, "TEST_ROOT="+dir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Windows smoke recovery: %v\n%s", err, output)
	}
}
