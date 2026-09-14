function Enter-WindowsSmoke {
  param([string]$InstallDir, [string]$ConfigDir)

  # 固定服务名只能由一个 smoke 使用；进程退出后系统自动释放互斥锁。
  $mutex = [System.Threading.Mutex]::new($false, "Global\GPTLoad-Windows-Smoke")
  $acquired = $false
  try {
    try { $acquired = $mutex.WaitOne(0) }
    catch [System.Threading.AbandonedMutexException] { $acquired = $true }
    if (-not $acquired) { throw "another Windows smoke is running" }

    $owners = @()
    foreach ($directory in @($InstallDir, $ConfigDir)) {
      if (-not (Test-Path -LiteralPath $directory)) { continue }
      if ((Get-Item -LiteralPath $directory -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw "refusing linked Windows smoke directory: $directory"
      }
      foreach ($name in @('.service-smoke-owner', '.installer-smoke-owner')) {
        $path = Join-Path $directory $name
        if (-not (Test-Path -LiteralPath $path)) { continue }
        $item = Get-Item -LiteralPath $path -Force
        if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
          throw "refusing invalid Windows smoke marker: $path"
        }
        $token = (Get-Content -LiteralPath $path -Raw).Trim()
        if ($token -notmatch '^[0-9a-fA-F]{32}$') { throw "invalid Windows smoke owner: $path" }
        $owners += @{ Name = $name; Token = $token }
      }
    }
    # 无标记时保留现场，让调用方的原有 preflight 拒绝真实安装。
    if ($owners.Count -eq 0) { return $mutex }
    $owner = $owners[0]
    foreach ($other in $owners) {
      if ($other.Name -ne $owner.Name -or $other.Token -ne $owner.Token) {
        throw "conflicting Windows smoke owners"
      }
    }
    $installer = $owner.Name -eq '.installer-smoke-owner'
    if ($installer) {
      # 安装器会删除、重建安装目录；其归属凭据保存在卸载时保留的 ProgramData。
      $proof = Join-Path $ConfigDir 'data/installer-smoke-failure.txt'
      if (-not (Test-Path -LiteralPath $proof) -or
          ((Get-Content -LiteralPath $proof -Raw).Trim() -ne $owner.Token)) {
        throw "missing installer smoke ownership proof"
      }
    } else {
      foreach ($directory in @($InstallDir, $ConfigDir)) {
        if ((Test-Path -LiteralPath $directory) -and
            -not (Test-Path -LiteralPath (Join-Path $directory $owner.Name))) {
          throw "unmarked Windows smoke directory: $directory"
        }
      }
    }

    $binary = Join-Path $InstallDir 'gpt-load.exe'
    $service = Get-CimInstance Win32_Service -Filter "Name='gpt-load'" -ErrorAction Stop
    if ($null -ne $service) {
      if ($service.PathName -ine ('"' + $binary + '" service run') -or
          $service.StartName -ine 'NT AUTHORITY\LocalService') {
        throw "refusing Windows service with unexpected configuration"
      }
      $controller = Get-Service -Name 'gpt-load' -ErrorAction Stop
      try {
        if ($controller.Status -ne 'Stopped') {
          $controller.Stop()
          $controller.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(15))
        }
      } finally { $controller.Dispose() }
      & sc.exe delete 'gpt-load' | Out-Null
      if ($LASTEXITCODE -ne 0) { throw "failed to remove stale Windows smoke service" }
    }

    # 只回收已确认归属的固定目标，不扫描或清理其他任务、服务和目录。
    if (Test-Path -LiteralPath $InstallDir) { Remove-Item -LiteralPath $InstallDir -Recurse -Force }
    if ($installer) {
      $paths = @(
        ([IO.Path]::Combine([Environment]::GetFolderPath('CommonDesktopDirectory'), 'GPT-Load.url')),
        ([IO.Path]::Combine([Environment]::GetFolderPath('CommonPrograms'), 'GPT-Load/GPT-Load.url')),
        'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\{E5A127DE-2676-4F6C-B763-CF53C6271883}_is1'
      )
      foreach ($path in $paths) {
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path -Recurse -Force }
      }
    }
    # 最后删除归属标记所在目录；中途失败时保留下一次恢复所需的证据。
    if (Test-Path -LiteralPath $ConfigDir) { Remove-Item -LiteralPath $ConfigDir -Recurse -Force }
    return $mutex
  } catch {
    if ($acquired) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
    throw
  }
}
