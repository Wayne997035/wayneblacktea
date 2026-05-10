<#
.SYNOPSIS
    wayneblacktea install script for Windows (PowerShell 5.1+ / pwsh 7+).

.DESCRIPTION
    One-line installer:
        irm https://raw.githubusercontent.com/Wayne997035/wayneblacktea/master/scripts/install.ps1 | iex

    Downloads the latest release binaries (or pinned $env:WBT_VERSION),
    verifies SHA256 against the published checksums.txt, extracts to
    %LOCALAPPDATA%\wayneblacktea\bin, prepends that path to the user PATH,
    writes a config .env (interactive prompt for DATABASE_URL + API_KEY),
    and registers the MCP server with the claude CLI if available.

.NOTES
    Environment overrides (set before piping to iex):
        $env:WBT_VERSION    Pin a specific release (e.g. '1.2.3')
        $env:WBT_NO_MCP     '1' = skip `claude mcp add`
        $env:WBT_NO_PROMPT  '1' = skip wizard, write placeholders

    Exit codes:
        0  success
        1  user-facing error
        2  download / checksum failure
#>

[CmdletBinding()]
param(
    [string]$Version  = $env:WBT_VERSION,
    [switch]$NoMcp    = ($env:WBT_NO_MCP -eq '1'),
    [switch]$NoPrompt = ($env:WBT_NO_PROMPT -eq '1')
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

$InstallDir = Join-Path $env:LOCALAPPDATA 'wayneblacktea\bin'
$ConfigDir  = Join-Path $env:LOCALAPPDATA 'wayneblacktea\config'
$EnvFile    = Join-Path $ConfigDir '.env'

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
    if ($Version) { return $Version.TrimStart('v') }
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
    return ($resp.tag_name.TrimStart('v'))
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
        Write-Info "non-interactive install — writing .env with placeholders"
        $apiKey = 'REPLACE_ME_RUN_openssl_rand_-hex_32'
    } else {
        Write-Host ""
        Write-Host "Enter DATABASE_URL (postgres://... — leave blank for SQLite local file):"
        $dbUrl = Read-Host -Prompt '> '

        while ([string]::IsNullOrWhiteSpace($apiKey)) {
            Write-Host "Enter API_KEY (required; suggested: openssl rand -hex 32):"
            $apiKey = Read-Host -Prompt '> '
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
        Write-Warn2 "  claude mcp add wayneblacktea -- `"$InstallDir\wayneblacktea-mcp.exe`""
        return
    }
    Write-Info "registering wayneblacktea MCP server with claude CLI"
    $exe = Join-Path $InstallDir 'wayneblacktea-mcp.exe'
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

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) "wbt-install-$([guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

try {
    $sumsPath = Join-Path $tmpDir $checksumsName
    Write-Info "fetching checksums.txt"
    try {
        Invoke-WebRequest -Uri "$GitHubDl/v$ver/$checksumsName" -OutFile $sumsPath -UseBasicParsing
    } catch {
        Stop-WithError "failed to download checksums file ($($_.Exception.Message))" 2
    }

    $checksumMap = Get-ChecksumMap -ChecksumsPath $sumsPath

    $mcpZip = Join-Path $tmpDir $mcpArchive
    $cliZip = Join-Path $tmpDir $cliArchive
    Invoke-VerifiedDownload -Url "$GitHubDl/v$ver/$mcpArchive" -OutFile $mcpZip -ChecksumMap $checksumMap
    Invoke-VerifiedDownload -Url "$GitHubDl/v$ver/$cliArchive" -OutFile $cliZip -ChecksumMap $checksumMap

    Write-Info "extracting archives"
    $extractDir = Join-Path $tmpDir 'extract'
    New-Item -ItemType Directory -Path $extractDir -Force | Out-Null
    Expand-Archive -Path $mcpZip -DestinationPath $extractDir -Force
    Expand-Archive -Path $cliZip -DestinationPath $extractDir -Force

    if (-not (Test-Path $InstallDir)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    }

    foreach ($exe in @('wayneblacktea-mcp.exe', 'wbt.exe', 'wbt-context.exe', 'wbt-hook.exe')) {
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
    Write-Info "next steps: edit $EnvFile if you skipped the wizard, then run: wbt --help"
}
finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
