# Xendfile per-user uninstaller for Windows 10/11.
[CmdletBinding()]
param(
    [switch]$RemoveUserData,
    [string]$InstallRoot
)
$ErrorActionPreference = 'Stop'

$IsIsolatedInstall = -not [string]::IsNullOrWhiteSpace($InstallRoot)
if ($IsIsolatedInstall) {
    $InstallRoot = [System.IO.Path]::GetFullPath($InstallRoot)
    if ($InstallRoot -eq [System.IO.Path]::GetPathRoot($InstallRoot)) { throw 'Refusing an unsafe InstallRoot' }
    $InstallDirectory = Join-Path $InstallRoot 'App'
    $StartupDirectory = Join-Path $InstallRoot 'Startup'
    $ProgramsDirectory = Join-Path $InstallRoot 'Programs'
    $UserDataDirectory = Join-Path $InstallRoot 'UserData'
} else {
    $InstallDirectory = Join-Path $env:LOCALAPPDATA 'Xendfile'
    $LegacyInstallDirectory = Join-Path $env:LOCALAPPDATA 'UniDrop'
    $StartupDirectory = [Environment]::GetFolderPath('Startup')
    $ProgramsDirectory = [Environment]::GetFolderPath('Programs')
    $UserDataDirectory = Join-Path $env:APPDATA 'Xendfile'
}

if (-not $IsIsolatedInstall) {
    Get-Process xendfile, xendfile-tray, unidrop, unidrop-tray -ErrorAction SilentlyContinue | Stop-Process -Force
}

$ShortcutPaths = @(
    (Join-Path $StartupDirectory 'Xendfile.lnk')
    (Join-Path $ProgramsDirectory 'Xendfile.lnk')
    (Join-Path $ProgramsDirectory 'Uninstall Xendfile.lnk')
    (Join-Path $StartupDirectory 'UniDrop.lnk')
    (Join-Path $ProgramsDirectory 'UniDrop.lnk')
    (Join-Path $ProgramsDirectory 'Uninstall UniDrop.lnk')
)
Remove-Item -LiteralPath $ShortcutPaths -Force -ErrorAction SilentlyContinue

if (-not $IsIsolatedInstall) {
    $UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $KeptParts = @($UserPath -split ';' | Where-Object {
        $_ -and
        $_.TrimEnd('\') -ine $InstallDirectory.TrimEnd('\') -and
        $_.TrimEnd('\') -ine $LegacyInstallDirectory.TrimEnd('\')
    })
    [Environment]::SetEnvironmentVariable('Path', ($KeptParts -join ';'), 'User')
}

if ($RemoveUserData -and (Test-Path -LiteralPath $UserDataDirectory)) {
    Remove-Item -LiteralPath $UserDataDirectory -Recurse -Force
}
if ($RemoveUserData -and -not $IsIsolatedInstall) {
    $LegacyUserDataDirectory = Join-Path $env:APPDATA 'UniDrop'
    if (Test-Path -LiteralPath $LegacyUserDataDirectory) {
        Remove-Item -LiteralPath $LegacyUserDataDirectory -Recurse -Force
    }
}

$CurrentScript = [System.IO.Path]::GetFullPath($PSCommandPath)
$InstalledUninstaller = [System.IO.Path]::GetFullPath((Join-Path $InstallDirectory 'uninstall.ps1'))
if ($CurrentScript -ieq $InstalledUninstaller -and (Test-Path -LiteralPath $InstallDirectory)) {
    Get-ChildItem -LiteralPath $InstallDirectory -Force | Where-Object {
        [System.IO.Path]::GetFullPath($_.FullName) -ine $CurrentScript
    } | Remove-Item -Recurse -Force
    $EscapedScript = $CurrentScript.Replace("'", "''")
    $EscapedDirectory = $InstallDirectory.Replace("'", "''")
    $Cleanup = "Wait-Process -Id $PID -ErrorAction SilentlyContinue; Remove-Item -LiteralPath '$EscapedScript' -Force -ErrorAction SilentlyContinue; Remove-Item -LiteralPath '$EscapedDirectory' -Force -ErrorAction SilentlyContinue"
    $EncodedCleanup = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($Cleanup))
    Start-Process powershell.exe -WindowStyle Hidden -ArgumentList '-NoProfile', '-EncodedCommand', $EncodedCleanup
} elseif (Test-Path -LiteralPath $InstallDirectory) {
    Remove-Item -LiteralPath $InstallDirectory -Recurse -Force
}

if (-not $IsIsolatedInstall -and (Test-Path -LiteralPath $LegacyInstallDirectory)) {
    Remove-Item -LiteralPath $LegacyInstallDirectory -Recurse -Force
}

if ($RemoveUserData) {
    Write-Host 'Xendfile application files and local identity/pairing data were removed; received files were preserved.'
} else {
    Write-Host 'Xendfile application files were removed; local identity and paired-device data were preserved.'
}
