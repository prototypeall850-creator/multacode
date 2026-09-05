#!/usr/bin/env bash
# Installer multacode untuk Termux (aman juga di Linux umum).
# Pakai: curl -fsSL https://raw.githubusercontent.com/prototypeall850-creator/multacode/main/install.sh | bash
set -euo pipefail

REPO="https://github.com/prototypeall850-creator/multacode.git"
SRC="${MULTACODE_SRC:-$HOME/multacode}"

is_android() { [ "$(uname -o 2>/dev/null)" = "Android" ]; }

if command -v pkg >/dev/null 2>&1; then
  pkg install -y golang git
fi
command -v go >/dev/null 2>&1 || { echo "butuh Go 1.24.2+: pkg install golang" >&2; exit 1; }
command -v git >/dev/null 2>&1 || { echo "butuh git: pkg install git" >&2; exit 1; }

if is_android; then
  # Toolchain Go resmi tidak ada untuk host android -> jangan pernah auto-download.
  go env -w GOTOOLCHAIN=local
  BIN_DIR="$PREFIX/bin"
else
  BIN_DIR="$HOME/.local/bin"
fi
mkdir -p "$BIN_DIR"

if [ -d "$SRC/.git" ]; then
  echo "update $SRC …"
  git -C "$SRC" pull --ff-only
else
  echo "clone ke $SRC …"
  git clone "$REPO" "$SRC"
fi

cd "$SRC"
echo "build …"
go build -o multacode ./cmd/multacode
ln -sf "$SRC/multacode" "$BIN_DIR/multacode"
hash -r 2>/dev/null || true

"$SRC/multacode" --setup
echo
echo "selesai ✓  coba: multacode"
echo "update lain kali: multacode update"
