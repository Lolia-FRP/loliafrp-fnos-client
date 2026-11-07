$ErrorActionPreference = 'Stop'

$ROOT = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path

$ARCH = 'amd64'
$VERSION = '1.0.0-beta'
$CORE = ''
$CORE_VERSION = 'bundled'
$FNPACK = if ($env:FNPACK_BIN) { $env:FNPACK_BIN } else { Join-Path $ROOT 'build/tools/fnpack' }
$STAGE_ONLY = $false
$SKIP_FRONTEND = $false

function Fail {
    param([Parameter(Mandatory)][string]$Message)
    [Console]::Error.WriteLine($Message)
    exit 1
}

function Set-Executable {
    param([Parameter(Mandatory)][string]$Path)
    if (Get-Command chmod -ErrorAction SilentlyContinue) {
        & chmod 755 -- $Path
    }
}

$i = 0
while ($i -lt $args.Count) {
    $arg = $args[$i]
    if ($arg -in '--arch', '--version', '--core', '--core-version', '--fnpack') {
        if ($i + 1 -ge $args.Count) { Fail "Missing value for $arg" }
        $val = $args[$i + 1]
        switch ($arg) {
            '--arch'         { $ARCH         = $val }
            '--version'      { $VERSION      = $val }
            '--core'         { $CORE         = $val }
            '--core-version' { $CORE_VERSION = $val }
            '--fnpack'       { $FNPACK       = $val }
        }
        $i += 2
    }
    elseif ($arg -eq '--stage-only')    { $STAGE_ONLY    = $true; $i++ }
    elseif ($arg -eq '--skip-frontend') { $SKIP_FRONTEND = $true; $i++ }
    else { Fail "Unknown argument: $arg" }
}

switch ($ARCH) {
    'amd64' { $PLATFORM = 'x86' }
    'arm64' { $PLATFORM = 'arm' }
    default { Fail 'Supported architectures: amd64, arm64' }
}

if ($VERSION -notmatch '^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$') {
    Fail 'Invalid version'
}

$buildDir = Join-Path $ROOT 'build'
$STAGE = Join-Path $buildDir "loliafrp-$VERSION-$ARCH"

New-Item -ItemType Directory -Force -Path $buildDir | Out-Null
if (Test-Path $STAGE) { Remove-Item -Recurse -Force $STAGE }

Copy-Item -Recurse -Path (Join-Path $ROOT 'packaging/fnos') -Destination $STAGE

New-Item -ItemType Directory -Force -Path (Join-Path $STAGE 'app/bin') | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $STAGE 'app/web') | Out-Null

if (-not $SKIP_FRONTEND) {
    Push-Location (Join-Path $ROOT 'frontend')
    try {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw 'npm run build failed' }
    } finally {
        Pop-Location
    }
}

$distDir = Join-Path $ROOT 'frontend/dist'
$webDir = Join-Path $STAGE 'app/web'
Get-ChildItem -Path $distDir -Force | ForEach-Object {
    Copy-Item -Recurse -Force -Path $_.FullName -Destination $webDir
}

$envNames = @('CGO_ENABLED', 'GOOS', 'GOARCH')
$savedEnv = @{}
foreach ($n in $envNames) { $savedEnv[$n] = [Environment]::GetEnvironmentVariable($n) }

Push-Location $ROOT
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'linux'
    $env:GOARCH = $ARCH
    $ldflags = "-s -w -X github.com/LoliaFRP/loliafrp-fnos/backend/internal/app.Version=$VERSION"
    $outBin = Join-Path $STAGE 'app/bin/loliafrp-manager'
    go build -trimpath "-ldflags=$ldflags" -o $outBin ./backend/cmd/loliafrp-manager
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
} finally {
    Pop-Location
    foreach ($n in $envNames) {
        [Environment]::SetEnvironmentVariable($n, $savedEnv[$n])
    }
}

if ($CORE) {
    $frpcPath = Join-Path $STAGE 'app/bin/frpc'
    Copy-Item -Force -Path $CORE -Destination $frpcPath
    [System.IO.File]::WriteAllText(
        (Join-Path $STAGE 'app/bin/frpc.version'),
        "$CORE_VERSION`n"
    )
    Set-Executable $frpcPath
}

$manifestPath = Join-Path $STAGE 'manifest'
$lines = [System.IO.File]::ReadAllLines($manifestPath)
$out = foreach ($line in $lines) {
    if ($line -like 'version=*') { "version=$VERSION" }
    elseif ($line -like 'platform=*') { "platform=$PLATFORM" }
    else { $line }
}
[System.IO.File]::WriteAllText($manifestPath, (($out -join "`n") + "`n"))

Copy-Item -Force -Path (Join-Path $ROOT 'LICENSE') -Destination (Join-Path $STAGE 'app/')
Copy-Item -Force -Path (Join-Path $ROOT 'NOTICE') -Destination (Join-Path $STAGE 'app/')

$cmdDir = Join-Path $STAGE 'cmd'
if (Test-Path $cmdDir) {
    Get-ChildItem -Path $cmdDir -Force | ForEach-Object { Set-Executable $_.FullName }
}
Set-Executable (Join-Path $STAGE 'app/bin/loliafrp-manager')

& python3 (Join-Path $ROOT 'scripts/validate-package.py') $STAGE
if ($LASTEXITCODE -ne 0) { throw 'validate-package.py failed' }

if (-not $STAGE_ONLY) {
    if (-not (Test-Path $FNPACK)) {
        Fail "fnpack missing: $FNPACK`nDownload it from https://developer.fnnas.com/docs/cli/fnpack/"
    }
    Push-Location $buildDir
    try {
        & $FNPACK build --directory $STAGE
        if ($LASTEXITCODE -ne 0) { throw 'fnpack build failed' }
    } finally {
        Pop-Location
    }
    $fpkSrc = Join-Path $buildDir 'loliafrp.fpk'
    $fpkDst = Join-Path $buildDir "loliafrp-$VERSION-$ARCH.fpk"
    Move-Item -Force -Path $fpkSrc -Destination $fpkDst
    Write-Host "Package: $fpkDst"
}

Write-Host "Staged app: $STAGE"