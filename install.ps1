<#
.SYNOPSIS
  aiss installer for Windows (PowerShell).

.EXAMPLE
  irm https://raw.githubusercontent.com/jarvan1/aiss/main/install.ps1 | iex

.EXAMPLE
  # Behind the Great Firewall — route downloads through a GitHub mirror:
  & ([scriptblock]::Create((irm https://raw.githubusercontent.com/jarvan1/aiss/main/install.ps1))) -Proxy https://gh-proxy.org/

.NOTES
  Params / env overrides:
    -Proxy <url> | AISS_PROXY   prefix GitHub download URLs with this mirror
                                (e.g. https://gh-proxy.org/); default: none
    AISS_VERSION                version to install (e.g. v0.2.0); default: latest
    AISS_INSTALL_DIR            install directory; default: %LOCALAPPDATA%\Programs\aiss
#>
[CmdletBinding()]
param(
  [string]$Proxy = $env:AISS_PROXY
)

$ErrorActionPreference = 'Stop'
$repo = 'jarvan1/aiss'
$bin  = 'aiss.exe'

function Info($m) { Write-Host "aiss-install: $m" }

# Mirror wraps a GitHub download URL with the proxy (github.com / raw / release
# assets). The GitHub API is intentionally NOT proxied — mirrors typically 403
# api.github.com, so version resolution always goes direct.
if ($Proxy) { $Proxy = $Proxy.TrimEnd('/') + '/' }
function Mirror($url) { if ($Proxy) { "$Proxy$url" } else { $url } }

# --- detect arch, matching goreleaser's asset names -------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  'x86'   { throw 'aiss-install: 32-bit Windows is not supported' }
  default { 'amd64' }
}

# --- resolve version --------------------------------------------------------
# Version resolution hits the GitHub API directly (not the proxy: mirrors 403
# api.github.com). If that fails behind a firewall, tell the user to pin one.
$ver = $env:AISS_VERSION
if (-not $ver) {
  Info 'resolving latest release...'
  try {
    $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest" `
      -Headers @{ 'User-Agent' = 'aiss-install' }
    $ver = $rel.tag_name
  } catch {
    throw "aiss-install: could not reach the GitHub API to find the latest version. Pin one explicitly, e.g. `$env:AISS_VERSION='v0.2.0' (see the releases page)"
  }
}
if (-not $ver) { throw 'aiss-install: could not determine version (set AISS_VERSION)' }
$num = $ver.TrimStart('v')  # asset names carry the version without the leading v

$asset = "aiss_${num}_windows_${arch}.zip"
$url   = Mirror "https://github.com/$repo/releases/download/$ver/$asset"

# --- download + extract -----------------------------------------------------
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("aiss-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
  $zip = Join-Path $tmp $asset
  Info "downloading $asset ($ver)..."
  Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing

  # Best-effort checksum verification.
  try {
    $sums = Invoke-WebRequest -Uri (Mirror "https://github.com/$repo/releases/download/$ver/checksums.txt") `
      -UseBasicParsing | Select-Object -ExpandProperty Content
    $line = ($sums -split "`n") | Where-Object { $_ -match [regex]::Escape($asset) } | Select-Object -First 1
    if ($line) {
      $want = ($line -split '\s+')[0]
      $got  = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLower()
      if ($want -and ($got -ne $want.ToLower())) {
        throw "aiss-install: checksum mismatch for $asset (want $want, got $got)"
      }
      Info 'checksum ok'
    }
  } catch {
    if ($_.Exception.Message -like '*checksum mismatch*') { throw }
    # otherwise: checksums unavailable, continue
  }

  Expand-Archive -Path $zip -DestinationPath $tmp -Force
  $src = Join-Path $tmp $bin
  if (-not (Test-Path $src)) { throw "aiss-install: archive did not contain $bin" }

  # --- install --------------------------------------------------------------
  $dir = $env:AISS_INSTALL_DIR
  if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\aiss' }
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  Copy-Item -Path $src -Destination (Join-Path $dir $bin) -Force
  Info "installed aiss $ver -> $(Join-Path $dir $bin)"

  # --- add to the user PATH if missing --------------------------------------
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  if (($userPath -split ';') -notcontains $dir) {
    [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
    $env:Path = "$env:Path;$dir"
    Info "added $dir to your user PATH (restart the terminal to pick it up)"
  }
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Host ''
Info 'done. Run `aiss` to start.'
Info 'Optional Ctrl-X Ctrl-W hotkey — add to your $PROFILE:'
Write-Host '    Invoke-Expression (& aiss init powershell | Out-String)'
