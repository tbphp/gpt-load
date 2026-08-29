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
Name: "chinesesimplified"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"

[CustomMessages]
english.AuthKeyCaption=Management key
english.AuthKeyDescription=Save this key before opening GPT-Load.
english.AuthKeySubCaption=Enter this AUTH_KEY on the GPT-Load sign-in page. The key remains protected in the service data directory.
english.AuthKeyUnavailable=The management key could not be read. Open %1 with administrator privileges after installation.
english.ServiceCommandFailed=Windows service command failed: %1 (exit code %2)
chinesesimplified.AuthKeyCaption=管理密钥
chinesesimplified.AuthKeyDescription=打开 GPT-Load 前请先保存此密钥。
chinesesimplified.AuthKeySubCaption=请在 GPT-Load 登录页输入此 AUTH_KEY。密钥会继续安全保存在服务数据目录中。
chinesesimplified.AuthKeyUnavailable=无法读取管理密钥。安装后请使用管理员权限打开 %1。
chinesesimplified.ServiceCommandFailed=Windows 服务命令失败：%1（退出码 %2）

[Files]
Source: "{#SourceBinary}"; DestDir: "{app}"; DestName: "gpt-load.exe"; Flags: ignoreversion

[Icons]
Name: "{group}\GPT-Load"; Filename: "http://127.0.0.1:3001"; IconFilename: "{app}\gpt-load.exe"
Name: "{commondesktop}\GPT-Load"; Filename: "http://127.0.0.1:3001"; IconFilename: "{app}\gpt-load.exe"

[Run]
Filename: "http://127.0.0.1:3001"; Description: "{cm:LaunchProgram,GPT-Load}"; Flags: postinstall shellexec skipifsilent

[Code]
var
  AuthKeyPage: TOutputMsgMemoWizardPage;
  ShowAuthKeyPage: Boolean;

function InitializeSetup(): Boolean;
begin
  ShowAuthKeyPage := not FileExists(
    ExpandConstant('{commonappdata}\GPT-Load\data\auth.key')
  );
  Result := True;
end;

function RunRequiredServiceCommand(const Arguments: String): Boolean;
var
  ResultCode: Integer;
begin
  Result := Exec(
    ExpandConstant('{app}\gpt-load.exe'),
    Arguments,
    ExpandConstant('{app}'),
    SW_HIDE,
    ewWaitUntilTerminated,
    ResultCode
  );
  if (not Result) or (ResultCode <> 0) then
    RaiseException(FmtMessage(CustomMessage('ServiceCommandFailed'), [Arguments, IntToStr(ResultCode)]));
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  ExistingBinary: String;
begin
  Result := '';
  ExistingBinary := ExpandConstant('{app}\gpt-load.exe');
  if not FileExists(ExistingBinary) then
    Exit;
  if (not Exec(ExistingBinary, 'service stop', ExpandConstant('{app}'), SW_HIDE,
      ewWaitUntilTerminated, ResultCode)) or (ResultCode <> 0) then
    Result := FmtMessage(CustomMessage('ServiceCommandFailed'), ['service stop', IntToStr(ResultCode)]);
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
  Result := (PageID = AuthKeyPage.ID) and (not ShowAuthKeyPage);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    RunRequiredServiceCommand('service stop');
    RunRequiredServiceCommand('service install');
    RunRequiredServiceCommand('service start');
  end;
end;

procedure CurPageChanged(CurPageID: Integer);
var
  AuthKey: AnsiString;
  AuthKeyPath: String;
begin
  if CurPageID <> AuthKeyPage.ID then
    Exit;
  AuthKeyPath := ExpandConstant('{commonappdata}\GPT-Load\data\auth.key');
  if LoadStringFromFile(AuthKeyPath, AuthKey) then
    AuthKeyPage.RichEditViewer.Lines.Text := Trim(String(AuthKey))
  else
    AuthKeyPage.RichEditViewer.Lines.Text :=
      FmtMessage(CustomMessage('AuthKeyUnavailable'), [AuthKeyPath]);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
  begin
    RunRequiredServiceCommand('service stop');
    RunRequiredServiceCommand('service uninstall');
  end;
end;
