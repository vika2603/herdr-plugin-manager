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
url=https://github.com/vika2603/herdr-plugin-manager/releases/download/v$version/hpm_${os}_${arch}.tar.gz

# The new binary replaces bin/hpm by a rename, so an hpm that is running,
# such as the one updating itself, keeps its own file.
tmp=bin/.new
rm -rf "$tmp"
mkdir -p "$tmp"
if ! { curl -fsSL -o "$tmp/hpm.tar.gz" "$url" && tar -xzf "$tmp/hpm.tar.gz" -C "$tmp" hpm; }; then
	if ! command -v go >/dev/null 2>&1; then
		rm -rf "$tmp"
		echo "cannot download $url, and Go is not installed to build hpm" >&2
		exit 1
	fi
	go build -o "$tmp/hpm" ./cmd/hpm
fi
mv -f "$tmp/hpm" bin/hpm
rm -rf "$tmp"

# A copy goes in HPM_BIN_DIR, ~/.local/bin by default, so hpm runs from a
# shell too. An hpm there that is not this program is left alone.
dir=${HPM_BIN_DIR:-$HOME/.local/bin}
target=$dir/hpm
if [ -L "$target" ] || { [ -e "$target" ] && ! grep -q github.com/vika2603/herdr-plugin-manager "$target"; }; then
	echo "left $target alone: it is not hpm" >&2
elif mkdir -p "$dir" && new=$(mktemp "$dir/.hpm.XXXXXX") && cp bin/hpm "$new" && chmod 755 "$new" && mv -f "$new" "$target"; then
	case ":$PATH:" in
	*":$dir:"*) ;;
	*) echo "installed $target; add $dir to PATH to run hpm" >&2 ;;
	esac
else
	rm -f "${new:-}"
	echo "could not install $target" >&2
fi
