; s3-anysync per-user installer. Built by scripts/release.sh:
;   makensis -DVERSION=.. -DVERSION4=.. -DSRCDIR=.. -DICON=.. -DOUTFILE=.. s3-anysync.nsi
Unicode true
!include "MUI2.nsh"
!include "Sections.nsh"
!include "LogicLib.nsh"
!include "WinMessages.nsh"

!define APP "s3-anysync"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP}"
!define RUNKEY "Software\Microsoft\Windows\CurrentVersion\Run"

Name "${APP} ${VERSION}"
OutFile "${OUTFILE}"
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\${APP}"
InstallDirRegKey HKCU "${UNINSTKEY}" "InstallLocation"
SetCompressor /SOLID lzma

VIProductVersion "${VERSION4}"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "CompanyName" "aupr"
VIAddVersionKey "FileDescription" "${APP} installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "aupr"

!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_FINISHPAGE_RUN "$INSTDIR\s3-anysync.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open s3-anysync"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

; Stops a running agent and GUI so their files can be replaced or removed.
; /F is acceptable: there is no IPC quit, the WAL survives a kill, local writes are
; temp+rename, and the remote lock expires after 2 minutes.
!macro StopApp
  nsExec::Exec '"$SYSDIR\taskkill.exe" /IM s3-anysync-agent.exe /F'
  Pop $0
  nsExec::Exec '"$SYSDIR\taskkill.exe" /IM s3-anysync.exe /F'
  Pop $0
  Sleep 500
!macroend

Section "${APP} (required)" SecMain
  SectionIn RO
  !insertmacro StopApp
  SetOutPath "$INSTDIR"
  File "${SRCDIR}\s3-anysync.exe"
  File "${SRCDIR}\s3-anysync-agent.exe"
  File "${SRCDIR}\s3-anysync-cli.exe"
  File "path.ps1"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\s3-anysync.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "${APP}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "Publisher" "aupr"
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\s3-anysync.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
SectionEnd

; PATH editing goes through path.ps1 (raw registry access, keeps REG_EXPAND_SZ).
; $0 = exit code afterwards; the directory travels in S3ANYSYNC_DIR (no quoting issues).
; Usage: !insertmacro RunPath "<script>" Add|Remove
!macro RunPath SCRIPT ACTION
  System::Call 'Kernel32::SetEnvironmentVariable(t "S3ANYSYNC_DIR", t "$INSTDIR")'
  nsExec::ExecToLog '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "${SCRIPT}" -Action ${ACTION}'
  Pop $0
  ${If} $0 S== "0"
    SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
  ${Else}
    DetailPrint "Could not update PATH (exit code $0)."
  ${EndIf}
!macroend

Section /o "Add the command-line tool to PATH" SecPath
  InitPluginsDir
  File "/oname=$PLUGINSDIR\path.ps1" "path.ps1"
  !insertmacro RunPath "$PLUGINSDIR\path.ps1" Add
  ${If} $0 S== "0"
    WriteRegDWORD HKCU "${UNINSTKEY}" "AddedToPath" 1
  ${EndIf}
SectionEnd

Section "Uninstall"
  !insertmacro StopApp
  DeleteRegValue HKCU "${RUNKEY}" "${APP}"
  ReadRegDWORD $1 HKCU "${UNINSTKEY}" "AddedToPath"
  ${If} $1 == 1
    !insertmacro RunPath "$INSTDIR\path.ps1" Remove
  ${EndIf}
  Delete "$INSTDIR\s3-anysync.exe"
  Delete "$INSTDIR\s3-anysync-agent.exe"
  Delete "$INSTDIR\s3-anysync-cli.exe"
  Delete "$INSTDIR\path.ps1"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APP}.lnk"
  DeleteRegKey HKCU "${UNINSTKEY}"
  ; WebView2 user-data folder (go-webview2 default, named after the exe); cache only.
  RMDir /r "$APPDATA\s3-anysync.exe"
  MessageBox MB_OK "s3-anysync was removed. Your settings and sync data were kept in $APPDATA\s3-anysync; delete that folder to remove them too." /SD IDOK
SectionEnd
