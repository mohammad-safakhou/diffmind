#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/diffmind-installer-test.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

release_dir="$tmp_dir/releases/download/v9.8.7"
install_dir="$tmp_dir/bin"
package_dir="$tmp_dir/package"
mkdir -p "$release_dir" "$install_dir" "$package_dir"
printf '#!/bin/sh\necho "diffmind 9.8.7"\n' > "$package_dir/diffmind"
chmod +x "$package_dir/diffmind"
archive="diffmind_9.8.7_$(uname -s | tr '[:upper:]' '[:lower:]')_"
case "$(uname -m)" in
  x86_64|amd64) archive="${archive}amd64.tar.gz" ;;
  arm64|aarch64) archive="${archive}arm64.tar.gz" ;;
  *) echo "unsupported test architecture" >&2; exit 1 ;;
esac
tar -C "$package_dir" -czf "$release_dir/$archive" diffmind
if command -v sha256sum >/dev/null 2>&1; then
  (cd "$release_dir" && sha256sum "$archive" > checksums.txt)
else
  (cd "$release_dir" && shasum -a 256 "$archive" > checksums.txt)
fi

DIFFMIND_RELEASE_BASE_URL="file://$tmp_dir/releases" \
DIFFMIND_INSTALL_DIR="$install_dir" \
DIFFMIND_VERSION="9.8.7" \
sh "$root/install.sh"

test -x "$install_dir/diffmind"
test "$("$install_dir/diffmind" version)" = "diffmind 9.8.7"
echo "installer smoke test: ok"
