<#
.SYNOPSIS
  aiss installer for Windows (PowerShell).

.EXAMPLE
  irm https://raw.githubusercontent.com/jarvan1/aiss/main/install.ps1 | iex

.NOTES
  Env overrides:
    AISS_VERSION      version to install (e.g. v0.1.4); default: latest release
    AISS_INSTALL_DIR  install directory; default: %LOCALAPPDATA%\Programs\aiss
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$repo = 'jarvan1/aiss'
$bin  = 'aiss.exe'

function Info($m) { Write-Host "aiss-install: $m" }

# --- detect arch, matching goreleaser's asset names -------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  'x86'   { throw 'aiss-install: 32-bit Windows is not supported' }
  default { 'amd64' }
}

# --- resolve version --------------------------------------------------------
$ver = $env:AISS_VERSION
if (-not $ver) {
  Info 'resolving latest release...'
  $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest" `
    -Headers @{ 'User-Agent' = 'aiss-install' }
  $ver = $rel.tag_name
}
if (-not $ver) { throw 'aiss-install: could not determine version (set AISS_VERSION)' }
$num = $ver.TrimStart('v')  # asset names carry the version without the leading v

$asset = "aiss_${num}_windows_${arch}.zip"
$url   = "https://github.com/$repo/releases/download/$ver/$asset"

# --- download + extract -----------------------------------------------------
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("aiss-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
  $zip = Join-Path $tmp $asset
  Info "downloading $asset ($ver)..."
  Invoke-WebRequest -Uri $url -OutFile $zip -UseBasicParsing

  # Best-effort checksum verification.
  try {
    $sums = Invoke-WebRequest -Uri "https://github.com/$repo/releases/download/$ver/checksums.txt" `
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
