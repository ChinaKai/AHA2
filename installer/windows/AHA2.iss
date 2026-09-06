#ifndef MyAppVersion
  #define MyAppVersion "dev"
#endif
#ifndef SourceExe
  #define SourceExe "..\..\dist\aha2-windows-amd64.exe"
#endif
#ifndef OutputDir
  #define OutputDir "..\..\dist\installer"
#endif

#define AppName "AHA2"
#define ServiceName "AHA2"
#define ListenAddress "127.0.0.1:8766"

[Setup]
AppId={{6F51A727-6446-4A71-83C8-6A6E8FB61192}
AppName={#AppName}
AppVersion={#MyAppVersion}
AppPublisher=AHA2
DefaultDirName={autopf}\AHA2
DefaultGroupName=AHA2
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#OutputDir}
OutputBaseFilename=AHA2-Setup-x64
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\aha2.exe
CloseApplications=yes
RestartApplications=no

[Dirs]
Name: "{commonappdata}\AHA2"; Flags: uninsneveruninstall

[Files]
Source: "{#SourceExe}"; DestDir: "{app}"; DestName: "aha2.exe"; Flags: ignoreversion

[Icons]
Name: "{group}\AHA2"; Filename: "http://127.0.0.1:8766"
Name: "{group}\卸载 AHA2"; Filename: "{uninstallexe}"

[Run]
Filename: "http://127.0.0.1:8766"; Description: "打开 AHA2 管理页面"; Flags: postinstall shellexec skipifsilent

[Code]
const
  AHA2ServiceName = '{#ServiceName}';

function RunSC(const Arguments: String): Boolean;
var
  ResultCode: Integer;
begin
  Result := Exec(ExpandConstant('{sys}\sc.exe'), Arguments, '', SW_HIDE,
    ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

function ServiceExists(): Boolean;
begin
  Result := RunSC('query "' + AHA2ServiceName + '"');
end;

procedure RequireSC(const Arguments, FailureMessage: String);
begin
  if not RunSC(Arguments) then
    RaiseException(FailureMessage);
end;

procedure StopAHA2Service();
var
  ResultCode: Integer;
begin
  if ServiceExists() then
    Exec(ExpandConstant('{sys}\net.exe'), 'stop "' + AHA2ServiceName + '" /y',
      '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

function ServiceBinaryPath(): String;
begin
  Result := '\"' + ExpandConstant('{app}\aha2.exe') +
    '\" service run --listen {#ListenAddress} --data-dir \"' +
    ExpandConstant('{commonappdata}\AHA2') + '\"';
end;

procedure ConfigureAndStartAHA2Service();
var
  BinaryPath: String;
begin
  BinaryPath := ServiceBinaryPath();
  if not ServiceExists() then
    RequireSC('create "' + AHA2ServiceName + '" start= auto binPath= "' + BinaryPath + '"',
      '无法注册 AHA2 Windows 服务。');
  RequireSC('config "' + AHA2ServiceName + '" start= delayed-auto binPath= "' + BinaryPath + '"',
    '无法配置 AHA2 Windows 服务。');
  RequireSC('description "' + AHA2ServiceName + '" "AHA2 local AI task and agent control plane"',
    '无法配置 AHA2 服务描述。');
  RequireSC('failure "' + AHA2ServiceName + '" reset= 86400 actions= restart/5000/restart/15000/restart/60000',
    '无法配置 AHA2 服务失败恢复。');
  RequireSC('failureflag "' + AHA2ServiceName + '" 1',
    '无法启用 AHA2 服务失败恢复。');
  RequireSC('start "' + AHA2ServiceName + '"', '无法启动 AHA2 Windows 服务。');
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssInstall then
    StopAHA2Service()
  else if CurStep = ssPostInstall then
    ConfigureAndStartAHA2Service();
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
  begin
    StopAHA2Service();
    RunSC('delete "' + AHA2ServiceName + '"');
  end;
end;
