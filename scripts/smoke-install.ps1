$ErrorActionPreference = 'Stop'
$Repository = Split-Path -Parent $PSScriptRoot
$SmokeRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('unidrop-smoke-' + [guid]::NewGuid().ToString('N'))
$UserData = Join-Path $SmokeRoot 'UserData'
$Downloads = Join-Path $SmokeRoot 'Downloads'

try {
    New-Item -ItemType Directory -Force -Path $UserData | Out-Null
    Set-Content -NoNewline -Path (Join-Path $UserData 'upgrade-sentinel') -Value 'preserve-me'

    & (Join-Path $Repository 'install.ps1') -NoStart -InstallRoot $SmokeRoot
    $Core = Join-Path $SmokeRoot 'App\unidrop.exe'
    $Tray = Join-Path $SmokeRoot 'App\unidrop-tray.exe'
    if (-not (Test-Path $Core) -or -not (Test-Path $Tray)) { throw 'installed binaries are missing' }
    $Version = (Get-Content (Join-Path $Repository 'internal\version\VERSION') -Raw).Trim()
    if ((& $Core --version) -notmatch [regex]::Escape("UniDrop $Version")) { throw 'installed version is wrong' }

    & (Join-Path $Repository 'install.ps1') -NoStart -InstallRoot $SmokeRoot
    if ((Get-Content (Join-Path $UserData 'upgrade-sentinel') -Raw) -ne 'preserve-me') { throw 'upgrade changed user data' }
    if (-not (Test-Path (Join-Path $SmokeRoot 'Startup\UniDrop.lnk'))) { throw 'startup shortcut is missing' }
    if (-not (Test-Path (Join-Path $SmokeRoot 'Programs\UniDrop.lnk'))) { throw 'Start-menu shortcut is missing' }
    if (-not (Test-Path (Join-Path $SmokeRoot 'Programs\Uninstall UniDrop.lnk'))) { throw 'uninstall shortcut is missing' }

    $OldConfig = $env:UNIDROP_CONFIG_DIR
    $OldDownloads = $env:UNIDROP_DOWNLOAD_DIR
    try {
        $env:UNIDROP_CONFIG_DIR = $UserData
        $env:UNIDROP_DOWNLOAD_DIR = $Downloads
        $Process = Start-Process -FilePath $Core -ArgumentList '--no-open' -PassThru
        $Deadline = [DateTime]::UtcNow.AddSeconds(10)
        while (-not (Test-Path (Join-Path $UserData 'control-token')) -and [DateTime]::UtcNow -lt $Deadline) {
            Start-Sleep -Milliseconds 100
        }
        if (-not (Test-Path (Join-Path $UserData 'control-token'))) { throw 'core did not start' }
        & $Core stop
        if (-not $Process.WaitForExit(10000)) { throw 'core did not stop cleanly' }
    } finally {
        $env:UNIDROP_CONFIG_DIR = $OldConfig
        $env:UNIDROP_DOWNLOAD_DIR = $OldDownloads
        if ($Process -and -not $Process.HasExited) { $Process.Kill() }
    }

    & (Join-Path $Repository 'uninstall.ps1') -InstallRoot $SmokeRoot
    if (Test-Path $Core) { throw 'uninstall left the core binary behind' }
    if ((Get-Content (Join-Path $UserData 'upgrade-sentinel') -Raw) -ne 'preserve-me') { throw 'uninstall removed user data' }

    & (Join-Path $Repository 'install.ps1') -NoStart -InstallRoot $SmokeRoot
    & (Join-Path $Repository 'uninstall.ps1') -InstallRoot $SmokeRoot -RemoveUserData
    if (Test-Path $Core) { throw 'reinstall/uninstall left the core binary behind' }
    if (Test-Path $UserData) { throw 'explicit user-data removal failed' }

    Write-Host 'UniDrop Windows install, upgrade, lifecycle, uninstall, and reinstall smoke test passed.'
} finally {
    if (Test-Path $SmokeRoot) { Remove-Item -LiteralPath $SmokeRoot -Recurse -Force }
}
