#!/bin/sh
# Downloads bin/hpm, the binary the plugin runs, from the release matching the
# manifest's version, or builds it from source when that fails and Go is
# installed.
set -eu
cd "$(dirname "$0")/.."

version=$(sed -n 's/^version = "\(.*\)"$/\1/p' herdr-plugin.toml | head -n 1)
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case $arch in
x86_64) arch=amd64 ;;
aarch64) arch=arm64 ;;
esac
url=https://github.com/vika2603/herdr-plugin-manager/releases/download/v$version/hpm_${os}_${arch}

mkdir -p bin
if curl -fsSL -o bin/hpm "$url"; then
	chmod +x bin/hpm
elif command -v go >/dev/null 2>&1; then
	go build -o bin/hpm ./cmd/hpm
else
	rm -f bin/hpm
	echo "cannot download $url, and Go is not installed to build hpm" >&2
	exit 1
fi
