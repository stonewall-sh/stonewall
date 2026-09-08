#!/bin/sh
# Installs the latest stonewall release. Usage: curl -fsSL https://stonewall.sh/install.sh | sh
# Set STONEWALL_INSTALL_DIR to install somewhere other than /usr/local/bin.
set -eu

as_root() {
	if [ "$(id -u)" -eq 0 ]; then "$@"
	elif command -v sudo >/dev/null 2>&1; then sudo "$@"
	elif command -v doas >/dev/null 2>&1; then doas "$@"
	else echo "run as root, or install sudo or doas" >&2; exit 1
	fi
}

main() {
	REPO=stonewall-sh/stonewall
	DIR=${STONEWALL_INSTALL_DIR:-/usr/local/bin}

	case $(uname -s) in
		Darwin) OS=darwin ;;
		Linux) OS=linux ;;
		*) echo "stonewall runs on macOS and Linux only" >&2; exit 1 ;;
	esac
	case $(uname -m) in
		x86_64 | amd64) ARCH=amd64 ;;
		aarch64 | arm64) ARCH=arm64 ;;
		*) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
	esac

	TAG=$(curl -fsSLI --proto "=https" -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest")
	TAG=${TAG##*/}
	VERSION=${TAG#v}
	TAR=stonewall_${VERSION}_${OS}_${ARCH}.tar.gz
	URL=https://github.com/$REPO/releases/download/$TAG

	TMP=$(mktemp -d)
	trap 'rm -rf "$TMP"' EXIT
	echo "Downloading stonewall $TAG for $OS/$ARCH"
	curl -fsSL --proto "=https" -o "$TMP/$TAR" "$URL/$TAR"
	curl -fsSL --proto "=https" -o "$TMP/checksums.txt" "$URL/stonewall_${VERSION}_checksums.txt"

	want=$(grep " $TAR\$" "$TMP/checksums.txt" | cut -d' ' -f1)
	if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$TMP/$TAR" | cut -d' ' -f1); else got=$(shasum -a 256 "$TMP/$TAR" | cut -d' ' -f1); fi
	if [ -z "$want" ] || [ "$want" != "$got" ]; then echo "checksum mismatch for $TAR" >&2; exit 1; fi
	tar -xzf "$TMP/$TAR" -C "$TMP" stonewall

	mkdir -p "$DIR" 2>/dev/null || as_root mkdir -p "$DIR"
	if [ -w "$DIR" ]; then install -m 755 "$TMP/stonewall" "$DIR/stonewall"; else as_root install -m 755 "$TMP/stonewall" "$DIR/stonewall"; fi
	echo "Installed $DIR/stonewall"

	if [ "$OS" = linux ] && ! command -v bwrap >/dev/null 2>&1; then
		echo "Installing bubblewrap"
		if command -v apt-get >/dev/null 2>&1; then as_root apt-get update -qq && as_root apt-get install -y bubblewrap
		elif command -v dnf >/dev/null 2>&1; then as_root dnf install -y bubblewrap
		elif command -v pacman >/dev/null 2>&1; then as_root pacman -S --noconfirm bubblewrap
		elif command -v zypper >/dev/null 2>&1; then as_root zypper install -y bubblewrap
		elif command -v apk >/dev/null 2>&1; then as_root apk add bubblewrap
		else echo "Install bubblewrap with your package manager; stonewall needs bwrap" >&2
		fi
	fi

	case ":$PATH:" in *":$DIR:"*) ;; *) echo "Add $DIR to your PATH" ;; esac
	"$DIR/stonewall" --version
}

main "$@"
