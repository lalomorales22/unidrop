# Xendfile per-user installer for Windows 10/11.
[CmdletBinding()]
param(
    [switch]$NoStart,
    [string]$InstallRoot
)
$ErrorActionPreference = 'Stop'

$GoVersion = '1.26.5'
$MinimumGoVersion = [version]'1.25.0'
$ScriptDirectory = $PSScriptRoot
$AppVersion = (Get-Content (Join-Path $ScriptDirectory 'internal\version\VERSION') -Raw).Trim()
$TempDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("xendfile-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $TempDirectory | Out-Null

function Write-Xendfile([string]$Message) { Write-Host "Xendfile: $Message" }

function Get-XendfileGo {
    if ($script:GoExecutable) { return $script:GoExecutable }
    $GoCommand = Get-Command go -ErrorAction SilentlyContinue
    if ($GoCommand) {
        try {
            $InstalledGoVersionText = (& $GoCommand.Source env GOVERSION).Trim()
            if ($InstalledGoVersionText.StartsWith('go')) { $InstalledGoVersionText = $InstalledGoVersionText.Substring(2) }
            $InstalledGoVersion = [version]$InstalledGoVersionText
            if ($InstalledGoVersion -lt $MinimumGoVersion) {
                Write-Xendfile "the installed Go compiler is older than $MinimumGoVersion; using the verified current toolchain"
                $GoCommand = $null
            }
        } catch {
            $GoCommand = $null
        }
    }
    if ($GoCommand) {
        $script:GoExecutable = $GoCommand.Source
        Write-Xendfile 'building with the installed Go compiler'
        return $script:GoExecutable
    }
    $ArchiveName = "go$GoVersion.windows-$TargetArch.zip"
    $ArchivePath = Join-Path $TempDirectory $ArchiveName
    Write-Xendfile "fetching the official Go $GoVersion toolchain from go.dev"
    Invoke-WebRequest -UseBasicParsing -Uri "https://go.dev/dl/$ArchiveName" -OutFile $ArchivePath
    $ActualHash = (Get-FileHash -Algorithm SHA256 $ArchivePath).Hash.ToLowerInvariant()
    if ($ActualHash -ne $ExpectedHash) { throw "SHA-256 verification failed for $ArchiveName" }
    Expand-Archive -Path $ArchivePath -DestinationPath $TempDirectory
    $script:GoExecutable = Join-Path $TempDirectory 'go\bin\go.exe'
    return $script:GoExecutable
}

function Install-BundledBinary([string]$Bundled, [string]$Destination) {
    $Manifest = Join-Path $ScriptDirectory 'dist\SHA256SUMS'
    if (Test-Path $Manifest) {
        $BundledName = [System.IO.Path]::GetFileName($Bundled)
        $ManifestLine = Get-Content $Manifest | Where-Object { $_ -match "^[0-9a-fA-F]{64}\s+\*?$([regex]::Escape($BundledName))$" } | Select-Object -First 1
        if (-not $ManifestLine) { throw "$BundledName is missing from SHA256SUMS" }
        $ExpectedBinaryHash = ($ManifestLine -split '\s+')[0].ToLowerInvariant()
        $ActualBinaryHash = (Get-FileHash -Algorithm SHA256 $Bundled).Hash.ToLowerInvariant()
        if ($ActualBinaryHash -ne $ExpectedBinaryHash) { throw "SHA-256 verification failed for $BundledName" }
    }
    Copy-Item -Force $Bundled $Destination
}

function Build-XendfileBinary([string]$Package, [string]$Destination, [switch]$WindowsGUI) {
    $GoExecutable = Get-XendfileGo
    if (-not (Test-Path (Join-Path $ScriptDirectory 'go.mod')) -or -not (Test-Path (Join-Path $ScriptDirectory 'vendor'))) {
        throw 'Vendored Xendfile source or a bundled binary is required'
    }
    $OldCgo = $env:CGO_ENABLED
    $OldGoos = $env:GOOS
    $OldGoarch = $env:GOARCH
    try {
        $env:CGO_ENABLED = '0'; $env:GOOS = 'windows'; $env:GOARCH = $TargetArch
        $LinkerFlags = "-s -w -X main.appVersion=$AppVersion"
        if ($WindowsGUI) { $LinkerFlags = "-s -w -H=windowsgui -X main.appVersion=$AppVersion" }
        Push-Location $ScriptDirectory
        try {
            & $GoExecutable build -mod=vendor -trimpath "-ldflags=$LinkerFlags" -o $Destination $Package
            if ($LASTEXITCODE -ne 0) { throw "Go build failed for $Package" }
        } finally { Pop-Location }
    } finally {
        $env:CGO_ENABLED = $OldCgo; $env:GOOS = $OldGoos; $env:GOARCH = $OldGoarch
    }
}

try {
    $Machine = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    switch ($Machine) {
        'X64'   { $TargetArch = 'amd64'; $ExpectedHash = '97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38' }
        'Arm64' { $TargetArch = 'arm64'; $ExpectedHash = 'f96ee46396d69f1e231c8d981ec6a70216238a646a1f2cd74aea0d0016bbc017' }
        default { throw "Unsupported CPU architecture: $Machine" }
    }

    $IsIsolatedInstall = -not [string]::IsNullOrWhiteSpace($InstallRoot)
    if ($IsIsolatedInstall) {
        $InstallRoot = [System.IO.Path]::GetFullPath($InstallRoot)
        if ($InstallRoot -eq [System.IO.Path]::GetPathRoot($InstallRoot)) { throw 'Refusing an unsafe InstallRoot' }
        $InstallDirectory = Join-Path $InstallRoot 'App'
        $StartupDirectory = Join-Path $InstallRoot 'Startup'
        $ProgramsDirectory = Join-Path $InstallRoot 'Programs'
        $LegacyInstallDirectory = $null
        New-Item -ItemType Directory -Force -Path $StartupDirectory, $ProgramsDirectory | Out-Null
    } else {
        $InstallDirectory = Join-Path $env:LOCALAPPDATA 'Xendfile'
        $LegacyInstallDirectory = Join-Path $env:LOCALAPPDATA 'UniDrop'
        $StartupDirectory = [Environment]::GetFolderPath('Startup')
        $ProgramsDirectory = [Environment]::GetFolderPath('Programs')
    }
    $Destination = Join-Path $InstallDirectory 'xendfile.exe'
    $TrayDestination = Join-Path $InstallDirectory 'xendfile-tray.exe'
    New-Item -ItemType Directory -Force -Path $InstallDirectory | Out-Null
    $Bundled = Join-Path $ScriptDirectory "dist\xendfile-windows-$TargetArch.exe"
    $BundledTray = Join-Path $ScriptDirectory "dist\xendfile-tray-windows-$TargetArch.exe"

    # Stop this user's existing processes so an upgrade can replace both executables.
    if (-not $IsIsolatedInstall) {
        Get-Process xendfile, xendfile-tray, unidrop, unidrop-tray -ErrorAction SilentlyContinue | Stop-Process -Force
    }

    if (Test-Path $Bundled) {
        Write-Xendfile "using bundled Windows/$TargetArch core"
        Install-BundledBinary $Bundled $Destination
    } else {
        Write-Xendfile "building Xendfile $AppVersion (standard library only)"
        Build-XendfileBinary '.' $Destination
    }
    if (Test-Path $BundledTray) {
        Write-Xendfile "using bundled Windows/$TargetArch notification-area companion"
        Install-BundledBinary $BundledTray $TrayDestination
    } else {
        Write-Xendfile 'building the native Windows notification-area companion'
        Build-XendfileBinary './cmd/xendfile-tray-windows' $TrayDestination -WindowsGUI
    }

    # Keep the installed binary console-capable so `xendfile send` and
    # `xendfile peers` behave normally in PowerShell. The separate tray binary
    # uses the Windows GUI subsystem, so startup never flashes a console.
    $BackgroundLauncher = Join-Path $InstallDirectory 'xendfile-background.vbs'
    $OpenLauncher = Join-Path $InstallDirectory 'xendfile-open.vbs'
    Remove-Item $BackgroundLauncher, $OpenLauncher -Force -ErrorAction SilentlyContinue

    if (-not $IsIsolatedInstall) {
        $UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $PathParts = @($UserPath -split ';' | Where-Object {
            $_ -and $_.TrimEnd('\') -ine $LegacyInstallDirectory.TrimEnd('\')
        })
        $HadXendfilePath = [bool]($PathParts | Where-Object { $_.TrimEnd('\') -ieq $InstallDirectory.TrimEnd('\') })
        if (-not $HadXendfilePath) { $PathParts += $InstallDirectory }
        [Environment]::SetEnvironmentVariable('Path', ($PathParts -join ';'), 'User')
        if (-not $HadXendfilePath) {
            $env:Path = "$env:Path;$InstallDirectory"
            Write-Xendfile 'added the Xendfile command to your user PATH (new terminals will see it)'
        }
    }

    $Shell = New-Object -ComObject WScript.Shell
    $StartupShortcut = $Shell.CreateShortcut((Join-Path $StartupDirectory 'Xendfile.lnk'))
    $StartupShortcut.TargetPath = $TrayDestination
    $StartupShortcut.Arguments = ''
    $StartupShortcut.WorkingDirectory = $InstallDirectory
    $StartupShortcut.Description = 'Xendfile secure local file sharing'
    $StartupShortcut.Save()

    $MenuShortcut = $Shell.CreateShortcut((Join-Path $ProgramsDirectory 'Xendfile.lnk'))
    $MenuShortcut.TargetPath = $TrayDestination
    $MenuShortcut.Arguments = '--open'
    $MenuShortcut.WorkingDirectory = $InstallDirectory
    $MenuShortcut.Description = 'Open Xendfile'
    $MenuShortcut.Save()

    Copy-Item -Force (Join-Path $ScriptDirectory 'uninstall.ps1') (Join-Path $InstallDirectory 'uninstall.ps1')
    Copy-Item -Force (Join-Path $ScriptDirectory 'LICENSE') (Join-Path $InstallDirectory 'LICENSE')
    Copy-Item -Force (Join-Path $ScriptDirectory 'NOTICE') (Join-Path $InstallDirectory 'NOTICE')
    Copy-Item -Force (Join-Path $ScriptDirectory 'THIRD_PARTY_NOTICES.md') (Join-Path $InstallDirectory 'THIRD_PARTY_NOTICES.md')
    $UninstallShortcut = $Shell.CreateShortcut((Join-Path $ProgramsDirectory 'Uninstall Xendfile.lnk'))
    $UninstallShortcut.TargetPath = 'powershell.exe'
    $UninstallShortcut.Arguments = '-NoProfile -ExecutionPolicy Bypass -File "' + (Join-Path $InstallDirectory 'uninstall.ps1') + '"'
    if ($IsIsolatedInstall) { $UninstallShortcut.Arguments += ' -InstallRoot "' + $InstallRoot + '"' }
    $UninstallShortcut.WorkingDirectory = $InstallDirectory
    $UninstallShortcut.Description = 'Uninstall Xendfile while preserving paired-device data'
    $UninstallShortcut.Save()

    if (-not $IsIsolatedInstall) {
        $LegacyShortcuts = @(
            (Join-Path $StartupDirectory 'UniDrop.lnk')
            (Join-Path $ProgramsDirectory 'UniDrop.lnk')
            (Join-Path $ProgramsDirectory 'Uninstall UniDrop.lnk')
        )
        Remove-Item -LiteralPath $LegacyShortcuts -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $LegacyInstallDirectory) {
            Remove-Item -LiteralPath $LegacyInstallDirectory -Recurse -Force
        }
    }

    Write-Xendfile "installed $Destination and $TrayDestination"
    if (-not $NoStart) {
        Start-Process -FilePath $TrayDestination -WorkingDirectory $InstallDirectory
        Write-Xendfile 'Xendfile is running in the Windows notification area. Click its icon or open it from Start.'
    } else {
        Write-Xendfile 'startup shortcuts installed; automatic start was skipped'
    }
    Write-Xendfile 'Windows may ask once for permission to communicate on private networks.'
    Write-Xendfile 'From a new PowerShell window, try: xendfile peers'
} finally {
    if (Test-Path $TempDirectory) { Remove-Item -Recurse -Force $TempDirectory }
}
