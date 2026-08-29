$ErrorActionPreference = "Stop"

$binary = $env:CI_WINDOWS_SERVICE_BINARY
if ([string]::IsNullOrWhiteSpace($binary) -or -not (Test-Path $binary)) {
  throw "CI_WINDOWS_SERVICE_BINARY must name the built Windows binary"
}
$binary = [System.IO.Path]::GetFullPath($binary)
$serviceName = "gpt-load"
$port = 39114
$installDir = Join-Path $env:ProgramFiles "GPT-Load"
$serviceBinary = Join-Path $installDir "gpt-load.exe"
$installOwnerMarker = Join-Path $installDir ".service-smoke-owner"
$installOwnerToken = [guid]::NewGuid().ToString("N")
$programData = [Environment]::GetFolderPath("CommonApplicationData")
$configDir = Join-Path $programData "GPT-Load"
$dataDir = Join-Path $configDir "data"
$envFile = Join-Path $configDir ".env"
$dataOwnerMarker = Join-Path $configDir ".service-smoke-owner"

if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
  throw "refusing pre-existing Windows service: $serviceName"
}
if (Test-Path $installDir) {
  throw "refusing pre-existing installation directory: $installDir"
}
if (Test-Path $configDir) {
  throw "refusing pre-existing ProgramData directory: $configDir"
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
  $actual = @($acl.Access | ForEach-Object {
    $_.IdentityReference.Translate(
      [System.Security.Principal.SecurityIdentifier]
    ).Value
  })
  if ($actual -contains $localServiceSID) {
    throw "service path grants the shared LocalService SID: $Path"
  }
  foreach ($requiredSID in @($serviceSID, $administratorsSID)) {
    if ($actual -notcontains $requiredSID) {
      throw "service path is missing SID $requiredSID`: $Path"
    }
  }
  foreach ($sid in $actual) {
    if ($sid -ne $serviceSID -and $sid -ne $administratorsSID) {
      throw "service path grants an unexpected SID $sid`: $Path"
    }
  }
}

try {
  New-Item -ItemType Directory -Path $installDir | Out-Null
  [System.IO.File]::WriteAllText($installOwnerMarker, $installOwnerToken)
  Copy-Item -Path $binary -Destination $serviceBinary
  New-Item -ItemType Directory -Path $configDir | Out-Null
  [System.IO.File]::WriteAllText($dataOwnerMarker, $installOwnerToken)
  @(
    "HOST=127.0.0.1",
    "PORT=$port",
    "LOG_FORMAT=json"
  ) | Set-Content -Path $envFile -Encoding utf8NoBOM

  & $serviceBinary service install
  if ($LASTEXITCODE -ne 0) { throw "service install failed with code $LASTEXITCODE" }

  & $serviceBinary service start
  if ($LASTEXITCODE -ne 0) { throw "service start failed with code $LASTEXITCODE" }

  $health = $null
  for ($attempt = 0; $attempt -lt 80; $attempt++) {
    try {
      $health = Invoke-RestMethod "http://127.0.0.1:$port/health"
      break
    } catch {
      Start-Sleep -Milliseconds 250
    }
  }
  if ($null -eq $health) { throw "Windows service health check timed out" }

  $authFile = Join-Path $dataDir "auth.key"
  $encryptionFile = Join-Path $dataDir "encryption.key"
  foreach ($path in @($configDir, $dataDir, $authFile, $encryptionFile)) {
    if (-not (Test-Path $path)) { throw "Windows service path is missing: $path" }
    Assert-ServiceAcl -Path $path
  }
  $authKey = (Get-Content $authFile -Raw).Trim()
  Invoke-RestMethod "http://127.0.0.1:$port/api/system/info" -Headers @{
    Authorization = "Bearer $authKey"
  } | Out-Null

  & $serviceBinary service stop
  if ($LASTEXITCODE -ne 0) { throw "service stop failed with code $LASTEXITCODE" }
  if ((Get-Service -Name $serviceName).Status -ne "Stopped") {
    throw "Windows service did not stop cleanly"
  }
  & $serviceBinary service uninstall
  if ($LASTEXITCODE -ne 0) { throw "service uninstall failed with code $LASTEXITCODE" }
  if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
    throw "Windows service remains installed"
  }
} finally {
  if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
    $ownsInstall = (Test-Path $installOwnerMarker) -and
      ((Get-Content $installOwnerMarker -Raw).Trim() -eq $installOwnerToken)
    if ($ownsInstall -and (Test-Path $serviceBinary)) {
      & $serviceBinary service stop 2>$null
      & $serviceBinary service uninstall 2>$null
    }
    if ($ownsInstall -and
        (Get-Service -Name $serviceName -ErrorAction SilentlyContinue)) {
      & sc.exe stop $serviceName 2>$null | Out-Null
      & sc.exe delete $serviceName 2>$null | Out-Null
    }
  }
  if ((Test-Path $installOwnerMarker) -and
      ((Get-Content $installOwnerMarker -Raw).Trim() -eq $installOwnerToken)) {
    Remove-Item -Recurse -Force $installDir -ErrorAction SilentlyContinue
  }
  if ((Test-Path $dataOwnerMarker) -and
      ((Get-Content $dataOwnerMarker -Raw).Trim() -eq $installOwnerToken)) {
    Remove-Item -Recurse -Force $configDir -ErrorAction SilentlyContinue
  }
}
