$ErrorActionPreference = "Stop"

$binary = $env:RELEASE_SMOKE_BINARY
$checksumFile = $env:RELEASE_SMOKE_CHECKSUM_FILE
$releaseVersion = $env:RELEASE_VERSION
$port = if ($env:RELEASE_SMOKE_PORT) { $env:RELEASE_SMOKE_PORT } else { "39113" }
foreach ($required in @($binary, $checksumFile, $releaseVersion)) {
  if ([string]::IsNullOrWhiteSpace($required)) {
    throw "RELEASE_SMOKE_BINARY, RELEASE_SMOKE_CHECKSUM_FILE, and RELEASE_VERSION are required"
  }
}

function Assert-CurrentUserOnlyAcl {
  param(
    [Parameter(Mandatory = $true)][string]$Path,
    [Parameter(Mandatory = $true)]
    [System.Security.Principal.SecurityIdentifier]$CurrentSid,
    [Parameter(Mandatory = $true)][bool]$RequireProtected
  )

  $acl = Get-Acl $Path
  if ($RequireProtected -and -not $acl.AreAccessRulesProtected) {
    throw "managed path DACL inherits rules: $Path"
  }
  $rules = @($acl.Access)
  if ($rules.Count -eq 0) {
    throw "managed path DACL has no access rule: $Path"
  }
  $allowRules = @($rules | Where-Object {
    $_.AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Allow
  })
  if ($allowRules.Count -eq 0) {
    throw "managed path DACL has no allow rule: $Path"
  }
  if (-not $acl.AreAccessRulesProtected) {
    $inheritedRules = @($rules | Where-Object { $_.IsInherited })
    if ($inheritedRules.Count -eq 0) {
      throw "managed path DACL is neither protected nor inherited: $Path"
    }
  }
  foreach ($rule in $rules) {
    $sid = $rule.IdentityReference.Translate(
      [System.Security.Principal.SecurityIdentifier]
    )
    if ($sid.Value -ne $CurrentSid.Value) {
      throw "managed path DACL references another principal: $Path"
    }
  }
}

Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;

public sealed class ReleaseNativeProcess : IDisposable
{
    private const uint CREATE_NEW_PROCESS_GROUP = 0x00000200;
    private const uint CTRL_BREAK_EVENT = 1;
    private const int ERROR_ACCESS_DENIED = 5;
    private const uint WAIT_OBJECT_0 = 0x00000000;
    private const uint WAIT_TIMEOUT = 0x00000102;
    private const uint WAIT_FAILED = 0xffffffff;

    private IntPtr processHandle;

    private ReleaseNativeProcess(IntPtr processHandle, int processId)
    {
        this.processHandle = processHandle;
        Id = processId;
    }

    public int Id { get; private set; }

    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct STARTUPINFO
    {
        public uint cb;
        public string lpReserved;
        public string lpDesktop;
        public string lpTitle;
        public uint dwX;
        public uint dwY;
        public uint dwXSize;
        public uint dwYSize;
        public uint dwXCountChars;
        public uint dwYCountChars;
        public uint dwFillAttribute;
        public uint dwFlags;
        public ushort wShowWindow;
        public ushort cbReserved2;
        public IntPtr lpReserved2;
        public IntPtr hStdInput;
        public IntPtr hStdOutput;
        public IntPtr hStdError;
    }

    [StructLayout(LayoutKind.Sequential)]
    private struct PROCESS_INFORMATION
    {
        public IntPtr hProcess;
        public IntPtr hThread;
        public uint dwProcessId;
        public uint dwThreadId;
    }

    [DllImport(
        "kernel32.dll",
        CharSet = CharSet.Unicode,
        ExactSpelling = true,
        SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CreateProcessW(
        string applicationName,
        StringBuilder commandLine,
        IntPtr processAttributes,
        IntPtr threadAttributes,
        [MarshalAs(UnmanagedType.Bool)] bool inheritHandles,
        uint creationFlags,
        IntPtr environment,
        string currentDirectory,
        ref STARTUPINFO startupInfo,
        out PROCESS_INFORMATION processInformation);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GenerateConsoleCtrlEvent(
        uint ctrlEvent,
        uint processGroupId);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern uint WaitForSingleObject(
        IntPtr handle,
        uint milliseconds);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetExitCodeProcess(
        IntPtr process,
        out uint exitCode);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern uint GetConsoleCP();

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool AllocConsole();

    [DllImport("kernel32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CloseHandle(IntPtr handle);

    private static void EnsureConsole()
    {
        if (GetConsoleCP() != 0 || AllocConsole())
        {
            return;
        }
        var error = Marshal.GetLastWin32Error();
        if (error != ERROR_ACCESS_DENIED || GetConsoleCP() == 0)
        {
            throw new Win32Exception(error);
        }
    }

    public static ReleaseNativeProcess Start(string path)
    {
        EnsureConsole();
        var startupInfo = new STARTUPINFO {
            cb = (uint)Marshal.SizeOf<STARTUPINFO>()
        };
        var commandLine = new StringBuilder("\"" + path + "\"");
        PROCESS_INFORMATION processInformation;
        if (!CreateProcessW(
            path,
            commandLine,
            IntPtr.Zero,
            IntPtr.Zero,
            false,
            CREATE_NEW_PROCESS_GROUP,
            IntPtr.Zero,
            null,
            ref startupInfo,
            out processInformation))
        {
            throw new Win32Exception(Marshal.GetLastWin32Error());
        }
        CloseHandle(processInformation.hThread);
        return new ReleaseNativeProcess(
            processInformation.hProcess,
            (int)processInformation.dwProcessId);
    }

    public void SendCtrlBreak()
    {
        if (!GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, (uint)Id))
        {
            throw new Win32Exception(Marshal.GetLastWin32Error());
        }
    }

    public bool WaitForExit(int milliseconds)
    {
        EnsureNotDisposed();
        var result = WaitForSingleObject(processHandle, (uint)milliseconds);
        if (result == WAIT_OBJECT_0)
        {
            return true;
        }
        if (result == WAIT_TIMEOUT)
        {
            return false;
        }
        if (result == WAIT_FAILED)
        {
            throw new Win32Exception(Marshal.GetLastWin32Error());
        }
        throw new InvalidOperationException("unexpected process wait result: " + result);
    }

    public bool HasExited
    {
        get { return WaitForExit(0); }
    }

    public int GetExitCode()
    {
        EnsureNotDisposed();
        uint exitCode;
        if (!GetExitCodeProcess(processHandle, out exitCode))
        {
            throw new Win32Exception(Marshal.GetLastWin32Error());
        }
        return unchecked((int)exitCode);
    }

    public void Dispose()
    {
        if (processHandle == IntPtr.Zero)
        {
            return;
        }
        CloseHandle(processHandle);
        processHandle = IntPtr.Zero;
        GC.SuppressFinalize(this);
    }

    private void EnsureNotDisposed()
    {
        if (processHandle == IntPtr.Zero)
        {
            throw new ObjectDisposedException("ReleaseNativeProcess");
        }
    }
}
'@

$filename = [System.IO.Path]::GetFileName($binary)
$checksumLine = Get-Content $checksumFile |
  Where-Object { $_ -match "\s$([regex]::Escape($filename))$" }
if (-not $checksumLine) { throw "missing checksum" }
$expectedHash = ($checksumLine -split "\s+")[0].ToLowerInvariant()
$beforeHash = (Get-FileHash -Algorithm SHA256 $binary).Hash.ToLowerInvariant()
if ($beforeHash -ne $expectedHash) { throw "checksum mismatch before execution" }

& $binary help | Out-Null
if ($LASTEXITCODE -ne 0) { throw "help failed" }

$dataDir = Join-Path $env:RUNNER_TEMP "gpt-load-native-smoke-$([guid]::NewGuid())"
New-Item -ItemType Directory -Path $dataDir | Out-Null
$env:DATA_DIR = $dataDir
$env:PORT = $port
$process = [ReleaseNativeProcess]::Start($binary)
try {
  $health = $null
  for ($attempt = 0; $attempt -lt 80; $attempt++) {
    try {
      $health = Invoke-RestMethod "http://127.0.0.1:$port/health"
      break
    } catch {
      Start-Sleep -Milliseconds 250
    }
  }
  if ($null -eq $health) { throw "health check timed out" }
  if ($health.version -ne $releaseVersion) { throw "version mismatch" }

  $authFile = Join-Path $dataDir "auth.key"
  $encryptionFile = Join-Path $dataDir "encryption.key"
  $databaseFile = Join-Path $dataDir "gpt-load.db"
  foreach ($file in @($authFile, $encryptionFile, $databaseFile)) {
    if (-not (Test-Path $file)) { throw "missing generated asset: $file" }
  }

  $authKey = (Get-Content $authFile -Raw).Trim()
  Invoke-WebRequest "http://127.0.0.1:$port/" -UseBasicParsing | Out-Null
  $headers = @{ Authorization = "Bearer $authKey" }
  $writeHeaders = @{
    Authorization = "Bearer $authKey"
    "Idempotency-Key" = [guid]::NewGuid().ToString().ToLowerInvariant()
  }
  Invoke-RestMethod "http://127.0.0.1:$port/api/usage?range=24h" -Headers $headers | Out-Null
  Invoke-RestMethod "http://127.0.0.1:$port/api/model-prices" -Headers $headers | Out-Null
  Invoke-RestMethod `
    "http://127.0.0.1:$port/api/access-keys" `
    -Method Post `
    -Headers $writeHeaders `
    -ContentType "application/json" `
    -Body '{"name":"Release Native Smoke Access"}' | Out-Null

  $walFile = "$databaseFile-wal"
  $shmFile = "$databaseFile-shm"
  foreach ($file in @($walFile, $shmFile)) {
    if (-not (Test-Path $file)) { throw "missing SQLite recovery file: $file" }
  }
  $currentSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User
  foreach ($target in @(
    @{ Path = $dataDir; RequireProtected = $true },
    @{ Path = $authFile; RequireProtected = $true },
    @{ Path = $encryptionFile; RequireProtected = $true },
    @{ Path = $databaseFile; RequireProtected = $false },
    @{ Path = $walFile; RequireProtected = $false },
    @{ Path = $shmFile; RequireProtected = $false }
  )) {
    Assert-CurrentUserOnlyAcl `
      -Path $target.Path `
      -CurrentSid $currentSid `
      -RequireProtected $target.RequireProtected
  }

  $process.SendCtrlBreak()
  if (-not $process.WaitForExit(15000)) {
    throw "graceful shutdown timed out"
  }
  $exitCode = $process.GetExitCode()
  if ($exitCode -ne 0) {
    throw "graceful shutdown exited with code $exitCode"
  }

  $afterHash = (Get-FileHash -Algorithm SHA256 $binary).Hash.ToLowerInvariant()
  if ($afterHash -ne $expectedHash) { throw "checksum mismatch after execution" }
} finally {
  # Stop-Process is failure cleanup only; success is proven by CTRL_BREAK and exit code 0.
  if (-not $process.HasExited) {
    Stop-Process -Id $process.Id -ErrorAction SilentlyContinue
    $process.WaitForExit(5000) | Out-Null
  }
  $process.Dispose()
  Remove-Item -Recurse -Force $dataDir -ErrorAction SilentlyContinue
}
