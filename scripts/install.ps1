<#
.SYNOPSIS
    wayneblacktea install script for Windows (PowerShell 5.1+ / pwsh 7+).

.DESCRIPTION
    One-line installer:
        irm https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.ps1 | iex

    Downloads the latest release binaries (or pinned $env:WBT_VERSION),
    verifies SHA256 against the published checksums.txt, verifies the
    cosign keyless signature on checksums.txt (when cosign is available),
    extracts to %LOCALAPPDATA%\wayneblacktea\bin, prepends that path to the
    user PATH, writes a config .env (interactive prompt for DATABASE_URL +
    API_KEY), and registers the MCP server with the claude CLI if available.

    IMPORTANT: when piping to iex, stdin is the pipeline output, not a
    terminal — the script writes an EMPTY API_KEY= line and you MUST edit
    %LOCALAPPDATA%\wayneblacktea\config\.env before starting the server.
    To use the wizard, save the script to disk first and run it directly.

.NOTES
    Environment overrides (set before piping to iex):
        $env:WBT_VERSION              Pin a specific release (e.g. '1.2.3')
        $env:WBT_NO_MCP               '1' = skip `claude mcp add`
        $env:WBT_NO_PROMPT            '1' = skip wizard, write empty placeholders
        $env:WBT_INSECURE_SKIP_VERIFY '1' = skip cosign verification (NOT RECOMMENDED).
                                      Default behaviour is fail-closed: cosign + sig + cert
                                      MUST all be present and the signature MUST verify,
                                      otherwise installation aborts. (PR #85 R3 / S-N1)

    Exit codes:
        0  success
        1  user-facing error
        2  download / checksum / signature / archive-safety failure
#>

[CmdletBinding()]
param(
    [string]$Version           = $env:WBT_VERSION,
    [switch]$NoMcp             = ($env:WBT_NO_MCP -eq '1'),
    [switch]$NoPrompt          = ($env:WBT_NO_PROMPT -eq '1'),
    [switch]$InsecureSkipVerify = ($env:WBT_INSECURE_SKIP_VERIFY -eq '1')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# Force TLS 1.2 (required for older Windows PowerShell to talk to GitHub)
try {
    [Net.ServicePointManager]::SecurityProtocol = `
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {
    # PowerShell 7+ on .NET Core uses OS defaults; ignore
}

$RepoOwner = 'Wayne997035'
$RepoName  = 'wayneblacktea'
$GitHubApi = "https://api.github.com/repos/$RepoOwner/$RepoName/releases/latest"
$GitHubDl  = "https://github.com/$RepoOwner/$RepoName/releases/download"

# cosign keyless verification — sigstore certificate identity / OIDC issuer.
# Anchored regex (^...$) to prevent prefix/suffix injection attacks; restricts
# to the release.yml workflow on a semver tag. (PR #85 R3 / S-N2)
$CosignCertIdentityRegex = '^https://github\.com/Wayne997035/wayneblacktea/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$'
$CosignOidcIssuer        = 'https://token.actions.githubusercontent.com'

$InstallDir = Join-Path $env:LOCALAPPDATA 'wayneblacktea\bin'
$ConfigDir  = Join-Path $env:LOCALAPPDATA 'wayneblacktea\config'
$EnvFile    = Join-Path $ConfigDir '.env'

# Binaries shipped in the cli archive (must stay in sync with .goreleaser.yaml
# archive id "cli" `ids:` list).
$CliBinaries = @('wbt.exe', 'wbt-context.exe', 'wbt-hook.exe', 'wbt-guard.exe', 'wbt-doctor.exe')
$McpBinary   = 'wayneblacktea-mcp.exe'

function Write-Info  { param([string]$Msg) Write-Host "[install] $Msg" -ForegroundColor Cyan }
function Write-Warn2 { param([string]$Msg) Write-Host "[install] WARN: $Msg" -ForegroundColor Yellow }
function Write-Err   { param([string]$Msg) Write-Host "[install] ERROR: $Msg" -ForegroundColor Red }

function Stop-WithError {
    param([string]$Msg, [int]$Code = 1)
    Write-Err $Msg
    exit $Code
}

function Get-Architecture {
    $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    switch ($arch) {
        'X64'   { return 'x86_64' }
        'Arm64' { return 'arm64' }
        default { Stop-WithError "unsupported architecture: $arch" 1 }
    }
}

function Get-LatestVersion {
    # Resolves and STRICTLY validates the version string against semver before
    # any URL interpolation, so a hostile $env:WBT_VERSION cannot inject path or
    # query-string components into the GitHub release URL.
    # (PR #85 R3 / S-N3)
    $resolved = $null
    if ($Version) {
        $resolved = $Version
    } else {
        Write-Info "fetching latest release tag from GitHub"
        try {
            $headers = @{ 'User-Agent' = 'wayneblacktea-installer' }
            $resp = Invoke-RestMethod -Uri $GitHubApi -Headers $headers -UseBasicParsing
        } catch {
            Stop-WithError "failed to query GitHub releases API: $($_.Exception.Message)" 2
        }
        if (-not $resp.tag_name) {
            Stop-WithError "GitHub releases response missing tag_name" 2
        }
        $resolved = $resp.tag_name
    }
    $resolved = $resolved.TrimStart('v')
    if ($resolved -notmatch '^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$') {
        Stop-WithError "invalid version format: $resolved (expected MAJOR.MINOR.PATCH[-prerelease])" 1
    }
    return $resolved
}

function Get-ChecksumMap {
    param([string]$ChecksumsPath)
    $map = @{}
    Get-Content -Path $ChecksumsPath | ForEach-Object {
        # format: <sha256>  <filename>
        if ($_ -match '^([0-9a-fA-F]{64})\s+(.+)$') {
            $map[$Matches[2].Trim()] = $Matches[1].ToLower()
        }
    }
    return $map
}

function Test-FileSha256 {
    param([string]$Path, [string]$Expected)
    $actual = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLower()
    if ($actual -ne $Expected.ToLower()) {
        Write-Err "checksum mismatch for $Path"
        Write-Err "  expected: $Expected"
        Write-Err "  actual:   $actual"
        exit 2
    }
}

function Invoke-VerifiedDownload {
    param(
        [string]$Url,
        [string]$OutFile,
        [hashtable]$ChecksumMap
    )
    $name = Split-Path -Leaf $OutFile
    Write-Info "downloading $name"
    try {
        Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing -MaximumRedirection 5
    } catch {
        Stop-WithError "failed to download $Url ($($_.Exception.Message))" 2
    }
    if (-not $ChecksumMap.ContainsKey($name)) {
        Stop-WithError "checksum entry for $name missing from checksums.txt" 2
    }
    Test-FileSha256 -Path $OutFile -Expected $ChecksumMap[$name]
}

# Invoke-CosignVerify verifies checksums.txt authenticity using cosign keyless mode.
#
# DEFAULT: fail-closed. If cosign is missing OR the signature/cert artefacts
# are missing OR verification fails, the installer aborts. This prevents an
# attacker who can MITM (or 404-spoof) the .sig/.pem from forcing a
# warn-and-proceed code path.
#
# Escape hatch (NOT RECOMMENDED): set $env:WBT_INSECURE_SKIP_VERIFY=1 to skip
# the entire signature step. (PR #85 R3 / S-N1)
function Invoke-CosignVerify {
    param(
        [string]$ChecksumsPath,
        [string]$SignaturePath,
        [string]$CertificatePath
    )
    if ($InsecureSkipVerify) {
        Write-Warn2 "WBT_INSECURE_SKIP_VERIFY=1 — skipping cosign verification (NOT RECOMMENDED for production installs)"
        return
    }
    $cosign = Get-Command cosign -ErrorAction SilentlyContinue
    if ($null -eq $cosign) {
        Stop-WithError "cosign not installed — install cosign or set `$env:WBT_INSECURE_SKIP_VERIFY=1 to bypass (not recommended). https://docs.sigstore.dev/system_config/installation/" 2
    }
    if (-not (Test-Path $SignaturePath) -or ((Get-Item $SignaturePath).Length -eq 0)) {
        Stop-WithError "signature file $SignaturePath missing/empty — refusing to install. Set `$env:WBT_INSECURE_SKIP_VERIFY=1 to bypass (NOT RECOMMENDED)." 2
    }
    if (-not (Test-Path $CertificatePath) -or ((Get-Item $CertificatePath).Length -eq 0)) {
        Stop-WithError "certificate file $CertificatePath missing/empty — refusing to install. Set `$env:WBT_INSECURE_SKIP_VERIFY=1 to bypass (NOT RECOMMENDED)." 2
    }
    Write-Info "verifying checksums.txt signature with cosign"
    $cosignArgs = @(
        'verify-blob',
        '--certificate-identity-regexp', $CosignCertIdentityRegex,
        '--certificate-oidc-issuer',     $CosignOidcIssuer,
        '--certificate',                 $CertificatePath,
        '--signature',                   $SignaturePath,
        $ChecksumsPath
    )
    & cosign @cosignArgs 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Stop-WithError "cosign verification failed" 2
    }
    Write-Info "cosign signature OK"
}

# Test-ArchiveSafe inspects every entry in a ZIP archive and rejects unsafe
# paths (absolute, parent-traversal, drive letter, leading backslash).
# (Round 2 / S-M3)
function Test-ArchiveSafe {
    param([string]$ArchivePath)
    Add-Type -AssemblyName System.IO.Compression.FileSystem | Out-Null
    $zip = [System.IO.Compression.ZipFile]::OpenRead($ArchivePath)
    try {
        foreach ($entry in $zip.Entries) {
            # Reject: absolute paths (Unix or Windows), parent traversal, tilde, leading drive.
            if ($entry.FullName -match '(^/|(^|/)\.\.($|/)|^[A-Za-z]:|^\\|^~)') {
                Stop-WithError "archive contains unsafe path: $($entry.FullName)" 2
            }
        }
    } finally {
        $zip.Dispose()
    }
}

function Add-ToUserPath {
    param([string]$Dir)
    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($null -eq $current) { $current = '' }
    $entries = $current -split ';' | Where-Object { $_ -ne '' }
    if ($entries -contains $Dir) {
        Write-Info "$Dir already on user PATH"
        return
    }
    Write-Info "prepending $Dir to user PATH"
    $newPath = if ($current) { "$Dir;$current" } else { $Dir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    # propagate to current session
    $env:Path = "$Dir;$env:Path"
    Write-Warn2 "open a new terminal for the PATH update to take effect in other shells"
}

function Set-CurrentUserOnlyAcl {
    param([string]$Path)
    try {
        $acl = Get-Acl -Path $Path
        $acl.SetAccessRuleProtection($true, $false)  # disable inheritance, drop inherited rules
        $acl.Access | ForEach-Object { [void]$acl.RemoveAccessRule($_) }
        $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
            [System.Security.Principal.WindowsIdentity]::GetCurrent().User,
            'FullControl',
            'Allow'
        )
        $acl.AddAccessRule($rule)
        Set-Acl -Path $Path -AclObject $acl
    } catch {
        Write-Warn2 "could not restrict ACL on $Path ($($_.Exception.Message)); please review manually"
    }
}

function Write-EnvFile {
    if (Test-Path $EnvFile) {
        Write-Info "config already exists at $EnvFile (skipping wizard)"
        return
    }
    if (-not (Test-Path $ConfigDir)) {
        New-Item -ItemType Directory -Path $ConfigDir -Force | Out-Null
    }

    $dbUrl = ''
    $apiKey = ''
    $workspaceId = ''

    if ($NoPrompt -or [Console]::IsInputRedirected) {
        # IMPORTANT: write empty API_KEY rather than a guessable placeholder.
        # Earlier versions wrote `REPLACE_ME_RUN_openssl_rand_-hex_32` which
        # any attacker can try literally. Empty value forces fail-closed
        # behaviour at the server (API_KEY required at startup). (S-M4)
        Write-Info "non-interactive install — writing .env with empty API_KEY"
        Write-Warn2 "edit $EnvFile and set API_KEY before starting the server"
        Write-Warn2 "  generate a random key with: openssl rand -hex 32"
    } else {
        Write-Host ""
        Write-Host "Enter DATABASE_URL (postgres://... — leave blank for SQLite local file):"
        $dbUrl = Read-Host -Prompt '> '

        while ([string]::IsNullOrWhiteSpace($apiKey)) {
            Write-Host "Enter API_KEY (required; suggested: openssl rand -hex 32):"
            # Read-Host -AsSecureString suppresses on-screen echo (CWE-200).
            # We immediately decrypt and use the value, then clear locals.
            # (Round 2 / S-M1)
            $secure = Read-Host -Prompt '> ' -AsSecureString
            $apiKey = [System.Net.NetworkCredential]::new('', $secure).Password
            if ([string]::IsNullOrWhiteSpace($apiKey)) {
                Write-Warn2 "API_KEY cannot be empty"
            }
        }

        Write-Host "Enter WORKSPACE_ID (optional, leave blank to use default):"
        $workspaceId = Read-Host -Prompt '> '
    }

    $content = @"
# wayneblacktea config — managed by install.ps1
# DATABASE_URL — leave blank to use SQLite local file at %LOCALAPPDATA%\wayneblacktea\data.db
DATABASE_URL=$dbUrl
# API_KEY — gates every /api/* route; rotate via openssl rand -hex 32
API_KEY=$apiKey
# WORKSPACE_ID — optional UUID for multi-workspace scoping
WORKSPACE_ID=$workspaceId
"@

    # write as UTF-8 without BOM (compat with godotenv)
    [System.IO.File]::WriteAllText($EnvFile, $content, [System.Text.UTF8Encoding]::new($false))
    Set-CurrentUserOnlyAcl -Path $EnvFile
    # Drop sensitive value from PowerShell variable scope after persisting.
    $apiKey = ''
    $secure = $null
    Write-Info "wrote $EnvFile (current-user-only ACL)"
}

function Register-McpServer {
    if ($NoMcp) {
        Write-Info "NoMcp set — skipping claude mcp add"
        return
    }
    $claude = Get-Command claude -ErrorAction SilentlyContinue
    if ($null -eq $claude) {
        Write-Warn2 "claude CLI not found — skipping MCP registration"
        Write-Warn2 "after installing Claude Code, run:"
        Write-Warn2 "  claude mcp add wayneblacktea -- `"$InstallDir\$McpBinary`""
        return
    }
    Write-Info "registering wayneblacktea MCP server with claude CLI"
    $exe = Join-Path $InstallDir $McpBinary
    & claude mcp add wayneblacktea -- $exe 2>$null
    if ($LASTEXITCODE -eq 0) {
        Write-Info "MCP server registered"
    } else {
        Write-Warn2 "claude mcp add failed (already registered?) — verify with: claude mcp list"
    }
}

# --- main ---

$arch = Get-Architecture
$os   = 'windows'
Write-Info "detected platform: $os/$arch"

$ver = Get-LatestVersion
Write-Info "installing wayneblacktea v$ver"

$mcpArchive    = "wayneblacktea-mcp_${ver}_${os}_${arch}.zip"
$cliArchive    = "wayneblacktea-cli_${ver}_${os}_${arch}.zip"
$checksumsName = 'checksums.txt'
$signatureName = 'checksums.txt.sig'
$certName      = 'checksums.txt.pem'

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "wbt-install-$([guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

try {
    $sumsPath = Join-Path $tmpDir $checksumsName
    $sigPath  = Join-Path $tmpDir $signatureName
    $certPath = Join-Path $tmpDir $certName
    Write-Info "fetching checksums.txt"
    try {
        Invoke-WebRequest -Uri "$GitHubDl/v$ver/$checksumsName" -OutFile $sumsPath -UseBasicParsing
    } catch {
        Stop-WithError "failed to download checksums file ($($_.Exception.Message))" 2
    }

    # Best-effort fetch for cosign artifacts — older releases may not have them.
    try {
        Invoke-WebRequest -Uri "$GitHubDl/v$ver/$signatureName" -OutFile $sigPath -UseBasicParsing -ErrorAction SilentlyContinue
    } catch { }
    try {
        Invoke-WebRequest -Uri "$GitHubDl/v$ver/$certName" -OutFile $certPath -UseBasicParsing -ErrorAction SilentlyContinue
    } catch { }

    Invoke-CosignVerify -ChecksumsPath $sumsPath -SignaturePath $sigPath -CertificatePath $certPath

    $checksumMap = Get-ChecksumMap -ChecksumsPath $sumsPath

    $mcpZip = Join-Path $tmpDir $mcpArchive
    $cliZip = Join-Path $tmpDir $cliArchive
    Invoke-VerifiedDownload -Url "$GitHubDl/v$ver/$mcpArchive" -OutFile $mcpZip -ChecksumMap $checksumMap
    Invoke-VerifiedDownload -Url "$GitHubDl/v$ver/$cliArchive" -OutFile $cliZip -ChecksumMap $checksumMap

    Write-Info "verifying archive contents are safe"
    Test-ArchiveSafe -ArchivePath $mcpZip
    Test-ArchiveSafe -ArchivePath $cliZip

    Write-Info "extracting archives"
    $extractDir = Join-Path $tmpDir 'extract'
    New-Item -ItemType Directory -Path $extractDir -Force | Out-Null
    Expand-Archive -Path $mcpZip -DestinationPath $extractDir -Force
    Expand-Archive -Path $cliZip -DestinationPath $extractDir -Force

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    # Install all 6 binaries: wayneblacktea-mcp + 5 CLI tools (wbt, wbt-context,
    # wbt-hook, wbt-guard, wbt-doctor). MUST stay aligned with .goreleaser.yaml
    # archive `cli` ids list. (Round 2 / R-M2)
    $allBinaries = @($McpBinary) + $CliBinaries
    foreach ($exe in $allBinaries) {
        $src = Join-Path $extractDir $exe
        if (-not (Test-Path $src)) {
            Stop-WithError "expected binary $exe missing from extracted archives" 2
        }
        Copy-Item -Path $src -Destination (Join-Path $InstallDir $exe) -Force
        Write-Info "installed $InstallDir\$exe"
    }

    Add-ToUserPath -Dir $InstallDir
    Write-EnvFile
    Register-McpServer

    Write-Host ""
    Write-Info "wayneblacktea v$ver installed successfully"
    Write-Info "binaries:   $InstallDir"
    Write-Info "config:     $EnvFile"
    Write-Info "next steps: edit $EnvFile (set API_KEY first) then run: wbt --help"
}
finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
