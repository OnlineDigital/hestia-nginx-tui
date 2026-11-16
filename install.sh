#!/usr/bin/env bash
# hestia-nginx-tui installer: downloads the latest GitHub release for this
# machine (linux x86_64 / arm64), verifies its sha256 and installs the binary.
#
#   curl -fsSL https://raw.githubusercontent.com/onlinedigital/hestia-nginx-tui/main/install.sh | sudo bash
#
# Environment:
#   VERSION=v1.2.3      install a specific release instead of the latest
#   INSTALL_DIR=/path   where to put the binary (default: /usr/local/bin as
#                       root, ~/.local/bin otherwise)
set -euo pipefail

REPO="onlinedigital/hestia-nginx-tui"
BIN="hestia-nginx-tui"
GITHUB="${GITHUB_URL:-https://github.com}"

say() { printf '\033[1;35m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || die "only Linux is supported (this is $(uname -s))"

case "$(uname -m)" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *) die "unsupported architecture: $(uname -m) (need x86_64 or arm64)" ;;
esac

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
  final_url() { curl -fsSL -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO "$2" "$1"; }
  final_url() { wget -qO /dev/null --server-response "$1" 2>&1 | awk '/^  Location: /{u=$2} END{print u}'; }
else
  die "curl or wget is required"
fi

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  else die "sha256sum or shasum is required"; fi
}

# Resolve the version: github.com/<repo>/releases/latest redirects to .../tag/<tag>
TAG="${VERSION:-}"
if [ -z "$TAG" ]; then
  say "looking up the latest release"
  url="$(final_url "$GITHUB/$REPO/releases/latest")" || die "could not reach GitHub"
  TAG="${url##*/}"
  case "$url" in */releases/tag/*) ;; *) die "no release published yet at $GITHUB/$REPO/releases" ;; esac
fi
VER="${TAG#v}"
ASSET="${BIN}_${VER}_linux_${ARCH}.tar.gz"
BASE="$GITHUB/$REPO/releases/download/$TAG"

if [ -n "${INSTALL_DIR:-}" ]; then
  DEST="$INSTALL_DIR"
elif [ "$(id -u)" -eq 0 ]; then
  DEST="/usr/local/bin"
else
  DEST="$HOME/.local/bin"
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

say "downloading $ASSET ($TAG)"
fetch "$BASE/$ASSET" "$TMP/$ASSET" || die "download failed: $BASE/$ASSET"
fetch "$BASE/checksums.txt" "$TMP/checksums.txt" || die "download failed: $BASE/checksums.txt"

want="$(awk -v f="$ASSET" '$2 == f || $2 == "*" f {print $1}' "$TMP/checksums.txt")"
[ -n "$want" ] || die "$ASSET is not listed in checksums.txt"
have="$(sha256 "$TMP/$ASSET")"
[ "$want" = "$have" ] || die "checksum mismatch (expected $want, got $have)"
say "sha256 verified"

tar -xzf "$TMP/$ASSET" -C "$TMP" "$BIN"
mkdir -p "$DEST"
install -m 0755 "$TMP/$BIN" "$DEST/$BIN"
chmod +x "$DEST/$BIN"

say "installed $DEST/$BIN ($TAG)"
case ":$PATH:" in
  *":$DEST:"*) ;;
  *) printf '\033[1;33mnote:\033[0m %s is not in your PATH; add it, e.g.  export PATH="%s:$PATH"\n' "$DEST" "$DEST" ;;
esac
echo
echo "  run it as root:   sudo $BIN"
echo "  update later:     sudo $BIN update      (or Ctrl+U inside the TUI)"
