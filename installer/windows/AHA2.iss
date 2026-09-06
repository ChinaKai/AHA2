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
#define FirewallRuleName "AHA2 Local Control Plane"

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
Name: "{code:SelectedDataDir}"; Flags: uninsneveruninstall

[Files]
Source: "{#SourceExe}"; DestDir: "{app}"; DestName: "aha2.exe"; Flags: ignoreversion

[Icons]
Name: "{group}\AHA2"; Filename: "{code:LocalManagementURL}"
Name: "{group}\卸载 AHA2"; Filename: "{uninstallexe}"

[Run]
Filename: "{code:LocalManagementURL}"; Description: "打开 AHA2 管理页面"; Flags: postinstall shellexec skipifsilent

[Code]
const
  AHA2ServiceName = '{#ServiceName}';
  AHA2FirewallRuleName = '{#FirewallRuleName}';

var
  DataDirPage: TInputDirWizardPage;
  ListenModePage: TInputOptionWizardPage;
  PortPage: TInputQueryWizardPage;
  LANPage: TInputQueryWizardPage;
  FirewallPage: TInputOptionWizardPage;

function SelectedDataDir(Param: String): String;
begin
  Result := Trim(DataDirPage.Values[0]);
end;

function SelectedPort(): String;
begin
  Result := Trim(PortPage.Values[0]);
end;

function SelectedListenHost(): String;
begin
  if ListenModePage.SelectedValueIndex = 0 then
    Result := '127.0.0.1'
  else
    Result := Trim(LANPage.Values[0]);
end;

function SelectedListenAddress(): String;
begin
  Result := SelectedListenHost() + ':' + SelectedPort();
end;

function LocalManagementURL(Param: String): String;
begin
  Result := 'http://127.0.0.1:' + SelectedPort();
end;

function IsLANMode(): Boolean;
begin
  Result := ListenModePage.SelectedValueIndex = 1;
end;

function FirewallSelected(): Boolean;
begin
  Result := IsLANMode() and FirewallPage.Values[0];
end;

function IsIPv4Address(const Value: String): Boolean;
var
  Index, Parts, PartValue, Digits: Integer;
  Current: Char;
begin
  Result := False;
  if Value = '' then
    Exit;
  Parts := 1;
  PartValue := 0;
  Digits := 0;
  for Index := 1 to Length(Value) do
  begin
    Current := Value[Index];
    if Current = '.' then
    begin
      if (Digits = 0) or (PartValue > 255) then
        Exit;
      Parts := Parts + 1;
      PartValue := 0;
      Digits := 0;
    end
    else
    begin
      if (Current < '0') or (Current > '9') then
        Exit;
      PartValue := (PartValue * 10) + Ord(Current) - Ord('0');
      Digits := Digits + 1;
      if (Digits > 3) or (PartValue > 255) then
        Exit;
    end;
  end;
  Result := (Parts = 4) and (Digits > 0) and (PartValue <= 255);
end;

procedure InitializeWizard();
var
  PreviousMode: String;
begin
  DataDirPage := CreateInputDirPage(wpSelectDir,
    '选择数据目录', 'AHA2 的持久数据保存在哪里？',
    '数据库、配置、密钥和日志将保存在此目录。升级和卸载不会自动删除该目录。',
    False, SetupMessage(msgNewFolderName));
  DataDirPage.Add('');
  DataDirPage.Values[0] := GetPreviousData('DataDir', ExpandConstant('{commonappdata}\AHA2'));

  ListenModePage := CreateInputOptionPage(DataDirPage.ID,
    '选择访问范围', '谁可以访问 AHA2？',
    '推荐仅本机访问。只有明确需要其他设备访问时才启用局域网模式。', True, False);
  ListenModePage.Add('仅本机访问（127.0.0.1）');
  ListenModePage.Add('允许局域网访问');
  PreviousMode := GetPreviousData('ListenMode', 'local');
  if PreviousMode = 'lan' then
    ListenModePage.SelectedValueIndex := 1
  else
    ListenModePage.SelectedValueIndex := 0;

  PortPage := CreateInputQueryPage(ListenModePage.ID,
    '选择端口', 'AHA2 使用哪个 HTTP 端口？',
    '端口范围为 1 到 65535，默认使用 8766。');
  PortPage.Add('端口：', False);
  PortPage.Values[0] := GetPreviousData('Port', '8766');

  LANPage := CreateInputQueryPage(PortPage.ID,
    '配置局域网监听', '选择监听的 IPv4 地址',
    '使用 0.0.0.0 监听所有网卡，或填写一张网卡的具体 IPv4 地址。');
  LANPage.Add('监听 IPv4：', False);
  LANPage.Values[0] := GetPreviousData('ListenHost', '0.0.0.0');

  FirewallPage := CreateInputOptionPage(LANPage.ID,
    'Windows 防火墙', '是否允许本地子网访问？',
    '安装器只会为 Private profile 和本地子网创建 TCP 入站规则。', False, False);
  FirewallPage.Add('创建 AHA2 Windows 防火墙规则');
  FirewallPage.Values[0] := GetPreviousData('Firewall', '0') = '1';
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := (not IsLANMode()) and ((PageID = LANPage.ID) or (PageID = FirewallPage.ID));
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  Port: Integer;
begin
  Result := True;
  if CurPageID = DataDirPage.ID then
  begin
    if (SelectedDataDir('') = '') or (ExtractFileDrive(SelectedDataDir('')) = '') or
      (Copy(SelectedDataDir(''), 1, 2) = '\\') or (Pos('"', SelectedDataDir('')) > 0) then
    begin
      MsgBox('请选择本机磁盘上的绝对数据目录；Windows 服务不能使用网络共享路径。', mbError, MB_OK);
      Result := False;
    end;
  end
  else if CurPageID = PortPage.ID then
  begin
    Port := StrToIntDef(SelectedPort(), 0);
    if (Port < 1) or (Port > 65535) then
    begin
      MsgBox('端口必须是 1 到 65535 之间的整数。', mbError, MB_OK);
      Result := False;
    end;
  end
  else if (CurPageID = LANPage.ID) and (not IsIPv4Address(SelectedListenHost())) then
  begin
    MsgBox('请输入 IPv4 地址，例如 0.0.0.0 或 192.168.1.10。', mbError, MB_OK);
    Result := False;
  end;
end;

procedure RegisterPreviousData(PreviousDataKey: Integer);
begin
  SetPreviousData(PreviousDataKey, 'DataDir', SelectedDataDir(''));
  if IsLANMode() then
    SetPreviousData(PreviousDataKey, 'ListenMode', 'lan')
  else
    SetPreviousData(PreviousDataKey, 'ListenMode', 'local');
  SetPreviousData(PreviousDataKey, 'Port', SelectedPort());
  SetPreviousData(PreviousDataKey, 'ListenHost', Trim(LANPage.Values[0]));
  if FirewallSelected() then
    SetPreviousData(PreviousDataKey, 'Firewall', '1')
  else
    SetPreviousData(PreviousDataKey, 'Firewall', '0');
end;

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
    '\" service run --listen ' + SelectedListenAddress() + ' --data-dir \"' +
    SelectedDataDir('') + '\"';
end;

procedure ConfigureFirewall();
var
  ResultCode: Integer;
begin
  Exec(ExpandConstant('{sys}\netsh.exe'),
    'advfirewall firewall delete rule name="' + AHA2FirewallRuleName + '"',
    '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if FirewallSelected() then
    if not Exec(ExpandConstant('{sys}\netsh.exe'),
      'advfirewall firewall add rule name="' + AHA2FirewallRuleName +
      '" dir=in action=allow protocol=TCP localport=' + SelectedPort() +
      ' remoteip=localsubnet profile=private', '', SW_HIDE,
      ewWaitUntilTerminated, ResultCode) or (ResultCode <> 0) then
      RaiseException('无法创建 AHA2 Windows 防火墙规则。');
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
  ConfigureFirewall();
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
var
  ResultCode: Integer;
begin
  if CurUninstallStep = usUninstall then
  begin
    StopAHA2Service();
    RunSC('delete "' + AHA2ServiceName + '"');
    Exec(ExpandConstant('{sys}\netsh.exe'),
      'advfirewall firewall delete rule name="' + AHA2FirewallRuleName + '"',
      '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;
