[CmdletBinding()]
param(
    [string]$CacheRoot
)

$ErrorActionPreference = 'Stop'

$version = '2.2.2'
$archiveSha256 = '63cb41763bb4b20f600b6de04e991a9c2be73279e317d4d82f237b150c5f3f15'
$dllSha256 = 'c1e060ee19444a259b2162f8af0f3fe8c4428a1c6f694dce20de194ac8d7d9a2'
$driverSha256 = '8da085332782708D8767BCACE5327A6EC7283C17CFB85E40B03CD2323A90DDC2'.ToLowerInvariant()
$downloadUrl = "https://github.com/basil00/WinDivert/releases/download/v$version/WinDivert-$version-A.zip"

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
if (-not $CacheRoot) {
    $CacheRoot = Join-Path $repoRoot ".tools\windivert\$version"
}
$cachePath = [IO.Path]::GetFullPath($CacheRoot)
$expectedCacheBase = [IO.Path]::GetFullPath((Join-Path $repoRoot '.tools\windivert'))
if (-not $cachePath.StartsWith($expectedCacheBase + '\', [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing WinDivert cache outside $expectedCacheBase"
}

$archivePath = Join-Path $cachePath "WinDivert-$version-A.zip"
$extractPath = Join-Path $cachePath 'expanded'
$sourceRoot = Join-Path $extractPath "WinDivert-$version-A"
$assetRoot = Join-Path $repoRoot 'pkg\agent\shadowport\assets\bin'
$licenseRoot = Join-Path $repoRoot 'third_party\windivert'

New-Item -ItemType Directory -Force -Path $cachePath, $assetRoot, $licenseRoot | Out-Null

if (-not (Test-Path -LiteralPath $archivePath)) {
    Invoke-WebRequest -UseBasicParsing -Uri $downloadUrl -OutFile $archivePath
}

$actualArchiveHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
if ($actualArchiveHash -ne $archiveSha256) {
    throw "WinDivert archive SHA256 mismatch: $actualArchiveHash"
}

if (-not (Test-Path -LiteralPath $sourceRoot)) {
    Expand-Archive -LiteralPath $archivePath -DestinationPath $extractPath -Force
}

$dllPath = Join-Path $sourceRoot 'x64\WinDivert.dll'
$driverPath = Join-Path $sourceRoot 'x64\WinDivert64.sys'
$licensePath = Join-Path $sourceRoot 'LICENSE'

foreach ($item in @(
    @{ Path = $dllPath; Hash = $dllSha256 },
    @{ Path = $driverPath; Hash = $driverSha256 }
)) {
    if (-not (Test-Path -LiteralPath $item.Path)) {
        throw "WinDivert file is missing: $($item.Path)"
    }
    $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $item.Path).Hash.ToLowerInvariant()
    if ($actualHash -ne $item.Hash) {
        throw "WinDivert file SHA256 mismatch for $($item.Path): $actualHash"
    }
}

$driverSignature = Get-AuthenticodeSignature -LiteralPath $driverPath
if ($driverSignature.Status -ne 'Valid') {
    throw "WinDivert driver Authenticode validation failed: $($driverSignature.Status)"
}

Copy-Item -Force -LiteralPath $dllPath -Destination (Join-Path $assetRoot 'WinDivert.dll')
Copy-Item -Force -LiteralPath $driverPath -Destination (Join-Path $assetRoot 'WinDivert64.sys')
$licenseDestination = Join-Path $licenseRoot 'LICENSE.txt'
$licenseText = [IO.File]::ReadAllText($licensePath).TrimEnd("`r", "`n") + "`n"
[IO.File]::WriteAllText($licenseDestination, $licenseText, (New-Object Text.UTF8Encoding($false)))

[pscustomobject]@{
    Version = $version
    DownloadUrl = $downloadUrl
    ArchiveSha256 = $archiveSha256
    DllSha256 = $dllSha256
    DriverSha256 = $driverSha256
    AssetRoot = $assetRoot
}
