$ErrorActionPreference = "Stop"

$setup = $env:RELEASE_WINDOWS_SETUP
$portableBinary = $env:RELEASE_WINDOWS_BINARY
$checksumFile = $env:RELEASE_SMOKE_CHECKSUM_FILE
$releaseVersion = $env:RELEASE_VERSION
foreach ($required in @($setup, $portableBinary, $checksumFile, $releaseVersion)) {
  if ([string]::IsNullOrWhiteSpace($required)) {
    throw "RELEASE_WINDOWS_SETUP, RELEASE_WINDOWS_BINARY, RELEASE_SMOKE_CHECKSUM_FILE, and RELEASE_VERSION are required"
  }
}

$setup = [System.IO.Path]::GetFullPath($setup)
$portableBinary = [System.IO.Path]::GetFullPath($portableBinary)
$filename = [System.IO.Path]::GetFileName($setup)
$checksumLine = Get-Content $checksumFile |
  Where-Object { $_ -match "\s$([regex]::Escape($filename))$" }
if (-not $checksumLine) { throw "missing Windows setup checksum" }
$expectedHash = ($checksumLine -split "\s+")[0].ToLowerInvariant()
$beforeHash = (Get-FileHash -Algorithm SHA256 $setup).Hash.ToLowerInvariant()
if ($beforeHash -ne $expectedHash) { throw "Windows setup checksum mismatch before execution" }

$serviceName = "gpt-load"
$suffix = [guid]::NewGuid().ToString("N")
$installDir = Join-Path $env:RUNNER_TEMP "gpt-load-installer-smoke-$suffix"
$programData = [Environment]::GetFolderPath("CommonApplicationData")
$configDir = Join-Path $programData "GPT-Load"
$dataDir = Join-Path $configDir "data"
$installedBinary = Join-Path $installDir "gpt-load.exe"
$uninstaller = Join-Path $installDir "unins000.exe"
$commonDesktop = [Environment]::GetFolderPath("CommonDesktopDirectory")
$commonPrograms = [Environment]::GetFolderPath("CommonPrograms")
$desktopShortcut = Join-Path $commonDesktop "GPT-Load.url"
$startMenuShortcut = Join-Path $commonPrograms "GPT-Load\GPT-Load.url"

if ([System.IO.Path]::GetFullPath($configDir) -ne
    [System.IO.Path]::GetFullPath((Join-Path $programData "GPT-Load"))) {
  throw "refusing unexpected ProgramData cleanup target: $configDir"
}

function Invoke-CheckedProcess {
  param(
    [Parameter(Mandatory = $true)][string]$Path,
    [Parameter(Mandatory = $true)][string[]]$Arguments
  )
  $process = Start-Process -FilePath $Path -ArgumentList $Arguments -Wait -PassThru
  if ($process.ExitCode -ne 0) {
    throw "$Path exited with code $($process.ExitCode)"
  }
}

function Assert-ServiceAcl {
  param([Parameter(Mandatory = $true)][string]$Path)

  $serviceSID = (New-Object System.Security.Principal.NTAccount(
    "NT SERVICE\gpt-load"
  )).Translate([System.Security.Principal.SecurityIdentifier]).Value
  $administratorsSID = "S-1-5-32-544"
  $localServiceSID = "S-1-5-19"
  $acl = Get-Acl $Path
  if (-not $acl.AreAccessRulesProtected) {
    throw "service path inherits a DACL: $Path"
  }
  $actual = @()
  foreach ($rule in $acl.Access) {
    if ($rule.AccessControlType -ne
        [System.Security.AccessControl.AccessControlType]::Allow) {
      throw "service path has a deny rule: $Path"
    }
    $sid = $rule.IdentityReference.Translate(
      [System.Security.Principal.SecurityIdentifier]
    ).Value
    if ($sid -eq $localServiceSID) {
      throw "service path grants the shared LocalService SID: $Path"
    }
    if ($sid -ne $serviceSID -and $sid -ne $administratorsSID) {
      throw "service path grants an unexpected SID $sid`: $Path"
    }
    $actual += $sid
  }
  foreach ($requiredSID in @($serviceSID, $administratorsSID)) {
    if ($actual -notcontains $requiredSID) {
      throw "service path is missing SID $requiredSID`: $Path"
    }
  }
}

try {
  Invoke-CheckedProcess -Path $setup -Arguments @(
    "/VERYSILENT",
    "/SUPPRESSMSGBOXES",
    "/NORESTART",
    "/DIR=$installDir"
  )

  if (-not (Test-Path $installedBinary)) { throw "installed binary is missing" }
  if (-not (Test-Path $uninstaller)) { throw "uninstaller is missing" }
  if (-not (Test-Path $desktopShortcut)) { throw "desktop shortcut is missing" }
  if (-not (Test-Path $startMenuShortcut)) { throw "Start Menu shortcut is missing" }
  $portableHash = (Get-FileHash -Algorithm SHA256 $portableBinary).Hash.ToLowerInvariant()
  $installedHash = (Get-FileHash -Algorithm SHA256 $installedBinary).Hash.ToLowerInvariant()
  if ($installedHash -ne $portableHash) {
    throw "installed binary differs from the portable release binary"
  }

  $service = Get-Service -Name $serviceName -ErrorAction Stop
  if ($service.Status -ne "Running") {
    throw "installed service status = $($service.Status), want Running"
  }
  $serviceConfig = Get-CimInstance Win32_Service -Filter "Name='$serviceName'"
  if ($serviceConfig.StartName -ine "NT AUTHORITY\LocalService" -or
      $serviceConfig.StartMode -ine "Auto") {
    throw "unexpected service account/start mode: $($serviceConfig.StartName)/$($serviceConfig.StartMode)"
  }

  $health = $null
  for ($attempt = 0; $attempt -lt 80; $attempt++) {
    try {
      $health = Invoke-RestMethod "http://127.0.0.1:3001/health"
      break
    } catch {
      Start-Sleep -Milliseconds 250
    }
  }
  if ($null -eq $health) { throw "installed service health check timed out" }
  if ($health.version -ne $releaseVersion) { throw "installed service version mismatch" }

  $authFile = Join-Path $dataDir "auth.key"
  $encryptionFile = Join-Path $dataDir "encryption.key"
  foreach ($path in @($configDir, $dataDir, $authFile, $encryptionFile)) {
    if (-not (Test-Path $path)) { throw "installed service path is missing: $path" }
    Assert-ServiceAcl -Path $path
  }
  $authKey = (Get-Content $authFile -Raw).Trim()
  $headers = @{ Authorization = "Bearer $authKey" }
  Invoke-RestMethod "http://127.0.0.1:3001/api/system/info" -Headers $headers | Out-Null

  & $installedBinary service stop
  if ($LASTEXITCODE -ne 0) { throw "service stop failed with code $LASTEXITCODE" }
  if ((Get-Service -Name $serviceName).Status -ne "Stopped") {
    throw "service did not stop cleanly"
  }

  Invoke-CheckedProcess -Path $uninstaller -Arguments @(
    "/VERYSILENT",
    "/SUPPRESSMSGBOXES",
    "/NORESTART"
  )
  if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
    throw "service remains installed after uninstall"
  }
  if (-not (Test-Path $dataDir)) {
    throw "uninstall removed persistent service data"
  }

  $afterHash = (Get-FileHash -Algorithm SHA256 $setup).Hash.ToLowerInvariant()
  if ($afterHash -ne $expectedHash) { throw "Windows setup checksum mismatch after execution" }
} finally {
  if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
    if (Test-Path $installedBinary) {
      & $installedBinary service stop 2>$null
      & $installedBinary service uninstall 2>$null
    } else {
      & sc.exe stop $serviceName 2>$null | Out-Null
      & sc.exe delete $serviceName 2>$null | Out-Null
    }
  }
  if (Test-Path $uninstaller) {
    Start-Process -FilePath $uninstaller -ArgumentList @(
      "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"
    ) -Wait -ErrorAction SilentlyContinue | Out-Null
  }
  Remove-Item -Recurse -Force $installDir -ErrorAction SilentlyContinue
  Remove-Item -Recurse -Force $configDir -ErrorAction SilentlyContinue
}
