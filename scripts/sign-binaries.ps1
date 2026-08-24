<#
.SYNOPSIS
Signs the committed Windows binaries with Azure Artifact Signing (Trusted Signing).

.DESCRIPTION
The Claude Code marketplace ships the plugin binaries straight from this repo,
so binaries built locally (al-lsp-wrapper.exe per the CLAUDE.md checklist) must
be signed BEFORE committing. CI signing in release.yml only covers the GitHub
release zips and .vsix bundles; al-call-hierarchy.exe copies arrive already
signed via al-sem's build-and-deploy workflow.

Prerequisites (one-time):
  Install-Module -Name TrustedSigning -Scope CurrentUser
  az login   # account needs the "Artifact Signing Certificate Profile Signer" role

Usage:
  ./scripts/sign-binaries.ps1
  ./scripts/sign-binaries.ps1 -Force        # re-sign even if already validly signed

Endpoint/account/profile default from env vars AZURE_SIGNING_ENDPOINT,
AZURE_SIGNING_ACCOUNT, AZURE_SIGNING_PROFILE (same names as the CI repo vars).
#>
param(
    [string]$Endpoint = $env:AZURE_SIGNING_ENDPOINT,
    [string]$AccountName = $env:AZURE_SIGNING_ACCOUNT,
    [string]$ProfileName = $env:AZURE_SIGNING_PROFILE,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'

if (-not $Endpoint -or -not $AccountName -or -not $ProfileName) {
    throw "Missing signing config. Set AZURE_SIGNING_ENDPOINT / AZURE_SIGNING_ACCOUNT / AZURE_SIGNING_PROFILE or pass -Endpoint/-AccountName/-ProfileName."
}

$repoRoot = Split-Path -Parent $PSScriptRoot

# Every committed .exe in the repo. al-language-server-go/bin is the dev/test
# harness binary; signing it too keeps local runs identical to shipped bits.
$candidates = @(
    "al-language-server-go-windows\bin\al-lsp-wrapper.exe",
    "al-language-server-go-windows\bin\al-call-hierarchy.exe",
    "al-language-server-go-windows\bin\alsem.exe",
    "al-language-server-go\bin\al-lsp-wrapper.exe",
    "al-language-server-go\bin\al-call-hierarchy.exe",
    "al-language-server-go\bin\alsem.exe"
) | ForEach-Object { Join-Path $repoRoot $_ } | Where-Object { Test-Path $_ }

$toSign = @()
foreach ($file in $candidates) {
    $sig = Get-AuthenticodeSignature -FilePath $file
    if ($sig.Status -eq 'Valid' -and -not $Force) {
        Write-Host "already signed, skipping: $file"
    } else {
        $toSign += $file
    }
}

if (-not $toSign) {
    Write-Host "Nothing to sign."
    exit 0
}

Write-Host "Signing $($toSign.Count) file(s) with $AccountName/$ProfileName..."
# -Files takes ONE path string, not an array — sign each file in turn.
foreach ($file in $toSign) {
    Write-Host "signing: $file"
    Invoke-TrustedSigning `
        -Endpoint $Endpoint `
        -CodeSigningAccountName $AccountName `
        -CertificateProfileName $ProfileName `
        -Files $file `
        -FileDigest SHA256 `
        -TimestampRfc3161 'http://timestamp.acs.microsoft.com' `
        -TimestampDigest SHA256
}

foreach ($file in $toSign) {
    $sig = Get-AuthenticodeSignature -FilePath $file
    if ($sig.Status -ne 'Valid') {
        throw "Signature verification failed for $file : $($sig.Status)"
    }
    Write-Host "signed: $file ($($sig.SignerCertificate.Subject))"
}
