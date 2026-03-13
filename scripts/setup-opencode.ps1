#Requires -Version 5.1
<#
.SYNOPSIS
    AL LSP for Agents — OpenCode Setup Script (Windows)
.DESCRIPTION
    Downloads AL LSP binaries and configures OpenCode to use them.
.EXAMPLE
    .\setup-opencode.ps1
    irm https://raw.githubusercontent.com/SShadowS/al-lsp-for-agents/main/scripts/setup-opencode.ps1 | iex
#>

$ErrorActionPreference = 'Stop'

$Repo = "SShadowS/al-lsp-for-agents"
$InstallBase = Join-Path $env:LOCALAPPDATA "al-lsp"
$InstallDir = Join-Path $InstallBase "bin"
$VersionFile = Join-Path $InstallBase ".version"
$GlobalConfig = Join-Path $env:APPDATA "opencode\opencode.json"
$ProjectConfig = Join-Path (Get-Location) "opencode.json"

function Write-Info  { param([string]$Msg) Write-Host "[INFO] $Msg" -ForegroundColor Green }
function Write-Warn  { param([string]$Msg) Write-Host "[WARN] $Msg" -ForegroundColor Yellow }
function Write-Err   { param([string]$Msg) Write-Host "[ERROR] $Msg" -ForegroundColor Red }
function Stop-Setup  { param([string]$Msg) Write-Err $Msg; exit 1 }

function Test-ALExtension {
    $searchDirs = @(
        Join-Path $env:USERPROFILE ".vscode\extensions"
        Join-Path $env:USERPROFILE ".vscode-insiders\extensions"
        Join-Path $env:USERPROFILE ".vscode-server\extensions"
        Join-Path $env:USERPROFILE ".vscode-server-insiders\extensions"
        Join-Path $env:USERPROFILE ".vscode-oss\extensions"
        Join-Path $env:USERPROFILE ".cursor\extensions"
    )

    # Collect ALL matches across all directories, then pick the newest
    $allMatches = @()
    foreach ($dir in $searchDirs) {
        if (Test-Path $dir) {
            $found = Get-ChildItem -Path $dir -Directory -Filter "ms-dynamics-smb.al-*" -ErrorAction SilentlyContinue
            if ($found) {
                $allMatches += $found
            }
        }
    }

    if ($allMatches.Count -eq 0) {
        Stop-Setup @"
Microsoft AL Language extension not found.
Install it in VS Code:  ext install ms-dynamics-smb.al
Or specify a custom path via --al-extension-path in the OpenCode config after setup.
Searched: $($searchDirs -join ', ')
"@
    }

    # Sort by version number (numeric semver comparison, matches paths.go logic)
    $latest = $allMatches | Sort-Object {
        [version]($_.Name -replace '^ms-dynamics-smb\.al-', '')
    } -Descending | Select-Object -First 1
    Write-Info "Found AL extension: $($latest.FullName)"
}

function Install-Binaries {
    Write-Info "Fetching latest release info..."
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
    } catch {
        Stop-Setup "Failed to fetch release info from GitHub. Check your internet connection."
    }

    $tag = $release.tag_name
    if (-not $tag) {
        Stop-Setup "Could not determine latest release tag."
    }

    # Check if already up to date
    if ((Test-Path $VersionFile) -and ((Get-Content $VersionFile -Raw).Trim() -eq $tag)) {
        Write-Info "Already up to date ($tag). Skipping download."
        return
    }

    $assetName = "al-lsp-wrapper-windows-x64.zip"
    $asset = $release.assets | Where-Object { $_.name -eq $assetName }
    if (-not $asset) {
        Stop-Setup "Release $tag does not contain asset '$assetName'."
    }

    $downloadUrl = $asset.browser_download_url
    Write-Info "Downloading $tag ($assetName)..."

    $tmpDir = Join-Path $env:TEMP "al-lsp-setup-$(Get-Random)"
    New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null
    $zipPath = Join-Path $tmpDir $assetName

    try {
        Invoke-WebRequest -Uri $downloadUrl -OutFile $zipPath -UseBasicParsing
    } catch {
        Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
        Stop-Setup "Failed to download $downloadUrl"
    }

    Write-Info "Extracting to $InstallDir..."
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    # Archive contains flat files (no subdirectory): al-lsp-wrapper.exe, al-call-hierarchy.exe
    Expand-Archive -Path $zipPath -DestinationPath $InstallDir -Force

    # Write version file (UTF-8 without BOM)
    [System.IO.File]::WriteAllText($VersionFile, $tag)

    Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
    Write-Info "Installed $tag to $InstallDir"
}

function Get-ConfigPath {
    # If SCOPE env var is set, use it (works for both piped and interactive)
    $scope = $env:SCOPE
    if ($scope -eq "project") {
        return $ProjectConfig
    }
    if ($scope -eq "global") {
        return $GlobalConfig
    }

    # Check if running non-interactively (piped via irm | iex)
    try {
        $isRedirected = [Console]::IsInputRedirected
    } catch {
        $isRedirected = $true
    }

    if ($isRedirected) {
        # Non-interactive: default to global
        return $GlobalConfig
    }

    # Interactive: ask the user
    Write-Host ""
    Write-Host "Where should the OpenCode config be written?"
    Write-Host "  1) Global  ($GlobalConfig)"
    Write-Host "  2) Project (.\opencode.json)"
    Write-Host ""
    $choice = Read-Host "Choice [1]"
    if ($choice -eq "2") {
        return $ProjectConfig
    }
    return $GlobalConfig
}

function Write-OpenCodeConfig {
    param([string]$ConfigPath)

    $wrapperPath = Join-Path $InstallDir "al-lsp-wrapper.exe"
    # Use forward slashes in JSON for cross-platform consistency
    $wrapperPathJson = $wrapperPath -replace '\\', '/'

    $alConfig = [ordered]@{
        command    = @($wrapperPathJson)
        extensions = @(".al", ".dal")
    }

    if (Test-Path $ConfigPath) {
        # Merge into existing config
        try {
            $config = Get-Content $ConfigPath -Raw | ConvertFrom-Json
        } catch {
            Stop-Setup "Failed to parse $ConfigPath. Is it valid JSON?"
        }

        # Ensure lsp object exists
        if (-not $config.lsp) {
            $config | Add-Member -NotePropertyName "lsp" -NotePropertyValue ([PSCustomObject]@{})
        }

        # Set or replace the al entry
        if ($config.lsp.al) {
            $config.lsp.al = [PSCustomObject]$alConfig
        } else {
            $config.lsp | Add-Member -NotePropertyName "al" -NotePropertyValue ([PSCustomObject]$alConfig)
        }

        # Write UTF-8 without BOM
        $json = $config | ConvertTo-Json -Depth 10
        [System.IO.File]::WriteAllText($ConfigPath, $json)
        Write-Info "Updated $ConfigPath"
    } else {
        # Write new config file
        $parentDir = Split-Path $ConfigPath -Parent
        if ($parentDir -and -not (Test-Path $parentDir)) {
            New-Item -ItemType Directory -Path $parentDir -Force | Out-Null
        }

        $config = [ordered]@{
            lsp = [ordered]@{
                al = $alConfig
            }
        }
        $json = $config | ConvertTo-Json -Depth 10
        [System.IO.File]::WriteAllText($ConfigPath, $json)
        Write-Info "Created $ConfigPath"
    }
}

# Main execution
Write-Info "AL LSP for Agents - OpenCode Setup"
Write-Host ""

Test-ALExtension
Install-Binaries

$configPath = Get-ConfigPath
Write-OpenCodeConfig -ConfigPath $configPath

Write-Host ""
Write-Info "Setup complete!"
Write-Info "Binary location: $InstallDir"
Write-Info "Config written to: $configPath"
Write-Host ""
Write-Info "Open an .al file in OpenCode to verify the language server starts."
