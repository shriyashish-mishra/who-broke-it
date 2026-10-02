#!/usr/bin/env bash
# Build release archives + checksums into dist/. Usage: scripts/release.sh v0.1.0
set -euo pipefail
VERSION="${1:?usage: release.sh vX.Y.Z}"
cd "$(dirname "$0")/.."
rm -rf dist && mkdir -p dist
for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${t%/*}; arch=${t#*/}
  name="wbi_${VERSION#v}_${os}_${arch}"
  stage="dist/$name"; mkdir -p "$stage"
  exe=wbi; [ "$os" = windows ] && exe=wbi.exe
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$stage/$exe" ./cmd/wbi
  cp LICENSE README.md "$stage/"
  if [ "$os" = windows ]; then (cd dist && zip -qr "$name.zip" "$name"); else tar -C dist -czf "dist/$name.tar.gz" "$name"; fi
  rm -rf "$stage"
  echo "built $name"
done
cd dist
if command -v sha256sum >/dev/null; then sha256sum *.tar.gz *.zip > checksums.txt; else shasum -a 256 *.tar.gz *.zip > checksums.txt; fi
cat checksums.txt
