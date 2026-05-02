# Install development dependencies for hf-sync on Windows.
# Run from PowerShell: .\scripts\install-deps.ps1

$ErrorActionPreference = "Stop"

function Write-Info($msg)  { Write-Host "[✓] $msg" -ForegroundColor Green }
function Write-Warn($msg)  { Write-Host "[!] $msg" -ForegroundColor Yellow }
function Write-Err($msg)   { Write-Host "[✗] $msg" -ForegroundColor Red }

Write-Host "=== hf-sync: Install Dependencies ===" -ForegroundColor Cyan
Write-Host ""

# --- Go ---
$goCmd = Get-Command go -ErrorAction SilentlyContinue
if ($goCmd) {
    $goVer = & go version
    Write-Info "Go found: $goVer"
} else {
    Write-Warn "Go not found. Installing..."
    $winget = Get-Command winget -ErrorAction SilentlyContinue
    $choco = Get-Command choco -ErrorAction SilentlyContinue
    $scoop = Get-Command scoop -ErrorAction SilentlyContinue

    if ($winget) {
        winget install --id GoLang.Go --accept-source-agreements --accept-package-agreements
    } elseif ($choco) {
        choco install golang -y
    } elseif ($scoop) {
        scoop install go
    } else {
        Write-Err "No package manager found (winget/choco/scoop)."
        Write-Err "Install Go manually from https://go.dev/dl/"
        exit 1
    }

    # Refresh PATH for current session.
    $env:Path = [System.Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path", "User")
    Write-Info "Go installed. You may need to restart your terminal."
}

# --- golangci-lint ---
$lintCmd = Get-Command golangci-lint -ErrorAction SilentlyContinue
if ($lintCmd) {
    Write-Info "golangci-lint found."
} else {
    Write-Warn "golangci-lint not found. Installing..."
    go install github.com/golangci-lint/golangci-lint/cmd/golangci-lint@latest
    Write-Info "golangci-lint installed."
}

# --- goreleaser (optional) ---
$goreleaserCmd = Get-Command goreleaser -ErrorAction SilentlyContinue
if ($goreleaserCmd) {
    Write-Info "goreleaser found."
} else {
    Write-Warn "goreleaser not found. Installing (optional, for releases)..."
    try {
        go install github.com/goreleaser/goreleaser/v2@latest
        Write-Info "goreleaser installed."
    } catch {
        Write-Warn "goreleaser install failed (non-critical)."
    }
}

# --- Git ---
$gitCmd = Get-Command git -ErrorAction SilentlyContinue
if ($gitCmd) {
    $gitVer = & git --version
    Write-Info "git found: $gitVer"
} else {
    Write-Err "git not found. Install via: winget install --id Git.Git"
    exit 1
}

# --- Go module dependencies ---
Write-Host ""
Write-Info "Downloading Go module dependencies..."
go mod download
Write-Info "Go modules ready."

Write-Host ""
Write-Host "=== All dependencies installed ===" -ForegroundColor Cyan
Write-Host ""
Write-Host "Quick start:"
Write-Host "  go build -o hf-sync.exe .   # Build binary"
Write-Host "  go test -race ./...          # Run tests"
Write-Host "  golangci-lint run ./...      # Run linter"
