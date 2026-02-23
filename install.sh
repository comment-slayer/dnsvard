#!/bin/sh
set -eu

BIN="dnsvard"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BASE_URL="https://downloads.dnsvard.com"
VERSION="${DNSVARD_INSTALL_VERSION:-latest}"

need() { command -v "$1" >/dev/null 2>&1 || { printf "error: missing required command: %s\n" "$1" >&2; exit 1; }; }
fetch() { curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "$@"; }

YELLOW=""
RESET=""
if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
  YELLOW="\033[33m"
  RESET="\033[0m"
fi

comment() {
  printf "%b# %s%b\n" "$YELLOW" "$1" "$RESET"
}

for cmd in curl tar mktemp awk install; do need "$cmd"; done

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  darwin|linux) ;;
  *) printf "error: unsupported OS: %s\n" "$OS" >&2; exit 1 ;;
esac

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) printf "error: unsupported arch: %s\n" "$ARCH" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  printf "dnsvard installer: resolving latest version...\n" >&2
  VERSION="$(fetch "$BASE_URL/LATEST")"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -f "$TMP_DIR/$BIN" "$TMP_DIR/archive.tar.gz" "$TMP_DIR/checksums.txt"; rmdir "$TMP_DIR"' EXIT INT TERM

ARCHIVE="${BIN}_${VERSION}_${OS}_${ARCH}.tar.gz"

printf "dnsvard installer: downloading %s...\n" "$ARCHIVE" >&2
fetch "$BASE_URL/$VERSION/checksums.txt" -o "$TMP_DIR/checksums.txt"
fetch "$BASE_URL/$VERSION/$ARCHIVE" -o "$TMP_DIR/archive.tar.gz"

EXPECTED_SUM="$(awk -v f="$ARCHIVE" '$2 == f {print $1}' "$TMP_DIR/checksums.txt")"
[ -n "$EXPECTED_SUM" ] || { printf "error: checksum entry not found for %s\n" "$ARCHIVE" >&2; exit 1; }

if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL_SUM="$(sha256sum "$TMP_DIR/archive.tar.gz" | awk '{print $1}')"
else
  need shasum
  ACTUAL_SUM="$(shasum -a 256 "$TMP_DIR/archive.tar.gz" | awk '{print $1}')"
fi

[ "$EXPECTED_SUM" = "$ACTUAL_SUM" ] || { printf "error: checksum mismatch\n" >&2; exit 1; }

tar -xzf "$TMP_DIR/archive.tar.gz" -C "$TMP_DIR" "$BIN"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$TMP_DIR/$BIN" "$INSTALL_DIR/$BIN"

DISPLAY_INSTALL_DIR="$INSTALL_DIR"
case "$INSTALL_DIR" in
  "$HOME"|"$HOME"/*) DISPLAY_INSTALL_DIR="~${INSTALL_DIR#"$HOME"}" ;;
esac
printf "installed %s/%s\n" "$DISPLAY_INSTALL_DIR" "$BIN"

TARGET_USER="${SUDO_USER:-$(id -un)}"
LOGIN_SHELL="${SHELL:-}"
if command -v getent >/dev/null 2>&1; then
  if USER_ENTRY="$(getent passwd "$TARGET_USER" 2>/dev/null)" && [ -n "$USER_ENTRY" ]; then
    ENTRY_SHELL="$(printf '%s' "$USER_ENTRY" | awk -F: '{print $7}')"
    [ -n "$ENTRY_SHELL" ] && LOGIN_SHELL="$ENTRY_SHELL"
  fi
elif [ "$OS" = "darwin" ] && command -v dscl >/dev/null 2>&1; then
  if ENTRY_SHELL="$(dscl . -read "/Users/$TARGET_USER" UserShell 2>/dev/null | awk '{print $2}')" && [ -n "$ENTRY_SHELL" ]; then
    LOGIN_SHELL="$ENTRY_SHELL"
  fi
fi

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    SHELL_RC="~/.profile"
    case "$LOGIN_SHELL" in
      */zsh) SHELL_RC="~/.zshrc" ;;
      */bash) SHELL_RC="~/.bashrc" ;;
    esac
    printf "\n"
    comment "$DISPLAY_INSTALL_DIR is not in PATH"
    comment "Add permanently ($SHELL_RC):"
    printf "echo 'export PATH=\"%s:\$PATH\"' >> %s\n" "$INSTALL_DIR" "$SHELL_RC"
    printf "source %s\n" "$SHELL_RC"
    comment "Or for this shell only:"
    printf "export PATH=\"%s:\$PATH\"\n" "$INSTALL_DIR"
    ;;
esac

printf "\n"
comment "To set up dnsvard for the first time"
printf "sudo %s/%s bootstrap\n" "$INSTALL_DIR" "$BIN"

case "$OS" in
  darwin)
    printf "\n"
    comment "NOTES"
    printf "On first request to dnsvard hosts, macOS may prompt for Local Network access; click Allow\n"
    ;;
esac
