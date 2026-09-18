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
DisableDirPage=no
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
Source: "..\..\third_party\hysteria\LICENSE.md"; DestDir: "{app}\licenses"; DestName: "Hysteria-LICENSE.txt"; Flags: ignoreversion
Source: "..\..\third_party\yaml-v3\LICENSE"; DestDir: "{app}\licenses"; DestName: "yaml-v3-LICENSE.txt"; Flags: ignoreversion
Source: "..\..\third_party\yaml-v3\NOTICE"; DestDir: "{app}\licenses"; DestName: "yaml-v3-NOTICE.txt"; Flags: ignoreversion
Source: "..\..\third_party\xray-core\LICENSE"; DestDir: "{app}\licenses"; DestName: "Xray-core-MPL-2.0.txt"; Flags: ignoreversion
Source: "..\..\third_party\xray-core\README.AHA2.md"; DestDir: "{app}\licenses"; DestName: "Xray-core-SOURCE.txt"; Flags: ignoreversion
#ifdef SourceFeishuPlugin
Source: "{#SourceFeishuPlugin}"; DestDir: "{code:SelectedDataDir}\plugins\channels\feishu"; DestName: "aha2-channel-feishu.exe"; Flags: ignoreversion
Source: "{#SourceFeishuManifest}"; DestDir: "{code:SelectedDataDir}\plugins\channels\feishu"; DestName: "plugin.json"; Flags: ignoreversion
#endif

[InstallDelete]
Type: files; Name: "{app}\Run-AHA2User.vbs"
; The previous release created "启动 AHA2" pointing at schtasks.exe. Inno only
; removes shortcuts it created in this run, so without this the old entry survives
; an upgrade and the Start menu shows both it and the new entries.
Type: files; Name: "{group}\启动 AHA2.lnk"

[Icons]
; Two distinct entries, because they do different things and Inno silently keeps
; only the last one when names collide.
;   打开 AHA2  -- opens the running instance's web interface.
;   AHA2       -- starts the tray, which owns the server process. This is also the
;                 entry that carries the AHA2 icon; it used to point at
;                 schtasks.exe, so the Start menu showed the generic Windows task
;                 icon under the name "启动 AHA2".
; If an instance is already running, the tray exits on its single-instance lock
; rather than starting a second one. Use 打开 AHA2 in that case.
Name: "{group}\AHA2"; Filename: "{app}\aha2-tray.exe"; Parameters: "{code:TrayParameters|}"; WorkingDir: "{app}"; IconFilename: "{app}\aha2-tray.exe"; Flags: runminimized
Name: "{group}\打开 AHA2"; Filename: "{code:LocalManagementURL}"; IconFilename: "{app}\aha2-tray.exe"
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
  UserTaskWasPresent: Boolean;
  UserProcessWasRunning: Boolean;
  InstallCommitted: Boolean;
  { Set only once the install step has actually begun. Everything the rollback in
    DeinitializeSetup undoes — stopping processes, disabling and removing the
    login task — happens at that step and not before, so this is what decides
    whether there is anything to roll back. }
  InstallStarted: Boolean;
  ExistingTaskArgumentsLoaded: Boolean;
  ExistingTaskArguments: String;

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

function LocalHealthURL(): String;
begin
  Result := LocalManagementURL('') + '/healthz';
end;

{ Agent API is deliberately not configurable from the installer. The server
  derives its base URL from the listen address, and the value it derives is the
  one the reverse tunnel needs. Accepting a URL here would override that derived
  value and would also gate the insecure-HTTP permission on the wrong address, so
  a URL typed into the wizard could only make the result worse. Anyone who truly
  needs a fixed address, such as behind an HTTPS reverse proxy, can still set one
  on the running instance. }

function TrayParameters(Param: String): String;
begin
  Result := '--server "' + ExpandConstant('{app}\aha2.exe') + '" --listen "' +
    SelectedListenAddress() + '" --data-dir "' + SelectedDataDir('') + '"';
end;

function RegisterUserTaskParameters(Param: String): String;
begin
  Result := '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' +
    ExpandConstant('{app}\Register-AHA2UserTask.ps1') + '" -TrayExecutable "' +
    ExpandConstant('{app}\aha2-tray.exe') + '" -ServerExecutable "' +
    ExpandConstant('{app}\aha2.exe') + '" -Listen "' + SelectedListenAddress() +
    '" -DataDir "' + SelectedDataDir('') + '"';
end;

function PrepareDataDirParameters(Param: String): String;
begin
  Result := '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' +
    ExpandConstant('{app}\Prepare-AHA2DataDir.ps1') + '" -DataDir "' +
    SelectedDataDir('') + '" -TaskName "' + AHA2UserTaskName + '"';
end;

function QuotedArgumentValue(const Arguments, Name: String): String;
var
  Marker, Tail: String;
  StartPos, EndPos: Integer;
begin
  Result := '';
  Marker := Name + ' "';
  StartPos := Pos(Marker, Arguments);
  if StartPos = 0 then
    Exit;
  Tail := Copy(Arguments, StartPos + Length(Marker), MaxInt);
  EndPos := Pos('"', Tail);
  if EndPos > 0 then
    Result := Trim(Copy(Tail, 1, EndPos - 1));
end;

function ExistingUserTaskCommandLine(): String;
var
  ResultCode: Integer;
  Output: TExecOutput;
begin
  if ExistingTaskArgumentsLoaded then
  begin
    Result := ExistingTaskArguments;
    Exit;
  end;
  ExistingTaskArgumentsLoaded := True;
  ExistingTaskArguments := '';
  try
    if ExecAndCaptureOutput(
      ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
      '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "' +
      '$task=Get-ScheduledTask -TaskName ''' + AHA2UserTaskName + ''' -ErrorAction SilentlyContinue;' +
      'if($task){[Console]::Out.Write([string]$task.Actions[0].Arguments)}"',
      '', SW_SHOWNORMAL, ewWaitUntilTerminated, ResultCode, Output) and
      (ResultCode = 0) and (not Output.Error) and
      (GetArrayLength(Output.StdOut) > 0) then
    begin
      ExistingTaskArguments := Trim(Output.StdOut[0]);
      if ExistingTaskArguments <> '' then
        Log('Using the existing AHA2 user task configuration for the upgrade wizard.');
    end;
  except
    Log('Could not inspect the existing AHA2 user task configuration: ' + GetExceptionMessage);
    ExistingTaskArguments := '';
  end;
  Result := ExistingTaskArguments;
end;

function ExistingUserTaskDataDir(): String;
begin
  Result := QuotedArgumentValue(ExistingUserTaskCommandLine(), '--data-dir');
  if Result <> '' then
    Log('Using the existing AHA2 user task data directory for the upgrade wizard.');
end;

function ExistingUserTaskListenAddress(): String;
begin
  Result := QuotedArgumentValue(ExistingUserTaskCommandLine(), '--listen');
  if Result <> '' then
    Log('Using the existing AHA2 user task listen address for the upgrade wizard.');
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
  PreviousMode, ExplicitDataDir, TaskDataDir, PreviousDataDir: String;
  TaskListenAddress, TaskListenHost, TaskListenPort: String;
  ListenSeparator: Integer;
begin
  DataDirPage := CreateInputDirPage(wpSelectDir,
    '选择数据目录', 'AHA2 的持久数据保存在哪里？',
    '数据库、配置、密钥和日志将保存在此目录。升级和卸载不会自动删除该目录。',
    False, SetupMessage(msgNewFolderName));
  DataDirPage.Add('');
#ifdef PerUserInstall
  ExplicitDataDir := Trim(ExpandConstant('{param:DATADIR|}'));
  TaskDataDir := ExistingUserTaskDataDir();
  PreviousDataDir := GetPreviousData('DataDir', ExpandConstant('{localappdata}\AHA2'));
  if ExplicitDataDir <> '' then
    DataDirPage.Values[0] := ExplicitDataDir
  else if TaskDataDir <> '' then
    DataDirPage.Values[0] := TaskDataDir
  else
    DataDirPage.Values[0] := PreviousDataDir;
#else
  DataDirPage.Values[0] := GetPreviousData('DataDir', ExpandConstant('{commonappdata}\AHA2'));
#endif

  TaskListenAddress := ExistingUserTaskListenAddress();
  ListenSeparator := Pos(':', TaskListenAddress);
  if ListenSeparator > 0 then
  begin
    TaskListenHost := Trim(Copy(TaskListenAddress, 1, ListenSeparator - 1));
    TaskListenPort := Trim(Copy(TaskListenAddress, ListenSeparator + 1, MaxInt));
  end;

  ListenModePage := CreateInputOptionPage(DataDirPage.ID,
    '选择访问范围', '谁可以访问 AHA2？',
    '推荐仅本机访问。只有明确需要其他设备访问时才启用局域网模式。', True, False);
  ListenModePage.Add('仅本机访问（127.0.0.1）');
  ListenModePage.Add('允许局域网访问');
  PreviousMode := GetPreviousData('ListenMode', 'local');
  if TaskListenHost <> '' then
  begin
    if TaskListenHost = '127.0.0.1' then
      PreviousMode := 'local'
    else
      PreviousMode := 'lan';
  end;
  if PreviousMode = 'lan' then
    ListenModePage.SelectedValueIndex := 1
  else
    ListenModePage.SelectedValueIndex := 0;

  PortPage := CreateInputQueryPage(ListenModePage.ID,
    '选择端口', 'AHA2 使用哪个 HTTP 端口？',
    '端口范围为 1 到 65535，默认使用 8766。');
  PortPage.Add('端口：', False);
  if TaskListenPort <> '' then
    PortPage.Values[0] := TaskListenPort
  else
    PortPage.Values[0] := GetPreviousData('Port', '8766');

  LANPage := CreateInputQueryPage(PortPage.ID,
    '配置局域网监听', '选择监听的 IPv4 地址',
    '使用 0.0.0.0 监听所有网卡，或填写一张网卡的具体 IPv4 地址。');
  LANPage.Add('监听 IPv4：', False);
  if (TaskListenHost <> '') and (TaskListenHost <> '127.0.0.1') then
    LANPage.Values[0] := TaskListenHost
  else
    LANPage.Values[0] := GetPreviousData('ListenHost', '0.0.0.0');

  FirewallPage := CreateInputOptionPage(LANPage.ID,
    'Windows 防火墙', '是否允许本地子网访问？',
    '安装器只会为 Private profile 和本地子网创建 TCP 入站规则。', False, False);
  FirewallPage.Add('创建 AHA2 Windows 防火墙规则');
  FirewallPage.Values[0] := GetPreviousData('Firewall', '0') = '1';

  { No Agent API page. Left unset, the server derives its Agent API base URL from
    the listen address (127.0.0.1 for a wildcard bind), and that loopback address
    is exactly what the reverse tunnel gate expects: for an SSH workspace the
    tunnel replaces it with a per-Turn port the workspace can actually reach. The
    old page asked the user to answer a question AHA can answer itself, and an
    answer typed here could only make things worse. }
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
#ifdef PerUserInstall
  Result := (PageID = FirewallPage.ID) or
    ((not IsLANMode()) and (PageID = LANPage.ID));
#else
  Result := (not IsLANMode()) and ((PageID = LANPage.ID) or (PageID = FirewallPage.ID));
#endif
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
  ;
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
  { Agent API is not a wizard input, so nothing to carry over between runs.
    A stale AgentAPIURL left in the registry from an older version would still be
    read by the page initialisation that no longer exists, so make sure it is
    cleared rather than inherited. }
  SetPreviousData(PreviousDataKey, 'AgentAPIURL', '');
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
  { The per-user deployment wrapper owns task registration because it has the
    authoritative listen and data-directory arguments. Silent upgrades otherwise
    reuse stale wizard values and can reject an already-correct ACL-protected
    task before the wrapper gets a chance to validate and reuse it. }
  if CompareText(ExpandConstant('{param:SKIPUSERTASK|0}'), '1') = 0 then
    Exit;
#ifdef PerUserInstall
  { A manual per-user upgrade must preserve an existing login task. Its action
    is the authoritative runtime configuration and may be owner-startable but
    ACL-protected against Register-ScheduledTask -Force. The deployment wrapper
    performs stricter post-install validation when explicit settings change. }
  if UserTaskWasPresent then
  begin
    Log('Existing AHA2 user task will be preserved for per-user upgrade.');
    if not RunScheduledTask('/Change /Enable /TN "' + AHA2UserTaskName + '"') then
      RaiseException('无法重新启用已有 AHA2 登录任务。');
    Exit;
  end;
#endif
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
    InstallStarted := True;
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
  { Only undo what this run actually did. Cancelling or closing the wizard before
    the install step leaves a running AHA2 entirely alone: stopping it there, or
    removing its login task, would break a working installation the user never
    asked us to touch. InstallStarted is what distinguishes "we began replacing
    files and must roll back" from "we never got that far". }
  if InstallStarted and (not InstallCommitted) then
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
