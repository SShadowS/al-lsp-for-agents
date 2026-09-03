<#
.SYNOPSIS
Pre-release checks for the artifacts this repo ships directly to users.

.DESCRIPTION
The Claude Code marketplace serves the committed binaries straight from this
repo, so whatever is committed IS the release. Two ways that has gone wrong,
both of which this script now catches:

  1. An unsigned binary got committed. During v1.14.0 `build.sh` overwrote the
     CI-built, Authenticode-signed al-call-hierarchy.exe with a local unsigned
     build; it was caught only by an ad-hoc manual check.
  2. The four version files drifted out of sync, so the plugin and the VS Code
     extension advertised different versions.

Run before tagging:
    ./scripts/preflight-release.ps1

Exit code 0 means safe to tag; non-zero lists what to fix.
#>
param([switch]$Quiet)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$problems = @()

function Note($msg) { if (-not $Quiet) { Write-Host $msg } }

# ---- 1. Every SHIPPED Windows binary must be Authenticode-signed ------------
# Only the plugin dirs ship: the Claude Code marketplace serves those straight
# from the repo. al-language-server-go/bin is the dev/test harness location,
# which test_lsp_go.py now rebuilds on every run (so it is routinely unsigned,
# and that is fine) - it is reported but never fails the check.
Note "Checking Authenticode signatures (shipped binaries)..."
$shippedDir = Join-Path $repoRoot 'al-language-server-go-windows\bin'
$devDir     = Join-Path $repoRoot 'al-language-server-go\bin'

$shipped = @(Get-ChildItem -Path $shippedDir -Filter *.exe -ErrorAction SilentlyContinue)
if ($shipped.Count -eq 0) { $problems += "No Windows binaries in $shippedDir - is the repo built?" }
foreach ($exe in $shipped) {
    $sig = Get-AuthenticodeSignature -FilePath $exe.FullName
    if ($sig.Status -eq 'Valid') {
        Note ("  OK       {0}" -f $exe.Name)
    } else {
        Note ("  UNSIGNED {0} [{1}]" -f $exe.Name, $sig.Status)
        $problems += "Unsigned (SHIPPED): $($exe.FullName) [$($sig.Status)] - run ./scripts/sign-binaries.ps1"
    }
}
foreach ($exe in @(Get-ChildItem -Path $devDir -Filter *.exe -ErrorAction SilentlyContinue)) {
    $sig = Get-AuthenticodeSignature -FilePath $exe.FullName
    $state = if ($sig.Status -eq 'Valid') { 'OK' } else { 'unsigned' }
    Note ("  {0,-8} {1} (dev/test, not shipped)" -f $state, $exe.Name)
}

# ---- 2. The four version files must agree ----------------------------------
Note "Checking version consistency..."
$versionFiles = @{
    'vscode-extension\package.json'             = 'version'
    'al-language-server-go-windows\plugin.json' = 'version'
    'al-language-server-go-linux\plugin.json'   = 'version'
}
$found = @{}
foreach ($rel in $versionFiles.Keys) {
    $path = Join-Path $repoRoot $rel
    if (-not (Test-Path $path)) { $problems += "Missing $rel"; continue }
    $found[$rel] = (Get-Content $path -Raw | ConvertFrom-Json).version
}
# marketplace.json carries one entry per plugin
$mkt = Get-Content (Join-Path $repoRoot '.claude-plugin\marketplace.json') -Raw | ConvertFrom-Json
foreach ($p in $mkt.plugins) { $found["marketplace.json ($($p.name))"] = $p.version }

$distinct = @($found.Values | Sort-Object -Unique)
foreach ($k in $found.Keys | Sort-Object) { Note ("  {0,-45} {1}" -f $k, $found[$k]) }
if ($distinct.Count -ne 1) {
    $problems += "Version mismatch across files: $($distinct -join ', ')"
} else {
    Note "  All agree on $($distinct[0])"
}

# ---- report ----------------------------------------------------------------
Write-Host ""
if ($problems.Count -eq 0) {
    Write-Host "preflight: OK - safe to tag" -ForegroundColor Green
    exit 0
}
Write-Host "preflight: $($problems.Count) problem(s)" -ForegroundColor Red
$problems | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
exit 1
