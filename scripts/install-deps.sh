#!/usr/bin/env bash
# Install development dependencies for hf-sync.
# Supports macOS (Homebrew) and Linux (apt/dnf/pacman).

set -euo pipefail

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { printf "${GREEN}[✓]${NC} %s\n" "$*"; }
warn()  { printf "${YELLOW}[!]${NC} %s\n" "$*"; }
error() { printf "${RED}[✗]${NC} %s\n" "$*"; }

OS="$(uname -s)"

GO_VERSION="1.23.0"

install_go() {
    case "$OS" in
        Darwin)
            if command -v brew &>/dev/null; then
                info "Installing Go via Homebrew..."
                brew install go
            else
                error "Homebrew not found. Install from https://brew.sh then re-run."
                exit 1
            fi
            ;;
        Linux)
            local arch
            arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
            case "$arch" in
                x86_64|amd64) arch="amd64" ;;
                aarch64|arm64) arch="arm64" ;;
                *) error "Unsupported architecture: $arch"; exit 1 ;;
            esac
            local tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
            info "Installing Go ${GO_VERSION} from official tarball..."
            curl -fsSL "https://go.dev/dl/${tarball}" -o "/tmp/${tarball}"
            sudo rm -rf /usr/local/go
            sudo tar -C /usr/local -xzf "/tmp/${tarball}"
            rm -f "/tmp/${tarball}"
            if ! grep -q '/usr/local/go/bin' ~/.profile 2>/dev/null; then
                echo 'export PATH="/usr/local/go/bin:$PATH"' >> ~/.profile
            fi
            export PATH="/usr/local/go/bin:$PATH"
            ;;
        *)
            error "Unsupported OS: $OS"
            exit 1
            ;;
    esac
}

install_golangci_lint() {
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b "$(go env GOPATH)/bin"
}

install_goreleaser() {
    case "$OS" in
        Darwin)
            brew install goreleaser/tap/goreleaser
            ;;
        Linux)
            go install github.com/goreleaser/goreleaser/v2@latest
            ;;
    esac
}

echo "=== hf-sync: Install Dependencies ==="
echo ""

# --- Go ---
needs_go_install=false
if command -v go &>/dev/null; then
    current_go="$(go env GOVERSION 2>/dev/null || echo "")"
    current_go="${current_go#go}"  # strip "go" prefix
    if [ -z "$current_go" ]; then
        warn "Go found but could not determine version. Reinstalling..."
        needs_go_install=true
    else
        required_major="${GO_VERSION%%.*}"
        required_minor="${GO_VERSION#*.}"; required_minor="${required_minor%%.*}"
        current_major="${current_go%%.*}"
        current_minor="${current_go#*.}"; current_minor="${current_minor%%.*}"
        if [ "$current_major" -lt "$required_major" ] 2>/dev/null || \
           { [ "$current_major" -eq "$required_major" ] && [ "$current_minor" -lt "$required_minor" ]; } 2>/dev/null; then
            warn "Go found (go${current_go}) but >= go${GO_VERSION} is required. Upgrading..."
            needs_go_install=true
        else
            info "Go found: $(go version)"
        fi
    fi
else
    warn "Go not found. Installing..."
    needs_go_install=true
fi

if [ "$needs_go_install" = true ]; then
    install_go
    info "Go installed: $(go version)"
fi

# --- golangci-lint ---
if command -v golangci-lint &>/dev/null; then
    info "golangci-lint found."
else
    warn "golangci-lint not found. Installing..."
    install_golangci_lint
    info "golangci-lint installed."
fi

# --- goreleaser (optional) ---
if command -v goreleaser &>/dev/null; then
    info "goreleaser found."
else
    warn "goreleaser not found. Installing (optional, for releases)..."
    install_goreleaser || warn "goreleaser install failed (non-critical)."
fi

# --- Git ---
if command -v git &>/dev/null; then
    info "git found: $(git --version)"
else
    error "git not found. Please install git."
    exit 1
fi

# --- git-lfs ---
if command -v git-lfs &>/dev/null; then
    info "git-lfs found: $(git lfs version)"
else
    warn "git-lfs not found. Installing..."
    case "$OS" in
        Darwin)
            brew install git-lfs
            ;;
        Linux)
            if command -v apt-get &>/dev/null; then
                sudo apt-get update && sudo apt-get install -y git-lfs
            elif command -v dnf &>/dev/null; then
                sudo dnf install -y git-lfs
            elif command -v pacman &>/dev/null; then
                sudo pacman -Sy --noconfirm git-lfs
            else
                error "No supported package manager found. Install git-lfs manually: https://git-lfs.com/"
                exit 1
            fi
            ;;
    esac
    git lfs install
    info "git-lfs installed: $(git lfs version)"
fi

# --- Go module dependencies ---
echo ""
info "Downloading Go module dependencies..."
go mod tidy
info "Go modules ready."

echo ""
echo "=== All dependencies installed ==="
echo ""
echo "Quick start:"
echo "  make build    # Build binary"
echo "  make test     # Run tests"
echo "  make lint     # Run linter"