# Build bin\hpm.exe from this checkout, or use a verified release binary.
# Windows PowerShell 5.1 is available on supported Windows installations.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
Set-Location (Split-Path -Parent $PSScriptRoot)
New-Item -ItemType Directory -Force -Path bin | Out-Null

function Fail([string]$message) {
    [Console]::Error.WriteLine("build.ps1: $message")
    exit 1
}

# A running executable cannot be overwritten on Windows. Rename it first and
# leave it aside until a later build can remove it after the process exits.
function Install-Binary([string]$source) {
    Get-ChildItem -Path bin -Filter 'hpm.*.old' | Remove-Item -Force -ErrorAction SilentlyContinue
    $previous = $null
    if (Test-Path 'bin/hpm.exe') {
        $previous = "bin/hpm.{0}.old" -f [guid]::NewGuid().ToString('N')
        Move-Item -Path 'bin/hpm.exe' -Destination $previous
    }
    try {
        Move-Item -Path $source -Destination 'bin/hpm.exe'
    } catch {
        if ($previous -and (Test-Path $previous)) {
            Move-Item -Path $previous -Destination 'bin/hpm.exe' -ErrorAction SilentlyContinue
        }
        throw
    }
}

$temp = "bin/hpm.{0}.tmp.exe" -f [guid]::NewGuid().ToString('N')
if (Get-Command go -ErrorAction SilentlyContinue) {
    & go build -o $temp ./cmd/hpm
    if ($LASTEXITCODE -ne 0) {
        Fail 'go build failed'
    }
    Install-Binary $temp
    exit 0
}

$version = Select-String -Path herdr-plugin.toml -Pattern '^version\s*=\s*"([^"]*)"' |
    Select-Object -First 1 |
    ForEach-Object { $_.Matches[0].Groups[1].Value }
if (-not $version) {
    Fail 'herdr-plugin.toml names no version'
}

# A 32-bit PowerShell process on a 64-bit machine reports the machine here.
$machine = $env:PROCESSOR_ARCHITEW6432
if (-not $machine) {
    $machine = $env:PROCESSOR_ARCHITECTURE
}
switch ($machine) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { Fail "no release binary for processor '$machine'; install Go and retry" }
}

$asset = "hpm_windows_$arch.zip"
$release = "https://github.com/vika2603/herdr-plugin-manager/releases/download/v$version"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

function Get-Release([string]$name, [string]$path) {
    try {
        Invoke-WebRequest -UseBasicParsing -Uri "$release/$name" -OutFile $path
    } catch {
        Fail "could not download $release/${name}: $($_.Exception.Message)"
    }
}

$tmp = "bin/.download.{0}" -f [guid]::NewGuid().ToString('N')
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Get-Release 'checksums.txt' "$tmp/checksums.txt"
    $expected = Get-Content "$tmp/checksums.txt" |
        ForEach-Object { if ($_ -match "^([0-9a-f]{64})  $([regex]::Escape($asset))$") { $Matches[1] } } |
        Select-Object -First 1
    if (-not $expected) {
        Fail "release checksums have no entry for $asset"
    }

    Get-Release $asset "$tmp/$asset"
    $actual = (Get-FileHash -Algorithm SHA256 -Path "$tmp/$asset").Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        Fail "checksum mismatch for $asset"
    }

    Expand-Archive -Path "$tmp/$asset" -DestinationPath "$tmp/unpacked"
    Move-Item -Path "$tmp/unpacked/hpm.exe" -Destination $temp
    Install-Binary $temp
} finally {
    Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
}
