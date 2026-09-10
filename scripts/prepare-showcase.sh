#!/bin/sh
# Prepare the canonical public DiffMind demo. Refuses existing destinations.
set -eu
if [ "$#" -ne 1 ]; then
  echo "Usage: sh scripts/prepare-showcase.sh NEW_DIRECTORY" >&2
  exit 2
fi
root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
showcase_dir=$1
umask 077
mkdir -- "$showcase_dir"
showcase_dir=$(CDPATH= cd -- "$showcase_dir" && pwd)
mkdir "$showcase_dir/repositories" "$showcase_dir/workspace" "$showcase_dir/analysis"
for template in "$root"/examples/demo-shop/repositories/*; do
  service=$(basename "$template")
  cp -R "$template" "$showcase_dir/repositories/$service"
  git -C "$showcase_dir/repositories/$service" init -q -b main
  git -C "$showcase_dir/repositories/$service" add .
  git -C "$showcase_dir/repositories/$service" -c user.name='Demo Developer' \
    -c user.email='developer@example.test' -c commit.gpgsign=false commit -q -m 'Create synthetic demo service'
done
printf '\nDiffMind Demo Shop prepared at:\n  %s\n' "$showcase_dir"
printf 'Repositories:\n  %s/repositories\n' "$showcase_dir"
printf 'Workspace:\n  %s/workspace\n' "$showcase_dir"
printf 'All names and hostnames are synthetic and public-safe.\n'
