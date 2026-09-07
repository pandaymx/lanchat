; LAN Chat 桌面端 Windows 安装器（NSIS，M10.3）
; CI 用法（在 dist/raw 下执行）：
;   makensis /DVERSION=0.9.0 /DOUTFILE=../../dist/lanchat-desktop-setup-0.9.0-windows-amd64.exe ../../.github/installer/desktop.nsi
; 功能：安装到 Program Files、开始菜单快捷方式、注册卸载项（控制面板可卸载）。

Unicode true
!include "MUI2.nsh"

; File 指令的路径相对「脚本所在目录」而非 makensis 的 cwd；CI 在
; dist/raw 下调用本脚本，二进制在 dist/raw，这里先把编译 cwd 切过去。
; !cd 路径相对脚本目录（.github/installer -> ../../dist/raw = repo/dist/raw）。
!cd "..\..\dist\raw"

Name "LAN Chat"
!ifndef OUTFILE
  !define OUTFILE "lanchat-desktop-setup.exe"
!endif
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\LAN Chat"
InstallDirRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat" "InstallLocation"
RequestExecutionLevel admin

!ifndef VERSION
  !define VERSION "0.0.0"
!endif

!define MUI_ABORTWARNING
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Section "LAN Chat" SEC01
  SetOutPath "$INSTDIR"
  File "lanchat-desktop.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateDirectory "$SMPROGRAMS\LAN Chat"
  CreateShortcut "$SMPROGRAMS\LAN Chat\LAN Chat.lnk" "$INSTDIR\lanchat-desktop.exe"

  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat" "DisplayName" "LAN Chat"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat" "Publisher" "pandaymx"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat" "NoModify" 1
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat" "NoRepair" 1
SectionEnd

Section "Uninstall"
  Delete "$SMPROGRAMS\LAN Chat\LAN Chat.lnk"
  RMDir "$SMPROGRAMS\LAN Chat"
  Delete "$INSTDIR\lanchat-desktop.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\LAN Chat"
SectionEnd