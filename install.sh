#!/bin/sh
# Install the cello client.
#
#   curl -fsSL https://raw.githubusercontent.com/henockt/cello/main/install.sh | sh
#
# Set CELLO_INSTALL_DIR to choose where it lands.
set -eu

REPO="henockt/cello"

# The server installed binaries talk to. This lives here, not in the binary:
# install.sh is fetched from main on every install, so changing the domain is
# one commit and needs no release. Override with CELLO_SERVER=... to point a
# client at your own instance.
SERVER="${CELLO_SERVER:-cello.henock.me}"

fail() { echo "install: $*" >&2; exit 1; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported architecture: $arch" ;;
esac
case "$os" in
  linux) ;;
  darwin) [ "$arch" = amd64 ] || [ "$arch" = arm64 ] || fail "unsupported: $os/$arch" ;;
  *) fail "unsupported OS: $os (Windows: download the .zip from the releases page)" ;;
esac

# Somewhere on PATH that we can actually write to.
if [ -n "${CELLO_INSTALL_DIR:-}" ]; then
  dir="$CELLO_INSTALL_DIR"
elif [ -w /usr/local/bin ] 2>/dev/null; then
  dir=/usr/local/bin
else
  dir="$HOME/.local/bin"
fi
mkdir -p "$dir" || fail "cannot create $dir"

command -v curl >/dev/null 2>&1 || fail "curl is required"

echo "install: finding the latest release..."
tag=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
      | grep '"tag_name"' | head -1 | cut -d'"' -f4)
[ -n "$tag" ] || fail "could not determine the latest release"

version="${tag#v}"
asset="cello_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "install: downloading $asset"
curl -fsSL -o "$tmp/$asset" "$base/$asset" || fail "no build for $os/$arch in $tag"

# Verify the checksum when the tooling is present. Not fatal if it is not:
# say so rather than pretending the download was checked.
if curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp" && grep " $asset\$" checksums.txt | sha256sum -c -) >/dev/null 2>&1 \
      || fail "checksum mismatch for $asset"
    echo "install: checksum ok"
  elif command -v shasum >/dev/null 2>&1; then
    want=$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)
    got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
    [ "$want" = "$got" ] || fail "checksum mismatch for $asset"
    echo "install: checksum ok"
  else
    echo "install: no sha256 tool found, skipping checksum verification" >&2
  fi
fi

tar xzf "$tmp/$asset" -C "$tmp"
install -m 0755 "$tmp/cello" "$dir/cello" 2>/dev/null \
  || { cp "$tmp/cello" "$dir/cello" && chmod 0755 "$dir/cello"; }

# Record the server host where the client will look for it.
if [ -n "$SERVER" ]; then
  conf_dir="${XDG_CONFIG_HOME:-$HOME/.config}/cello"
  mkdir -p "$conf_dir"
  printf '# server cello connects to. edit freely, or pass -server\n%s\n' "$SERVER" > "$conf_dir/server"
  echo "install: server set to $SERVER ($conf_dir/server)"
fi

echo "install: cello $version -> $dir/cello"
case ":$PATH:" in
  *":$dir:"*) echo "install: run  cello 3000" ;;
  *) echo "install: $dir is not on your PATH; run  $dir/cello 3000" ;;
esac
