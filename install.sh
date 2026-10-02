#!/bin/sh
# Install wbi (Who Broke It?) from the latest GitHub release. Usage:
#   curl -fsSL https://raw.githubusercontent.com/shriyashish-mishra/who-broke-it/main/install.sh | sh
# Env: WBI_VERSION=v0.1.0 to pin, WBI_INSTALL_DIR to choose the target (default: ~/.local/bin, or /usr/local/bin if writable).
set -eu
REPO="shriyashish-mishra/who-broke-it"
os=$(uname -s | tr '[:upper:]' '[:lower:]'); case "$os" in linux|darwin) ;; *) echo "unsupported OS: $os (Windows: download the .zip from the releases page)" >&2; exit 1;; esac
arch=$(uname -m); case "$arch" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo "unsupported architecture: $arch" >&2; exit 1;; esac
ver="${WBI_VERSION:-}"
if [ -z "$ver" ]; then ver=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1); fi
[ -n "$ver" ] || { echo "could not determine the latest release" >&2; exit 1; }
name="wbi_${ver#v}_${os}_${arch}"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
echo "downloading wbi $ver for $os/$arch"
curl -fsSL "https://github.com/$REPO/releases/download/$ver/$name.tar.gz" -o "$tmp/$name.tar.gz"
curl -fsSL "https://github.com/$REPO/releases/download/$ver/checksums.txt" -o "$tmp/checksums.txt"
want=$(grep " $name.tar.gz\$" "$tmp/checksums.txt" | awk '{print $1}')
if command -v sha256sum >/dev/null; then got=$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}'); else got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}'); fi
[ -n "$want" ] && [ "$want" = "$got" ] || { echo "checksum mismatch, refusing to install" >&2; exit 1; }
tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
dir="${WBI_INSTALL_DIR:-}"
if [ -z "$dir" ]; then if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi; fi
mkdir -p "$dir" && install -m 0755 "$tmp/$name/wbi" "$dir/wbi"
echo "installed $dir/wbi"; "$dir/wbi" version
case ":$PATH:" in *":$dir:"*) ;; *) echo "note: $dir is not on your PATH";; esac
