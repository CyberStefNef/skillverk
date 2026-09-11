# Install Skillverk on Windows.
#
#   powershell -c "irm https://raw.githubusercontent.com/CyberStefNef/skillverk/main/scripts/install.ps1 | iex"
#
# Environment:
#   SKILLVERK_VERSION      release tag to install, "latest" by default
#   SKILLVERK_INSTALL_DIR  where the binary goes, %LOCALAPPDATA%\skillverk\bin by default
#   SKILLVERK_NO_MODIFY_PATH=1  skip the user PATH edit

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo = 'CyberStefNef/skillverk'
$version = if ($env:SKILLVERK_VERSION) { $env:SKILLVERK_VERSION } else { 'latest' }
$installDir = if ($env:SKILLVERK_INSTALL_DIR) { $env:SKILLVERK_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'skillverk\bin' }

$arch = switch -Wildcard ("$env:PROCESSOR_ARCHITECTURE") {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE. Build from source instead: https://github.com/$repo" }
}

$asset = "skillverk-windows-$arch.zip"
$base = if ($version -eq 'latest') {
    "https://github.com/$repo/releases/latest/download"
} else {
    "https://github.com/$repo/releases/download/$version"
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("skillverk-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    $archive = Join-Path $tmp $asset
    Write-Host "Downloading $asset ($version)"
    try {
        Invoke-WebRequest -Uri "$base/$asset" -OutFile $archive -UseBasicParsing
    } catch {
        throw "No build for windows-$arch at $version. See https://github.com/$repo/releases"
    }

    $sums = Join-Path $tmp 'checksums.txt'
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile $sums -UseBasicParsing

    $line = Get-Content $sums | Where-Object { $_ -match "[ *]$([regex]::Escape($asset))$" } | Select-Object -First 1
    if (-not $line) { throw "checksums.txt has no entry for $asset" }
    $want = ($line -split '\s+')[0]
    $got = (Get-FileHash -Path $archive -Algorithm SHA256).Hash.ToLower()
    if ($want.ToLower() -ne $got) {
        throw "Checksum mismatch for ${asset}: expected $want, got $got"
    }

    Expand-Archive -Path $archive -DestinationPath $tmp -Force
    $binary = Join-Path $tmp 'skillverk.exe'
    if (-not (Test-Path $binary)) { throw 'Archive did not contain skillverk.exe' }

    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
    $target = Join-Path $installDir 'skillverk.exe'
    # Windows will not overwrite a running binary, so move the old one aside.
    if (Test-Path $target) {
        $old = "$target.old"
        Remove-Item $old -Force -ErrorAction SilentlyContinue
        Move-Item $target $old -Force
    }
    Move-Item $binary $target -Force

    $reported = & $target --version
    Write-Host "Installed $reported to $target"
} finally {
    Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

if ($env:SKILLVERK_NO_MODIFY_PATH -eq '1') {
    Write-Host ''
    Write-Host "Add $installDir to your PATH to run skillverk from any directory."
    return
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $installDir) {
    $joined = if ($userPath) { "$installDir;$userPath" } else { $installDir }
    [Environment]::SetEnvironmentVariable('Path', $joined, 'User')
    Write-Host ''
    Write-Host "Added $installDir to your user PATH."
}
$env:Path = "$installDir;$env:Path"
Write-Host 'Open a new terminal, then run: skillverk'
