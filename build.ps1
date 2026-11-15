# Builds the Linux binary and fnOS .fpk packages on Windows.
#
# Usage:
#   .\build.ps1                      # x86 + arm packages
#   .\build.ps1 -Arch amd64          # x86 only
#   .\build.ps1 -Fnpack C:\tools\fnpack.exe
param(
    [string]$Fnpack = "",
    [string]$Version = "",
    [string[]]$Arch = @("amd64", "arm64"),
    [switch]$SkipFpk
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

if (-not $Version) {
    $versionFile = Join-Path $root "internal/version/version.go"
    $m = [regex]::Match([IO.File]::ReadAllText($versionFile), 'Version\s*=\s*"([^"]+)"')
    if (-not $m.Success) { throw "无法从 internal/version/version.go 读取版本号" }
    $Version = $m.Groups[1].Value
}

if (-not $SkipFpk -and -not $Fnpack) {
    $candidate = Get-Command fnpack -ErrorAction SilentlyContinue
    if ($candidate) {
        $Fnpack = $candidate.Source
    } elseif (Test-Path (Join-Path $root "tools/fnpack.exe")) {
        $Fnpack = Join-Path $root "tools/fnpack.exe"
    } else {
        throw "未找到 fnpack。请从 https://developer.fnnas.com/docs/cli/fnpack/ 下载，并用 -Fnpack 指定路径。"
    }
}

$dist = Join-Path $root "dist"
New-Item -ItemType Directory -Force -Path $dist | Out-Null

$utf8 = New-Object System.Text.UTF8Encoding($false)
# Strict decoder: a non-UTF-8 manifest must fail loudly instead of being
# silently rewritten with U+FFFD replacement characters, which would ship
# mojibake in display_name / desc / changelog.
$utf8Strict = New-Object System.Text.UTF8Encoding($false, $true)

# The package name and the injected binary version both come from $Version, but
# pack/manifest stores its own copy. Refuse to build when they drift, or the
# .fpk is named 1.0.1 while the app center still reports 1.0.0.
$manifestVersion = [regex]::Match(
    [IO.File]::ReadAllText((Join-Path $root "pack/manifest"), $utf8Strict),
    '(?m)^version\s*=\s*(\S+)\s*$')
if (-not $manifestVersion.Success) {
    throw "pack/manifest 缺少 version 字段"
}
if ($manifestVersion.Groups[1].Value -ne $Version) {
    throw "版本号不一致：internal/version/version.go 为 $Version，pack/manifest 为 $($manifestVersion.Groups[1].Value)。请先统一后再构建。"
}
$oldGoos = $env:GOOS
$oldGoarch = $env:GOARCH
$oldCgo = $env:CGO_ENABLED

# Prebuild the permission fixer for the host platform. Running it after the
# cross-compiled builds keeps GOOS/GOARCH from affecting this binary.
$fixer = ""
if (-not $SkipFpk) {
    $fixer = Join-Path $dist "fixfpk.exe"
    Write-Host "==> 构建 fixfpk"
    & go build -o $fixer ./tools/fixfpk
    if ($LASTEXITCODE -ne 0) { throw "构建 fixfpk 失败" }
}

try {
    foreach ($goarch in $Arch) {
        $archName = if ($goarch -eq "amd64") { "x86" } elseif ($goarch -eq "arm64") { "arm" } else { throw "不支持的架构：$goarch" }
        $stage = Join-Path $dist "stage-$archName"
        if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
        Copy-Item -Recurse -Path (Join-Path $root "pack") -Destination $stage

        Write-Host "==> 编译 linux/$goarch"
        $env:GOOS = "linux"
        $env:GOARCH = $goarch
        $env:CGO_ENABLED = "0"
        $binary = Join-Path $stage "app/fnososs"
        & go build -trimpath -ldflags "-s -w -X fnososs/internal/version.Version=$Version" -o $binary ./cmd/ossd
        if ($LASTEXITCODE -ne 0) { throw "go build 失败：linux/$goarch" }

        $manifestPath = Join-Path $stage "manifest"
        try {
            $manifest = [IO.File]::ReadAllText($manifestPath, $utf8Strict)
        } catch {
            throw "pack/manifest 不是 UTF-8 编码，请先转换为 UTF-8（无 BOM）：$manifestPath"
        }
        if (-not [regex]::IsMatch($manifest, '(?m)^platform\s*=')) {
            throw "pack/manifest 缺少 platform 字段：$manifestPath"
        }
        $manifest = [regex]::Replace($manifest, '(?m)^platform\s*=.*$', "platform=$archName")
        [IO.File]::WriteAllText($manifestPath, $manifest, $utf8)

        if ($SkipFpk) { continue }

        Write-Host "==> 打包 $archName"
        Push-Location $dist
        try {
            & $Fnpack build --directory $stage
            if ($LASTEXITCODE -ne 0) { throw "fnpack build 失败：$archName" }
        } finally {
            Pop-Location
        }
        $out = Join-Path $dist "fnos-oss.fpk"
        if (-not (Test-Path $out)) { throw "未找到 fnpack 输出：$out" }
        $final = Join-Path $dist "fnos-oss_${Version}_${archName}.fpk"
        Move-Item -Force $out $final

        # Windows has no POSIX mode bits, so fnpack packages everything as 0666.
        # Restore executable bits for cmd/* and the server binary.
        Write-Host "==> 修正包内文件权限"
        & $fixer $final
        if ($LASTEXITCODE -ne 0) { throw "修正 fpk 权限失败：$archName" }

        Write-Host "==> $final"
    }
} finally {
    $env:GOOS = $oldGoos
    $env:GOARCH = $oldGoarch
    $env:CGO_ENABLED = $oldCgo
    if ($fixer) { Remove-Item -Force $fixer -ErrorAction SilentlyContinue }
}

Write-Host "完成。安装包位于 $dist"
