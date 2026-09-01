#!/bin/sh
set -eu

REPOSITORY="mohammad-safakhou/diffmind"
INSTALL_DIR="${DIFFMIND_INSTALL_DIR:-/usr/local/bin}"
VERSION="${DIFFMIND_VERSION:-latest}"
RELEASE_BASE_URL="${DIFFMIND_RELEASE_BASE_URL:-https://github.com/$REPOSITORY/releases}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  darwin|linux) ;;
  *) echo "DiffMind releases do not yet support $os" >&2; exit 1 ;;
esac

machine=$(uname -m)
case "$machine" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "DiffMind releases do not yet support architecture $machine" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  release_url="$RELEASE_BASE_URL/latest/download"
  archive="diffmind_latest_${os}_${arch}.tar.gz"
else
  tag=${VERSION#v}
  release_url="$RELEASE_BASE_URL/download/v${tag}"
  archive="diffmind_${tag}_${os}_${arch}.tar.gz"
fi

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/diffmind-install.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

curl -fsSL "$release_url/$archive" -o "$tmp_dir/$archive"
curl -fsSL "$release_url/checksums.txt" -o "$tmp_dir/checksums.txt"

expected=$(awk -v file="$archive" '$2 == file { print $1 }' "$tmp_dir/checksums.txt")
if [ -z "$expected" ]; then
  echo "No checksum was published for $archive" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp_dir/$archive" | awk '{print $1}')
else
  actual=$(shasum -a 256 "$tmp_dir/$archive" | awk '{print $1}')
fi
if [ "$actual" != "$expected" ]; then
  echo "Checksum verification failed for $archive" >&2
  exit 1
fi

tar -xzf "$tmp_dir/$archive" -C "$tmp_dir"
mkdir -p "$INSTALL_DIR"
if [ -w "$INSTALL_DIR" ]; then
  install -m 0755 "$tmp_dir/diffmind" "$INSTALL_DIR/diffmind"
else
  sudo install -m 0755 "$tmp_dir/diffmind" "$INSTALL_DIR/diffmind"
fi

"$INSTALL_DIR/diffmind" version
echo "Run 'diffmind doctor', then 'diffmind' to open the workspace."
