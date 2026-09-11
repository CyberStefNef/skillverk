#!/bin/sh
# Install Skillverk on Linux or macOS.
#
#   curl -LsSf https://raw.githubusercontent.com/CyberStefNef/skillverk/main/scripts/install.sh | sh
#
# Environment:
#   SKILLVERK_VERSION      release tag to install, "latest" by default
#   SKILLVERK_INSTALL_DIR  where the binary goes, ~/.local/bin by default
#   SKILLVERK_NO_MODIFY_PATH=1  skip the shell profile edit
set -eu

REPO="CyberStefNef/skillverk"
VERSION="${SKILLVERK_VERSION:-latest}"
INSTALL_DIR="${SKILLVERK_INSTALL_DIR:-${XDG_BIN_HOME:-$HOME/.local/bin}}"

say() { printf '%s\n' "$*"; }
err() {
	printf 'skillverk: %s\n' "$*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || err "$1 is required but not installed"
}

target() {
	os="$(uname -s)"
	arch="$(uname -m)"
	case "$os" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		err "on Windows, run the PowerShell installer:
  powershell -c \"irm https://raw.githubusercontent.com/$REPO/main/scripts/install.ps1 | iex\"" ;;
	*) err "unsupported operating system: $os" ;;
	esac
	case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) err "unsupported architecture: $arch. Build from source instead: https://github.com/$REPO" ;;
	esac
	printf '%s-%s' "$os" "$arch"
}

download() {
	# $1 is the URL, $2 the file to write.
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto '=https' --tlsv1.2 -o "$2" "$1" || return 1
	elif command -v wget >/dev/null 2>&1; then
		wget -q --https-only -O "$2" "$1" || return 1
	else
		err "curl or wget is required but neither is installed"
	fi
}

checksum() {
	# $1 is the file. Prints its SHA-256 as lowercase hex.
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		err "sha256sum or shasum is required to verify the download"
	fi
}

# hint prints the line that puts INSTALL_DIR on PATH, for the shell in use.
hint() {
	quoted_dir="$(printf '%s' "$INSTALL_DIR" | sed "s/'/'\\\\''/g")"
	case "$(basename "${SHELL:-sh}")" in
	fish) printf "fish_add_path '%s'" "$quoted_dir" ;;
	*) printf "export PATH='%s':\$PATH" "$quoted_dir" ;;
	esac
}

# profile names the startup file for the shell in use, or nothing when the
# shell is one this script should not guess at.
profile() {
	case "$(basename "${SHELL:-sh}")" in
	bash) [ -f "$HOME/.bash_profile" ] && printf '%s' "$HOME/.bash_profile" || printf '%s' "$HOME/.bashrc" ;;
	zsh) printf '%s' "${ZDOTDIR:-$HOME}/.zshrc" ;;
	fish) printf '%s' "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/skillverk.fish" ;;
	esac
}

need uname
need tar
TARGET="$(target)"
asset="skillverk-$TARGET.tar.gz"

if [ "$VERSION" = latest ]; then
	base="https://github.com/$REPO/releases/latest/download"
else
	base="https://github.com/$REPO/releases/download/$VERSION"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $asset ($VERSION)"
download "$base/$asset" "$tmp/$asset" ||
	err "no build for $TARGET at $VERSION. See https://github.com/$REPO/releases"
download "$base/checksums.txt" "$tmp/checksums.txt" ||
	err "could not download checksums.txt to verify the archive"

want="$(grep " \*\{0,1\}$asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
[ -n "$want" ] || err "checksums.txt has no entry for $asset"
got="$(checksum "$tmp/$asset")"
[ "$want" = "$got" ] || err "checksum mismatch for $asset: expected $want, got $got"

tar -xzf "$tmp/$asset" -C "$tmp"
[ -f "$tmp/skillverk" ] || err "archive did not contain a skillverk binary"

mkdir -p "$INSTALL_DIR" || err "cannot create $INSTALL_DIR"
# Replace rather than overwrite, so a running copy keeps its open file.
mv -f "$tmp/skillverk" "$INSTALL_DIR/skillverk.new"
chmod 755 "$INSTALL_DIR/skillverk.new"
mv -f "$INSTALL_DIR/skillverk.new" "$INSTALL_DIR/skillverk"

say "Installed $("$INSTALL_DIR/skillverk" --version) to $INSTALL_DIR/skillverk"

case ":$PATH:" in
*":$INSTALL_DIR:"*) exit 0 ;;
esac

rc="$(profile)"
if [ "${SKILLVERK_NO_MODIFY_PATH:-}" = 1 ] || [ -z "$rc" ]; then
	say ""
	say "$INSTALL_DIR is not on your PATH. Add it with:"
	say "  $(hint)"
	exit 0
fi

mkdir -p "$(dirname "$rc")"
if ! grep -Fq "$INSTALL_DIR" "$rc" 2>/dev/null; then
	printf '\n# Added by the Skillverk installer\n%s\n' "$(hint)" >>"$rc"
	say ""
	say "Added $INSTALL_DIR to your PATH in $rc."
fi
say "Open a new shell, or run: $(hint)"
