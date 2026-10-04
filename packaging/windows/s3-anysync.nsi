; s3-anysync per-user installer. Built by scripts/release.sh:
;   makensis -DVERSION=.. -DVERSION4=.. -DSRCDIR=.. -DICON=.. -DOUTFILE=.. s3-anysync.nsi
Unicode true
!include "MUI2.nsh"
!include "Sections.nsh"
!include "LogicLib.nsh"

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
VIAddVersionKey "CompanyName" "niceit"
VIAddVersionKey "FileDescription" "${APP} installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "niceit"

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
!macro StopApp
  nsExec::Exec 'taskkill /IM s3-anysync-agent.exe /F'
  Pop $0
  nsExec::Exec 'taskkill /IM s3-anysync.exe /F'
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
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\${APP}.lnk" "$INSTDIR\s3-anysync.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "${APP}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "Publisher" "niceit"
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\s3-anysync.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
SectionEnd

; PATH editing goes through PowerShell: NSIS string buffers are limited and
; could truncate a long PATH.
Section /o "Add the command-line tool to PATH" SecPath
  nsExec::ExecToLog `powershell -NoProfile -ExecutionPolicy Bypass -Command "$$d='$INSTDIR'; $$p=[Environment]::GetEnvironmentVariable('Path','User'); $$parts=@(); if ($$p) { $$parts=$$p.Split(';') | Where-Object { $$_ -ne '' } }; if ($$parts -notcontains $$d) { [Environment]::SetEnvironmentVariable('Path', (($$parts + $$d) -join ';'), 'User') }"`
  Pop $0
  WriteRegDWORD HKCU "${UNINSTKEY}" "AddedToPath" 1
SectionEnd

Section "Uninstall"
  !insertmacro StopApp
  DeleteRegValue HKCU "${RUNKEY}" "${APP}"
  ReadRegDWORD $1 HKCU "${UNINSTKEY}" "AddedToPath"
  ${If} $1 == 1
    nsExec::ExecToLog `powershell -NoProfile -ExecutionPolicy Bypass -Command "$$d='$INSTDIR'; $$p=[Environment]::GetEnvironmentVariable('Path','User'); if ($$p) { [Environment]::SetEnvironmentVariable('Path', (($$p.Split(';') | Where-Object { $$_ -ne '' -and $$_ -ne $$d }) -join ';'), 'User') }"`
    Pop $0
  ${EndIf}
  Delete "$INSTDIR\s3-anysync.exe"
  Delete "$INSTDIR\s3-anysync-agent.exe"
  Delete "$INSTDIR\s3-anysync-cli.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${APP}.lnk"
  DeleteRegKey HKCU "${UNINSTKEY}"
  MessageBox MB_OK "s3-anysync was removed. Your settings and sync data were kept in $APPDATA\s3-anysync; delete that folder to remove them too."
SectionEnd
