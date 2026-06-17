#!/usr/bin/env sh
set -eu

REPO="${VIBESWAP_REPO:-anosognosia/vibe-swap}"
VERSION="${1:-${VIBESWAP_VERSION:-latest}}"
INSTALL_DIR="${VIBESWAP_INSTALL_DIR:-$HOME/.local/bin}"
BIN_NAME="vibeswap"

fail() {
  echo "vibeswap install: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

need curl
need tar
need uname

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Darwin) ASSET_OS="Darwin" ;;
  *) fail "unsupported OS: $OS. VibeSwap currently supports macOS." ;;
esac

case "$ARCH" in
  arm64|aarch64) ASSET_ARCH="arm64" ;;
  x86_64|amd64) ASSET_ARCH="x86_64" ;;
  *) fail "unsupported architecture: $ARCH" ;;
esac

ASSET="${BIN_NAME}_${ASSET_OS}_${ASSET_ARCH}.tar.gz"
if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

echo "Downloading $URL"
curl -fL "$URL" -o "$TMP_DIR/$ASSET"
tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$TMP_DIR/$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"

echo "Installed $BIN_NAME to $INSTALL_DIR/$BIN_NAME"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    echo
    echo "Add this to your shell profile if $BIN_NAME is not found:"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    ;;
esac

"$INSTALL_DIR/$BIN_NAME" --version
