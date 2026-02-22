#!/bin/sh
# Install axe CLI.
# To install a specific version, set VERSION before running:
#   VERSION=v1.0.0 sh install.sh
set -e

REPO="k-kohey/axe"
INSTALL_DIR="${INSTALL_DIR:-${HOME}/.local/bin}"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  darwin) ;;
  *) echo "Error: axe only supports macOS (darwin), got $OS" >&2; exit 1 ;;
esac

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
  arm64|aarch64) ARCH="arm64" ;;
  x86_64)        ARCH="amd64" ;;
  *) echo "Error: unsupported architecture $ARCH" >&2; exit 1 ;;
esac

# Resolve version
if [ -z "$VERSION" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed 's/.*"tag_name": *"//;s/".*//')"
  if [ -z "$VERSION" ]; then
    echo "Error: could not determine latest version" >&2
    exit 1
  fi
fi

BINARY="axe-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}"

echo "Installing axe ${VERSION} (${OS}/${ARCH})..."

mkdir -p "$INSTALL_DIR"
curl -fsSL -o "${INSTALL_DIR}/axe" "$URL"
chmod +x "${INSTALL_DIR}/axe"

echo "Installed axe to ${INSTALL_DIR}/axe"

case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *) echo "Note: Add ${INSTALL_DIR} to your PATH if not already set" ;;
esac

# Install idb_companion for --serve mode (interactive preview)
if command -v brew >/dev/null 2>&1; then
  if ! command -v idb_companion >/dev/null 2>&1; then
    echo ""
    echo "Installing idb_companion (required for --serve mode)..."
    brew install facebook/fb/idb-companion
  fi
else
  echo ""
  echo "Note: idb_companion is required for --serve mode (interactive preview)."
  echo "      Install Homebrew (https://brew.sh) then run: brew install facebook/fb/idb-companion"
fi
