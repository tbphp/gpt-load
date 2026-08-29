#ifndef AppVersion
  #define AppVersion "dev"
#endif
#ifndef SourceBinary
  #define SourceBinary "..\..\release\gpt-load-windows-amd64.exe"
#endif
#ifndef OutputDir
  #define OutputDir "..\..\release"
#endif

[Setup]
AppId={{E5A127DE-2676-4F6C-B763-CF53C6271883}
AppName=GPT-Load
AppVersion={#AppVersion}
AppPublisher=GPT-Load
AppPublisherURL=https://github.com/tbphp/gpt-load
AppSupportURL=https://github.com/tbphp/gpt-load/issues
DefaultDirName={autopf}\GPT-Load
DisableDirPage=yes
UsePreviousAppDir=no
DefaultGroupName=GPT-Load
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#OutputDir}
OutputBaseFilename=gpt-load-windows-setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\gpt-load.exe
CloseApplications=no
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"

[CustomMessages]
english.AuthKeyCaption=Management key
english.AuthKeyDescription=Save this key before opening GPT-Load.
english.AuthKeySubCaption=Enter this AUTH_KEY on the GPT-Load sign-in page. The key remains protected in the service data directory.
english.AuthKeyUnavailable=The management key could not be read. Open %1 with administrator privileges after installation.
english.ServiceCommandFailed=Windows service command failed: %1 (exit code %2)
english.UpgradeBackupFailed=Could not back up the existing GPT-Load executable to %1.
english.InstallFailedCaption=GPT-Load setup could not be completed
english.InstallFailedDescription=%1 Setup restored the previous GPT-Load program and service state where possible.
chinesesimplified.AuthKeyCaption=管理密钥
chinesesimplified.AuthKeyDescription=打开 GPT-Load 前请先保存此密钥。
chinesesimplified.AuthKeySubCaption=请在 GPT-Load 登录页输入此 AUTH_KEY。密钥会继续安全保存在服务数据目录中。
chinesesimplified.AuthKeyUnavailable=无法读取管理密钥。安装后请使用管理员权限打开 %1。
chinesesimplified.ServiceCommandFailed=Windows 服务命令失败：%1（退出码 %2）
chinesesimplified.UpgradeBackupFailed=无法将已有 GPT-Load 程序备份到 %1。
chinesesimplified.InstallFailedCaption=无法完成 GPT-Load 安装
chinesesimplified.InstallFailedDescription=%1 安装程序已尽力恢复此前的 GPT-Load 程序和服务状态。

[Files]
Source: "{#SourceBinary}"; DestDir: "{app}"; DestName: "gpt-load.exe"; Flags: ignoreversion

[Icons]
Name: "{group}\GPT-Load"; Filename: "http://127.0.0.1:3001"; IconFilename: "{app}\gpt-load.exe"
Name: "{commondesktop}\GPT-Load"; Filename: "http://127.0.0.1:3001"; IconFilename: "{app}\gpt-load.exe"

[Run]
Filename: "http://127.0.0.1:3001"; Description: "{cm:LaunchProgram,GPT-Load}"; Flags: postinstall shellexec skipifsilent; Check: CanLaunchManagementPage

[Code]
var
  AuthKeyPage: TOutputMsgMemoWizardPage;
  ShowAuthKeyPage: Boolean;
  InstallPrepared: Boolean;
  InstallCompleted: Boolean;
  InstallationFailed: Boolean;
  InstallationFailureMessage: String;
  PreviousBinaryPath: String;
  PreviousBinaryBackedUp: Boolean;
  PreviousServiceExisted: Boolean;
  PreviousServiceWasRunning: Boolean;
  PreviousInstallationRestored: Boolean;

function InitializeSetup(): Boolean;
begin
  ShowAuthKeyPage := not FileExists(
    ExpandConstant('{commonappdata}\GPT-Load\data\auth.key')
  );
  Result := True;
end;

function TryRunServiceCommand(
  const BinaryPath: String;
  const Arguments: String;
  var ErrorMessage: String
): Boolean;
var
  ResultCode: Integer;
begin
  ResultCode := -1;
  Result := Exec(
    BinaryPath,
    Arguments,
    ExpandConstant('{app}'),
    SW_HIDE,
    ewWaitUntilTerminated,
    ResultCode
  );
  if (not Result) or (ResultCode <> 0) then
  begin
    ErrorMessage := FmtMessage(CustomMessage('ServiceCommandFailed'), [Arguments, IntToStr(ResultCode)]);
    Result := False;
  end;
end;

procedure RunRequiredServiceCommand(const Arguments: String);
var
  ErrorMessage: String;
begin
  if not TryRunServiceCommand(
    ExpandConstant('{app}\gpt-load.exe'),
    Arguments,
    ErrorMessage
  ) then
    RaiseException(ErrorMessage);
end;

function BestEffortServiceCommand(
  const BinaryPath: String;
  const Arguments: String
): Boolean;
var
  ErrorMessage: String;
begin
  Result := TryRunServiceCommand(BinaryPath, Arguments, ErrorMessage);
  if not Result then
    Log('GPT-Load recovery warning: ' + ErrorMessage);
end;

procedure QueryPreviousServiceState(const BinaryPath: String);
var
  I: Integer;
  Output: TExecOutput;
  ResultCode: Integer;
begin
  PreviousServiceExisted := False;
  PreviousServiceWasRunning := False;
  if not ExecAndCaptureOutput(
    BinaryPath,
    'service status',
    ExpandConstant('{app}'),
    SW_HIDE,
    ewWaitUntilTerminated,
    ResultCode,
    Output
  ) then
    Exit;
  if Output.Error or (ResultCode <> 0) then
    Exit;

  PreviousServiceExisted := True;
  for I := 0 to GetArrayLength(Output.StdOut) - 1 do
    if CompareText(Trim(Output.StdOut[I]), 'running') = 0 then
      PreviousServiceWasRunning := True;
end;

function RestorePreviousInstallation(): Boolean;
var
  AppBinary: String;
begin
  Result := True;
  if not InstallPrepared then
    Exit;

  AppBinary := ExpandConstant('{app}\gpt-load.exe');
  if PreviousServiceExisted then
  begin
    if FileExists(AppBinary) and
       (not BestEffortServiceCommand(AppBinary, 'service stop')) then
      Result := False;
  end
  else if FileExists(AppBinary) then
  begin
    if not BestEffortServiceCommand(AppBinary, 'service stop') then
      Result := False;
    if not BestEffortServiceCommand(AppBinary, 'service uninstall') then
      Result := False;
  end;

  if PreviousBinaryBackedUp then
  begin
    if not FileCopy(PreviousBinaryPath, AppBinary, False) then
    begin
      Log('GPT-Load recovery warning: could not restore the previous executable');
      Result := False;
      Exit;
    end;
  end;

  if PreviousServiceExisted then
  begin
    if not BestEffortServiceCommand(AppBinary, 'service install') then
      Result := False;
    if PreviousServiceWasRunning and
       (not BestEffortServiceCommand(AppBinary, 'service start')) then
      Result := False;
  end;
end;

procedure MarkInstallationFailed(const ErrorMessage: String);
begin
  InstallationFailed := True;
  InstallationFailureMessage := FmtMessage(CustomMessage('InstallFailedDescription'), [ErrorMessage]);
  PreviousInstallationRestored := RestorePreviousInstallation();
  SuppressibleMsgBox(
    InstallationFailureMessage,
    mbCriticalError,
    MB_OK,
    IDOK
  );
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  AppBinary: String;
  ErrorMessage: String;
begin
  Result := '';
  if InstallPrepared then
    Exit;

  AppBinary := ExpandConstant('{app}\gpt-load.exe');
  if FileExists(AppBinary) then
  begin
    PreviousBinaryPath := ExpandConstant('{tmp}\gpt-load.exe.previous');
    DeleteFile(PreviousBinaryPath);
    if not FileCopy(AppBinary, PreviousBinaryPath, True) then
    begin
      Result := FmtMessage(CustomMessage('UpgradeBackupFailed'), [PreviousBinaryPath]);
      Exit;
    end;
    PreviousBinaryBackedUp := True;
    QueryPreviousServiceState(AppBinary);
  end;

  if PreviousServiceExisted and
     (not TryRunServiceCommand(AppBinary, 'service stop', ErrorMessage)) then
  begin
    if PreviousServiceWasRunning then
      BestEffortServiceCommand(AppBinary, 'service start');
    Result := ErrorMessage;
    Exit;
  end;

  InstallPrepared := True;
end;

procedure InitializeWizard;
begin
  AuthKeyPage := CreateOutputMsgMemoPage(
    wpInstalling,
    CustomMessage('AuthKeyCaption'),
    CustomMessage('AuthKeyDescription'),
    CustomMessage('AuthKeySubCaption'),
    ''
  );
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := (PageID = AuthKeyPage.ID) and
    ((not ShowAuthKeyPage) or InstallationFailed);
end;

function CanLaunchManagementPage(): Boolean;
begin
  Result := not InstallationFailed;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  AppBinary: String;
  ErrorMessage: String;
begin
  if CurStep = ssPostInstall then
  begin
    AppBinary := ExpandConstant('{app}\gpt-load.exe');
    if not PreviousServiceExisted then
      QueryPreviousServiceState(AppBinary);
    if not TryRunServiceCommand(AppBinary, 'service stop', ErrorMessage) then
      MarkInstallationFailed(ErrorMessage)
    else if not TryRunServiceCommand(AppBinary, 'service install', ErrorMessage) then
      MarkInstallationFailed(ErrorMessage)
    else if not TryRunServiceCommand(AppBinary, 'service start', ErrorMessage) then
      MarkInstallationFailed(ErrorMessage);
  end
  else if (CurStep = ssDone) and (not InstallationFailed) then
  begin
    InstallCompleted := True;
    if PreviousBinaryBackedUp then
      DeleteFile(PreviousBinaryPath);
  end;
end;

procedure CurPageChanged(CurPageID: Integer);
var
  AuthKey: AnsiString;
  AuthKeyPath: String;
begin
  if CurPageID = AuthKeyPage.ID then
  begin
    AuthKeyPath := ExpandConstant('{commonappdata}\GPT-Load\data\auth.key');
    if LoadStringFromFile(AuthKeyPath, AuthKey) then
      AuthKeyPage.RichEditViewer.Lines.Text := Trim(String(AuthKey))
    else
      AuthKeyPage.RichEditViewer.Lines.Text :=
        FmtMessage(CustomMessage('AuthKeyUnavailable'), [AuthKeyPath]);
  end
  else if (CurPageID = wpFinished) and InstallationFailed then
  begin
    WizardForm.FinishedHeadingLabel.Caption :=
      CustomMessage('InstallFailedCaption');
    WizardForm.FinishedLabel.Caption := InstallationFailureMessage;
    WizardForm.RunList.Visible := False;
  end;
end;

function GetCustomSetupExitCode(): Integer;
begin
  if InstallationFailed then
    Result := 10
  else
    Result := 0;
end;

procedure DeinitializeSetup();
begin
  if InstallPrepared and (not InstallCompleted) and
     (not PreviousInstallationRestored) then
    PreviousInstallationRestored := RestorePreviousInstallation();
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
  begin
    RunRequiredServiceCommand('service stop');
    RunRequiredServiceCommand('service uninstall');
  end;
end;
