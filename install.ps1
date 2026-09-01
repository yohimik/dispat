<#
.SYNOPSIS
Install the dispat CLI from its GitHub release.

.DESCRIPTION
The Windows half of install.sh, kept deliberately parallel to it: same options,
same tag filter, same digest check, same output contract — the resolved version
on stdout, everything written for a human on the information stream.

    irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 | iex

To pass options through that pipeline, download first:

    irm https://raw.githubusercontent.com/yohimik/dispat/main/install.ps1 -OutFile install.ps1
    .\install.ps1 -Version 1.2.3

.PARAMETER Version
Version or tag to install: 1.2.3, v1.2.3 or services/dispat/v1.2.3. Defaults to
the latest stable release.

.PARAMETER BinDir
Where to install. Defaults to $env:LOCALAPPDATA\dispat\bin.

.PARAMETER Arch
amd64 or arm64. Defaults to this machine's.

.PARAMETER Token
Token for the releases API. Only raises the rate limit; the releases themselves
are public. Defaults to $env:GITHUB_TOKEN.
#>
[CmdletBinding()]
param(
    [string]$Version = $env:DISPAT_VERSION,
    [string]$BinDir = $env:DISPAT_BIN_DIR,
    [string]$Arch = '',
    [string]$Token = $env:GITHUB_TOKEN
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Owner = 'yohimik'
$Repo = 'dispat'
# This repository publishes a GitHub release per module, so /releases/latest is
# as likely to be a library as the CLI and the listing has to be filtered by tag
# instead. Mirrors selfupdate.DefaultTagPrefix.
$TagPrefix = 'services/dispat/v'
$ApiUrl = if ($env:DISPAT_API_URL) { $env:DISPAT_API_URL } else { 'https://api.github.com' }
$DownloadUrl = if ($env:DISPAT_DOWNLOAD_URL) { $env:DISPAT_DOWNLOAD_URL } else { 'https://github.com' }

$headers = @{ Accept = 'application/vnd.github+json' }
if ($Token) { $headers['Authorization'] = "Bearer $Token" }

if (-not $Arch) {
    $Arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
        'X64' { 'amd64' }
        'Arm64' { 'arm64' }
        default { throw "unsupported architecture: $_" }
    }
}

# Mirrors selfupdate.AssetName, the other half of this contract.
$asset = "dispat-windows-$Arch.exe"

# Both spellings are accepted because the releases page shows the tag and the
# changelog shows the number, and a reader should be able to paste either.
if ($Version) {
    $Version = $Version -replace "^$([regex]::Escape($TagPrefix))", '' -replace '^v', ''
}

if (-not $Version) {
    Write-Information 'resolving the latest stable release...' -InformationAction Continue
    # Three pages mirrors the walk internal/selfupdate makes: one release run
    # cuts a release per package, so the newest stable of this one can sit past
    # the first page. An empty page is the end of the listing.
    #
    # The ForEach-Object is load-bearing: PowerShell 7.6 changed
    # Invoke-RestMethod to hand a JSON array over as one Object[] instead of
    # enumerating it, and a page kept whole turns the tag filter below into
    # member enumeration over every tag on the page — where a tag from another
    # package, shorter than this prefix, makes Substring throw. Re-emitting
    # through a script block enumerates on every PowerShell version.
    $releases = @()
    for ($page = 1; $page -le 3; $page++) {
        $batch = @(Invoke-RestMethod -Uri "$ApiUrl/repos/$Owner/$Repo/releases?per_page=100&page=$page" -Headers $headers | ForEach-Object { $_ })
        if ($batch.Count -eq 0) { break }
        $releases += $batch
    }
    # Highest, not most recent: a patch cut on an older line comes back first and
    # would otherwise look like an upgrade to everyone on the newer one. A stable
    # version has no hyphen, which is the whole of the prerelease filter.
    $Version = $releases |
        Where-Object { $_.tag_name.StartsWith($TagPrefix) } |
        ForEach-Object { $_.tag_name.Substring($TagPrefix.Length) } |
        Where-Object { $_ -notmatch '-' } |
        Sort-Object { [version]$_ } |
        Select-Object -Last 1
    if (-not $Version) { throw "no stable release found under $TagPrefix*" }
}

if ($Version -notmatch '^\d+\.\d+\.\d+') {
    throw "not a version: $Version (expected 1.2.3, v1.2.3 or ${TagPrefix}1.2.3)"
}
$tag = "$TagPrefix$Version"

# Fetching the release by tag also turns "no such version" into a clean failure
# here rather than a 404 on the download.
try {
    $release = Invoke-RestMethod -Uri "$ApiUrl/repos/$Owner/$Repo/releases/tags/$tag" -Headers $headers
} catch {
    throw "no release for $tag. Check the version, or the releases page."
}
# Guarded rather than dotted into directly: under Set-StrictMode a response that
# is not the release object — a proxy's error document, an Enterprise instance
# answering differently — would otherwise fail with "the property 'assets'
# cannot be found", which says nothing about what actually went wrong.
if (-not ($release.PSObject.Properties.Name -contains 'assets')) {
    throw "the API did not answer with a release for $tag."
}
$assetInfo = $release.assets | Where-Object { $_.name -eq $asset } | Select-Object -First 1
if (-not $assetInfo) {
    throw "$asset is not attached to $tag. It carries: $(($release.assets.name) -join ', ')"
}

if (-not $BinDir) { $BinDir = Join-Path $env:LOCALAPPDATA 'dispat\bin' }
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

$target = Join-Path $BinDir 'dispat.exe'
# Staged beside the target so the final move is a rename on the same volume: a
# half-downloaded binary never appears on PATH.
$tmp = "$target.download"

Write-Information "downloading $asset $Version..." -InformationAction Continue
try {
    # No headers on the download itself, like install.sh: the asset is public,
    # and the redirect to the storage host rejects the API's Authorization
    # header on Windows PowerShell 5.1, which forwards it across redirects.
    Invoke-WebRequest -Uri "$DownloadUrl/$Owner/$Repo/releases/download/$tag/$asset" -OutFile $tmp

    # GitHub reports a "digest" per asset, which is what internal/selfupdate
    # checks too. Older GitHub Enterprise versions do not send one.
    $digest = if ($assetInfo.PSObject.Properties.Name -contains 'digest') { $assetInfo.digest } else { '' }
    if ($digest -match '^sha256:(.+)$') {
        $want = $Matches[1]
        $got = (Get-FileHash -Algorithm SHA256 -Path $tmp).Hash.ToLowerInvariant()
        if ($got -ne $want) { throw "checksum mismatch: expected $want, got $got" }
        Write-Information 'checksum verified' -InformationAction Continue
    } else {
        Write-Information "warning: the release reports no digest for $asset; skipping verification" -InformationAction Continue
    }

    Move-Item -Force -Path $tmp -Destination $target
} finally {
    if (Test-Path $tmp) { Remove-Item -Force $tmp }
}
Write-Information "installed $target" -InformationAction Continue

# Restored after the smoke test, because under `irm | iex` this process is the
# user's own shell session, not a script of its own.
$updateCheck = $env:DISPAT_UPDATE_CHECK
$env:DISPAT_UPDATE_CHECK = '0'
try {
    & $target --version | Write-Information -InformationAction Continue
} finally {
    $env:DISPAT_UPDATE_CHECK = $updateCheck
}

# The PATH story, both halves of it: a directory missing from PATH gets the
# one-off assignment and the line that makes it permanent; a directory already
# on PATH can still lose to an older dispat installed somewhere earlier, which
# looks exactly like the new version failing to install.
if (($env:PATH -split ';') -notcontains $BinDir) {
    Write-Information "note: $BinDir is not on PATH." -InformationAction Continue
    Write-Information "  this session only:  `$env:PATH = `"$BinDir;`$env:PATH`"" -InformationAction Continue
    Write-Information "  permanently:        [Environment]::SetEnvironmentVariable('Path', `"$BinDir;`" + [Environment]::GetEnvironmentVariable('Path', 'User'), 'User')" -InformationAction Continue
    Write-Information "  then open a new terminal." -InformationAction Continue
} else {
    $found = (Get-Command dispat -ErrorAction SilentlyContinue).Source
    if ($found -and $found -ne $target) {
        Write-Information "warning: $found comes earlier on PATH and shadows $target." -InformationAction Continue
        Write-Information "  ``dispat --version`` will keep answering with the old binary; remove it or reorder PATH." -InformationAction Continue
    }
}

# The output contract: the version alone on stdout.
Write-Output $Version
