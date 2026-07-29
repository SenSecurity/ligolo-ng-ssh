[CmdletBinding()]
param(
    [string]$GoExe,
    [string]$GarbleExe
)

$ErrorActionPreference = 'Stop'

function New-GarbleSeed {
    $seedBytes = [byte[]]::new(8)
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($seedBytes)
    } finally {
        $random.Dispose()
    }
    return [Convert]::ToBase64String($seedBytes).TrimEnd('=')
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
if (-not $GoExe) {
    $GoExe = Join-Path $repoRoot '.tools\go1.26.5\go\bin\go.exe'
}
if (-not $GarbleExe) {
    $GarbleExe = Join-Path $repoRoot '.tools\bin\garble.exe'
}
$GoExe = (Resolve-Path -LiteralPath $GoExe).Path
$GarbleExe = (Resolve-Path -LiteralPath $GarbleExe).Path

$releaseRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot 'release'))
$artifactsRoot = [IO.Path]::GetFullPath((Join-Path $repoRoot 'artifacts'))
$webSource = Join-Path $repoRoot 'web\ligolo-ng-web'
$webIndex = Join-Path $repoRoot 'web\dist\index.html'
$releaseReadmeSource = Join-Path $repoRoot 'doc\release-readme.md'
$expectedReleaseRoot = $repoRoot.TrimEnd('\') + '\release'
if (-not $releaseRoot.Equals($expectedReleaseRoot, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing unexpected release path: $releaseRoot"
}
if (-not (Test-Path -LiteralPath $releaseReadmeSource)) {
    throw "Release README source is missing: $releaseReadmeSource"
}
$winDivert = & (Join-Path $PSScriptRoot 'fetch-windivert.ps1')
$winDivertLicense = Join-Path $repoRoot 'third_party\windivert\LICENSE.txt'
if (-not (Test-Path -LiteralPath $winDivertLicense)) {
    throw "WinDivert license is missing: $winDivertLicense"
}

if (-not (Test-Path -LiteralPath (Join-Path $webSource 'package-lock.json'))) {
    throw 'Web UI submodule is missing. Run: git submodule update --init --recursive web/ligolo-ng-web'
}
$npmCommand = Get-Command npm.cmd -ErrorAction SilentlyContinue
if (-not $npmCommand) {
    $npmCommand = Get-Command npm -ErrorAction SilentlyContinue
}
if (-not $npmCommand) {
    throw 'npm is required to build the embedded Web UI'
}
& $npmCommand.Source ci --prefix $webSource
if ($LASTEXITCODE -ne 0) {
    throw 'Web UI dependency installation failed'
}
& $npmCommand.Source run build-ligolo --prefix $webSource -- --emptyOutDir
if ($LASTEXITCODE -ne 0) {
    throw 'Web UI build failed'
}
if (-not (Test-Path -LiteralPath $webIndex)) {
    throw 'Web UI build did not produce web\dist\index.html'
}
if ((Get-Content -Raw -LiteralPath $webIndex) -match 'WebUI is not built') {
    throw 'Refusing to embed the Web UI placeholder'
}
$webIndexContent = [IO.File]::ReadAllText($webIndex).Replace("`r`n", "`n")
$utf8NoBom = New-Object Text.UTF8Encoding($false)
[IO.File]::WriteAllText($webIndex, $webIndexContent, $utf8NoBom)

if (Test-Path -LiteralPath $releaseRoot) {
    Get-ChildItem -Force -LiteralPath $releaseRoot | Remove-Item -Recurse -Force
} else {
    New-Item -ItemType Directory -Path $releaseRoot | Out-Null
}
New-Item -ItemType Directory -Force -Path $artifactsRoot | Out-Null

$goVersion = (& $GoExe version).Trim()
$garbleVersion = (& $GarbleExe version | Select-Object -First 1).Trim()
if ($LASTEXITCODE -ne 0) {
    throw 'Unable to run Garble'
}

Push-Location $repoRoot
try {
    $commit = (git rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to resolve Git commit'
    }
    $webCommit = (git -C $webSource rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to resolve Web UI commit'
    }
    $webStatus = git -C $webSource status --porcelain
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to inspect Web UI working tree'
    }
    $webWorkingTreeDirty = [bool]$webStatus
    $webLockSha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $webSource 'package-lock.json')).Hash.ToLowerInvariant()
    $workingTreeStatus = git status --porcelain --untracked-files=all
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to inspect working tree'
    }
    $workingTreeDirty = [bool]$workingTreeStatus
    $buildDate = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
    $version = 'ssh-' + $commit.Substring(0, 12)
    if ($workingTreeDirty) {
        $version += '-dirty'
    }
    $ldflags = "-s -w -X main.version=$version -X main.commit=$commit -X main.date=$buildDate"

    $env:GOTOOLCHAIN = 'local'
    $env:CGO_ENABLED = '0'
    $env:GOCACHE = Join-Path $repoRoot '.tools\gocache'
    $env:GOMODCACHE = Join-Path $repoRoot '.tools\gomodcache'
    $env:GARBLE_CACHE = Join-Path $repoRoot '.tools\garblecache'
    $env:PATH = "$(Split-Path -Parent $GoExe);$(Split-Path -Parent $GarbleExe);$env:PATH"

    $matrix = @(
        [pscustomobject]@{ Name = 'windows-amd64'; GOOS = 'windows'; GOARCH = 'amd64'; Extension = '.exe' },
        [pscustomobject]@{ Name = 'windows-arm64'; GOOS = 'windows'; GOARCH = 'arm64'; Extension = '.exe' },
        [pscustomobject]@{ Name = 'linux-amd64'; GOOS = 'linux'; GOARCH = 'amd64'; Extension = '' },
        [pscustomobject]@{ Name = 'linux-arm64'; GOOS = 'linux'; GOARCH = 'arm64'; Extension = '' }
    )

    $manifestTargets = @()
    foreach ($target in $matrix) {
        $env:GOOS = $target.GOOS
        $env:GOARCH = $target.GOARCH
        $targetRoot = Join-Path $releaseRoot $target.Name
        New-Item -ItemType Directory -Force -Path $targetRoot | Out-Null

        $proxyOutput = Join-Path $targetRoot ('proxy' + $target.Extension)
        $agentOutput = Join-Path $targetRoot ('agent' + $target.Extension)
        $garbleOutput = Join-Path $targetRoot ('agent-garble' + $target.Extension)

        & $GoExe build -trimpath -buildvcs=false -ldflags $ldflags -o $proxyOutput .\cmd\proxy
        if ($LASTEXITCODE -ne 0) {
            throw "Proxy build failed for $($target.Name)"
        }
        & $GoExe build -trimpath -buildvcs=false -ldflags $ldflags -o $agentOutput .\cmd\agent
        if ($LASTEXITCODE -ne 0) {
            throw "Agent build failed for $($target.Name)"
        }

        $garbleSeed = New-GarbleSeed
        & $GarbleExe "-seed=$garbleSeed" -literals -tiny build -trimpath -buildvcs=false -ldflags $ldflags -o $garbleOutput .\cmd\agent
        if ($LASTEXITCODE -ne 0) {
            throw "Garble agent build failed for $($target.Name)"
        }

        $manifestTargets += [pscustomobject]@{
            target = $target.Name
            goos = $target.GOOS
            goarch = $target.GOARCH
            garble_seed = $garbleSeed
            shadowport = $false
        }

        if ($target.Name -eq 'windows-amd64') {
            $shadowOutput = Join-Path $targetRoot 'agent-shadow.exe'
            $shadowGarbleOutput = Join-Path $targetRoot 'agent-shadow-garble.exe'

            & $GoExe build -tags shadowport -trimpath -buildvcs=false -ldflags $ldflags -o $shadowOutput .\cmd\agent
            if ($LASTEXITCODE -ne 0) {
                throw 'ShadowPort agent build failed for windows-amd64'
            }

            $shadowGarbleSeed = New-GarbleSeed
            & $GarbleExe "-seed=$shadowGarbleSeed" -literals -tiny build -tags shadowport -trimpath -buildvcs=false -ldflags $ldflags -o $shadowGarbleOutput .\cmd\agent
            if ($LASTEXITCODE -ne 0) {
                throw 'Garble ShadowPort agent build failed for windows-amd64'
            }

            $manifestTargets[-1].shadowport = $true
            $manifestTargets[-1] | Add-Member -NotePropertyName shadowport_garble_seed -NotePropertyValue $shadowGarbleSeed
        }
    }

    Copy-Item -LiteralPath $releaseReadmeSource -Destination (Join-Path $releaseRoot 'README.md')
    $licenseRoot = Join-Path $releaseRoot 'licenses'
    New-Item -ItemType Directory -Force -Path $licenseRoot | Out-Null
    Copy-Item -LiteralPath $winDivertLicense -Destination (Join-Path $licenseRoot 'WinDivert-LICENSE.txt')

    $hashLines = Get-ChildItem -File -Recurse -LiteralPath $releaseRoot |
        Sort-Object FullName |
        ForEach-Object {
            $relative = $_.FullName.Substring($releaseRoot.Length).TrimStart('\').Replace('\', '/')
            $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName).Hash.ToLowerInvariant()
            "$hash  $relative"
        }
    [IO.File]::WriteAllLines((Join-Path $artifactsRoot 'SHA256SUMS'), [string[]]$hashLines)

    $manifest = [ordered]@{
        upstream = 'https://github.com/nicocha30/ligolo-ng'
        commit = $commit
        web_ui_commit = $webCommit
        web_ui_working_tree_dirty = $webWorkingTreeDirty
        web_ui_package_lock_sha256 = $webLockSha256
        working_tree_dirty = $workingTreeDirty
        version = $version
        build_date_utc = $buildDate
        go = $goVersion
        garble = $garbleVersion
        garble_flags = @('-literals', '-tiny')
        shadowport = [ordered]@{
            platform = 'windows-amd64'
            build_tag = 'shadowport'
            runtime = 'WinDivert'
            version = $winDivert.Version
            archive_sha256 = $winDivert.ArchiveSha256
            dll_sha256 = $winDivert.DllSha256
            driver_sha256 = $winDivert.DriverSha256
        }
        targets = $manifestTargets
    }
    $manifest | ConvertTo-Json -Depth 6 | Set-Content -Encoding utf8 -LiteralPath (Join-Path $artifactsRoot 'build-manifest.json')
} finally {
    Pop-Location
}

Write-Output "Release: $releaseRoot"
Write-Output "Hashes: $(Join-Path $artifactsRoot 'SHA256SUMS')"
Write-Output "Manifest: $(Join-Path $artifactsRoot 'build-manifest.json')"
