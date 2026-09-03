#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
demo_test_dir=$(mktemp -d "${TMPDIR:-/tmp}/diffmind-demo-test.XXXXXX")
trap 'rm -rf "$demo_test_dir"' EXIT HUP INT TERM
sh "$root/scripts/prepare-demo.sh" "$demo_test_dir/demo with spaces"
for service in gateway catalog billing; do
  repo="$demo_test_dir/demo with spaces/repositories/$service"
  test "$(git -C "$repo" branch --show-current)" = master
  test -z "$(git -C "$repo" status --porcelain)"
  test "$(git -C "$repo" rev-list --count HEAD)" = 1
  git -C "$repo" log -1 --format=%ae | grep -qx 'developer@example.test'
done
if sh "$root/scripts/prepare-demo.sh" "$demo_test_dir/demo with spaces"; then
  echo 'demo overwrote an existing destination' >&2
  exit 1
fi
test -d "$demo_test_dir/demo with spaces/workspace/projects"
echo 'demo preparation checks: ok'
