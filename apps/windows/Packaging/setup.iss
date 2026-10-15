; AsterLink Windows x64 installer (round 8). Built by scripts/package-native-windows.py --installer; do not run ISCC by hand.
;
; Design: this installer is only a carrier and a user interface. It unpacks the stage tree (the same tree the
; development installer used: app\, service\, core\, build-config.json, manifest.json) into its own temporary
; folder and runs ONE privileged program, "AsterLink.Service.exe install --stage <dir>", which verifies every
; file against the manifest, protects %ProgramFiles%\AsterLink, keeps the previous version aside, registers the
; service and rolls back to the previous version on any failure. Inno therefore never copies product files into
; Program Files itself; it only records the uninstaller and the Start-menu entry.
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
UninstallDisplayName={#AppName}
CloseApplications=no
RestartApplications=no
SetupLogging=yes
#ifdef Sign
SignTool=asterlink
SignedUninstaller=yes
#endif

[Languages]
; Inno Setup ships no official Simplified Chinese file; the product messages below are bilingual instead.
Name: "english"; MessagesFile: "compiler:Default.isl"

[CustomMessages]
CloseFirst=AsterLink is running. Quit it from the tray icon (right-click, Exit), then press Retry.%n%nAsterLink 正在运行。请在托盘图标上右键选择“退出”，然后点“重试”。
InstallFailed=AsterLink could not be installed / 无法安装 AsterLink。%n%n%1%n%nNothing was left half-installed; any previous version was restored. 没有留下半安装状态，已恢复之前的版本。%n%n%2
ForgetPrompt=Also remove the saved passwords and preferences of the Windows user running this uninstall?%nYes: remove them. No: keep them (used again if you reinstall).%n%n同时删除当前 Windows 用户在本机保存的密码和偏好设置吗？%n是：删除。否：保留（重新安装后仍可使用）。
UninstallFailed=AsterLink could not be fully removed / 无法完全卸载 AsterLink。%n%n%1
LaunchAsUser=Start AsterLink now / 现在启动 AsterLink

[Files]
; Unpacked to Setup's own temporary folder and removed with it; the privileged verb installs from there.
Source: "{#StageDir}\*"; DestDir: "{tmp}\stage"; Flags: recursesubdirs createallsubdirs ignoreversion deleteafterinstall

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
  if CurStep <> ssPostInstall then Exit;
  Stage := ExpandConstant('{tmp}\stage');
  Service := Stage + '\service\AsterLink.Service.exe';
  InstallLog := ExpandConstant('{tmp}\install-verb.json');
  WizardForm.StatusLabel.Caption := 'AsterLink';
  if not RunVerb(Service, 'install --stage "' + Stage + '"', InstallLog, ExitCode) or (ExitCode <> 0) then
  begin
    Log('install verb exit code ' + IntToStr(ExitCode) + ': ' + VerbMessage(InstallLog));
    SuppressibleMsgBox(FmtMessage(CustomMessage('InstallFailed'), [IntToStr(ExitCode) + ' ' + VerbMessage(InstallLog), '(run Setup with /LOG=file for details)']),
                       mbCriticalError, MB_OK, IDOK);
    // Inno's own record (the uninstaller, the Start-menu entry) must not outlive a failed install.
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

  // 1. The current user's saved passwords / preferences, only if chosen, by the installed GUI as the ORIGINAL (non-elevated) user.
  //    The elevated uninstaller never reaches into any user's vault; other Windows users clean up their own.
  if ForgetRequested() then
  begin
    Gui := ExpandConstant('{app}\app\AsterLink.exe');
    if FileExists(Gui) then
      if not ExecAsOriginalUser(Gui, '--cleanup-user-data --passwords --preferences', '', SW_HIDE, ewWaitUntilTerminated, ExitCode) or (ExitCode <> 0) then
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
    SuppressibleMsgBox(FmtMessage(CustomMessage('UninstallFailed'), [IntToStr(ExitCode) + ' ' + Answer]), mbCriticalError, MB_OK, IDOK);
  end
  else
    Log('uninstall verb: ' + VerbMessage(Log1));
end;
