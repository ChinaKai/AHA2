#ifndef MyAppVersion
  #define MyAppVersion "dev"
#endif
#ifndef SourceExe
  #define SourceExe "..\..\dist\aha2-windows-amd64.exe"
#endif
#ifndef SourceTrayExe
  #define SourceTrayExe "..\..\dist\aha2-tray-windows-amd64.exe"
#endif
#ifndef OutputDir
  #define OutputDir "..\..\dist\installer"
#endif

#define AppName "AHA2"
#define ServiceName "AHA2"
#define ListenAddress "127.0.0.1:8766"
#define FirewallRuleName "AHA2 Local Control Plane"

[Setup]
#ifdef PerUserInstall
AppId={{D23028A1-597D-4D7B-BB12-E29A58AC53A8}
#else
AppId={{6F51A727-6446-4A71-83C8-6A6E8FB61192}
#endif
AppName={#AppName}
AppVersion={#MyAppVersion}
AppPublisher=AHA2
#ifdef PerUserInstall
DefaultDirName={localappdata}\Programs\AHA2
#else
DefaultDirName={autopf}\AHA2
#endif
DefaultGroupName=AHA2
DisableProgramGroupPage=yes
#ifdef PerUserInstall
PrivilegesRequired=lowest
#else
PrivilegesRequired=admin
#endif
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#OutputDir}
#ifdef PerUserInstall
OutputBaseFilename=AHA2-Setup-User-x64
#else
OutputBaseFilename=AHA2-Setup-x64
#endif
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\aha2.exe
CloseApplications=yes
RestartApplications=no

[Files]
Source: "{#SourceExe}"; DestDir: "{app}"; DestName: "aha2.exe"; Flags: ignoreversion
Source: "{#SourceTrayExe}"; DestDir: "{app}"; DestName: "aha2-tray.exe"; Flags: ignoreversion
Source: "Register-AHA2UserTask.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "Prepare-AHA2DataDir.ps1"; DestDir: "{app}"; Flags: ignoreversion
#ifdef SourceFeishuPlugin
Source: "{#SourceFeishuPlugin}"; DestDir: "{code:SelectedDataDir}\plugins\channels\feishu"; DestName: "aha2-channel-feishu.exe"; Flags: ignoreversion
Source: "{#SourceFeishuManifest}"; DestDir: "{code:SelectedDataDir}\plugins\channels\feishu"; DestName: "plugin.json"; Flags: ignoreversion
#endif

[InstallDelete]
Type: files; Name: "{app}\Run-AHA2User.vbs"

[Icons]
Name: "{group}\AHA2"; Filename: "{code:LocalManagementURL}"
Name: "{group}\启动 AHA2"; Filename: "{sys}\schtasks.exe"; Parameters: "/Run /TN ""AHA2 User"""; WorkingDir: "{app}"; Flags: runminimized
Name: "{group}\卸载 AHA2"; Filename: "{uninstallexe}"

[Run]
Filename: "{code:LocalManagementURL}"; Description: "打开 AHA2 管理页面"; Flags: postinstall shellexec skipifsilent runasoriginaluser

[Code]
const
  AHA2ServiceName = '{#ServiceName}';
  AHA2FirewallRuleName = '{#FirewallRuleName}';
  AHA2UserTaskName = 'AHA2 User';

var
  DataDirPage: TInputDirWizardPage;
  ListenModePage: TInputOptionWizardPage;
  PortPage: TInputQueryWizardPage;
  LANPage: TInputQueryWizardPage;
  FirewallPage: TInputOptionWizardPage;
  AgentAPIPage: TInputQueryWizardPage;
  InsecureAgentAPIPage: TInputOptionWizardPage;
  UserTaskWasPresent: Boolean;
  UserProcessWasRunning: Boolean;
  InstallCommitted: Boolean;

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
#ifdef PerUserInstall
  Result := '127.0.0.1';
#else
  if ListenModePage.SelectedValueIndex = 0 then
    Result := '127.0.0.1'
  else
    Result := Trim(LANPage.Values[0]);
#endif
end;

function SelectedListenAddress(): String;
begin
  Result := SelectedListenHost() + ':' + SelectedPort();
end;

function LocalManagementURL(Param: String): String;
begin
  Result := 'http://127.0.0.1:' + SelectedPort();
end;

function LocalHealthURL(): String;
begin
  Result := LocalManagementURL('') + '/healthz';
end;

function SelectedAgentAPIURL(): String;
begin
  Result := Trim(AgentAPIPage.Values[0]);
  while (Length(Result) > 0) and (Result[Length(Result)] = '/') do
    Delete(Result, Length(Result), 1);
end;

function AllowInsecureAgentAPI(): Boolean;
begin
  Result := InsecureAgentAPIPage.Values[0];
end;

function TrayParameters(Param: String): String;
begin
  Result := '--server "' + ExpandConstant('{app}\aha2.exe') + '" --listen "' +
    SelectedListenAddress() + '" --data-dir "' + SelectedDataDir('') + '"';
  if SelectedAgentAPIURL() <> '' then
    Result := Result + ' --agent-api-url "' + SelectedAgentAPIURL() + '"';
  if (SelectedAgentAPIURL() <> '') and AllowInsecureAgentAPI() then
    Result := Result + ' --allow-insecure-agent-api';
end;

function RegisterUserTaskParameters(Param: String): String;
begin
  Result := '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' +
    ExpandConstant('{app}\Register-AHA2UserTask.ps1') + '" -TrayExecutable "' +
    ExpandConstant('{app}\aha2-tray.exe') + '" -ServerExecutable "' +
    ExpandConstant('{app}\aha2.exe') + '" -Listen "' + SelectedListenAddress() +
    '" -DataDir "' + SelectedDataDir('') + '"';
  if SelectedAgentAPIURL() <> '' then
    Result := Result + ' -AgentAPIURL "' + SelectedAgentAPIURL() + '"';
  if (SelectedAgentAPIURL() <> '') and AllowInsecureAgentAPI() then
    Result := Result + ' -AllowInsecureAgentAPI';
end;

function PrepareDataDirParameters(Param: String): String;
begin
  Result := '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' +
    ExpandConstant('{app}\Prepare-AHA2DataDir.ps1') + '" -DataDir "' +
    SelectedDataDir('') + '" -TaskName "' + AHA2UserTaskName + '"';
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
#ifdef PerUserInstall
  DataDirPage.Values[0] := ExpandConstant('{param:DATADIR|' + GetPreviousData('DataDir', ExpandConstant('{localappdata}\AHA2')) + '}');
#else
  DataDirPage.Values[0] := GetPreviousData('DataDir', ExpandConstant('{commonappdata}\AHA2'));
#endif

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

  AgentAPIPage := CreateInputQueryPage(FirewallPage.ID,
    '配置 Agent API 初始默认值', '远程 Workspace 中的 Agent 如何访问 AHA2？',
    '可留空并在安装后自动探测。此值只作为全局初始默认，也可在高级设置和每个 Workspace 中修改或覆盖。');
  AgentAPIPage.Add('Agent API URL（可选初始值）：', False);
  AgentAPIPage.Values[0] := GetPreviousData('AgentAPIURL', '');

  InsecureAgentAPIPage := CreateInputOptionPage(AgentAPIPage.ID,
    'Agent API 传输安全', '是否允许非本机 HTTP 地址？',
    '仅在明确受信的开发网络中启用；公网必须使用 HTTPS。', False, False);
  InsecureAgentAPIPage.Add('允许受信开发网络中的非 loopback HTTP Agent API');
  InsecureAgentAPIPage.Values[0] := GetPreviousData('AllowInsecureAgentAPI', '0') = '1';
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
#ifdef PerUserInstall
  Result := (PageID = ListenModePage.ID) or (PageID = LANPage.ID) or (PageID = FirewallPage.ID);
#else
  Result := (not IsLANMode()) and ((PageID = LANPage.ID) or (PageID = FirewallPage.ID));
#endif
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  Port: Integer;
  AgentURL, LowerAgentURL: String;
begin
  Result := True;
  if CurPageID = DataDirPage.ID then
  begin
    if (SelectedDataDir('') = '') or (ExtractFileDrive(SelectedDataDir('')) = '') or
      (Copy(SelectedDataDir(''), 1, 2) = '\\') or (Pos('"', SelectedDataDir('')) > 0) then
    begin
      MsgBox('请选择当前登录用户可写的本机绝对数据目录；SQLite 数据目录不能使用网络共享路径。', mbError, MB_OK);
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
  end
  else if CurPageID = AgentAPIPage.ID then
  begin
    AgentURL := SelectedAgentAPIURL();
    LowerAgentURL := Lowercase(AgentURL);
    if (AgentURL <> '') and
      ((Pos('"', AgentURL) > 0) or (Pos(' ', AgentURL) > 0) or
       ((Copy(LowerAgentURL, 1, 7) <> 'http://') and
        (Copy(LowerAgentURL, 1, 8) <> 'https://'))) then
    begin
      MsgBox('Agent API URL 必须是无空格、无凭据的 http:// 或 https:// 基址。', mbError, MB_OK);
      Result := False;
    end;
  end
  else if CurPageID = InsecureAgentAPIPage.ID then
  begin
    LowerAgentURL := Lowercase(SelectedAgentAPIURL());
    if (Copy(LowerAgentURL, 1, 7) = 'http://') and
      (Copy(LowerAgentURL, 1, 17) <> 'http://127.0.0.1') and
      (Copy(LowerAgentURL, 1, 16) <> 'http://localhost') and
      (not AllowInsecureAgentAPI()) then
    begin
      MsgBox('非 loopback HTTP Agent API 仅允许用于受信开发网络；请勾选确认或改用 HTTPS。', mbError, MB_OK);
      Result := False;
    end;
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
  SetPreviousData(PreviousDataKey, 'AgentAPIURL', SelectedAgentAPIURL());
  if AllowInsecureAgentAPI() then
    SetPreviousData(PreviousDataKey, 'AllowInsecureAgentAPI', '1')
  else
    SetPreviousData(PreviousDataKey, 'AllowInsecureAgentAPI', '0');
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

function RunScheduledTask(const Arguments: String): Boolean;
var
  ResultCode: Integer;
begin
  Result := Exec(ExpandConstant('{sys}\schtasks.exe'), Arguments, '', SW_HIDE,
    ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

function UserTaskExists(): Boolean;
begin
  Result := RunScheduledTask('/Query /TN "' + AHA2UserTaskName + '"');
end;

procedure StopAHA2UserTask();
begin
  if UserTaskExists() then
  begin
    RunScheduledTask('/Change /Disable /TN "' + AHA2UserTaskName + '"');
    RunScheduledTask('/End /TN "' + AHA2UserTaskName + '"');
  end;
end;

procedure RemoveAHA2UserTask();
begin
  if UserTaskExists() then
  begin
    StopAHA2UserTask();
    RunScheduledTask('/Delete /F /TN "' + AHA2UserTaskName + '"');
  end;
end;

procedure StopAHA2Service();
var
  ResultCode: Integer;
begin
  if ServiceExists() then
  begin
    Exec(ExpandConstant('{sys}\net.exe'), 'stop "' + AHA2ServiceName + '" /y',
      '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

function AHA2IsHealthy(): Boolean;
var
  Request: Variant;
  ResponseText: String;
begin
  Result := False;
  try
    Request := CreateOleObject('WinHttp.WinHttpRequest.5.1');
    Request.SetTimeouts(2000, 2000, 2000, 2000);
    Request.Open('GET', LocalHealthURL(), False);
    Request.Send('');
    ResponseText := Request.ResponseText;
    Result := (Request.Status = 200) and
      (Pos('"service":"aha2"', ResponseText) > 0);
  except
    Result := False;
  end;
end;

procedure WaitForAHA2Health();
var
  Attempt: Integer;
begin
  for Attempt := 1 to 60 do
  begin
    if AHA2IsHealthy() then
      Exit;
    Sleep(500);
  end;
  RaiseException('AHA2 登录任务已启动，但 30 秒内健康检查未通过。');
end;

function StopInstalledUserProcesses(): Boolean;
var
  ResultCode: Integer;
  Script: String;
begin
  { AHA2 has one login task. Stop both the current and any previous-install-path
    runtimes before replacing files. Kill each AHA2 process tree so backend CLI
    descendants cannot keep a Codex thread-store writer alive across upgrade. }
  Script := '$items=@(Get-Process -Name ''aha2-tray'',''aha2'' -ErrorAction SilentlyContinue);' +
    'if($items.Count -eq 0){exit 3};' +
    'foreach($item in $items){& taskkill.exe /PID $item.Id /T /F 2>$null | Out-Null};' +
    'Start-Sleep -Milliseconds 500;exit 0';
  Result := Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "' + Script + '"',
    '', SW_HIDE, ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

function StartInstalledUserProcess(): Boolean;
var
  ResultCode: Integer;
begin
  Result := ExecAsOriginalUser(
    ExpandConstant('{app}\aha2-tray.exe'), TrayParameters(''), ExpandConstant('{app}'),
    SW_HIDE, ewNoWait, ResultCode);
end;

procedure ConfigureAHA2UserTask();
var
  ResultCode: Integer;
begin
  if not ExecAsOriginalUser(
    ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    RegisterUserTaskParameters(''), ExpandConstant('{app}'), SW_HIDE,
    ewWaitUntilTerminated, ResultCode) or (ResultCode <> 0) then
  begin
    if not UserTaskWasPresent then
      RemoveAHA2UserTask();
    RaiseException('无法为原始登录用户注册 AHA2 登录任务。');
  end;
#ifndef PerUserInstall
  if not Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    PrepareDataDirParameters(''), ExpandConstant('{app}'), SW_HIDE,
    ewWaitUntilTerminated, ResultCode) or (ResultCode <> 0) then
  begin
    if not UserTaskWasPresent then
      RemoveAHA2UserTask();
    RaiseException('无法为原始登录用户准备 AHA2 数据目录权限。');
  end;
#endif
end;

procedure ConfigureFirewall();
var
  ResultCode: Integer;
begin
#ifdef PerUserInstall
  Exit;
#else
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
#endif
end;

procedure DisableAndStopLegacyAHA2Service(Required: Boolean);
begin
  if ServiceExists() then
  begin
    if Required and (not RunSC('config "' + AHA2ServiceName + '" start= disabled')) then
      RaiseException('无法禁用旧版 AHA2 LocalSystem 服务；为避免下次启动抢占端口，安装已中止。');
    if not Required then
      RunSC('config "' + AHA2ServiceName + '" start= disabled');
    StopAHA2Service();
  end;
end;

procedure RemoveLegacyAHA2Service();
begin
  if ServiceExists() then
  begin
    DisableAndStopLegacyAHA2Service(True);
    if not RunSC('delete "' + AHA2ServiceName + '"') then
      Log('Legacy AHA2 service could not be deleted immediately; it remains stopped and disabled.');
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssInstall then
  begin
    UserTaskWasPresent := UserTaskExists();
#ifndef PerUserInstall
    DisableAndStopLegacyAHA2Service(True);
#endif
    UserProcessWasRunning := StopInstalledUserProcesses();
    StopAHA2UserTask();
  end
  else if CurStep = ssPostInstall then
  begin
    ConfigureFirewall();
    ConfigureAHA2UserTask();
    if not RunScheduledTask('/Run /TN "' + AHA2UserTaskName + '"') then
      RaiseException('无法启动 AHA2 登录任务。');
    WaitForAHA2Health();
#ifndef PerUserInstall
    RemoveLegacyAHA2Service();
#endif
    InstallCommitted := True;
  end;
end;

procedure DeinitializeSetup();
begin
  if not InstallCommitted then
  begin
    StopInstalledUserProcesses();
    if not UserTaskWasPresent then
      RemoveAHA2UserTask();
    if UserTaskWasPresent then
    begin
      RunScheduledTask('/Change /Enable /TN "' + AHA2UserTaskName + '"');
      if (not RunScheduledTask('/Run /TN "' + AHA2UserTaskName + '"')) and
        UserProcessWasRunning then
        StartInstalledUserProcess()
    end
    else if UserProcessWasRunning then
      StartInstalledUserProcess();
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ResultCode: Integer;
begin
  if CurUninstallStep = usUninstall then
  begin
    StopInstalledUserProcesses();
    RemoveAHA2UserTask();
#ifndef PerUserInstall
    RemoveLegacyAHA2Service();
    Exec(ExpandConstant('{sys}\netsh.exe'),
      'advfirewall firewall delete rule name="' + AHA2FirewallRuleName + '"',
      '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
#endif
  end;
end;
