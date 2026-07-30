# UniDrop per-user installer for Windows 10/11.
[CmdletBinding()]
param([switch]$NoStart)
$ErrorActionPreference = 'Stop'

$AppVersion = '0.1.0'
$GoVersion = '1.26.5'
$ScriptDirectory = $PSScriptRoot
$TempDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ("unidrop-install-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $TempDirectory | Out-Null

function Write-UniDrop([string]$Message) { Write-Host "UniDrop: $Message" }

try {
    $Machine = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    switch ($Machine) {
        'X64'   { $TargetArch = 'amd64'; $ExpectedHash = '97e6b2a833b6d89f9ff17d25419ac0a7e3b482a044e9ab18cdef834bd834fd38' }
        'Arm64' { $TargetArch = 'arm64'; $ExpectedHash = 'f96ee46396d69f1e231c8d981ec6a70216238a646a1f2cd74aea0d0016bbc017' }
        default { throw "Unsupported CPU architecture: $Machine" }
    }

    $InstallDirectory = Join-Path $env:LOCALAPPDATA 'UniDrop'
    $Destination = Join-Path $InstallDirectory 'unidrop.exe'
    New-Item -ItemType Directory -Force -Path $InstallDirectory | Out-Null
    $Bundled = Join-Path $ScriptDirectory "dist\unidrop-windows-$TargetArch.exe"

    # Stop this user's existing instance so an upgrade can replace the executable.
    Get-Process unidrop -ErrorAction SilentlyContinue | Stop-Process -Force

    if (Test-Path $Bundled) {
        Write-UniDrop "using bundled Windows/$TargetArch binary"
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
    } else {
        $GoCommand = Get-Command go -ErrorAction SilentlyContinue
        if (-not $GoCommand) {
            $ArchiveName = "go$GoVersion.windows-$TargetArch.zip"
            $ArchivePath = Join-Path $TempDirectory $ArchiveName
            Write-UniDrop "fetching the official Go $GoVersion toolchain from go.dev"
            Invoke-WebRequest -UseBasicParsing -Uri "https://go.dev/dl/$ArchiveName" -OutFile $ArchivePath
            $ActualHash = (Get-FileHash -Algorithm SHA256 $ArchivePath).Hash.ToLowerInvariant()
            if ($ActualHash -ne $ExpectedHash) { throw "SHA-256 verification failed for $ArchiveName" }
            Expand-Archive -Path $ArchivePath -DestinationPath $TempDirectory
            $GoExecutable = Join-Path $TempDirectory 'go\bin\go.exe'
        } else {
            $GoExecutable = $GoCommand.Source
            Write-UniDrop 'building with the installed Go compiler'
        }

        if (-not (Test-Path (Join-Path $ScriptDirectory 'go.mod')) -or -not (Test-Path (Join-Path $ScriptDirectory 'main.go'))) {
            throw 'Source files or a bundled binary are required'
        }
        Write-UniDrop "building UniDrop $AppVersion (standard library only)"
        $OldCgo = $env:CGO_ENABLED
        $OldGoos = $env:GOOS
        $OldGoarch = $env:GOARCH
        try {
            $env:CGO_ENABLED = '0'; $env:GOOS = 'windows'; $env:GOARCH = $TargetArch
            Push-Location $ScriptDirectory
            try {
                & $GoExecutable build -trimpath "-ldflags=-s -w -H=windowsgui -X main.appVersion=$AppVersion" -o $Destination .
                if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
            } finally { Pop-Location }
        } finally {
            $env:CGO_ENABLED = $OldCgo; $env:GOOS = $OldGoos; $env:GOARCH = $OldGoarch
        }
    }

    $Shell = New-Object -ComObject WScript.Shell
    $StartupDirectory = [Environment]::GetFolderPath('Startup')
    $StartupShortcut = $Shell.CreateShortcut((Join-Path $StartupDirectory 'UniDrop.lnk'))
    $StartupShortcut.TargetPath = $Destination
    $StartupShortcut.Arguments = '--no-open'
    $StartupShortcut.WorkingDirectory = $InstallDirectory
    $StartupShortcut.Description = 'UniDrop secure local file sharing service'
    $StartupShortcut.Save()

    $ProgramsDirectory = [Environment]::GetFolderPath('Programs')
    $MenuShortcut = $Shell.CreateShortcut((Join-Path $ProgramsDirectory 'UniDrop.lnk'))
    $MenuShortcut.TargetPath = $Destination
    $MenuShortcut.Arguments = '--open'
    $MenuShortcut.WorkingDirectory = $InstallDirectory
    $MenuShortcut.Description = 'Open UniDrop'
    $MenuShortcut.Save()

    if (-not $NoStart) {
        Start-Process -FilePath $Destination -ArgumentList '--no-open' -WorkingDirectory $InstallDirectory
    }
    Write-UniDrop "installed $Destination"
    Write-UniDrop 'Open UniDrop from the Start menu or visit http://127.0.0.1:43337'
    Write-UniDrop 'Windows may ask once for permission to communicate on private networks.'
} finally {
    if (Test-Path $TempDirectory) { Remove-Item -Recurse -Force $TempDirectory }
}
