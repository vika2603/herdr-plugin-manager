#!/bin/sh
# Build the checked-out hpm, or install a verified release when Go is unavailable.
set -eu
cd "$(dirname "$0")/.."

mkdir -p bin
tmp=$(mktemp -d bin/.download.XXXXXX)
trap 'rm -rf "$tmp"' EXIT
built=false

if command -v go >/dev/null 2>&1; then
	go build -o "$tmp/hpm" ./cmd/hpm
	mv -f "$tmp/hpm" bin/hpm
	built=true
fi

if [ "$built" = false ]; then
	version=$(sed -n 's/^version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' herdr-plugin.toml | head -n 1)
	case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) echo "install.sh: no release binary for $(uname -s)" >&2; exit 1 ;;
	esac
	case "$(uname -m)" in
	arm64 | aarch64) arch=arm64 ;;
	x86_64 | amd64) arch=amd64 ;;
	*) echo "install.sh: no release binary for $(uname -m)" >&2; exit 1 ;;
	esac
	asset="hpm_${os}_${arch}.tar.gz"
	release="https://github.com/vika2603/herdr-plugin-manager/releases/download/v$version"
	if command -v curl >/dev/null 2>&1; then
		fetch() { curl -fsSL -o "$1" "$2"; }
	elif command -v wget >/dev/null 2>&1; then
		fetch() { wget -q -O "$1" "$2"; }
	else
		echo "install.sh: Go is unavailable, and neither curl nor wget can fetch the release" >&2
		exit 1
	fi
	if ! fetch "$tmp/checksums.txt" "$release/checksums.txt"; then
		echo "install.sh: could not download $release/checksums.txt" >&2
		exit 1
	fi
	expected=$(sed -n "s/^\([0-9a-f]\{64\}\)  $asset\$/\1/p" "$tmp/checksums.txt")
	if [ -z "$expected" ]; then
		echo "install.sh: release checksums have no entry for $asset" >&2
		exit 1
	fi
	if ! fetch "$tmp/$asset" "$release/$asset"; then
		echo "install.sh: could not download $release/$asset" >&2
		exit 1
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		actual=$(sha256sum "$tmp/$asset" | cut -d ' ' -f 1)
	elif command -v shasum >/dev/null 2>&1; then
		actual=$(shasum -a 256 "$tmp/$asset" | cut -d ' ' -f 1)
	else
		echo "install.sh: no SHA-256 tool to verify the release" >&2
		exit 1
	fi
	if [ "$actual" != "$expected" ]; then
		echo "install.sh: checksum mismatch for $asset" >&2
		exit 1
	fi
	tar -xzf "$tmp/$asset" -C "$tmp" hpm
	mv -f "$tmp/hpm" bin/hpm
fi

# Copy to HPM_BIN_DIR (default ~/.local/bin), never over another hpm.
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
