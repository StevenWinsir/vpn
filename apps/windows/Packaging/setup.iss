; AsterLink Windows x64 installer (round 8). Built by scripts/package-native-windows.py --installer; do not run ISCC by hand.
;
; Design: this installer is only a carrier and a user interface. It unpacks the stage tree (the same tree the
; development installer used: app\, service\, core\, build-config.json, manifest.json) into its own temporary
; folder and runs ONE privileged program, "AsterLink.Service.exe install --stage <dir>", which verifies every
; file against the manifest, protects %ProgramFiles%\AsterLink, keeps the previous version aside, registers the
; service and rolls back to the previous version on any failure. Inno therefore never copies product files into
; Program Files itself; it only records the uninstaller and the Start-menu entry.
;
; Order matters: the verb runs at ssInstall, BEFORE Inno writes {app}\setup\unins000.exe and the Add/Remove
; Programs entry. Run later (ssPostInstall) the verb's rename of the install root would take the fresh uninstaller
; with it, and Inno ignores an Abort raised at ssPostInstall (Setup would still exit 0). At ssInstall a failure ends
; Setup with exit code 3 before Inno has recorded anything.
;
; Defines passed by the build script (environment): ASTERLINK_VERSION, ASTERLINK_STAGE, ASTERLINK_OUTPUT,
; and optionally ASTERLINK_SIGN=1 (sign the setup and the uninstaller through the "asterlink" sign tool).

#define AppName "AsterLink"
#define AppVersion GetEnv("ASTERLINK_VERSION")
#define StageDir GetEnv("ASTERLINK_STAGE")
#define OutputDir GetEnv("ASTERLINK_OUTPUT")
#if AppVersion == ""
  #error ASTERLINK_VERSION is not set
#endif
#if StageDir == ""
  #error ASTERLINK_STAGE is not set
#endif

[Setup]
AppId={{6C1F7E52-5B0E-4A6B-9D4E-A57E71C0B8D1}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher=AsterLink
VersionInfoVersion={#AppVersion}
DefaultDirName={autopf}\{#AppName}
DisableDirPage=yes
DisableProgramGroupPage=yes
DisableReadyPage=no
; The service insists on %ProgramFiles%\AsterLink, so the folder is not a user choice.
UsePreviousAppDir=no
PrivilegesRequired=admin
ArchitecturesAllowed=x64os
ArchitecturesInstallIn64BitMode=x64os
MinVersion=10.0.19041
OutputDir={#OutputDir}
OutputBaseFilename=AsterLink-Setup-x64-{#AppVersion}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
UninstallFilesDir={app}\setup
UninstallDisplayIcon={app}\app\AsterLink.exe
; Brand assets generated from frontend/brand/mark.svg (frontend/scripts/brand-apps.mjs).
SetupIconFile=..\AsterLink.App\Assets\AsterLink.ico
WizardSmallImageFile=wizard-small-55.bmp,wizard-small-110.bmp
UninstallDisplayName={#AppName}
CloseApplications=no
RestartApplications=no
SetupLogging=yes
#ifdef Sign
SignTool=asterlink
SignedUninstaller=yes
#endif

[Languages]
; Simplified Chinese only (handoff_app_standardization.md 6.6). Inno Setup ships no official Chinese file: the reviewed
; community translation from the vendor's repository is kept next to this script (see its header).
Name: "chinesesimplified"; MessagesFile: "Languages\ChineseSimplified.isl"

[CustomMessages]
; Plain words for the user: what happened and what to do. The service verb's own answer goes to the setup log only.
CloseFirst=AsterLink 正在运行。请右键点击任务栏右下角的 AsterLink 图标并选择“退出”（如果没有看到图标，请先点击“^”），然后点击“重试”。
InstallFailed=无法安装 AsterLink（错误码 %1）。%n%n没有留下未完成的安装，之前的版本已恢复。请重新运行安装程序；如果问题仍然存在，请联系客服。
ForgetPrompt=是否同时删除当前 Windows 用户在本机保存的密码和偏好设置？%n%n是：删除。%n否：保留（重新安装后仍可使用）。
UninstallFailed=无法完全卸载 AsterLink（错误码 %1）。%n%n请稍后重新运行卸载程序；如果问题仍然存在，请联系客服。
LaunchAsUser=立即启动 AsterLink

[Files]
; Never installed by Inno (dontcopy): CurStepChanged(ssInstall) unpacks it to Setup's own temporary folder, which is
; removed with Setup, and the privileged verb installs from there.
Source: "{#StageDir}\*"; DestDir: "{tmp}\stage"; Flags: dontcopy recursesubdirs

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\app\AsterLink.exe"

[Run]
; The product starts as the ordinary user that launched Setup, never with the administrator token.
Filename: "{app}\app\AsterLink.exe"; Description: "{cm:LaunchAsUser}"; Flags: postinstall nowait skipifsilent runasoriginaluser

[UninstallDelete]
Type: dirifempty; Name: "{app}\setup"
Type: dirifempty; Name: "{app}"

[Code]
var
  InstallLog: String;

function ProcessRunning(const ImageName: String): Boolean;
var
  ResultCode: Integer;
  Output: AnsiString;
  Capture: String;
begin
  Capture := ExpandConstant('{tmp}\running.txt');
  Result := False;
  if Exec(ExpandConstant('{sys}\cmd.exe'), '/C tasklist /FI "IMAGENAME eq ' + ImageName + '" /NH > "' + Capture + '"', '',
          SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    if LoadStringFromFile(Capture, Output) then
      Result := Pos(Lowercase(ImageName), Lowercase(String(Output))) > 0;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  Result := '';
  // The verb refuses to replace files that are in use; ask first so the user gets a button, not an error.
  while ProcessRunning('AsterLink.exe') do
  begin
    if SuppressibleMsgBox(CustomMessage('CloseFirst'), mbError, MB_RETRYCANCEL, IDCANCEL) = IDCANCEL then
    begin
      Result := CustomMessage('CloseFirst');
      Exit;
    end;
  end;
end;

// Runs "cmd /C exe args > log" so the verb's JSON answer can be read back; returns the exit code.
function RunVerb(const Exe, Args, LogFile: String; var ExitCode: Integer): Boolean;
begin
  Result := Exec(ExpandConstant('{sys}\cmd.exe'), '/C ""' + Exe + '" ' + Args + ' > "' + LogFile + '" 2>&1"', '',
                 SW_HIDE, ewWaitUntilTerminated, ExitCode);
end;

function VerbMessage(const LogFile: String): String;
var
  Text: AnsiString;
begin
  Result := '';
  if LoadStringFromFile(LogFile, Text) then
    Result := Trim(String(Text));
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ExitCode: Integer;
  Stage, Service: String;
begin
  // ssInstall: Inno has not written anything yet (see the header for why not ssPostInstall).
  if CurStep <> ssInstall then Exit;
  Stage := ExpandConstant('{tmp}\stage');
  Service := Stage + '\service\AsterLink.Service.exe';
  InstallLog := ExpandConstant('{tmp}\install-verb.json');
  WizardForm.StatusLabel.Caption := 'AsterLink';
  ExtractTemporaryFiles('{tmp}\stage\*');
  if not RunVerb(Service, 'install --stage "' + Stage + '"', InstallLog, ExitCode) or (ExitCode <> 0) then
  begin
    Log('install verb exit code ' + IntToStr(ExitCode) + ': ' + VerbMessage(InstallLog));
    SuppressibleMsgBox(FmtMessage(CustomMessage('InstallFailed'), [IntToStr(ExitCode)]), mbCriticalError, MB_OK, IDOK);
    // Raised at ssInstall this stops Setup (exit code 3) before the uninstaller and the Start-menu entry exist.
    Abort;
  end;
  Log('install verb: ' + VerbMessage(InstallLog));
end;

// ---- uninstall -------------------------------------------------------------------------------

function ForgetRequested(): Boolean;
begin
  // Silent uninstall: /REMOVEUSERDATA=1 is the explicit choice. Interactive: ask, default No.
  if ExpandConstant('{param:REMOVEUSERDATA|}') <> '' then
    Result := ExpandConstant('{param:REMOVEUSERDATA|}') = '1'
  else if UninstallSilent() then
    Result := False
  else
    Result := MsgBox(CustomMessage('ForgetPrompt'), mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES;
end;

function InitializeUninstall(): Boolean;
begin
  Result := True;
  while ProcessRunning('AsterLink.exe') do
  begin
    if SuppressibleMsgBox(CustomMessage('CloseFirst'), mbError, MB_RETRYCANCEL, IDCANCEL) = IDCANCEL then
    begin
      Result := False;
      Exit;
    end;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  ExitCode: Integer;
  Gui, CopyDir, Log1, Answer: String;
begin
  if CurUninstallStep <> usUninstall then Exit;

  // 1. Saved passwords / preferences, only if chosen, by the installed GUI (no window). Inno's uninstaller has no
  //    "original user" (ExecAsOriginalUser is refused there), so this runs as the account that approved the uninstall:
  //    the same user in the usual admin-approval case. Only that account's AsterLink/ items and folder are touched;
  //    other Windows users clean up their own.
  if ForgetRequested() then
  begin
    Gui := ExpandConstant('{app}\app\AsterLink.exe');
    if FileExists(Gui) then
      if not Exec(Gui, '--cleanup-user-data --passwords --preferences', '', SW_HIDE, ewWaitUntilTerminated, ExitCode) or (ExitCode <> 0) then
        Log('per-user cleanup did not complete (exit ' + IntToStr(ExitCode) + ')');
  end;

  // 2. A program cannot delete its own folder: run the verb from a copy outside the install directory.
  CopyDir := ExpandConstant('{tmp}\uninstall-service');
  Log1 := ExpandConstant('{tmp}\uninstall-verb.json');
  if not DirExists(CopyDir) then ForceDirectories(CopyDir);
  Exec(ExpandConstant('{sys}\xcopy.exe'), '"' + ExpandConstant('{app}\service') + '\*" "'+ CopyDir + '\service\" /E /I /Q /Y', '', SW_HIDE, ewWaitUntilTerminated, ExitCode);
  if (not RunVerb(CopyDir + '\service\AsterLink.Service.exe', 'uninstall', Log1, ExitCode)) or (ExitCode <> 0) then
  begin
    Answer := VerbMessage(Log1);
    Log('uninstall verb exit code ' + IntToStr(ExitCode) + ': ' + Answer);
    SuppressibleMsgBox(FmtMessage(CustomMessage('UninstallFailed'), [IntToStr(ExitCode)]), mbCriticalError, MB_OK, IDOK);
    // Raised at usUninstall this stops before Inno removes its entry and unins000.exe, so the user can run it again.
    Abort;
  end
  else
    Log('uninstall verb: ' + VerbMessage(Log1));
end;
