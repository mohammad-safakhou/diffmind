#!/bin/sh
# Exercise the public demo without starting a server or installing app runtimes.
set -eu

project_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
binary=$project_root/bin/diffmind
test -x "$binary"

showcase_tmp=$(mktemp -d "${TMPDIR:-/tmp}/diffmind-showcase.XXXXXX")
cleanup() {
  chmod -R u+w "$showcase_tmp" 2>/dev/null || true
  rm -rf "$showcase_tmp"
}
trap cleanup EXIT HUP INT TERM

sh "$project_root/scripts/prepare-showcase.sh" "$showcase_tmp/demo"
repo_count=$(find "$showcase_tmp/demo/repositories" -mindepth 2 -maxdepth 2 -type d -name .git | wc -l | tr -d ' ')
test "$repo_count" = 6

for repo in "$showcase_tmp"/demo/repositories/*; do
  service=${repo##*/}
  DIFFMIND_HOME=$showcase_tmp/demo/workspace "$binary" run --repo "$repo" \
    --out "$showcase_tmp/demo/analysis/$service"
  test -f "$(find "$showcase_tmp/demo/analysis/$service" -name run_manifest.json -print -quit)"
done

grep -q '"dependencies": 2' "$showcase_tmp"/demo/analysis/checkout/*/run_manifest.json
grep -q '"dependencies": 3' "$showcase_tmp"/demo/analysis/gateway/*/run_manifest.json
grep -q '"exposures": 1' "$showcase_tmp"/demo/analysis/catalog/*/run_manifest.json

sh "$project_root/scripts/apply-demo-shop-change.sh" "$showcase_tmp/demo"
test "$(git -C "$showcase_tmp/demo/repositories/checkout" log -1 --pretty=%s)" = 'Evolve checkout request contract'
grep -q 'deliveryPostalCode' "$showcase_tmp/demo/repositories/checkout/openapi.yaml"

if grep -Eir 'bonial|traffic-shaping' "$project_root/examples/demo-shop" >/dev/null; then
  echo 'private-company terminology found in public demo' >&2
  exit 1
fi

echo 'DiffMind Demo Shop validation passed.'
