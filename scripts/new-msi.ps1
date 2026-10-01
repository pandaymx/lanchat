<#
.SYNOPSIS
  Harvest a directory tree into a WiX v4 .wxs authoring file for LANChat.

.DESCRIPTION
  Reproduces the source folder hierarchy under Program Files with one
  Component per file (Guid="*"), a Start Menu shortcut and a MajorUpgrade
  rule. Only built-in WiX v4 namespaces are used (no extensions).
  Compile with: wix build -arch x64 file.wxs

.PARAMETER SourceDir
  Directory whose content is harvested (e.g. self-contained publish output).

.PARAMETER OutputWxs
  Path of the generated .wxs file.

.PARAMETER Version
  Four-part numeric ProductVersion, e.g. 0.13.0.5.

.PARAMETER UpgradeCode
  Stable GUID shared across releases (enables MajorUpgrade).
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$SourceDir,
    [Parameter(Mandatory = $true)][string]$OutputWxs,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$UpgradeCode
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = 'Stop'

$source = (Resolve-Path $SourceDir).Path.TrimEnd('\')

# Deterministic WiX id (letters/digits/underscore, <=72 chars) from a relative path.
function New-Id([string]$rel, [string]$prefix) {
    $clean = ($rel -replace '[^A-Za-z0-9]', '_').Trim('_')
    if ($clean.Length -gt 56) {
        $md5 = [System.Security.Cryptography.MD5]::Create()
        $hash = ([System.BitConverter]::ToString(
            $md5.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($rel.ToLowerInvariant()))) -replace '-', '').ToLower()
        $clean = $clean.Substring(0, 24) + '_' + $hash.Substring(0, 16)
    }
    return "${prefix}_$clean"
}

function Escape-Xml([string]$s) {
    return [System.Security.SecurityElement]::Escape($s)
}

# Collect all files with forward-slash relative paths.
$entries = @(Get-ChildItem -LiteralPath $source -File -Recurse | Sort-Object FullName | ForEach-Object {
    $rel = $_.FullName.Substring($source.Length + 1) -replace '\\', '/'
    [pscustomobject]@{
        Rel = $rel
        Dir = ($rel.Contains('/') ? ($rel.Substring(0, $rel.LastIndexOf('/'))) : '')
        Source = $_.FullName
    }
})

# Unique folder chains, shortest first, so every parent id exists before its child.
$folderChains = @($entries | ForEach-Object { $_.Dir } | Where-Object { $_ } | Sort-Object -Unique)

$dirIds = @{}

$sb = New-Object System.Text.StringBuilder
function Line([int]$level, [string]$text) {
    [void]$script:sb.AppendLine(('  ' * $level) + $text)
}

Line 0 '<?xml version="1.0" encoding="UTF-8"?>'
Line 0 '<Wix xmlns="http://wixtoolset.org/schemas/v4/wxs">'
Line 1 ('<Package Name="LANChat" Manufacturer="LANChat" Version="' + $Version + '"')
Line 1 ('           UpgradeCode="' + $UpgradeCode + '" Scope="perMachine" Compressed="yes">')
Line 2 '<MajorUpgrade DowngradeErrorMessage="A newer version of LANChat is already installed." />'
Line 2 '<MediaTemplate EmbedCab="yes" />'
Line 0 ''

# Directory tree (no components here).
Line 2 '<StandardDirectory Id="ProgramFiles64Folder">'
Line 3 '<Directory Id="APPLICATIONFOLDER" Name="LANChat">'
$dirIds[''] = 'APPLICATIONFOLDER'
foreach ($chain in $folderChains) {
    $parts = $chain -split '/'
    for ($i = 0; $i -lt $parts.Count; $i++) {
        $sub = ($parts[0..$i] -join '/')
        if (-not $dirIds.ContainsKey($sub)) {
            $id = New-Id $sub 'dir'
            $dirIds[$sub] = $id
            Line (4 + $i) "<Directory Id=`"$id`" Name=`"$(Escape-Xml $parts[$i])`" />"
        }
    }
}
Line 3 '</Directory>'
Line 2 '</StandardDirectory>'
Line 0 ''

# All components live in one ComponentGroup (a fragment). Each component references
# its target directory explicitly, so WiX pulls the whole group via ComponentGroupRef.
Line 1 '<Fragment>'
Line 2 '<ComponentGroup Id="AppFiles">'
foreach ($e in ($entries | Sort-Object Rel)) {
    $compId = New-Id $e.Rel 'cmp'
    $fileId = New-Id $e.Rel 'fil'
    Line 3 "<Component Id=`"$compId`" Directory=`"$($dirIds[$e.Dir])`" Guid=`"*`">"
    Line 4 "<File Id=`"$fileId`" Source=`"$(Escape-Xml $e.Source)`" KeyPath=`"yes`" />"
    Line 3 '</Component>'
}
Line 2 '</ComponentGroup>'
Line 1 '</Fragment>'
Line 0 ''

# Start Menu shortcut.
Line 2 '<StandardDirectory Id="ProgramMenuFolder">'
Line 3 '<Directory Id="ProgramMenuLANChat" Name="LANChat">'
Line 4 '<Component Id="StartMenuShortcut" Guid="*">'
Line 5 '<Shortcut Id="StartMenuLANChatShortcut" Name="LANChat"'
Line 6 'Target="[APPLICATIONFOLDER]LANChat.exe" WorkingDirectory="APPLICATIONFOLDER" />'
Line 5 '<RemoveFolder Id="RemoveProgramMenuLANChat" Directory="ProgramMenuLANChat" On="uninstall" />'
Line 5 '<RegistryValue Root="HKLM" Key="Software\LANChat" Name="startMenuInstalled" Type="integer" Value="1" KeyPath="yes" />'
Line 4 '</Component>'
Line 3 '</Directory>'
Line 2 '</StandardDirectory>'
Line 0 ''

Line 2 '<Feature Id="Main" Title="LANChat" Level="1">'
Line 3 '<ComponentGroupRef Id="AppFiles" />'
Line 3 '<ComponentRef Id="StartMenuShortcut" />'
Line 2 '</Feature>'
Line 1 '</Package>'
Line 0 '</Wix>'

$outDir = Split-Path $OutputWxs -Parent
if ($outDir -and -not (Test-Path $outDir)) { New-Item -ItemType Directory -Path $outDir -Force | Out-Null }
Set-Content -LiteralPath $OutputWxs -Value $sb.ToString() -Encoding UTF8
Write-Host "Generated $OutputWxs with $($entries.Count) files"
